package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"time"
)

// Request handlers

// handleConnect is the first step in UDP tracker communication
// The client sends a "connect" request to establish a session, and we give them
// a connection ID they must use in all future requests to prove they're legitimate
// This prevents IP spoofing attacks where someone could fake announce requests
func (tr *Tracker) handleConnect(conn net.PacketConn, addr *net.UDPAddr, transactionID uint32) {
	debug("connect request from %s, transaction_id=%d", addr, transactionID)

	if allowed, remaining := tr.checkRateLimit(addr); !allowed {
		debug("rate limited connect request from %s, wait %v", addr, remaining)
		msg := fmt.Sprintf("rate limit exceeded, try again in %v", remaining.Round(time.Second))
		tr.sendError(conn, addr, transactionID, msg)
		return
	}

	connectionID := generateConnectionID(addr)

	// Connect response format: [action:4][transaction_id:4][connection_id:8]
	response := make([]byte, 16)
	binary.BigEndian.PutUint32(response[0:4], actionConnect)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	binary.BigEndian.PutUint64(response[8:16], connectionID)

	if _, err := conn.WriteTo(response, addr); err != nil {
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
	if len(packet) < 98 {
		debug("announce request too short from %s", addr)
		tr.sendError(conn, addr, transactionID, "invalid packet size")
		return
	}

	infoHash := NewHashID(packet[16:36])
	peerID := NewHashID(packet[36:56])
	// Skip downloaded (8 bytes)
	left := binary.BigEndian.Uint64(packet[64:72])
	// Skip uploaded (8 bytes)
	event := binary.BigEndian.Uint32(packet[80:84])
	ipAddr := binary.BigEndian.Uint32(packet[84:88])
	numWantRaw := binary.BigEndian.Uint32(packet[92:96])
	port := binary.BigEndian.Uint16(packet[96:98])

	if port == 0 {
		tr.sendError(conn, addr, transactionID, "port cannot be 0")
		return
	}

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
		// #nosec G115 -- numWantRaw is validated as <= maxWant before this
		if numWantRaw > uint32(maxWant) {
			numWant = maxWant
		} else {
			numWant = int(numWantRaw)
		}
	}

	// Determine client's IP: use packet source by default, but IPv4 clients can specify a custom IP
	clientIP := addr.IP
	if ipAddr != 0 && clientIsV4 {
		clientIP = net.IP{byte(ipAddr >> 24), byte(ipAddr >> 16), byte(ipAddr >> 8), byte(ipAddr)}
	}
	// IPv6 clients must send IP field as 0 (per BEP 15)
	if ipAddr != 0 && !clientIsV4 {
		tr.sendError(conn, addr, transactionID, "IP address must be 0 for IPv6")
		return
	}

	debug("announce from %s: info_hash=%s peer_id=%s event=%d left=%d port=%d num_want=%d ip=%s",
		addr, infoHash.String(), peerID.String(), event, left, port, numWant, clientIP)

	torrent := tr.getOrCreateTorrent(infoHash)

	switch event {
	case eventStopped:
		torrent.removePeer(peerID)
	case eventCompleted:
		torrent.addPeer(peerID, clientIP, port, 0)
	default:
		torrent.addPeer(peerID, clientIP, port, left)
	}

	peers, seeders, leechers := torrent.getPeers(peerID, numWant, clientIsV4, peerSize)
	debug("returning %d seeders, %d leechers, %d peers", seeders, leechers, len(peers)/peerSize)

	// Announce response format: [action:4][transaction_id:4][interval:4][leechers:4][seeders:4][peers:variable]
	// Fixed header: 4 + 4 + 4 + 4 + 4 = 20 bytes
	response := make([]byte, 20+len(peers))
	binary.BigEndian.PutUint32(response[0:4], actionAnnounce)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	interval := announceInterval * int(time.Minute/time.Second)
	//nolint:gosec // interval is bounded by constants
	binary.BigEndian.PutUint32(response[8:12], uint32(interval))
	//nolint:gosec // leechers/seeders are bounded counts
	binary.BigEndian.PutUint32(response[12:16], uint32(leechers))
	//nolint:gosec // seeders are bounded counts
	binary.BigEndian.PutUint32(response[16:20], uint32(seeders))
	copy(response[20:], peers)

	if _, err := conn.WriteTo(response, addr); err != nil {
		info("failed to send announce response to %s: %v", addr, err)
	}
}

// handleScrape lets clients ask for statistics about torrents without announcing
// This is useful for checking if a torrent is active before downloading
// Scrape header format: [connection_id:8][action:4][transaction_id:4][info_hash:20]
func (tr *Tracker) handleScrape(conn net.PacketConn, addr *net.UDPAddr, packet []byte, transactionID uint32) {
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
	scrapeResponseSize := 8 + numHashes*12
	response := make([]byte, scrapeResponseSize)
	binary.BigEndian.PutUint32(response[0:4], actionScrape)
	binary.BigEndian.PutUint32(response[4:8], transactionID)

	offset := 8
	for i := range numHashes {
		infoHash := NewHashID(packet[16+i*20 : 16+(i+1)*20])

		var seeders, completed, leechers uint32
		torrent := tr.getTorrent(infoHash)
		if torrent != nil {
			torrent.mu.RLock()
			//nolint:gosec // seeders/leechers/completed are bounded int counts
			seeders = uint32(torrent.seeders)
			//nolint:gosec // seeders/leechers/completed are bounded int counts
			completed = uint32(torrent.completed)
			//nolint:gosec // seeders/leechers/completed are bounded int counts
			leechers = uint32(torrent.leechers)
			torrent.mu.RUnlock()
		}

		binary.BigEndian.PutUint32(response[offset:offset+4], seeders)
		binary.BigEndian.PutUint32(response[offset+4:offset+8], completed)
		binary.BigEndian.PutUint32(response[offset+8:offset+12], leechers)
		offset += 12

		debug("scrape for %s: seeders=%d completed=%d leechers=%d", infoHash.String(), seeders, completed, leechers)
	}

	if _, err := conn.WriteTo(response, addr); err != nil {
		info("failed to send scrape response to %s: %v", addr, err)
	}
}

// handlePacket processes any incoming UDP packet and routes it to the right handler
// based on the action field. Connection ID validation is performed for announce/scrape
// Packet header format: [connection_id:8][action:4][transaction_id:4]
func (tr *Tracker) handlePacket(ctx context.Context, conn net.PacketConn, addr *net.UDPAddr, packet []byte) {
	if ctx.Err() != nil {
		return
	}

	if len(packet) < 16 {
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

		if n == 0 {
			putBuffer(readBuf)
			continue
		}

		packet := make([]byte, n)
		copy(packet, (*readBuf)[:n])
		putBuffer(readBuf)

		tr.wg.Add(1)
		go func(addr *net.UDPAddr, pkt []byte) {
			defer tr.wg.Done()
			tr.handlePacket(ctx, conn, addr, pkt)
		}(clientAddr, packet)
	}
}
