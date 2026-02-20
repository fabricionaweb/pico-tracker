package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"net"
	"sync"
	"time"
)

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

	rateLimitWindow = 2  // (minutes) window duration for rate limiting
	rateLimitBurst  = 10 // max connect requests per rateLimitWindow

	connectionExpiration = 2 * time.Minute // per BEP 15
)

// hmacPool pools HMAC hashers to eliminate allocations in hot path
var hmacPool = sync.Pool{
	New: func() any {
		return hmac.New(sha256.New, nil)
	},
}

// getHMAC returns a pooled HMAC hasher with the secret already set
func getHMAC(secret []byte) hash.Hash {
	//nolint:errcheck // HMAC pool always returns hash.Hash
	mac := hmacPool.Get().(hash.Hash)
	mac.Reset()
	mac.Write(secret)
	return mac
}

// putHMAC returns an HMAC hasher to the pool
func putHMAC(mac hash.Hash) {
	hmacPool.Put(mac)
}

// bufferPool reuses 1500-byte read buffers to reduce allocations and GC pressure
var bufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, maxPacketSize)
		return &buf
	},
}

func getBuffer() *[]byte {
	//nolint:errcheck // Buffer pool always returns *[]byte
	buf := bufferPool.Get().(*[]byte)
	// Restore full capacity - putBuffer resets the slice to [:0] for the pool,
	// but net.UDPConn.ReadFromUDP respects len(buf) not cap(buf). Without this,
	// reading into the buffer would see length 0 and return n=0.
	*buf = (*buf)[:cap(*buf)]
	return buf
}

func putBuffer(buf *[]byte) {
	*buf = (*buf)[:0]
	bufferPool.Put(buf)
}

// peerSlicePool reuses peerInfo slices for getPeers to reduce allocations
// Maximum capacity is maxPeersPerPacketV4 (200) which covers typical use
var peerSlicePool = sync.Pool{
	New: func() any {
		s := make([]peerInfo, 0, maxPeersPerPacketV4)
		return &s
	},
}

func getPeerSlice() *[]peerInfo {
	//nolint:errcheck // Peer slice pool always returns *[]peerInfo
	return peerSlicePool.Get().(*[]peerInfo)
}

func putPeerSlice(s *[]peerInfo) {
	*s = (*s)[:0]
	peerSlicePool.Put(s)
}

// Connection ID generation and validation

// generateConnectionID creates a stateless connection ID using syn-cookie approach
// Connection ID format: [32-bit timestamp][32-bit signature]
// Signature = HMAC-SHA256(secret, client_ip + timestamp)[0:4]
func generateConnectionID(addr *net.UDPAddr, secret []byte) uint64 {
	//nolint:gosec // Intentionally truncating to 32-bit for protocol
	timestamp := uint32(time.Now().Unix())

	mac := getHMAC(secret)
	defer putHMAC(mac)

	mac.Write(addr.IP.To16())
	var tsBytes [4]byte
	binary.BigEndian.PutUint32(tsBytes[:], timestamp)
	mac.Write(tsBytes[:])
	sig := binary.BigEndian.Uint32(mac.Sum(nil)[:4])

	return uint64(timestamp)<<32 | uint64(sig)
}

// validateConnectionID verifies the syn-cookie signature and checks expiration
func validateConnectionID(id uint64, addr *net.UDPAddr, secret []byte) bool {
	//nolint:gosec // Intentionally extracting lower 32 bits for protocol
	timestamp := uint32(id >> 32)

	if time.Since(time.Unix(int64(timestamp), 0)) > connectionExpiration {
		return false
	}

	mac := getHMAC(secret)
	defer putHMAC(mac)

	mac.Write(addr.IP.To16())
	var tsBytes [4]byte
	binary.BigEndian.PutUint32(tsBytes[:], timestamp)
	mac.Write(tsBytes[:])
	expected := binary.BigEndian.Uint32(mac.Sum(nil)[:4])

	//nolint:gosec,gocritic // Intentionally comparing lower 32 bits
	return uint32(id) == expected
}
