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

// enables detailed logging when set via DEBUG env var or -debug flag.
var debugMode = os.Getenv("DEBUG") != ""

func debug(format string, v ...any) {
	if debugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
}

// Peer represents a BitTorrent peer in a swarm.
type Peer struct {
	IP   net.IP
	Port uint16
	Left int64 // bytes remaining (0 = seeder, >0 = leecher)
}

// Torrent tracks all peers for a specific info hash.
type Torrent struct {
	mu       sync.RWMutex
	peers    map[string]*Peer
	seeders  int
	leechers int
}

// Tracker manages multiple torrent swarms.
type Tracker struct {
	mu       sync.RWMutex
	torrents map[string]*Torrent
}

// getTorrent returns the torrent for the given hash, creating it if needed.
func (t *Tracker) getTorrent(hash string) *Torrent {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.torrents[hash] == nil {
		t.torrents[hash] = &Torrent{peers: make(map[string]*Peer)}
		debug("created new torrent %s", hash)
	}

	return t.torrents[hash]
}

// addPeer registers or updates a peer in the torrent.
func (t *Torrent) addPeer(id string, ip net.IP, port uint16, left int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if p, exists := t.peers[id]; exists {
		if p.Left == 0 && left > 0 {
			t.seeders--
			t.leechers++
			debug("peer %s became leecher @ %s:%d", id, ip, port)
		} else if p.Left > 0 && left == 0 {
			t.leechers--
			t.seeders++
			debug("peer %s became seeder @ %s:%d", id, ip, port)
		}
		p.IP, p.Port, p.Left = ip, port, left
		return
	}

	if left == 0 {
		t.seeders++
	} else {
		t.leechers++
	}
	t.peers[id] = &Peer{IP: ip, Port: port, Left: left}
	debug("added peer %s @ %s:%d", id, ip, port)
}

// removePeer removes a peer from the torrent.
func (t *Torrent) removePeer(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, exists := t.peers[id]
	if !exists {
		return
	}

	if p.Left == 0 {
		t.seeders--
	} else {
		t.leechers--
	}
	delete(t.peers, id)
	debug("removed peer %s @ %s:%d", id, p.IP, p.Port)
}

// getPeersAndCount returns peer lists and counts, excluding the requesting peer.
func (t *Torrent) getPeersAndCount(exclude string, numWant int) (peers4, peers6 []byte, seeders, leechers int) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	seeders, leechers = t.seeders, t.leechers

	for id, p := range t.peers {
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
	return peers4, peers6, seeders, leechers
}

// parseHash converts various hash formats to a normalized hex string.
func parseHash(hashParam string) (string, error) {
	// 40 hex chars = already hex-encoded 20 bytes (no unescaping needed)
	if decoded, err := hex.DecodeString(hashParam); err == nil && len(decoded) == 20 {
		return strings.ToLower(hashParam), nil
	}

	// BitTorrent BEP 3 says info_hash should be URL-encoded in query params.
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

// getIP extracts the client IP from the request, respecting proxy headers.
func getIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	ip := net.ParseIP(host)

	// Trust proxy headers only for private network connections
	if ip != nil && ip.IsPrivate() {
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			if parsed := net.ParseIP(realIP); parsed != nil {
				return parsed
			}
		}

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

// announce handles BitTorrent announce requests.
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

	port, err := strconv.ParseUint(q.Get("port"), 10, 16)
	if err != nil || port == 0 {
		debug("bad port: %q", q.Get("port"))
		w.WriteHeader(http.StatusBadRequest)
		bencode.Marshal(w, map[string]any{"failure reason": "bad port"})
		return
	}

	numWant, _ := strconv.Atoi(q.Get("numwant"))
	if numWant == 0 || numWant > 50 {
		numWant = 50
	}
	debug("numwant: %d", numWant)

	left, _ := strconv.ParseInt(q.Get("left"), 10, 64)
	torrent := tr.getTorrent(infoHash)
	clientIP := getIP(r)
	event := q.Get("event")

	debug("client IP: %s, port: %d, left: %d", clientIP, port, left)
	debug("torrent has %d peers", len(torrent.peers))
	for id, p := range torrent.peers {
		debug("  existing peer: %s @ %s:%d (left: %d)", id, p.IP, p.Port, p.Left)
	}
	debug("event=%s", event)

	switch event {
	case "stopped":
		torrent.removePeer(peerID)
	case "completed":
		torrent.addPeer(peerID, clientIP, uint16(port), 0) // Force seeder
	case "started", "":
		fallthrough
	default:
		torrent.addPeer(peerID, clientIP, uint16(port), left)
	}

	peers4, peers6, seeders, leechers := torrent.getPeersAndCount(peerID, numWant)
	debug("returning %d seeders, %d leechers, %d peers4 bytes, %d peers6 bytes",
		seeders, leechers, len(peers4), len(peers6))

	w.Header().Set("Content-Type", "text/plain")
	bencode.Marshal(w, map[string]any{
		"interval":     10 * time.Minute, // client re-announce
		"min interval": 3 * time.Minute,  // between announces
		"complete":     seeders,
		"incomplete":   leechers,
		"peers":        peers4,
		"peers6":       peers6,
	})
}

// cleanupLoop periodically clears inactive torrents every 30 minutes.
func (tr *Tracker) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		tr.mu.Lock()
		count := len(tr.torrents)
		for hash, t := range tr.torrents {
			t.mu.Lock()
			peerCount := len(t.peers)
			t.peers = make(map[string]*Peer)
			t.seeders, t.leechers = 0, 0
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

	tracker := &Tracker{torrents: make(map[string]*Torrent)}
	go tracker.cleanupLoop()

	http.HandleFunc("/", tracker.announce)

	log.Printf("[INFO] Tracker listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatalf("[ERROR] Server error: %v", err)
	}
}
