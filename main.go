package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync/atomic"
)

var version = "dev"

const fallbackSecret = "pico-tracker-default-secret-do-not-use-in-production"

// debugEnabled is an atomic boolean for thread-safe debug toggle
var debugEnabled atomic.Bool

// Hot path callers should check debugEnabled.Load() first
// to avoid expensive argument evaluation (e.g., HashID.String()).
// This function provides a safety check for non-hot-path calls.
func debug(format string, v ...any) {
	if debugEnabled.Load() {
		log.Printf("[DEBUG] "+format, v...)
	}
}

func info(format string, v ...any) {
	log.Printf("[INFO] "+format, v...)
}

func warn(format string, v ...any) {
	log.Printf("[WARN] "+format, v...)
}

func errorLog(format string, v ...any) {
	log.Printf("[ERROR] "+format, v...)
}

//nolint:govet // Field alignment is acceptable
type config struct {
	secret        string
	port          int
	showVersion   bool
	whitelistPath string
	debug         bool
}

// parseFlags parses command-line flags and returns configuration.
// Default values are read from environment variables:
//   - PICO_TRACKER__PORT: default port (must be > 0)
//   - PICO_TRACKER__SECRET: secret key for connection ID signing
//   - DEBUG: enables debug mode if set
func parseFlags(args []string) config {
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

	// Apply default secret later if not provided (hides from -help output)
	if *secret == "" {
		*secret = defaultSecret
	}

	return config{
		port:          *port,
		secret:        *secret,
		showVersion:   *showVersion,
		whitelistPath: *whitelist,
		debug:         *debug,
	}
}

func main() {
	cfg := parseFlags(os.Args[1:])

	if cfg.showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	debugEnabled.Store(cfg.debug)

	srv := NewServer(cfg)

	ctx, stop := setupSignalHandling()
	defer stop()

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("[ERROR] Server error: %v", err)
	}
}
