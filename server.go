package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os/signal"
	"syscall"
	"time"
)

type Server struct {
	tr  *Tracker
	cfg config
}

// NewServer creates and initializes a new server instance
func NewServer(cfg config) *Server {
	s := &Server{
		cfg: cfg,
		tr: &Tracker{
			torrents:    make(map[HashID]*Torrent),
			rateLimiter: make(map[string]*rateLimitEntry),
		},
	}

	h := sha256.New()
	h.Write([]byte(cfg.secret))
	copy(s.tr.secret[:], h.Sum(nil))

	return s
}

// Run starts the server and blocks until context cancellation
func (s *Server) Run(ctx context.Context) error {
	if s.cfg.secret == fallbackSecret {
		warn("Using insecure default secret key. Set -secret or PICO_TRACKER__SECRET for production use")
	}

	info("Starting Pico Tracker: %s", version)
	if debugEnabled.Load() {
		debug("Debug mode is enabled")
	}

	go s.tr.cleanupLoop()

	conn4, err := listenUDP("udp4", s.cfg.port)
	if err != nil {
		return fmt.Errorf("failed to listen on IPv4: %w", err)
	}
	info("UDP Tracker listening on 0.0.0.0:%d (IPv4)", s.cfg.port)

	conn6, err := listenUDP("udp6", s.cfg.port)
	if err != nil {
		warn("IPv6 not available: %v", err)
	} else {
		info("UDP Tracker listening on [::]:%d (IPv6)", s.cfg.port)
	}

	if s.cfg.whitelistPath != "" {
		startWhitelistManager(ctx, s.cfg.whitelistPath, &s.tr.whitelist)
	}

	go s.tr.listen(ctx, conn4)
	if conn6 != nil {
		go s.tr.listen(ctx, conn6)
	}

	<-ctx.Done()
	info("Shutting down gracefully...")

	if err := conn4.Close(); err != nil {
		debug("Failed to close IPv4 connection: %v", err)
	}
	if conn6 != nil {
		if err := conn6.Close(); err != nil {
			debug("Failed to close IPv6 connection: %v", err)
		}
	}

	info("Waiting for in-flight requests to complete...")
	done := make(chan struct{})
	go func() {
		s.tr.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		info("Shutdown complete")
		return nil
	case <-time.After(30 * time.Second):
		warn("Forcing shutdown after timeout, some handlers incomplete")
		return fmt.Errorf("shutdown timeout")
	}
}

// listenUDP creates a UDP listener for the specified network and port
func listenUDP(network string, port int) (*net.UDPConn, error) {
	var ip net.IP
	switch network {
	case "udp4":
		ip = net.ParseIP("0.0.0.0")
	case "udp6":
		ip = net.ParseIP("::")
	default:
		return nil, fmt.Errorf("unknown network: %s", network)
	}
	return net.ListenUDP(network, &net.UDPAddr{IP: ip, Port: port})
}

// setupSignalHandling creates a context that cancels on SIGINT/SIGTERM
func setupSignalHandling() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
