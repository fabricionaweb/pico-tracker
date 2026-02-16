package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

var version = "dev"

// Protocol constants for the UDP Tracker Protocol (BEP 15)
// https://bittorrent.org/beps/bep_0015.html
const (
	protocolID = 0x41727101980 // fixed "magic constant"

	actionConnect  = 0
	actionAnnounce = 1
	actionScrape   = 2
	actionError    = 3

	eventNone      = 0 // regular update
	eventCompleted = 1
	eventStarted   = 2
	eventStopped   = 3

	maxPacketSize    = 1500 // typical unfragmented Ethernet frame
	announceInterval = 600  // seconds between announces
)

var debugMode = os.Getenv("DEBUG") != ""

func debug(format string, v ...any) {
	if debugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
}

func info(format string, v ...any) {
	log.Printf("[INFO] "+format, v...)
}

type Peer struct {
	IP   net.IP
	Port uint16
	Left int64 // 0 = seeder, >0 = leecher
}

type Torrent struct {
	mu       sync.RWMutex
	peers    map[string]*Peer // key is peer_id
	seeders  int
	leechers int
}

type Tracker struct {
	mu       sync.RWMutex
	torrents map[string]*Torrent // key is info_hash
}

func (t *Tracker) getTorrent(hash string) *Torrent {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.torrents[hash]; !ok {
		t.torrents[hash] = &Torrent{peers: make(map[string]*Peer)}
		info("created new torrent %s", hash)
	}

	return t.torrents[hash]
}

func (t *Torrent) addPeer(id string, ip net.IP, port uint16, left int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if p, exists := t.peers[id]; exists {
		if p.Left == 0 && left > 0 {
			t.seeders--
			t.leechers++
			debug("peer %s became leecher @ %s:%d", id, ip, port)
		} else if p.Left > 0 && left == 0 {
			t.leechers--
			t.seeders++
			debug("peer %s became seeder @ %s:%d", id, ip, port)
		}
		p.IP, p.Port, p.Left = ip, port, left
		return
	}

	if left == 0 {
		t.seeders++
	} else {
		t.leechers++
	}
	t.peers[id] = &Peer{IP: ip, Port: port, Left: left}
	info("added peer %s @ %s:%d", id, ip, port)
}

func (t *Torrent) removePeer(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, exists := t.peers[id]
	if !exists {
		return
	}

	if p.Left == 0 {
		t.seeders--
	} else {
		t.leechers--
	}
	delete(t.peers, id)
	info("removed peer %s @ %s:%d", id, p.IP, p.Port)
}

// getPeersAndCount returns IPv4 and IPv6 peer lists for clients to connect to
// Returns up to numWant peers of each address family (not including requesting peer)
// The returned data is packed as:
//	[4 bytes IP][2 bytes port] for IPv4 (6 bytes per peer)
//	[16 bytes IP][2 bytes port] for IPv6 (18 bytes per peer)
func (t *Torrent) getPeersAndCount(exclude string, numWant int) (peers4, peers6 []byte, seeders, leechers int) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	seeders, leechers = t.seeders, t.leechers

	for id, p := range t.peers {
		if id == exclude {
			continue
		}

		if ip4 := p.IP.To4(); ip4 != nil {
			if len(peers4)/6 < numWant {
				peers4 = append(peers4, ip4...)
				peers4 = binary.BigEndian.AppendUint16(peers4, p.Port)
			}
		} else {
			if len(peers6)/18 < numWant {
				peers6 = append(peers6, p.IP.To16()...)
				peers6 = binary.BigEndian.AppendUint16(peers6, p.Port)
			}
		}
	}

	return peers4, peers6, seeders, leechers
}

// handleConnect is the first step in UDP tracker communication
// The client sends a "connect" request to establish a session, and we give them
// a connection ID they must use in all future requests to prove they're legitimate
// This prevents IP spoofing attacks where someone could fake announce requests
func (tr *Tracker) handleConnect(conn *net.UDPConn, addr *net.UDPAddr, transactionID uint32) {
	debug("connect request from %s, transaction_id=%d", addr, transactionID)

	// Generate a connection ID - should be unpredictable to prevent spoofing
	// TODO: use crypto/rand or include client IP for production security
	connectionID := uint64(time.Now().Unix())

	// Connect response format: [action:4][transaction_id:4][connection_id:8]
	response := make([]byte, 16)
	binary.BigEndian.PutUint32(response[0:4], actionConnect)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	binary.BigEndian.PutUint64(response[8:16], connectionID)

	if _, err := conn.WriteToUDP(response, addr); err != nil {
		info("failed to send connect response to %s: %v", addr, err)
	} else {
		debug("sent connect response with connection_id=%d", connectionID)
	}
}

// handleAnnounce is the main interaction - a client tells us they're downloading
// and asks for a list of other people to connect to
// Announce request format:
//	[connection_id:8] [action:4] [transaction_id:4] [info_hash:20] [peer_id:20]
//	[downloaded:8] [left:8] [uploaded:8] [event:4] [IP:4] [key:4] [num_want:4] [port:2]
func (tr *Tracker) handleAnnounce(conn *net.UDPConn, addr *net.UDPAddr, packet []byte, transactionID uint32) {
	if len(packet) < 98 {
		debug("announce request too short from %s", addr)
		tr.sendError(conn, addr, transactionID, "invalid packet size")
		return
	}

	infoHash := hex.EncodeToString(packet[16:36])
	peerID := hex.EncodeToString(packet[36:56])
	// Skip downloaded (8 bytes)
	left := binary.BigEndian.Uint64(packet[64:72])
	// Skip uploaded (8 bytes)
	event := binary.BigEndian.Uint32(packet[80:84])
	ipAddr := binary.BigEndian.Uint32(packet[84:88])
	numWant := binary.BigEndian.Uint32(packet[92:96])
	port := binary.BigEndian.Uint16(packet[96:98])

	// Determine client's IP: use packet source by default, but IPv4 clients can specify a custom IP
	clientIP := addr.IP
	if ipAddr != 0 && addr.IP.To4() != nil {
		clientIP = net.ParseIP(fmt.Sprintf("%d.%d.%d.%d",
			byte(ipAddr>>24), byte(ipAddr>>16), byte(ipAddr>>8), byte(ipAddr)))
	}

	debug("announce from %s: info_hash=%s peer_id=%s event=%d left=%d port=%d num_want=%d ip=%s",
		addr, infoHash, peerID, event, left, port, numWant, clientIP)

	// num_want 0 and 0xFFFFFFFF (-1) means "default", max is 50
	if numWant == 0 || numWant == 0xFFFFFFFF || numWant > 50 {
		numWant = 50
	}

	torrent := tr.getTorrent(infoHash)

	switch event {
	case eventStopped:
		torrent.removePeer(peerID)
	case eventCompleted:
		torrent.addPeer(peerID, clientIP, port, 0)
	case eventStarted, eventNone:
		fallthrough
	default:
		torrent.addPeer(peerID, clientIP, port, int64(left))
	}

	peers4, peers6, seeders, leechers := torrent.getPeersAndCount(peerID, int(numWant))

	var peers []byte
	if addr.IP.To4() != nil {
		peers = peers4
		debug("returning %d seeders, %d leechers, %d IPv4 peers", seeders, leechers, len(peers4)/6)
	} else {
		peers = peers6
		debug("returning %d seeders, %d leechers, %d IPv6 peers", seeders, leechers, len(peers6)/18)
	}

	// Announce response format: [action:4][transaction_id:4][interval:4][leechers:4][seeders:4][peers:variable]
	// Fixed header: 4 + 4 + 4 + 4 + 4 = 20 bytes
	response := make([]byte, 20+len(peers))
	binary.BigEndian.PutUint32(response[0:4], actionAnnounce)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	binary.BigEndian.PutUint32(response[8:12], announceInterval)
	binary.BigEndian.PutUint32(response[12:16], uint32(leechers))
	binary.BigEndian.PutUint32(response[16:20], uint32(seeders))
	copy(response[20:], peers)

	if _, err := conn.WriteToUDP(response, addr); err != nil {
		info("failed to send announce response to %s: %v", addr, err)
	}
}

// handleScrape lets clients ask for statistics about torrents without announcing
// This is useful for checking if a torrent is active before downloading
// Scrape request format: [connection_id:8][action:4][transaction_id:4][info_hash:20]
func (tr *Tracker) handleScrape(conn *net.UDPConn, addr *net.UDPAddr, packet []byte, transactionID uint32) {
	if len(packet) < 36 {
		debug("scrape request too short from %s", addr)
		tr.sendError(conn, addr, transactionID, "no info hashes provided")
		return
	}

	// info_hashes starts at byte 16, each is 20 bytes
	numHashes := (len(packet) - 16) / 20
	debug("scrape request from %s with %d hashes, transaction_id=%d", addr, numHashes, transactionID)

	// Scrape response format: [action:4][transaction_id:4] + [seeders:4][completed:4][leechers:4] per hash
	// Fixed header: 4 + 4 = 8 bytes
	response := make([]byte, 8, 8+numHashes*12)
	binary.BigEndian.PutUint32(response[0:4], actionScrape)
	binary.BigEndian.PutUint32(response[4:8], transactionID)

	for offset := 16; offset+20 <= len(packet); offset += 20 {
		infoHash := hex.EncodeToString(packet[offset : offset+20])

		torrent := tr.getTorrent(infoHash)
		torrent.mu.RLock()
		seeders := uint32(torrent.seeders)
		completed := uint32(0) // don't track completed
		leechers := uint32(torrent.leechers)
		torrent.mu.RUnlock()

		response = binary.BigEndian.AppendUint32(response, seeders)
		response = binary.BigEndian.AppendUint32(response, completed)
		response = binary.BigEndian.AppendUint32(response, leechers)

		debug("scrape for %s: seeders=%d leechers=%d", infoHash, seeders, leechers)
	}

	if _, err := conn.WriteToUDP(response, addr); err != nil {
		info("failed to send scrape response to %s: %v", addr, err)
	}
}

// sendError sends an error message back to the client when something goes wrong
// Error response format: [action:4][transaction_id:4][error_message:variable]
// Fixed header: 4 + 4 = 8 bytes
func (tr *Tracker) sendError(conn *net.UDPConn, addr *net.UDPAddr, transactionID uint32, message string) {
	response := make([]byte, 8+len(message))
	binary.BigEndian.PutUint32(response[0:4], actionError)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	copy(response[8:], message)
	if _, err := conn.WriteToUDP(response, addr); err != nil {
		info("failed to send error to %s: %v", addr, err)
	} else {
		debug("sent error to %s: %s", addr, message)
	}
}

// handlePacket processes any incoming UDP packet and routes it to the right handler
// Packet request format: [connection_id:8][action:4][transaction_id:4]
func (tr *Tracker) handlePacket(conn *net.UDPConn, addr *net.UDPAddr, packet []byte) {
	if len(packet) < 16 {
		debug("packet too short (%d bytes) from %s", len(packet), addr)
		return
	}

	connectionID := binary.BigEndian.Uint64(packet[0:8])
	action := binary.BigEndian.Uint32(packet[8:12])
	transactionID := binary.BigEndian.Uint32(packet[12:16])

	debug("packet from %s: connection_id=%d action=%d transaction_id=%d",
		addr, connectionID, action, transactionID)

	switch action {
	case actionConnect:
		if connectionID != protocolID {
			debug("invalid protocol ID from %s: %d", addr, connectionID)
			tr.sendError(conn, addr, transactionID, "invalid protocol ID")
			return
		}
		tr.handleConnect(conn, addr, transactionID)

	case actionAnnounce:
		tr.handleAnnounce(conn, addr, packet, transactionID)

	case actionScrape:
		tr.handleScrape(conn, addr, packet, transactionID)

	default:
		debug("unknown action %d from %s", action, addr)
		tr.sendError(conn, addr, transactionID, "unknown action")
	}
}

// cleanupLoop runs periodically to remove inactive torrents (those with no peers)
func (tr *Tracker) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		tr.mu.Lock()
		removedCount := 0
		for hash, t := range tr.torrents {
			if len(t.peers) == 0 {
				delete(tr.torrents, hash)
				removedCount++
				debug("cleanup: removed inactive torrent %s", hash)
			}
		}
		tr.mu.Unlock()
		if removedCount > 0 {
			info("cleanup: removed %d inactive torrents", removedCount)
		}
	}
}

func main() {
	defaultPort := 1337
	if p, err := strconv.Atoi(os.Getenv("PICO_TRACKER__PORT")); err == nil && p > 0 {
		defaultPort = p
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Pico Tracker: %s\nPortable BitTorrent Tracker (UDP)\n\n", version)
		flag.PrintDefaults()
	}

	port := flag.Int("port", defaultPort, "port to listen on")
	flag.IntVar(port, "p", defaultPort, "alias to -port")
	flag.BoolVar(&debugMode, "debug", debugMode, "enable debug logs")
	flag.BoolVar(&debugMode, "d", debugMode, "alias to -debug")
	showVersion := flag.Bool("version", false, "print version")
	flag.BoolVar(showVersion, "v", false, "alias to -version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	info("Starting Pico Tracker: %s", version)
	debug("Debug mode enabled")

	tracker := &Tracker{torrents: make(map[string]*Torrent)}
	go tracker.cleanupLoop()

	conn4, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: *port})
	if err != nil {
		log.Fatalf("[ERROR] Failed to listen on IPv4: %v", err)
	}
	defer conn4.Close()
	info("UDP Tracker listening on 0.0.0.0:%d (IPv4)", *port)

	// IPv6 is optional - if it fails, the tracker still works with IPv4 only
	conn6, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::"), Port: *port})
	if err != nil {
		log.Printf("[WARN] IPv6 not available: %v", err)
	} else {
		defer conn6.Close()
		info("UDP Tracker listening on [::]:%d (IPv6)", *port)
	}

	go tracker.listen(conn4)
	if conn6 != nil {
		go tracker.listen(conn6)
	}

	select {}
}

// listen handles incoming UDP packets on a connection
func (tr *Tracker) listen(conn *net.UDPConn) {
	buffer := make([]byte, maxPacketSize)

	for {
		n, clientAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("[ERROR] Failed to read UDP packet: %v", err)
			continue
		}

		// Copy packet since buffer is reused, handle concurrently
		packet := make([]byte, n)
		copy(packet, buffer[:n])

		go tr.handlePacket(conn, clientAddr, packet)
	}
}
