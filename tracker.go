package main

import (
	"encoding/binary"
	"math/rand"
	"net"
	"time"
)

// Error response buffer optimization constants
// Stack-allocate small error responses to avoid heap allocation
const (
	errorHeaderSize   = 8                                   // action:4 + transaction_id:4
	errorMaxStackSize = 128                                 // maximum stack buffer size for error responses
	errorMaxMsgLen    = errorMaxStackSize - errorHeaderSize // 120 bytes for message

	// Pre-computed rate limit cleanup threshold for zero-runtime-overhead cleanup
	rateLimitCleanupThreshold = rateLimitWindow * 2 // 2 windows are definitely stale
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
	window := rateLimitWindow

	tr.rateLimiterMu.Lock()

	rl, exists := tr.rateLimiter[key]
	if !exists {
		tr.rateLimiter[key] = &rateLimitEntry{count: 1, windowStart: time.Now()}
		tr.rateLimiterMu.Unlock()
		return true, 0
	}

	elapsed := time.Since(rl.windowStart)
	if elapsed >= window {
		rl.count = 1
		rl.windowStart = time.Now()
		tr.rateLimiterMu.Unlock()
		return true, 0
	}

	if rl.count < rateLimitBurst {
		rl.count++
		tr.rateLimiterMu.Unlock()
		return true, 0
	}

	tr.rateLimiterMu.Unlock()
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
	//nolint:gosec // G115: Port is 0-65535, safe to convert to uint16
	binary.BigEndian.PutUint16(key[16:18], uint16(addr.Port))
	return string(key[:])
}

func (tr *Tracker) getOrCreateTorrent(hash HashID) *Torrent {
	tr.mu.Lock()

	if t, ok := tr.torrents[hash]; ok {
		tr.mu.Unlock()
		return t
	}

	tr.torrents[hash] = &Torrent{peers: make(map[HashID]*Peer)}
	t := tr.torrents[hash]
	tr.mu.Unlock()
	info("created new torrent %s", hash.String())
	return t
}

func (tr *Tracker) getTorrent(hash HashID) *Torrent {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	return tr.torrents[hash]
}

func (t *Torrent) addPeer(id HashID, ip net.IP, port uint16, left uint64) {
	t.mu.Lock()

	if p, exists := t.peers[id]; exists {
		if p.Left == 0 && left > 0 {
			t.seeders--
			t.leechers++
			if debugEnabled.Load() {
				debug("peer %s became leecher @ %s:%d", id.String(), ip, port)
			}
		} else if p.Left > 0 && left == 0 {
			t.leechers--
			t.seeders++
			if !p.Completed {
				p.Completed = true
				t.completed++
				if debugEnabled.Load() {
					debug("peer %s completed torrent @ %s:%d", id.String(), ip, port)
				}
			}
		}
		p.IP, p.Port, p.Left = ip, port, left
		p.LastAnnounced = time.Now()
		t.mu.Unlock()
		return
	}

	peer := &Peer{IP: ip, Port: port, Left: left, LastAnnounced: time.Now()}
	if left == 0 {
		t.seeders++
		peer.Completed = true
		t.completed++ // peer starts as seeder (has full file) and counts as completed
		if debugEnabled.Load() {
			debug("new peer %s is seeder (completed) @ %s:%d", id.String(), ip, port)
		}
	} else {
		t.leechers++
	}
	t.peers[id] = peer
	t.mu.Unlock()
	if debugEnabled.Load() {
		debug("added peer %s @ %s:%d", id.String(), ip, port)
	}
}

func (t *Torrent) removePeer(id HashID) {
	t.mu.Lock()

	p, exists := t.peers[id]
	if !exists {
		t.mu.Unlock()
		return
	}

	if p.Left == 0 {
		t.seeders--
	} else {
		t.leechers--
	}
	delete(t.peers, id)
	t.mu.Unlock()
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

	//nolint:gosec // G404: math/rand acceptable for peer selection
	// (performance matters, cryptographic security not required)
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
	msgLen := len(message)
	totalSize := errorHeaderSize + msgLen

	var response []byte
	if msgLen <= errorMaxMsgLen {
		// Stack-allocate small errors to avoid heap allocation
		var buf [errorMaxStackSize]byte
		response = buf[:totalSize]
	} else {
		// Heap allocate only for large messages
		response = make([]byte, totalSize)
	}

	binary.BigEndian.PutUint32(response[0:4], actionError)
	binary.BigEndian.PutUint32(response[4:8], transactionID)
	copy(response[8:], message)

	if _, err := conn.WriteTo(response, addr); err != nil {
		info("failed to send error to %s: %v", addr, err)
	} else {
		debug("sent error to %s: %s", addr, message)
	}
}

// cleanupStalePeers removes stale peers and empty torrents, and cleans up rate limiters.
// Runs periodically (every 30 minutes) from cleanupLoop.
func (tr *Tracker) cleanupStalePeers() {
	peerDeadline := time.Now().Add(-stalePeerThreshold)
	rateLimiterDeadline := time.Now().Add(-rateLimitCleanupThreshold)

	// Phase 1: Clean peers and identify empty torrents
	emptyTorrents := tr.cleanupPeersAndFindEmpty(peerDeadline)

	// Phase 2: Remove empty torrents
	if len(emptyTorrents) > 0 {
		tr.removeEmptyTorrents(emptyTorrents)
	}

	// Phase 3: Clean rate limiters
	tr.cleanupRateLimiters(rateLimiterDeadline)
}

// cleanupPeersAndFindEmpty processes all torrents, removes stale peers,
// and returns list of torrent hashes that became empty.
func (tr *Tracker) cleanupPeersAndFindEmpty(deadline time.Time) []HashID {
	// Snapshot to allow concurrent access during cleanup
	tr.mu.RLock()
	hashes := make([]HashID, 0, len(tr.torrents))
	for h := range tr.torrents {
		hashes = append(hashes, h)
	}
	tr.mu.RUnlock()

	// Collect empty torrent hashes. No capacity pre-allocation to avoid assumptions
	// about empty ratio. Go's append handles growth efficiently for typical cases.
	var empty []HashID
	for _, hash := range hashes {
		if isEmpty := tr.cleanupSingleTorrent(hash, deadline); isEmpty {
			empty = append(empty, hash)
		}
	}
	return empty
}

// cleanupSingleTorrent removes stale peers from one torrent.
// Returns true if torrent is now empty.
func (tr *Tracker) cleanupSingleTorrent(hash HashID, deadline time.Time) bool {
	// Lock ordering: tracker -> torrent, then release tracker
	tr.mu.RLock()
	t, exists := tr.torrents[hash]
	if !exists {
		tr.mu.RUnlock()
		return false
	}
	t.mu.Lock()
	tr.mu.RUnlock()

	// Remove stale peers
	for id, p := range t.peers {
		if p.LastAnnounced.Before(deadline) {
			if p.Left == 0 {
				t.seeders--
			} else {
				t.leechers--
			}
			delete(t.peers, id)
			if debugEnabled.Load() {
				debug("cleanup: removed stale peer %s @ %s:%d", id.String(), p.IP, p.Port)
			}
		}
	}
	isEmpty := len(t.peers) == 0
	t.mu.Unlock()
	return isEmpty
}

// removeEmptyTorrents deletes torrents that are still empty after cleanup.
func (tr *Tracker) removeEmptyTorrents(empty []HashID) {
	tr.mu.Lock()
	for _, hash := range empty {
		if t, ok := tr.torrents[hash]; ok {
			t.mu.RLock()
			stillEmpty := len(t.peers) == 0
			t.mu.RUnlock()
			if stillEmpty {
				delete(tr.torrents, hash)
				if debugEnabled.Load() {
					debug("cleanup: removed inactive torrent %s", hash.String())
				}
			}
		}
	}
	tr.mu.Unlock()
}

// cleanupRateLimiters removes expired rate limiter entries.
func (tr *Tracker) cleanupRateLimiters(deadline time.Time) {
	tr.rateLimiterMu.Lock()
	for key, rl := range tr.rateLimiter {
		if !rl.windowStart.After(deadline) {
			delete(tr.rateLimiter, key)
		}
	}
	tr.rateLimiterMu.Unlock()
}

// cleanupLoop periodically runs cleanupStalePeers in a background goroutine
func (tr *Tracker) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		tr.cleanupStalePeers()
	}
}
