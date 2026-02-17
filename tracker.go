package main

import (
	"encoding/binary"
	"math/rand"
	"net"
	"time"
)

// Tracker methods

// checkRateLimit enforces per-IP rate limiting on connect requests using a sliding window
// Returns (allowed, timeRemaining) - timeRemaining is 0 if allowed, otherwise the duration
// until the client can retry. This prevents UDP amplification attacks
func (tr *Tracker) checkRateLimit(addr *net.UDPAddr) (allowed bool, timeRemaining time.Duration) {
	key := addr.String()
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

func (t *Tracker) getOrCreateTorrent(hash HashID) *Torrent {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.torrents[hash]; !ok {
		t.torrents[hash] = &Torrent{peers: make(map[HashID]*Peer)}
		info("created new torrent %s", hash.String())
	}

	return t.torrents[hash]
}

func (t *Tracker) getTorrent(hash HashID) *Torrent {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.torrents[hash]
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
func (t *Torrent) getPeers(exclude HashID, numWant int, clientIP net.IP) (peers []byte, seeders, leechers int) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	seeders, leechers = t.seeders, t.leechers
	wantV4Peers := clientIP.To4() != nil

	// Collect matching peers in single pass
	var peerIDs []HashID
	for id, p := range t.peers {
		if id == exclude {
			continue
		}
		isV4Peer := p.IP.To4() != nil
		if isV4Peer == wantV4Peers {
			peerIDs = append(peerIDs, id)
		}
	}

	if len(peerIDs) == 0 {
		return nil, seeders, leechers
	}

	// Limit to available peers
	numPeers := numWant
	if numPeers > len(peerIDs) {
		numPeers = len(peerIDs)
	}

	// Start at random offset for fair peer distribution across clients
	start := rand.Intn(len(peerIDs))
	for i := 0; i < numPeers; i++ {
		idx := (start + i) % len(peerIDs)
		p := t.peers[peerIDs[idx]]

		if wantV4Peers {
			peers = append(peers, p.IP.To4()...)
		} else {
			peers = append(peers, p.IP.To16()...)
		}
		peers = binary.BigEndian.AppendUint16(peers, p.Port)
	}

	return peers, seeders, leechers
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

// cleanupLoop periodically removes peers that haven't announced recently,
// deletes torrents that become empty, and cleans up stale rate limiter entries
func (tr *Tracker) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval * time.Minute)
	defer ticker.Stop()

	staleThreshold := stalePeerThreshold * time.Minute
	rateLimitStaleThreshold := rateLimitWindow * 2 * time.Minute // 2 windows are definitely stale
	var rateLimitStaleDeadline time.Time

	for range ticker.C {
		// clean peers (read lock allows concurrent access)
		staleDeadline := time.Now().Add(-staleThreshold)
		tr.mu.RLock()

		var toDelete []HashID
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
						id.String(), p.IP, p.Port, time.Since(p.LastAnnounced).Round(time.Minute))
				}
			}

			if len(t.peers) == 0 {
				toDelete = append(toDelete, hash)
			}
			t.mu.Unlock()
		}
		tr.mu.RUnlock()

		// delete empty torrents (write lock, re-check to avoid race)
		removedTorrents := 0
		if len(toDelete) > 0 {
			tr.mu.Lock()
			for _, hash := range toDelete {
				if t, ok := tr.torrents[hash]; ok {
					t.mu.Lock()
					if len(t.peers) == 0 {
						delete(tr.torrents, hash)
						removedTorrents++
						debug("cleanup: removed inactive torrent %s", hash.String())
					}
					t.mu.Unlock()
				}
			}
			tr.mu.Unlock()
		}

		// clean rate limiter
		tr.rateLimiterMu.Lock()
		rateLimitStaleDeadline = time.Now().Add(-rateLimitStaleThreshold)
		removedRateLimits := 0
		for key, rl := range tr.rateLimiter {
			if rl.windowStart.Before(rateLimitStaleDeadline) {
				delete(tr.rateLimiter, key)
				removedRateLimits++
			}
		}
		tr.rateLimiterMu.Unlock()

		if removedPeers > 0 || removedTorrents > 0 || removedRateLimits > 0 {
			info("cleanup: removed %d stale peers, %d inactive torrents, %d rate limit entries",
				removedPeers, removedTorrents, removedRateLimits)
		}
	}
}
