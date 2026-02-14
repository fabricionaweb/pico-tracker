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

func parseHash(s string) (string, error) {
	// BitTorrent BEP 3 says info_hash should be URL-encoded in query params.
	// However, clients vary: some send %12%34... (percent-encoded bytes),
	// some send raw 20 bytes, and some send 40 hex chars already.
	// We handle all three formats for maximum compatibility.
	if d, err := url.QueryUnescape(s); err == nil {
		s = d
	}
	// 40 hex chars = already hex-encoded 20 bytes
	if decoded, err := hex.DecodeString(s); err == nil && len(decoded) == 20 {
		return strings.ToLower(s), nil
	}
	// 20 raw bytes need hex encoding
	if len(s) == 20 {
		return hex.EncodeToString([]byte(s)), nil
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

	infoHash, err := parseHash(q.Get("info_hash"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		bencode.Marshal(w, map[string]any{"failure reason": "bad info_hash"})
		return
	}

	peerID, err := parseHash(q.Get("peer_id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		bencode.Marshal(w, map[string]any{"failure reason": "bad peer_id"})
		return
	}

	// Parse port as unsigned 16-bit int (validates 1-65535 range automatically)
	port, err := strconv.ParseUint(q.Get("port"), 10, 16)
	if err != nil || port == 0 {
		w.WriteHeader(http.StatusBadRequest)
		bencode.Marshal(w, map[string]any{"failure reason": "bad port"})
		return
	}

	numWant, _ := strconv.Atoi(q.Get("numwant"))
	// numWant is how many peers the client wants (0 means default, 50 is our max to prevent DoS)
	if numWant == 0 || numWant > 50 {
		numWant = 50
	}

	// Parse bytes remaining: base 10, 64-bit integer. left=0 means peer has complete file (seeder)
	left, _ := strconv.ParseInt(q.Get("left"), 10, 64)

	torrent := tr.getTorrent(infoHash)

	if q.Get("event") == "stopped" {
		torrent.removePeer(peerID)
	} else {
		torrent.addPeer(peerID, getIP(r), uint16(port), left)
	}

	peers4, peers6, seeders, leechers := torrent.getPeersAndCount(peerID, numWant)

	w.Header().Set("Content-Type", "text/plain")
	bencode.Marshal(w, map[string]any{
		"interval":     1800, // Client should re-announce after 30 minutes
		"min interval": 900,  // Minimum 15 minutes between announces
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
		for hash, t := range tr.torrents {
			t.mu.Lock()
			t.Peers = make(map[string]*Peer)
			t.Seeders, t.Leechers = 0, 0
			t.mu.Unlock()
			delete(tr.torrents, hash)
		}
		tr.mu.Unlock()
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
	flag.Parse()

	tracker := &Tracker{torrents: map[string]*Torrent{}}
	go tracker.cleanupLoop()

	http.HandleFunc("/", tracker.announce)

	log.Printf("Tracker listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
