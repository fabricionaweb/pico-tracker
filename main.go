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

	maxPacketSize       = 1500 // typical unfragmented Ethernet frame (MTU)
	maxPeersPerPacketV4 = 200  // IPv4: 200 * 6 peers = 1220 bytes (under 1500 MTU)
	maxPeersPerPacketV6 = 82   // IPv6: 82 * 18 peers = 1496 bytes (under 1500 MTU)
	defaultNumWant      = 50   // default number of peers to return when client doesn't specify

	announceInterval   = 10 // (minutes) between reannounces
	cleanupInterval    = 30 // (minutes) to remove stale peers and inactive torrents
	stalePeerThreshold = 60 // (minutes) allows one missed announce
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
	IP            net.IP
	Port          uint16
	Left          int64     // 0 = seeder, >0 = leecher
	Completed     bool      // true if peer has completed this torrent
	LastAnnounced time.Time // last time this peer announced (for stale cleanup)
}

type Torrent struct {
	mu        sync.RWMutex
	peers     map[string]*Peer // key is peer_id
	seeders   int
	leechers  int
	completed int // total completions (peers who finished downloading)
}

type Tracker struct {
	mu       sync.RWMutex
	torrents map[string]*Torrent // key is info_hash
}

func (t *Tracker) getOrCreateTorrent(hash string) *Torrent {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.torrents[hash]; !ok {
		t.torrents[hash] = &Torrent{peers: make(map[string]*Peer)}
		info("created new torrent %s", hash)
	}

	return t.torrents[hash]
}

func (t *Tracker) getTorrent(hash string) *Torrent {
	t.mu.RLock()
	defer t.mu.RUnlock()

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
			if !p.Completed {
				p.Completed = true
				t.completed++
				debug("peer %s completed torrent @ %s:%d", id, ip, port)
			}
		}
		p.IP, p.Port, p.Left = ip, port, left
		p.LastAnnounced = time.Now()
		return
	}

	peer := &Peer{IP: ip, Port: port, Left: left, LastAnnounced: time.Now()}
	if left == 0 {
		t.seeders++
		peer.Completed = true
		t.completed++ // peer starts as seeder (has full file) can counts as completed
		debug("new peer %s is seeder (completed) @ %s:%d", id, ip, port)
	} else {
		t.leechers++
	}
	t.peers[id] = peer
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

// getPeers returns a list of peers for a client to connect to
// Returns up to numWant peers matching the client's IP version (not including requesting peer)
// The returned data is packed as:
//
//	[4 bytes IP][2 bytes port] for IPv4 (6 bytes per peer)
//	[16 bytes IP][2 bytes port] for IPv6 (18 bytes per peer)
func (t *Torrent) getPeers(exclude string, numWant int, clientIP net.IP) (peers []byte, seeders, leechers int) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	seeders, leechers = t.seeders, t.leechers
	wantV4Peers := clientIP.To4() != nil
	peerSize := 6 // IPv4 peer size
	if !wantV4Peers {
		peerSize = 18 // IPv6 peer size
	}

	for id, p := range t.peers {
		if id == exclude {
			continue
		}

		isV4Peer := p.IP.To4() != nil
		if isV4Peer != wantV4Peers {
			continue
		}

		if len(peers)/peerSize >= numWant {
			break
		}

		if wantV4Peers {
			peers = append(peers, p.IP.To4()...)
		} else {
			peers = append(peers, p.IP.To16()...)
		}
		peers = binary.BigEndian.AppendUint16(peers, p.Port)
	}

	return peers, seeders, leechers
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
//
//	[connection_id:8][action:4][transaction_id:4][info_hash:20][peer_id:20]
//	[downloaded:8][left:8][uploaded:8][event:4][IP:4][key:4][num_want:4][port:2]
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
	numWantRaw := binary.BigEndian.Uint32(packet[92:96])
	port := binary.BigEndian.Uint16(packet[96:98])

	clientIsV4 := addr.IP.To4() != nil
	maxWant := maxPeersPerPacketV4
	peerSize := 6 // IPv4 peer size
	if !clientIsV4 {
		maxWant = maxPeersPerPacketV6
		peerSize = 18 // IPv6 peer size
	}
	numWant := defaultNumWant
	// num_want 0 or 0xFFFFFFFF (-1 but we have it unsigned 32bit) means "default"
	if numWantRaw != 0 && numWantRaw != 0xFFFFFFFF {
		if numWantRaw > uint32(maxWant) {
			numWant = maxWant
		} else {
			numWant = int(numWantRaw)
		}
	}

	// Determine client's IP: use packet source by default, but IPv4 clients can specify a custom IP
	clientIP := addr.IP
	if ipAddr != 0 && clientIsV4 {
		clientIP = net.ParseIP(fmt.Sprintf("%d.%d.%d.%d",
			byte(ipAddr>>24), byte(ipAddr>>16), byte(ipAddr>>8), byte(ipAddr)))
	}

	debug("announce from %s: info_hash=%s peer_id=%s event=%d left=%d port=%d num_want=%d ip=%s",
		addr, infoHash, peerID, event, left, port, numWant, clientIP)

	torrent := tr.getOrCreateTorrent(infoHash)

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

	peers, seeders, leechers := torrent.getPeers(peerID, numWant, addr.IP)
	peerCount := len(peers) / peerSize
	debug("returning %d seeders, %d leechers, %d peers", seeders, leechers, peerCount)

	// Announce response format: [action:4][transaction_id:4][interval:4][leechers:4][seeders:4][peers:variable]
	// Fixed header: 4 + 4 + 4 + 4 + 4 = 20 bytes
	response := make([]byte, 20+len(peers))
	binary.BigEndian.PutUint32(response[0:4], actionAnnounce)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	binary.BigEndian.PutUint32(response[8:12], uint32(time.Duration(announceInterval)*time.Minute/time.Second)) // convert minutes to seconds
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

		var seeders, completed, leechers uint32
		torrent := tr.getTorrent(infoHash)
		if torrent != nil {
			torrent.mu.RLock()
			seeders = uint32(torrent.seeders)
			completed = uint32(torrent.completed)
			leechers = uint32(torrent.leechers)
			torrent.mu.RUnlock()
		}

		response = binary.BigEndian.AppendUint32(response, seeders)
		response = binary.BigEndian.AppendUint32(response, completed)
		response = binary.BigEndian.AppendUint32(response, leechers)

		debug("scrape for %s: seeders=%d completed=%d leechers=%d", infoHash, seeders, completed, leechers)
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

// cleanupLoop periodically removes peers that haven't announced recently and
// deletes torrents that become empty
func (tr *Tracker) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval * time.Minute)
	defer ticker.Stop()

	staleThreshold := stalePeerThreshold * time.Minute

	for range ticker.C {
		staleDeadline := time.Now().Add(-staleThreshold)
		tr.mu.Lock()
		removedTorrents := 0
		removedPeers := 0

		for hash, t := range tr.torrents {
			t.mu.Lock()

			for id, p := range t.peers {
				if p.LastAnnounced.Before(staleDeadline) {
					if p.Left == 0 {
						t.seeders--
					} else {
						t.leechers--
					}
					delete(t.peers, id)
					removedPeers++
					debug("cleanup: removed stale peer %s @ %s:%d (last seen %s ago)",
						id, p.IP, p.Port, time.Since(p.LastAnnounced).Round(time.Minute))
				}
			}

			if len(t.peers) == 0 {
				delete(tr.torrents, hash)
				removedTorrents++
				debug("cleanup: removed inactive torrent %s", hash)
			}
			t.mu.Unlock()
		}
		tr.mu.Unlock()

		if removedPeers > 0 || removedTorrents > 0 {
			info("cleanup: removed %d stale peers and %d inactive torrents", removedPeers, removedTorrents)
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
