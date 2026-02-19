package main

import (
	"encoding/binary"
	"math/rand"
	"net"
	"time"
)

// Tracker methods

// peerInfo is a lightweight struct for copying peer data out of locks
type peerInfo struct {
	ip   net.IP
	port uint16
}

// checkRateLimit enforces per-IP rate limiting on connect requests using a sliding window
// Returns (allowed, timeRemaining) - timeRemaining is 0 if allowed, otherwise the duration
// until the client can retry. This prevents UDP amplification attacks
func (tr *Tracker) checkRateLimit(addr *net.UDPAddr) (allowed bool, timeRemaining time.Duration) {
	key := MakeRateLimitKey(addr)
	window := rateLimitWindow * time.Minute

	tr.rateLimiterMu.Lock()
	defer tr.rateLimiterMu.Unlock()

	rl, exists := tr.rateLimiter[key]
	if !exists {
		rl = &rateLimitEntry{count: 1, windowStart: time.Now()}
		tr.rateLimiter[key] = rl
		return true, 0
	}

	elapsed := time.Since(rl.windowStart)
	if elapsed >= window {
		rl.count = 1
		rl.windowStart = time.Now()
		return true, 0
	}

	if rl.count < rateLimitBurst {
		rl.count++
		return true, 0
	}

	return false, window - elapsed
}

// MakeRateLimitKey creates an efficient string key from UDPAddr without allocations
// Format: 16 bytes of IP (padded) + 2 bytes of port as string
// Exported for use in tests.
func MakeRateLimitKey(addr *net.UDPAddr) string {
	// For IPv4, To16() gives us 16 bytes; for IPv6 it's already 16 bytes
	ip := addr.IP.To16()
	if ip == nil {
		ip = net.IPv6zero
	}

	// Build key: 16 bytes IP + 2 bytes port = 18 bytes
	var key [18]byte
	copy(key[:16], ip)
	binary.BigEndian.PutUint16(key[16:18], uint16(addr.Port))
	return string(key[:])
}

func (tr *Tracker) getOrCreateTorrent(hash HashID) *Torrent {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if _, ok := tr.torrents[hash]; !ok {
		tr.torrents[hash] = &Torrent{peers: make(map[HashID]*Peer)}
		info("created new torrent %s", hash.String())
	}

	return tr.torrents[hash]
}

func (tr *Tracker) getTorrent(hash HashID) *Torrent {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	return tr.torrents[hash]
}

func (t *Torrent) addPeer(id HashID, ip net.IP, port uint16, left uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if p, exists := t.peers[id]; exists {
		if p.Left == 0 && left > 0 {
			t.seeders--
			t.leechers++
			debug("peer %s became leecher @ %s:%d", id.String(), ip, port)
		} else if p.Left > 0 && left == 0 {
			t.leechers--
			t.seeders++
			if !p.Completed {
				p.Completed = true
				t.completed++
				debug("peer %s completed torrent @ %s:%d", id.String(), ip, port)
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
		t.completed++ // peer starts as seeder (has full file) and counts as completed
		debug("new peer %s is seeder (completed) @ %s:%d", id.String(), ip, port)
	} else {
		t.leechers++
	}
	t.peers[id] = peer
	debug("added peer %s @ %s:%d", id.String(), ip, port)
}

func (t *Torrent) removePeer(id HashID) {
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
	info("removed peer %s @ %s:%d", id.String(), p.IP, p.Port)
}

// getPeers returns a list of peers for a client to connect to
// Returns up to numWant peers matching the client's IP version (not including requesting peer)
func (t *Torrent) getPeers(
	exclude HashID, numWant int, clientIsV4 bool, peerSize int,
) (peers []byte, seeders, leechers int) {
	t.mu.RLock()
	seeders, leechers = t.seeders, t.leechers

	// Get pooled slice to collect matching peers
	allPeersPtr := getPeerSlice()
	allPeers := *allPeersPtr

	// Collect all matching peers
	for id, p := range t.peers {
		if id != exclude && (p.IP.To4() != nil) == clientIsV4 {
			allPeers = append(allPeers, peerInfo{ip: p.IP, port: p.Port})
		}
	}
	t.mu.RUnlock()

	// Update pointer in case slice was reallocated
	*allPeersPtr = allPeers

	if len(allPeers) == 0 {
		putPeerSlice(allPeersPtr)
		return nil, seeders, leechers
	}

	// Randomly select peers with a starting offset for fair distribution
	numPeers := min(numWant, len(allPeers))
	peers = make([]byte, 0, numPeers*peerSize)

	//nolint:gosec // crypto/rand not needed for peer selection
	start := rand.Intn(len(allPeers))
	for i := range numPeers {
		p := allPeers[(start+i)%len(allPeers)]
		if clientIsV4 {
			peers = append(peers, p.ip.To4()...)
		} else {
			peers = append(peers, p.ip.To16()...)
		}
		peers = binary.BigEndian.AppendUint16(peers, p.port)
	}

	putPeerSlice(allPeersPtr)
	return peers, seeders, leechers
}

// sendError sends an error message back to the client when something goes wrong
// Error response format: [action:4][transaction_id:4][error_message:variable]
// Fixed header: 4 + 4 = 8 bytes
func (tr *Tracker) sendError(conn net.PacketConn, addr *net.UDPAddr, transactionID uint32, message string) {
	response := make([]byte, 8+len(message))
	binary.BigEndian.PutUint32(response[0:4], actionError)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	copy(response[8:], message)
	if _, err := conn.WriteTo(response, addr); err != nil {
		info("failed to send error to %s: %v", addr, err)
	} else {
		debug("sent error to %s: %s", addr, message)
	}
}

// cleanupStalePeers removes peers that haven't announced recently and deletes
// torrents that become empty. It also cleans up stale rate limiter entries.
func (tr *Tracker) cleanupStalePeers() {
	staleDeadline := time.Now().Add(-stalePeerThreshold * time.Minute)

	tr.mu.Lock()
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
				debug("cleanup: removed stale peer %s @ %s:%d", id.String(), p.IP, p.Port)
			}
		}
		if len(t.peers) == 0 {
			delete(tr.torrents, hash)
			debug("cleanup: removed inactive torrent %s", hash.String())
		}
		t.mu.Unlock()
	}
	tr.mu.Unlock()

	tr.rateLimiterMu.Lock()
	rateLimitStaleDeadline := time.Now().Add(-rateLimitWindow * 2 * time.Minute) // 2 windows are definitely stale
	for key, rl := range tr.rateLimiter {
		if rl.windowStart.Before(rateLimitStaleDeadline) {
			delete(tr.rateLimiter, key)
		}
	}
	tr.rateLimiterMu.Unlock()
}

// cleanupLoop periodically runs cleanupStalePeers in a background goroutine
func (tr *Tracker) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		tr.cleanupStalePeers()
	}
}
