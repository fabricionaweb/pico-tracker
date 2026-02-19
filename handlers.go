package main

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"time"
)

// Response buffer optimization constants
// Stack-allocate small responses to avoid heap allocation
const (
	// Connect response: action:4 + transaction_id:4 + connection_id:8
	connectResponseSize = 4 + 4 + 8 // 16 bytes

	// Announce request minimum size (sum of all fields):
	// connection_id:8 + action:4 + transaction_id:4 + info_hash:20 + peer_id:20 +
	// downloaded:8 + left:8 + uploaded:8 + event:4 + IP:4 + key:4 + num_want:4 + port:2
	minAnnouncePacketSize = 98

	// Packet header size: connection_id:8 + action:4 + transaction_id:4
	packetHeaderSize = 16

	// Minimum scrape packet size: connection_id:8 + action:4 + transaction_id:4 + info_hash:20
	minScrapePacketSize = 36

	announceHeaderSize      = 20 // action:4 + transaction_id:4 + interval:4 + leechers:4 + seeders:4
	maxStackAnnouncePeersV4 = 20 // 20 * 6 = 120 bytes, total with header = 140
	maxStackAnnouncePeersV6 = 10 // 10 * 18 = 180 bytes, total with header = 200
	maxStackAnnounceSizeV4  = announceHeaderSize + maxStackAnnouncePeersV4*6
	maxStackAnnounceSizeV6  = announceHeaderSize + maxStackAnnouncePeersV6*18

	scrapeHeaderSize     = 8  // action:4 + transaction_id:4
	scrapeEntrySize      = 12 // seeders:4 + completed:4 + leechers:4
	maxStackScrapeHashes = 10 // 10 * 12 = 120 bytes, total with header = 128
)

// announceRequest holds the parsed fields from an announce request packet.
type announceRequest struct {
	infoHash HashID
	peerID   HashID
	left     uint64
	event    uint32
	ipAddr   uint32
	numWant  uint32
	port     uint16
}

// scrapeStats holds the statistics for a single torrent in a scrape response.
type scrapeStats struct {
	seeders   uint32
	completed uint32
	leechers  uint32
}

// parseAnnounceRequest extracts all fields from an announce request packet.
// Returns the request and true if valid, or zero values and false if packet too short.
func parseAnnounceRequest(packet []byte) (announceRequest, bool) {
	if len(packet) < minAnnouncePacketSize {
		return announceRequest{}, false
	}
	return announceRequest{
		infoHash: NewHashID(packet[16:36]),
		peerID:   NewHashID(packet[36:56]),
		left:     binary.BigEndian.Uint64(packet[64:72]),
		event:    binary.BigEndian.Uint32(packet[80:84]),
		ipAddr:   binary.BigEndian.Uint32(packet[84:88]),
		numWant:  binary.BigEndian.Uint32(packet[92:96]),
		port:     binary.BigEndian.Uint16(packet[96:98]),
	}, true
}

// calculateNumWant determines the number of peers to return based on client request.
func calculateNumWant(numWantRaw uint32, maxWant int) int {
	// num_want 0 or 0xFFFFFFFF (-1 but we have it unsigned 32bit) means "default"
	if numWantRaw == 0 || numWantRaw == 0xFFFFFFFF {
		return defaultNumWant
	}
	// #nosec G115 -- numWantRaw is validated as <= maxWant
	if numWantRaw > uint32(maxWant) {
		return maxWant
	}
	return int(numWantRaw)
}

// determineClientIP extracts the client's IP from the announce request.
// Returns the client IP, whether it's valid, and an error message if invalid.
func determineClientIP(addr *net.UDPAddr, ipAddr uint32) (clientIP net.IP, isValid bool, errMsg string) {
	clientIsV4 := addr.IP.To4() != nil
	clientIP = addr.IP

	if ipAddr != 0 {
		if clientIsV4 {
			clientIP = net.IP{byte(ipAddr >> 24), byte(ipAddr >> 16), byte(ipAddr >> 8), byte(ipAddr)}
		} else {
			// IPv6 clients must send IP field as 0 (per BEP 15)
			return nil, false, "IP address must be 0 for IPv6"
		}
	}

	return clientIP, true, ""
}

// getPeerConfig returns the peer size and max peers based on IP version.
func getPeerConfig(clientIsV4 bool) (peerSize, maxWant int) {
	if clientIsV4 {
		return 6, maxPeersPerPacketV4
	}
	return 18, maxPeersPerPacketV6
}

// updateTorrentPeer updates the torrent based on the announce event.
func updateTorrentPeer(torrent *Torrent, peerID HashID, clientIP net.IP, port uint16, event uint32, left uint64) {
	switch event {
	case eventStopped:
		torrent.removePeer(peerID)
	case eventCompleted:
		torrent.addPeer(peerID, clientIP, port, 0)
	default:
		torrent.addPeer(peerID, clientIP, port, left)
	}
}

// buildAnnounceResponse creates the announce response buffer.
func buildAnnounceResponse(peers []byte, seeders, leechers int, transactionID uint32, clientIsV4 bool) []byte {
	responseSize := announceHeaderSize + len(peers)
	var response []byte
	switch {
	case clientIsV4 && responseSize <= maxStackAnnounceSizeV4:
		var buf [maxStackAnnounceSizeV4]byte
		response = buf[:responseSize]
	case !clientIsV4 && responseSize <= maxStackAnnounceSizeV6:
		var buf [maxStackAnnounceSizeV6]byte
		response = buf[:responseSize]
	default:
		response = make([]byte, responseSize)
	}

	binary.BigEndian.PutUint32(response[0:4], actionAnnounce)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	interval := announceInterval * int(time.Minute/time.Second)
	//nolint:gosec // interval is bounded by constants
	binary.BigEndian.PutUint32(response[8:12], uint32(interval))
	//nolint:gosec // leechers/seeders are bounded counts
	binary.BigEndian.PutUint32(response[12:16], uint32(leechers))
	//nolint:gosec // seeders are bounded counts
	binary.BigEndian.PutUint32(response[16:20], uint32(seeders))
	// Fast copy: only copy if there are peers (avoids slice bounds check in empty case)
	if len(peers) > 0 {
		copy(response[20:], peers)
	}

	return response
}

// getScrapeStats retrieves stats for a single info_hash.
func (tr *Tracker) getScrapeStats(infoHash HashID) scrapeStats {
	var s scrapeStats
	torrent := tr.getTorrent(infoHash)
	if torrent != nil {
		torrent.mu.RLock()
		//nolint:gosec // seeders/leechers/completed are bounded int counts
		s.seeders = uint32(torrent.seeders)
		//nolint:gosec // seeders/leechers/completed are bounded int counts
		s.completed = uint32(torrent.completed)
		//nolint:gosec // seeders/leechers/completed are bounded int counts
		s.leechers = uint32(torrent.leechers)
		torrent.mu.RUnlock()
	}
	return s
}

// buildScrapeResponse creates the scrape response buffer.
// Returns the response bytes and the number of hashes processed.
func (tr *Tracker) buildScrapeResponse(packet []byte, transactionID uint32) (response []byte, numHashes int) {
	// info_hashes starts at byte 16, each is 20 bytes
	numHashes = (len(packet) - 16) / 20

	responseSize := scrapeHeaderSize + numHashes*scrapeEntrySize
	if numHashes <= maxStackScrapeHashes {
		var buf [scrapeHeaderSize + maxStackScrapeHashes*scrapeEntrySize]byte
		response = buf[:responseSize]
	} else {
		response = make([]byte, responseSize)
	}

	binary.BigEndian.PutUint32(response[0:4], actionScrape)
	binary.BigEndian.PutUint32(response[4:8], transactionID)

	// Process all hashes in a single pass with pre-calculated offsets
	dataOffset := scrapeHeaderSize
	for i := range numHashes {
		infoHash := NewHashID(packet[16+i*20 : 16+(i+1)*20])

		var s scrapeStats
		if tr.isWhitelisted(infoHash) {
			s = tr.getScrapeStats(infoHash)
			debug("scrape for %s: seeders=%d completed=%d leechers=%d",
				infoHash.String(), s.seeders, s.completed, s.leechers)
		} else {
			debug("scrape filtered: info_hash %s not whitelisted", infoHash.String())
		}

		// Write directly to final buffer location to avoid intermediate allocations
		binary.BigEndian.PutUint32(response[dataOffset:dataOffset+4], s.seeders)
		binary.BigEndian.PutUint32(response[dataOffset+4:dataOffset+8], s.completed)
		binary.BigEndian.PutUint32(response[dataOffset+8:dataOffset+12], s.leechers)
		dataOffset += scrapeEntrySize
	}

	return response, numHashes
}

// Request handlers

// handleConnect is the first step in UDP tracker communication
// The client sends a "connect" request to establish a session, and we give them
// a connection ID they must use in all future requests to prove they're legitimate
// This prevents IP spoofing attacks where someone could fake announce requests
func (tr *Tracker) handleConnect(conn net.PacketConn, addr *net.UDPAddr, transactionID uint32) {
	debug("connect request from %s, transaction_id=%d", addr, transactionID)

	if allowed, remaining := tr.checkRateLimit(addr); !allowed {
		debug("rate limited connect request from %s, wait %v", addr, remaining)
		tr.sendError(conn, addr, transactionID, "rate limit exceeded, try again later")
		return
	}

	connectionID := generateConnectionID(addr)

	// Connect response format: [action:4][transaction_id:4][connection_id:8]
	// Stack-allocate fixed size response to avoid heap allocation
	var response [connectResponseSize]byte
	binary.BigEndian.PutUint32(response[0:4], actionConnect)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	binary.BigEndian.PutUint64(response[8:16], connectionID)

	if _, err := conn.WriteTo(response[:], addr); err != nil {
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
func (tr *Tracker) handleAnnounce(conn net.PacketConn, addr *net.UDPAddr, packet []byte, transactionID uint32) {
	req, ok := parseAnnounceRequest(packet)
	if !ok {
		debug("announce request too short from %s", addr)
		tr.sendError(conn, addr, transactionID, "invalid packet size")
		return
	}

	if !tr.isWhitelisted(req.infoHash) {
		info("announce rejected: info_hash %s not whitelisted from %s", req.infoHash.String(), addr)
		tr.sendError(conn, addr, transactionID, "torrent not authorized")
		return
	}

	if req.port == 0 {
		tr.sendError(conn, addr, transactionID, "port cannot be 0")
		return
	}

	clientIsV4 := addr.IP.To4() != nil
	peerSize, maxWant := getPeerConfig(clientIsV4)
	clientIP, valid, errMsg := determineClientIP(addr, req.ipAddr)
	if !valid {
		tr.sendError(conn, addr, transactionID, errMsg)
		return
	}

	numWant := calculateNumWant(req.numWant, maxWant)
	debug("announce from %s: info_hash=%s peer_id=%s event=%d left=%d port=%d num_want=%d ip=%s",
		addr, req.infoHash.String(), req.peerID.String(), req.event, req.left, req.port, numWant, clientIP)

	torrent := tr.getOrCreateTorrent(req.infoHash)
	updateTorrentPeer(torrent, req.peerID, clientIP, req.port, req.event, req.left)

	peers, seeders, leechers := torrent.getPeers(req.peerID, numWant, clientIsV4, peerSize)
	debug("returning %d seeders, %d leechers, %d peers", seeders, leechers, len(peers)/peerSize)

	response := buildAnnounceResponse(peers, seeders, leechers, transactionID, clientIsV4)
	if _, err := conn.WriteTo(response, addr); err != nil {
		info("failed to send announce response to %s: %v", addr, err)
	}
}

// handleScrape lets clients ask for statistics about torrents without announcing
// This is useful for checking if a torrent is active before downloading
// Scrape header format: [connection_id:8][action:4][transaction_id:4][info_hash:20]
func (tr *Tracker) handleScrape(conn net.PacketConn, addr *net.UDPAddr, packet []byte, transactionID uint32) {
	if len(packet) < minScrapePacketSize {
		debug("scrape request too short from %s", addr)
		tr.sendError(conn, addr, transactionID, "no info hashes provided")
		return
	}

	response, numHashes := tr.buildScrapeResponse(packet, transactionID)
	debug("scrape request from %s with %d hashes, transaction_id=%d", addr, numHashes, transactionID)

	if _, err := conn.WriteTo(response, addr); err != nil {
		info("failed to send scrape response to %s: %v", addr, err)
	}
}

// handlePacket processes any incoming UDP packet and routes it to the right handler
// based on the action field. Connection ID validation is performed for announce/scrape
// Packet header format: [connection_id:8][action:4][transaction_id:4]
func (tr *Tracker) handlePacket(conn net.PacketConn, addr *net.UDPAddr, packet []byte) {
	if len(packet) < packetHeaderSize {
		debug("packet too short (%d bytes) from %s", len(packet), addr)
		return
	}

	connectionID := binary.BigEndian.Uint64(packet[0:8])
	action := binary.BigEndian.Uint32(packet[8:12])
	transactionID := binary.BigEndian.Uint32(packet[12:16])

	// Don't need to debug everything from loopback, reduce spam (healthcheck)
	fromLoopback := addr.IP.IsLoopback()
	if !fromLoopback {
		debug("packet from %s: connection_id=%d action=%d transaction_id=%d",
			addr, connectionID, action, transactionID)
	}

	switch action {
	case actionConnect:
		if connectionID != protocolID {
			debug("invalid protocol ID from %s: %d", addr, connectionID)
			tr.sendError(conn, addr, transactionID, "invalid protocol ID")
			return
		}
		tr.handleConnect(conn, addr, transactionID)

	case actionAnnounce, actionScrape:
		if !validateConnectionID(connectionID, addr) {
			debug("invalid or expired connection ID from %s: %d", addr, connectionID)
			tr.sendError(conn, addr, transactionID, "invalid connection ID")
			return
		}
		if action == actionAnnounce {
			tr.handleAnnounce(conn, addr, packet, transactionID)
		} else {
			tr.handleScrape(conn, addr, packet, transactionID)
		}

	default:
		// tr.sendError will add debug and skip debug for loopback (healthcheck)
		if fromLoopback {
			if _, err := conn.WriteTo([]byte("unknown action\n"), addr); err != nil {
				debug("failed to respond to loopback: %v", err)
			}
			return
		}

		debug("unknown action %d from %s", action, addr)
		tr.sendError(conn, addr, transactionID, "unknown action")
	}
}

// listen reads incoming UDP packets and dispatches them to handlers in goroutines
func (tr *Tracker) listen(ctx context.Context, conn *net.UDPConn) {
	for {
		readBuf := getBuffer()

		n, clientAddr, err := conn.ReadFromUDP(*readBuf)
		if err != nil {
			putBuffer(readBuf)
			if ctx.Err() != nil {
				return
			}
			log.Printf("[ERROR] Failed to read UDP packet: %v", err)
			continue
		}

		// Resize slice to actual data size
		*readBuf = (*readBuf)[:n]

		tr.wg.Add(1)
		go func(addr *net.UDPAddr, buf *[]byte) {
			defer tr.wg.Done()
			defer putBuffer(buf)
			tr.handlePacket(conn, addr, *buf)
		}(clientAddr, readBuf)
	}
}
