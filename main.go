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

var debugMode = os.Getenv("DEBUG") != ""

func debug(format string, v ...any) {
	if debugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
}

func info(format string, v ...any) {
	log.Printf("[INFO] "+format, v...)
}

func main() {
	defaultPort := 1337
	if p, err := strconv.Atoi(os.Getenv("PICO_TRACKER__PORT")); err == nil && p > 0 {
		defaultPort = p
	}

	defaultSecret := os.Getenv("PICO_TRACKER__SECRET")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Pico Tracker: %s\nPortable BitTorrent Tracker (UDP)\n\n", version)
		flag.PrintDefaults()
	}

	port := flag.Int("port", defaultPort, "port to listen on")
	flag.IntVar(port, "p", defaultPort, "alias to -port")
	secret := flag.String("secret", defaultSecret, "secret key for connection ID signing")
	flag.StringVar(secret, "s", defaultSecret, "alias to -secret")
	flag.BoolVar(&debugMode, "debug", debugMode, "enable debug logs")
	flag.BoolVar(&debugMode, "d", debugMode, "alias to -debug")
	showVersion := flag.Bool("version", false, "print version")
	flag.BoolVar(showVersion, "v", false, "alias to -version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	secretStr := *secret
	if secretStr == "" {
		secretStr = "pico-tracker-default-secret-do-not-use-in-production"
		log.Printf("[WARN] Using insecure default secret key. Set PICO_TRACKER__SECRET or -secret for production use")
	}
	h := sha256.New()
	h.Write([]byte(secretStr))
	copy(secretKey[:], h.Sum(nil))

	debug("Debug mode is enabled")
	info("Starting Pico Tracker: %s", version)

	tr := &Tracker{
		torrents:    make(map[HashID]*Torrent),
		rateLimiter: make(map[string]*rateLimitEntry),
	}
	go tr.cleanupLoop()

	conn4, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: *port})
	if err != nil {
		log.Fatalf("[ERROR] Failed to listen on IPv4: %v", err)
	}
	info("UDP Tracker listening on 0.0.0.0:%d (IPv4)", *port)

	// IPv6 is optional - if it fails, the tracker still works with IPv4 only
	conn6, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.ParseIP("::"), Port: *port})
	if err != nil {
		log.Printf("[WARN] IPv6 not available: %v", err)
	} else {
		info("UDP Tracker listening on [::]:%d (IPv6)", *port)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go tr.listen(ctx, conn4)
	if conn6 != nil {
		go tr.listen(ctx, conn6)
	}

	<-ctx.Done()
	info("Shutting down gracefully...")

	conn4.Close()
	if conn6 != nil {
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
		os.Exit(1)
	}
}
