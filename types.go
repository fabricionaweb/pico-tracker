package main

import (
	"encoding/hex"
	"net"
	"sync"
	"time"
)

// HashID represents a 20-byte identifier (info_hash or peer_id)
// Per BEP 15, both info_hash and peer_id are exactly 20 bytes (SHA-1 hash length)
// Used as map keys to avoid 40-byte hex string overhead (saves 20 bytes per key)
type HashID [20]byte

// Caller must ensure b has at least 20 bytes (packet validation happens before this)
// If b > 20 bytes, only the first 20 are used
func NewHashID(b []byte) HashID {
	var h HashID
	copy(h[:], b)
	return h
}

func (h HashID) String() string {
	return hex.EncodeToString(h[:])
}

type Peer struct {
	IP            net.IP
	Port          uint16
	Left          uint64    // 0 = seeder, >0 = leecher
	Completed     bool      // true if peer has completed this torrent
	LastAnnounced time.Time // last time this peer announced (for stale cleanup)
}

type Torrent struct {
	mu        sync.RWMutex
	peers     map[HashID]*Peer // key is peer_id
	seeders   int
	leechers  int
	completed int // total completions (peers who finished downloading)
}

type Tracker struct {
	mu       sync.RWMutex
	torrents map[HashID]*Torrent // key is info_hash
	wg       sync.WaitGroup      // tracks in-flight request handlers

	rateLimiterMu sync.Mutex
	rateLimiter   map[string]*rateLimitEntry // key is "IP:port"
}

type rateLimitEntry struct {
	count       int       // requests in current window
	windowStart time.Time // start of current 2-minute window
}
