package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"time"
)

var secretKey [32]byte // secret for syn-cookie connection ID signing (prevents IP spoofing)

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
)

// Connection ID generation and validation

// generateConnectionID creates a stateless connection ID using syn-cookie approach
// Connection ID format: [32-bit timestamp][32-bit signature]
// Signature = HMAC-SHA256(secret_key, client_ip + timestamp)[0:4]
func generateConnectionID(addr *net.UDPAddr) uint64 {
	timestamp := uint32(time.Now().Unix())
	mac := hmac.New(sha256.New, secretKey[:])
	mac.Write(addr.IP.To16())
	var tsBytes [4]byte
	binary.BigEndian.PutUint32(tsBytes[:], timestamp)
	mac.Write(tsBytes[:])
	sig := binary.BigEndian.Uint32(mac.Sum(nil)[:4])

	return uint64(timestamp)<<32 | uint64(sig)
}

// validateConnectionID verifies the syn-cookie signature and checks expiration
func validateConnectionID(id uint64, addr *net.UDPAddr) bool {
	timestamp := uint32(id >> 32)
	expiration := 2 * time.Minute // 2 minutes per BEP 15

	if time.Since(time.Unix(int64(timestamp), 0)) > expiration {
		return false
	}

	mac := hmac.New(sha256.New, secretKey[:])
	mac.Write(addr.IP.To16())
	var tsBytes [4]byte
	binary.BigEndian.PutUint32(tsBytes[:], timestamp)
	mac.Write(tsBytes[:])
	expected := binary.BigEndian.Uint32(mac.Sum(nil)[:4])

	return uint32(id) == expected
}
