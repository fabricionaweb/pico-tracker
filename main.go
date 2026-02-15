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

// Protocol constants for the UDP Tracker Protocol (BEP 15)
// https://bittorrent.org/beps/bep_0015.html
const (
	// Fixed "magic constant" identifier
	protocolID = 0x41727101980

	// Action codes tell the tracker what the client wants to do
	actionConnect  = 0
	actionAnnounce = 1
	actionScrape   = 2
	actionError    = 3

	// Event codes tell the tracker what's happening with the download
	eventNone      = 0 // Regular update (happens every ~30 min during download)
	eventCompleted = 1
	eventStarted   = 2
	eventStopped   = 3

	// The standard MTU size for Ethernet (1500 bytes)
	maxPacketSize = 1500

	// How long clients should wait before re-announcing (in seconds)
	announceInterval = 600
)

var debugMode = os.Getenv("DEBUG") != ""

func debug(format string, v ...any) {
	if debugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
}

// Peer represents someone downloading or seeding a torrent
type Peer struct {
	IP   net.IP
	Port uint16
	Left int64 // bytes remaining (0 = they have the complete file = seeder, >0 = still downloading = leecher)
}

// Torrent keeps track of everyone sharing a specific file (identified by its info_hash)
type Torrent struct {
	mu       sync.RWMutex
	peers    map[string]*Peer // key is peer_id (unique identifier for each BitTorrent client)
	seeders  int              // how many people have the complete file
	leechers int              // how many people are still downloading
}

// Tracker manages multiple torrents (different files being shared)
type Tracker struct {
	mu       sync.RWMutex
	torrents map[string]*Torrent // key is info_hash (unique identifier for each torrent)
}

// getTorrent finds or creates a Torrent entry for the given info_hash
func (t *Tracker) getTorrent(hash string) *Torrent {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.torrents[hash]; !ok {
		t.torrents[hash] = &Torrent{peers: make(map[string]*Peer)}
		debug("created new torrent %s", hash)
	}

	return t.torrents[hash]
}

// addPeer registers a new peer or updates an existing one's information
func (t *Torrent) addPeer(id string, ip net.IP, port uint16, left int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if p, exists := t.peers[id]; exists {
		// Peer already exists, check if they changed from seeder to leecher or vice versa
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

	// New peer joining
	if left == 0 {
		t.seeders++
	} else {
		t.leechers++
	}
	t.peers[id] = &Peer{IP: ip, Port: port, Left: left}
	debug("added peer %s @ %s:%d", id, ip, port)
}

// removePeer removes a peer when they disconnect (eventStopped)
func (t *Torrent) removePeer(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, exists := t.peers[id]
	if !exists {
		return
	}

	// Peer is leaving
	if p.Left == 0 {
		t.seeders--
	} else {
		t.leechers--
	}
	delete(t.peers, id)
	debug("removed peer %s @ %s:%d", id, p.IP, p.Port)
}

// getPeersAndCount returns IPv4 and IPv6 peer lists for clients to connect to
// Returns up to numWant peers of each address family (not including requesting peer)
// The returned data is packed as:
//
//	[4 bytes IP][2 bytes port] for IPv4 (6 bytes per peer)
//	[16 bytes IP][2 bytes port] for IPv6 (18 bytes per peer)
func (t *Torrent) getPeersAndCount(exclude string, numWant int) (peers4, peers6 []byte, seeders, leechers int) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	seeders, leechers = t.seeders, t.leechers

	for id, p := range t.peers {
		// Don't return the requesting peer to themselves - they already know their own address
		if id == exclude {
			continue
		}

		// Add peer to appropriate list if we haven't reached numWant yet
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

	// Generate a connection ID - this should be unpredictable to prevent spoofing
	// In production, you'd want to use crypto/rand or at least include the client's IP
	// For simplicity, we use current timestamp, but this is not secure against spoofing
	connectionID := uint64(time.Now().Unix())

	// Response format: [action:4][transaction_id:4][connection_id:8]
	// Total: 4 + 4 + 8 = 16 bytes
	response := make([]byte, 16)
	// Bytes 0-3: action (4 bytes) - tells client this is a connect response
	binary.BigEndian.PutUint32(response[0:4], actionConnect)
	// Bytes 4-7: transaction_id (4 bytes) - echoed back so client can match response to request
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	// Bytes 8-15: connection_id (8 bytes) - session token for future requests
	binary.BigEndian.PutUint64(response[8:16], connectionID)

	if _, err := conn.WriteToUDP(response, addr); err != nil {
		debug("failed to send connect response to %s: %v", addr, err)
	} else {
		debug("sent connect response with connection_id=%d", connectionID)
	}
}

// handleAnnounce is the main interaction - a client tells us they're downloading
// and asks for a list of other people to connect to
// Announce packet format from client:
//
//	[connection_id:8] [action:4] [transaction_id:4] [info_hash:20] [peer_id:20]
//	[downloaded:8] [left:8] [uploaded:8] [event:4] [IP:4] [key:4] [num_want:4] [port:2]
func (tr *Tracker) handleAnnounce(conn *net.UDPConn, addr *net.UDPAddr, packet []byte, transactionID uint32) {
	// Minimum announce packet is 98 bytes
	if len(packet) < 98 {
		debug("announce request too short from %s", addr)
		tr.sendError(conn, addr, transactionID, "invalid packet size")
		return
	}

	// Parse the announce request fields
	infoHash := hex.EncodeToString(packet[16:36]) // 20 bytes - identifies which torrent
	peerID := hex.EncodeToString(packet[36:56])   // 20 bytes - identifies this specific client
	// Skip downloaded (bytes 56:64)
	left := binary.BigEndian.Uint64(packet[64:72]) // Bytes remaining to download
	// Skip uploaded (bytes 72:80)
	event := binary.BigEndian.Uint32(packet[80:84])  // What happened (started/completed/stopped)
	ipAddr := binary.BigEndian.Uint32(packet[84:88]) // Client's IP (0 = use packet source IP)
	// Skip key (bytes 88:92) - used by client to verify responses match requests
	numWant := binary.BigEndian.Uint32(packet[92:96]) // How many peers they want (max 50)
	port := binary.BigEndian.Uint16(packet[96:98])    // Port where they accept connections

	// Determine client's IP: use packet source IP by default
	// IPv4 clients can specify a custom IP (rare, for proxies/NAT)
	// IPv6 clients must use 0 for IP field - the 32-bit field can't hold a 128-bit address
	clientIP := addr.IP
	if ipAddr != 0 && addr.IP.To4() != nil {
		// IPv4 client specified their own IP - convert 32-bit integer to dotted notation
		clientIP = net.ParseIP(fmt.Sprintf("%d.%d.%d.%d",
			byte(ipAddr>>24), byte(ipAddr>>16), byte(ipAddr>>8), byte(ipAddr)))
	}

	debug("announce from %s: info_hash=%s peer_id=%s event=%d left=%d port=%d num_want=%d ip=%s",
		addr, infoHash, peerID, event, left, port, numWant, clientIP)

	// num_want=-1 means "default" (0xFFFFFFFF as uint32)
	// num_want=0 means "not specified"
	// Both cases should use the tracker's default of 50 peers
	// Also clamp anything above 50 to prevent excessive responses
	if numWant == 0 || numWant == 0xFFFFFFFF || numWant > 50 {
		numWant = 50
	}

	torrent := tr.getTorrent(infoHash)

	switch event {
	case eventStopped:
		// Client is leaving, remove them from the swarm
		torrent.removePeer(peerID)
	case eventCompleted:
		// Client finished downloading, they're now a seeder
		torrent.addPeer(peerID, clientIP, port, 0)
	case eventStarted, eventNone:
		// Client just started or is giving a regular update
		fallthrough
	default:
		torrent.addPeer(peerID, clientIP, port, int64(left))
	}

	// Get lists of IPv4 and IPv6 peers
	peers4, peers6, seeders, leechers := torrent.getPeersAndCount(peerID, int(numWant))

	// Return peers matching the client's IP type
	// IPv4 clients get IPv4 peers (6 bytes each), IPv6 clients get IPv6 peers (18 bytes each)
	var peers []byte
	if addr.IP.To4() != nil {
		// Client connected via IPv4 - return IPv4 peers
		peers = peers4
		debug("returning %d seeders, %d leechers, %d IPv4 peers",
			seeders, leechers, len(peers4)/6)
	} else {
		// Client connected via IPv6 - return IPv6 peers
		peers = peers6
		debug("returning %d seeders, %d leechers, %d IPv6 peers",
			seeders, leechers, len(peers6)/18)
	}

	// Build the announce response
	// Format: [action:4][transaction_id:4][interval:4][leechers:4][seeders:4][peers:variable]
	// Fixed header: 4 + 4 + 4 + 4 + 4 = 20 bytes, then variable length peer list
	response := make([]byte, 20+len(peers))
	// Bytes 0-3: action (4 bytes) - tells client this is an announce response
	binary.BigEndian.PutUint32(response[0:4], actionAnnounce)
	// Bytes 4-7: transaction_id (4 bytes) - echoed back so client can match response to request
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	// Bytes 8-11: interval (4 bytes) - seconds until client should announce again (600 = 10 minutes)
	binary.BigEndian.PutUint32(response[8:12], announceInterval)
	// Bytes 12-15: leechers (4 bytes) - number of people still downloading
	binary.BigEndian.PutUint32(response[12:16], uint32(leechers))
	// Bytes 16-19: seeders (4 bytes) - number of people with complete file
	binary.BigEndian.PutUint32(response[16:20], uint32(seeders))
	// Bytes 20+: peers (variable) - packed list of [IP:port] pairs for other peers to connect to
	copy(response[20:], peers)

	if _, err := conn.WriteToUDP(response, addr); err != nil {
		debug("failed to send announce response to %s: %v", addr, err)
	}
}

// handleScrape lets clients ask for statistics about torrents without announcing
// This is useful for checking if a torrent is active before downloading
// Scrape request format: [connection_id:8][action:4][transaction_id:4][info_hash:20]
// (can include multiple info_hashes)
func (tr *Tracker) handleScrape(conn *net.UDPConn, addr *net.UDPAddr, packet []byte, transactionID uint32) {
	// info_hashes start at byte 16, each is 20 bytes
	// Minimum valid packet: 16 byte header + 20 byte hash = 36 bytes
	if len(packet) < 36 {
		debug("scrape request too short from %s", addr)
		tr.sendError(conn, addr, transactionID, "no info hashes provided")
		return
	}

	// Count how many info_hashes are in the packet
	numHashes := (len(packet) - 16) / 20
	debug("scrape request from %s with %d hashes, transaction_id=%d", addr, numHashes, transactionID)

	// Build response dynamically using a buffer
	// Fixed header: 8 bytes (action + transaction_id)
	// Each hash stats: 12 bytes (seeders + completed + leechers)
	response := make([]byte, 8, 8+numHashes*12)
	binary.BigEndian.PutUint32(response[0:4], actionScrape)
	binary.BigEndian.PutUint32(response[4:8], transactionID)

	// Parse each info_hash and append stats to response
	// Start at byte 16, step by 20 bytes for each hash
	for hashOffset := 16; hashOffset+20 <= len(packet); hashOffset += 20 {
		infoHash := hex.EncodeToString(packet[hashOffset : hashOffset+20])

		torrent := tr.getTorrent(infoHash)
		torrent.mu.RLock()
		seeders := uint32(torrent.seeders)
		completed := uint32(0) // We don't track how many times torrent was completed
		leechers := uint32(torrent.leechers)
		torrent.mu.RUnlock()

		// Append 12 bytes of stats: seeders + completed + leechers
		response = binary.BigEndian.AppendUint32(response, seeders)
		response = binary.BigEndian.AppendUint32(response, completed)
		response = binary.BigEndian.AppendUint32(response, leechers)

		debug("scrape for %s: seeders=%d leechers=%d", infoHash, seeders, leechers)
	}

	if _, err := conn.WriteToUDP(response, addr); err != nil {
		debug("failed to send scrape response to %s: %v", addr, err)
	}
}

// sendError sends an error message back to the client when something goes wrong
// Error format: [action:4][transaction_id:4][error_message:variable]
// Fixed header: 4 + 4 = 8 bytes, then variable length error message
func (tr *Tracker) sendError(conn *net.UDPConn, addr *net.UDPAddr, transactionID uint32, message string) {
	response := make([]byte, 8+len(message))
	// Bytes 0-3: action (4 bytes) - tells client this is an error response
	binary.BigEndian.PutUint32(response[0:4], actionError)
	// Bytes 4-7: transaction_id (4 bytes) - echoed back so client can match response to request
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	// Bytes 8+: error_message (variable) - human-readable error description
	copy(response[8:], message)
	if _, err := conn.WriteToUDP(response, addr); err != nil {
		debug("failed to send error to %s: %v", addr, err)
	} else {
		debug("sent error to %s: %s", addr, message)
	}
}

// handlePacket processes any incoming UDP packet and routes it to the right handler
// All UDP tracker packets start with:
//
//	[connection_id:8] - identifies the session (or protocolID for connect)
//	[action:4] - what they want to do (connect/announce/scrape)
//	[transaction_id:4] - random number client sends, we echo it back (so they can match responses)
func (tr *Tracker) handlePacket(conn *net.UDPConn, addr *net.UDPAddr, packet []byte) {
	// All valid packets are at least 16 bytes (connection_id + action + transaction_id)
	if len(packet) < 16 {
		debug("packet too short (%d bytes) from %s", len(packet), addr)
		return
	}

	// Parse the common header that all packets have
	// All UDP tracker packets start with 16 bytes: [connection_id:8][action:4][transaction_id:4]
	// Bytes 0-7: connection_id (8 bytes) - session identifier or protocol magic number for connect
	connectionID := binary.BigEndian.Uint64(packet[0:8])
	// Bytes 8-11: action (4 bytes) - what the client wants (connect=0, announce=1, scrape=2, error=3)
	action := binary.BigEndian.Uint32(packet[8:12])
	// Bytes 12-15: transaction_id (4 bytes) - random number to match requests with responses
	transactionID := binary.BigEndian.Uint32(packet[12:16])

	debug("packet from %s: connection_id=%d action=%d transaction_id=%d",
		addr, connectionID, action, transactionID)

	switch action {
	case actionConnect:
		// Connect is special: it uses the fixed protocolID as the connection ID
		// to prove the client speaks the BitTorrent UDP tracker protocol
		if connectionID != protocolID {
			debug("invalid protocol ID from %s: %d", addr, connectionID)
			tr.sendError(conn, addr, transactionID, "invalid protocol ID")
			return
		}
		tr.handleConnect(conn, addr, transactionID)

	case actionAnnounce:
		// In a real implementation, we'd validate the connectionID here
		// to ensure this client actually connected properly
		tr.handleAnnounce(conn, addr, packet, transactionID)

	case actionScrape:
		tr.handleScrape(conn, addr, packet, transactionID)

	default:
		debug("unknown action %d from %s", action, addr)
		tr.sendError(conn, addr, transactionID, "unknown action")
	}
}

// cleanupLoop runs periodically to remove inactive torrents (those with no peers)
// Only torrents with zero peers are removed
func (tr *Tracker) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		tr.mu.Lock()
		removedCount := 0
		for hash, t := range tr.torrents {
			// Only remove torrents with no active peers
			if len(t.peers) == 0 {
				delete(tr.torrents, hash)
				removedCount++
				debug("cleanup: removed inactive torrent %s", hash)
			}
		}
		tr.mu.Unlock()
		if removedCount > 0 {
			debug("cleanup: removed %d inactive torrents", removedCount)
		}
	}
}

func main() {
	defaultPort := 1337
	if p, err := strconv.Atoi(os.Getenv("PICO_TRACKER__PORT")); err == nil && p > 0 {
		defaultPort = p
	}

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Pico Tracker: Portable BitTorrent Tracker (UDP)")
		flag.PrintDefaults()
	}

	port := flag.Int("port", defaultPort, "port to listen on")
	flag.BoolVar(&debugMode, "debug", debugMode, "enable debug logs")
	flag.Parse()

	tracker := &Tracker{torrents: make(map[string]*Torrent)}
	go tracker.cleanupLoop()

	// Listen on IPv4 (0.0.0.0 means all IPv4 interfaces)
	conn4, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: *port})
	if err != nil {
		log.Fatalf("[ERROR] Failed to listen on IPv4: %v", err)
	}
	defer conn4.Close()
	log.Printf("[INFO] UDP Tracker listening on 0.0.0.0:%d (IPv4)", *port)

	// Listen on IPv6 ([::] means all IPv6 interfaces)
	// IPv6 is optional - if it fails, the tracker still works with IPv4 only
	conn6, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::"), Port: *port})
	if err != nil {
		log.Printf("[WARN] IPv6 not available: %v", err)
	} else {
		defer conn6.Close()
		log.Printf("[INFO] UDP Tracker listening on [::]:%d (IPv6)", *port)
	}

	go tracker.listen(conn4)
	if conn6 != nil {
		go tracker.listen(conn6)
	}

	// Block forever - the goroutines handle all the work
	select {}
}

// listen handles incoming UDP packets on a connection
func (tr *Tracker) listen(conn *net.UDPConn) {
	buffer := make([]byte, maxPacketSize)

	for {
		// ReadFromUDP blocks until a packet arrives
		n, clientAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("[ERROR] Failed to read UDP packet: %v", err)
			continue
		}

		// Copy packet data since buffer is reused and handle in goroutine
		// This allows us to handle multiple clients concurrently
		packet := make([]byte, n)
		copy(packet, buffer[:n])

		go tr.handlePacket(conn, clientAddr, packet)
	}
}
