package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewServer(t *testing.T) {
	cfg := config{
		port:   1337,
		secret: "test-secret-key",
	}

	srv := NewServer(cfg)

	if srv == nil {
		t.Fatal("NewServer returned nil")
	}

	if srv.cfg.port != 1337 {
		t.Errorf("cfg.port = %d, want 1337", srv.cfg.port)
	}

	if srv.cfg.secret != "test-secret-key" {
		t.Errorf("cfg.secret = %s, want 'test-secret-key'", srv.cfg.secret)
	}

	if srv.tr == nil {
		t.Error("srv.tr is nil")
	}

	if srv.tr.torrents == nil {
		t.Error("srv.tr.torrents is nil")
	}

	if srv.tr.rateLimiter == nil {
		t.Error("srv.tr.rateLimiter is nil")
	}

	// Verify secret was properly hashed and stored
	expectedSecret := deriveSecret("test-secret-key")
	if srv.tr.secret != expectedSecret {
		t.Error("secret not properly derived")
	}
}

func TestNewServer_DefaultSecret(t *testing.T) {
	cfg := config{
		port:   1337,
		secret: fallbackSecret,
	}

	srv := NewServer(cfg)

	// Secret should be derived from fallbackSecret
	expectedSecret := deriveSecret(fallbackSecret)
	if srv.tr.secret != expectedSecret {
		t.Error("fallback secret not properly derived")
	}
}

func TestListenUDP_IPv4(t *testing.T) {
	// Use a random high port to avoid conflicts
	port := 40000
	conn, err := listenUDP("udp4", port)
	if err != nil {
		t.Fatalf("listenUDP(udp4, %d) failed: %v", port, err)
	}
	defer conn.Close()

	// Verify it's listening on the correct address
	addr := conn.LocalAddr()
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		t.Fatal("LocalAddr is not UDPAddr")
	}

	if !udpAddr.IP.Equal(net.ParseIP("0.0.0.0")) {
		t.Errorf("IP = %s, want 0.0.0.0", udpAddr.IP)
	}

	if udpAddr.Port != port {
		t.Errorf("Port = %d, want %d", udpAddr.Port, port)
	}
}

func TestListenUDP_IPv6(t *testing.T) {
	// Use a random high port to avoid conflicts
	port := 40001
	conn, err := listenUDP("udp6", port)
	if err != nil {
		t.Fatalf("listenUDP(udp6, %d) failed: %v", port, err)
	}
	defer conn.Close()

	// Verify it's listening on the correct address
	addr := conn.LocalAddr()
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		t.Fatal("LocalAddr is not UDPAddr")
	}

	// IPv6 should be :: (unspecified)
	if !udpAddr.IP.Equal(net.ParseIP("::")) {
		t.Errorf("IP = %s, want ::", udpAddr.IP)
	}

	if udpAddr.Port != port {
		t.Errorf("Port = %d, want %d", udpAddr.Port, port)
	}
}

func TestListenUDP_UnknownNetwork(t *testing.T) {
	_, err := listenUDP("tcp", 1337)
	if err == nil {
		t.Error("expected error for unknown network")
	}
}

func TestServer_Run_ContextCancellation(t *testing.T) {
	cfg := config{
		port:   40002, // Random high port
		secret: "test-secret",
	}

	srv := NewServer(cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Run server in goroutine
	var runErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = srv.Run(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Wait for server to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}

	if runErr != nil {
		t.Errorf("Run returned error: %v", runErr)
	}
}

func TestSetupSignalHandling(t *testing.T) {
	ctx, cancel := setupSignalHandling()
	defer cancel()

	// Verify context is not done initially
	select {
	case <-ctx.Done():
		t.Error("context should not be done immediately after creation")
	default:
		// Expected
	}
}

func TestDebugLogging_AtomicFlag(_ *testing.T) {
	// Save original state
	originalDebug := debugEnabled.Load()
	defer debugEnabled.Store(originalDebug)

	// Test that debug logging respects atomic flag
	debugEnabled.Store(false)

	// When disabled, debug should not produce output
	// (we can't easily capture log output, but we can verify no panic)
	debug("test message %s", "arg")

	// Enable debug
	debugEnabled.Store(true)
	debug("another test %d", 42)

	// Disable again
	debugEnabled.Store(false)

	// Test concurrent access (race detector will catch issues)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				debugEnabled.Store(true)
			} else {
				debugEnabled.Store(false)
			}
			debug("concurrent test %d", i)
		}(i)
	}
	wg.Wait()
}

// deriveSecret replicates the secret derivation in NewServer
func deriveSecret(secret string) [32]byte {
	var result [32]byte
	h := sha256.New()
	h.Write([]byte(secret))
	copy(result[:], h.Sum(nil))
	return result
}

// Ensure debugEnabled is accessed properly
var _ = atomic.Bool{}

func TestHMACPool_CleanState(t *testing.T) {
	secret1 := []byte("secret-one")
	secret2 := []byte("secret-two")

	// Get HMAC hasher and use it with secret1
	mac1 := getHMAC(secret1)
	mac1.Write([]byte("some data"))
	sum1 := mac1.Sum(nil)
	putHMAC(mac1)

	// Get hasher again - should be clean
	mac2 := getHMAC(secret2)
	mac2.Write([]byte("some data"))
	sum2 := mac2.Sum(nil)
	putHMAC(mac2)

	// Different secrets should produce different results
	if bytes.Equal(sum1, sum2) {
		t.Error("HMAC with different secrets produced same result - pool not resetting properly")
	}
}

func TestHMACPool_Reuse(_ *testing.T) {
	secret := []byte("test-secret")

	// Get and return multiple times
	for i := 0; i < 10; i++ {
		mac := getHMAC(secret)
		mac.Write([]byte("data"))
		_ = mac.Sum(nil)
		putHMAC(mac)
	}

	// If we get here without panic, pool is working
}

func TestConnectionIDExpiration_ExactlyAtBoundary(t *testing.T) {
	secret := deriveSecretForTest("test-secret")
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	// Create ID at exactly 2 minutes old (120 seconds) - should NOT be expired (uses > not >=)
	exactBoundary := uint32(time.Now().Unix() - 120) // Exactly 120 seconds ago
	boundaryID := generateConnectionIDWithTimestamp(addr, secret, exactBoundary)

	// Should still be valid - 120 seconds is not > 120 seconds
	if !validateConnectionID(boundaryID, addr, secret) {
		t.Error("connection ID at exactly 2 minutes should still be valid (boundary case)")
	}

	// Create ID at 121 seconds - should be expired
	expiredTimestamp := uint32(time.Now().Unix() - 121)
	expiredID := generateConnectionIDWithTimestamp(addr, secret, expiredTimestamp)

	if validateConnectionID(expiredID, addr, secret) {
		t.Error("connection ID at 121 seconds should be expired")
	}

	// Create ID at 119 seconds - should be valid
	validTimestamp := uint32(time.Now().Unix() - 119)
	validID := generateConnectionIDWithTimestamp(addr, secret, validTimestamp)

	if !validateConnectionID(validID, addr, secret) {
		t.Error("connection ID at 119 seconds should still be valid")
	}
}

func TestConnectionIDExpiration_FutureTimestamp(t *testing.T) {
	secret := deriveSecretForTest("test-secret")
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	// Create ID with future timestamp (should be valid)
	futureTimestamp := uint32(time.Now().Unix() + 60) // 1 minute in future
	futureID := generateConnectionIDWithTimestamp(addr, secret, futureTimestamp)

	// Should be valid (not expired yet) - signature is valid and timestamp is in future
	if !validateConnectionID(futureID, addr, secret) {
		t.Error("connection ID with future timestamp should be valid")
	}
}

// generateConnectionIDWithTimestamp creates a connection ID with a specific timestamp
func generateConnectionIDWithTimestamp(addr *net.UDPAddr, secret []byte, timestamp uint32) uint64 {
	mac := getHMAC(secret)
	defer putHMAC(mac)

	mac.Write(addr.IP.To16())
	var tsBytes [4]byte
	binary.BigEndian.PutUint32(tsBytes[:], timestamp)
	mac.Write(tsBytes[:])
	sig := binary.BigEndian.Uint32(mac.Sum(nil)[:4])

	return uint64(timestamp)<<32 | uint64(sig)
}

// deriveSecretForTest is a helper for tests that need a secret byte slice
func deriveSecretForTest(secret string) []byte {
	s := deriveSecret(secret)
	return s[:]
}
