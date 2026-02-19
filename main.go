package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

var version = "dev"

var debugMode bool

var fallbackSecret = "pico-tracker-default-secret-do-not-use-in-production"

func debug(format string, v ...any) {
	if debugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
}

func info(format string, v ...any) {
	log.Printf("[INFO] "+format, v...)
}

//nolint:govet // Field alignment is acceptable
type config struct {
	secret        string
	port          int
	showVersion   bool
	whitelistPath string
}

// parseFlags parses command-line flags and returns configuration.
// Default values are read from environment variables:
//   - PICO_TRACKER__PORT: default port (must be > 0)
//   - PICO_TRACKER__SECRET: secret key for connection ID signing
//   - DEBUG: enables debug mode if set
func parseFlags(args []string) *config {
	defaultPort := 1337
	if p, err := strconv.Atoi(os.Getenv("PICO_TRACKER__PORT")); err == nil && p > 0 {
		defaultPort = p
	}

	defaultSecret := os.Getenv("PICO_TRACKER__SECRET")
	if defaultSecret == "" {
		defaultSecret = fallbackSecret
	}

	defaultWhitelist := os.Getenv("PICO_TRACKER__WHITELIST")

	debugDefault := os.Getenv("DEBUG") != ""

	fs := flag.NewFlagSet("pico-tracker", flag.ExitOnError)
	port := fs.Int("port", defaultPort, "port to listen on [env PICO_TRACKER__PORT]")
	fs.IntVar(port, "p", defaultPort, "alias to -port")

	secret := fs.String("secret", "", "secret key for connection ID signing [env PICO_TRACKER__SECRET]")
	fs.StringVar(secret, "s", "", "alias to -secret")

	whitelist := fs.String("whitelist", defaultWhitelist,
		"path to whitelist file for private tracker mode [env PICO_TRACKER__WHITELIST]")
	fs.StringVar(whitelist, "w", defaultWhitelist, "alias to -whitelist")

	debug := fs.Bool("debug", debugDefault, "enable debug logs [env DEBUG]")
	fs.BoolVar(debug, "d", debugDefault, "alias to -debug")

	showVersion := fs.Bool("version", false, "print version")
	fs.BoolVar(showVersion, "v", false, "alias to -version")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "\nPico Tracker: %s\nPortable BitTorrent Tracker (UDP)\n\n", version)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n")
	}

	// With ExitOnError, flag package exits on error
	//nolint:errcheck // Test flags are valid, parsing error will exit
	_ = fs.Parse(args)

	debugMode = *debug

	// Apply default secret later if not provided (hides from -help output)
	if *secret == "" {
		*secret = defaultSecret
	}

	return &config{
		port:          *port,
		secret:        *secret,
		showVersion:   *showVersion,
		whitelistPath: *whitelist,
	}
}

func main() {
	cfg := parseFlags(os.Args[1:])

	if cfg.showVersion {
		fmt.Println(version)
		os.Exit(0)
	}
	if cfg.secret == fallbackSecret {
		log.Printf("[WARN] Using insecure default secret key. Set -secret or PICO_TRACKER__SECRET for production use")
	}

	info("Starting Pico Tracker: %s", version)
	debug("Debug mode is enabled")

	h := sha256.New()
	h.Write([]byte(cfg.secret))
	copy(secretKey[:], h.Sum(nil))

	tr := &Tracker{
		torrents:    make(map[HashID]*Torrent),
		rateLimiter: make(map[string]*rateLimitEntry),
	}
	go tr.cleanupLoop()

	conn4, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: cfg.port})
	if err != nil {
		log.Fatalf("[ERROR] Failed to listen on IPv4: %v", err)
	}
	info("UDP Tracker listening on 0.0.0.0:%d (IPv4)", cfg.port)

	// IPv6 is optional - if it fails, the tracker still works with IPv4 only
	conn6, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::"), Port: cfg.port})
	if err != nil {
		log.Printf("[WARN] IPv6 not available: %v", err)
	} else {
		info("UDP Tracker listening on [::]:%d (IPv6)", cfg.port)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.whitelistPath != "" {
		startWhitelistManager(ctx, cfg.whitelistPath, &tr.whitelist)
	}

	go tr.listen(ctx, conn4)
	if conn6 != nil {
		go tr.listen(ctx, conn6)
	}

	<-ctx.Done()
	info("Shutting down gracefully...")

	//nolint:errcheck // Connection close errors are ignored during shutdown
	conn4.Close()
	if conn6 != nil {
		//nolint:errcheck // Connection close errors are ignored during shutdown
		conn6.Close()
	}

	info("Waiting for in-flight requests to complete...")
	done := make(chan struct{})
	go func() {
		tr.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		info("Shutdown complete")
	case <-time.After(30 * time.Second):
		log.Printf("[WARN] Forcing shutdown after timeout, some handlers incomplete")
		//nolint:gocritic // Intentional exit after deferred cleanup timeout
		os.Exit(1)
	}
}
