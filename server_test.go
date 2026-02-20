package main

import (
	"context"
	"crypto/sha256"
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
