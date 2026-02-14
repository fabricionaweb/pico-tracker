package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackpal/bencode-go"
)

// Logging: DEBUG=1 env var enables debug output
var debugMode = os.Getenv("DEBUG") != ""

func debug(format string, v ...any) {
	if debugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
}

type Peer struct {
	IP   net.IP
	Port uint16
	Left int64 // bytes remaining (0 = seeder, >0 = leecher)
}

type Torrent struct {
	Peers    map[string]*Peer
	Seeders  int
	Leechers int
	mu       sync.RWMutex
}

type Tracker struct {
	torrents map[string]*Torrent
	mu       sync.RWMutex
}

func (t *Tracker) getTorrent(hash string) *Torrent {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.torrents[hash] == nil {
		t.torrents[hash] = &Torrent{Peers: map[string]*Peer{}}
		debug("created new torrent %s", hash)
	}
	return t.torrents[hash]
}

func (t *Torrent) addPeer(id string, ip net.IP, port uint16, left int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.Peers[id]; ok {
		if p.Left == 0 {
			t.Seeders--
		} else {
			t.Leechers--
		}
		debug("updated peer %s: %s:%d -> %s:%d (left: %d -> %d)", id, p.IP, p.Port, ip, port, p.Left, left)
	} else {
		debug("added peer %s @ %s:%d (left: %d)", id, ip, port, left)
	}
	t.Peers[id] = &Peer{IP: ip, Port: port, Left: left}
	if left == 0 {
		t.Seeders++
	} else {
		t.Leechers++
	}
}

func (t *Torrent) removePeer(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.Peers[id]; ok {
		if p.Left == 0 {
			t.Seeders--
		} else {
			t.Leechers--
		}
		debug("removed peer %s @ %s:%d", id, p.IP, p.Port)
		delete(t.Peers, id)
	}
}

func (t *Torrent) getPeersAndCount(exclude string, numWant int) (peers4, peers6 []byte, seeders, leechers int) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Return cached counters - O(1) instead of O(n)
	seeders, leechers = t.Seeders, t.Leechers

	// Build compact peer list up to numWant (check actual byte count instead of counter)
	for id, p := range t.Peers {
		if id == exclude {
			continue
		}
		// Stop when we have enough peers (6 bytes for IPv4, 18 for IPv6)
		if (len(peers4)/6 + len(peers6)/18) >= numWant {
			break
		}
		if ip4 := p.IP.To4(); ip4 != nil {
			peers4 = append(peers4, ip4...)
			peers4 = binary.BigEndian.AppendUint16(peers4, p.Port)
		} else {
			peers6 = append(peers6, p.IP.To16()...)
			peers6 = binary.BigEndian.AppendUint16(peers6, p.Port)
		}
	}
	return
}

func parseHash(hashParam string) (string, error) {
	// BitTorrent BEP 3 says info_hash should be URL-encoded in query params.
	// However, clients vary: some send %12%34... (percent-encoded bytes),
	// some send raw 20 bytes, and some send 40 hex chars already.
	// We handle all three formats for maximum compatibility.

	// 40 hex chars = already hex-encoded 20 bytes (no unescaping needed)
	if decoded, err := hex.DecodeString(hashParam); err == nil && len(decoded) == 20 {
		return strings.ToLower(hashParam), nil
	}

	// Try URL unescaping for percent-encoded binary (e.g., %12%34...)
	if d, err := url.QueryUnescape(hashParam); err == nil {
		hashParam = d
	}

	// 20 raw bytes need hex encoding
	if len(hashParam) == 20 {
		return hex.EncodeToString([]byte(hashParam)), nil
	}
	return "", fmt.Errorf("invalid hash")
}

func getIP(r *http.Request) net.IP {
	// Get the direct connection IP first
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)

	// Only trust proxy headers if connection is from private network (reverse proxy)
	if ip != nil && ip.IsPrivate() {
		// Check X-Real-IP first (some proxies use this)
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			if parsed := net.ParseIP(realIP); parsed != nil {
				return parsed
			}
		}
		// Then check X-Forwarded-For (can be a list, use first = closest proxy)
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			for _, ipStr := range strings.Split(fwd, ",") {
				if parsed := net.ParseIP(strings.TrimSpace(ipStr)); parsed != nil {
					return parsed
				}
			}
		}
	}

	return ip
}

func (tr *Tracker) announce(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if q.Get("info_hash") == "" || q.Get("peer_id") == "" {
		w.WriteHeader(http.StatusBadRequest)
		bencode.Marshal(w, map[string]any{"failure reason": "missing info_hash or peer_id"})
		return
	}

	infoHashRaw := q.Get("info_hash")
	peerIDRaw := q.Get("peer_id")

	infoHash, err := parseHash(infoHashRaw)
	if err != nil {
		debug("bad info_hash: %q", infoHashRaw)
		w.WriteHeader(http.StatusBadRequest)
		bencode.Marshal(w, map[string]any{"failure reason": "bad info_hash"})
		return
	}

	peerID, err := parseHash(peerIDRaw)
	if err != nil {
		debug("bad peer_id: %q", peerIDRaw)
		w.WriteHeader(http.StatusBadRequest)
		bencode.Marshal(w, map[string]any{"failure reason": "bad peer_id"})
		return
	}

	debug("info_hash=%s peer_id=%s", infoHash, peerID)

	// Parse port as unsigned 16-bit int (validates 1-65535 range automatically)
	port, err := strconv.ParseUint(q.Get("port"), 10, 16)
	if err != nil || port == 0 {
		debug("bad port: %q", q.Get("port"))
		w.WriteHeader(http.StatusBadRequest)
		bencode.Marshal(w, map[string]any{"failure reason": "bad port"})
		return
	}

	numWant, _ := strconv.Atoi(q.Get("numwant"))
	// numWant is how many peers the client wants (0 means default, 50 is our max to prevent DoS)
	if numWant == 0 || numWant > 50 {
		numWant = 50
	}
	debug("numwant: %d", numWant)

	// Parse bytes remaining: base 10, 64-bit integer. left=0 means peer has complete file (seeder)
	left, _ := strconv.ParseInt(q.Get("left"), 10, 64)

	torrent := tr.getTorrent(infoHash)
	clientIP := getIP(r)

	debug("client IP: %s, port: %d, left: %d", clientIP, port, left)
	debug("torrent has %d peers", len(torrent.Peers))
	for id, p := range torrent.Peers {
		debug("  existing peer: %s @ %s:%d (left: %d)", id, p.IP, p.Port, p.Left)
	}

	event := q.Get("event")
	if event == "stopped" {
		debug("event=stopped")
		torrent.removePeer(peerID)
	} else {
		switch event {
		case "completed":
			debug("event=completed")
		case "started":
			debug("event=started")
		}
		torrent.addPeer(peerID, clientIP, uint16(port), left)
	}

	peers4, peers6, seeders, leechers := torrent.getPeersAndCount(peerID, numWant)

	debug("returning %d seeders, %d leechers, %d peers4 bytes, %d peers6 bytes", seeders, leechers, len(peers4), len(peers6))

	w.Header().Set("Content-Type", "text/plain")
	bencode.Marshal(w, map[string]any{
		"interval":     10 * 60, // Minutes to client re-announce
		"min interval": 3 * 60,  // Minutes between announces
		"complete":     seeders,
		"incomplete":   leechers,
		"peers":        peers4,
		"peers6":       peers6,
	})
}

func (tr *Tracker) cleanupLoop() {
	// Clear all peers every 30 minutes to prevent memory growth and stale connections
	for range time.Tick(30 * time.Minute) {
		tr.mu.Lock()
		count := len(tr.torrents)
		for hash, t := range tr.torrents {
			t.mu.Lock()
			peerCount := len(t.Peers)
			t.Peers = make(map[string]*Peer)
			t.Seeders, t.Leechers = 0, 0
			t.mu.Unlock()
			debug("cleanup: removed torrent %s with %d peers", hash, peerCount)
			delete(tr.torrents, hash)
		}
		tr.mu.Unlock()
		debug("cleanup: cleared %d torrents", count)
	}
}

func main() {
	defaultAddr := os.Getenv("PICO_TRACKER__ADDR")
	if defaultAddr == "" {
		defaultAddr = "127.0.0.1:8080"
	}

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Pico Tracker: Portable BitTorrent Tracker")
		flag.PrintDefaults()
	}

	addr := flag.String("addr", defaultAddr, "bind address")
	flag.BoolVar(&debugMode, "debug", debugMode, "enable debug logs")
	flag.Parse()

	tracker := &Tracker{torrents: map[string]*Torrent{}}
	go tracker.cleanupLoop()

	http.HandleFunc("/", tracker.announce)

	log.Printf("[INFO] Tracker listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatalf("[ERROR] Server error: %v", err)
	}
}
