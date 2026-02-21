package main

import (
	"encoding/hex"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// HashID represents a 20-byte identifier (info_hash or peer_id)
// Per BEP 15, both info_hash and peer_id are exactly 20 bytes (SHA-1 hash length)
// Used as map keys to avoid 40-byte hex string overhead (saves 20 bytes per key)
type HashID [20]byte

// NewHashID creates a HashID from a byte slice.
// Caller must ensure b has at least 20 bytes (packet validation happens before this).
// If b > 20 bytes, only the first 20 are used.
func NewHashID(b []byte) HashID {
	var h HashID
	copy(h[:], b)
	return h
}

func (h HashID) String() string {
	return hex.EncodeToString(h[:])
}

type Peer struct {
	LastAnnounced time.Time
	IP            net.IP
	Left          uint64
	Port          uint16
	Completed     bool
}

type Torrent struct {
	peers     map[HashID]*Peer
	mu        sync.RWMutex
	seeders   int
	leechers  int
	completed int
}

type Tracker struct {
	torrents      map[HashID]*Torrent
	rateLimiter   map[string]*rateLimitEntry
	whitelist     atomic.Pointer[map[HashID]struct{}]
	secret        [32]byte
	mu            sync.RWMutex
	rateLimiterMu sync.Mutex
	wg            sync.WaitGroup
}

type rateLimitEntry struct {
	windowStart time.Time
	count       int
}
