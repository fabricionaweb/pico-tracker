package main

import (
	"net"
	"testing"
)

func TestAddPeer_NewSeeder(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 0) // left=0 means seeder

	if torrent.seeders != 1 {
		t.Errorf("seeders = %d, want 1", torrent.seeders)
	}
	if torrent.leechers != 0 {
		t.Errorf("leechers = %d, want 0", torrent.leechers)
	}
	if torrent.completed != 1 {
		t.Errorf("completed = %d, want 1", torrent.completed)
	}
}

func TestAddPeer_NewLeecher(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000) // left>0 means leecher

	if torrent.seeders != 0 {
		t.Errorf("seeders = %d, want 0", torrent.seeders)
	}
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}
	if torrent.completed != 0 {
		t.Errorf("completed = %d, want 0", torrent.completed)
	}
}

func TestAddPeer_UpdateExisting(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.addPeer(peerID, net.ParseIP("192.168.1.2"), 6882, 500)

	if torrent.seeders != 0 {
		t.Errorf("seeders = %d, want 0", torrent.seeders)
	}
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}

	p := torrent.peers[peerID]
	if p.IP.String() != "192.168.1.2" {
		t.Errorf("IP = %v, want 192.168.1.2", p.IP)
	}
	if p.Port != 6882 {
		t.Errorf("Port = %d, want 6882", p.Port)
	}
}

func TestAddPeer_LeecherToSeeder(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 0) // now complete

	if torrent.seeders != 1 {
		t.Errorf("seeders = %d, want 1", torrent.seeders)
	}
	if torrent.leechers != 0 {
		t.Errorf("leechers = %d, want 0", torrent.leechers)
	}
	if torrent.completed != 1 {
		t.Errorf("completed = %d, want 1", torrent.completed)
	}
}

func TestAddPeer_SeederToLeecher(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 0)
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000) // re-announced with left>0

	if torrent.seeders != 0 {
		t.Errorf("seeders = %d, want 0", torrent.seeders)
	}
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}
}

func TestRemovePeer_Seeder(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 0)
	torrent.removePeer(peerID)

	if torrent.seeders != 0 {
		t.Errorf("seeders = %d, want 0", torrent.seeders)
	}
	if len(torrent.peers) != 0 {
		t.Errorf("peer still in map")
	}
}

func TestRemovePeer_Leecher(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.removePeer(peerID)

	if torrent.leechers != 0 {
		t.Errorf("leechers = %d, want 0", torrent.leechers)
	}
}

func TestRemovePeer_NonExistent(t *testing.T) {
	torrent := &Torrent{peers: make(map[HashID]*Peer)}

	peerID := NewHashID([]byte("nonexistent_________"))
	torrent.removePeer(peerID) // should not panic

	if torrent.seeders != 0 || torrent.leechers != 0 {
		t.Error("counters changed for non-existent peer")
	}
}

func TestGetPeers_Empty(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peers, seeders, leechers := torrent.getPeers(NewHashID([]byte("requester__________")), 50, true, 6)

	if peers != nil {
		t.Errorf("peers = %v, want nil", peers)
	}
	if seeders != 0 || leechers != 0 {
		t.Errorf("seeders=%d, leechers=%d, want 0,0", seeders, leechers)
	}
}

func TestGetPeers_IPv4Filter(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	torrent.addPeer(NewHashID([]byte("ipv4peer1___________")), net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.addPeer(NewHashID([]byte("ipv4peer2___________")), net.ParseIP("192.168.1.2"), 6881, 1000)
	torrent.addPeer(NewHashID([]byte("ipv6peer1___________")), net.ParseIP("2001:db8::1"), 6881, 1000)

	requester := NewHashID([]byte("requester__________"))
	peers, _, leechers := torrent.getPeers(requester, 50, true, 6)

	if len(peers) != 12 {
		t.Errorf("len(peers) = %d, want 12", len(peers))
	}
	if leechers != 3 {
		t.Errorf("leechers = %d, want 3 (total, not filtered)", leechers)
	}
}

func TestGetPeers_IPv6Filter(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	torrent.addPeer(NewHashID([]byte("ipv4peer1___________")), net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.addPeer(NewHashID([]byte("ipv6peer1___________")), net.ParseIP("2001:db8::1"), 6881, 1000)
	torrent.addPeer(NewHashID([]byte("ipv6peer2___________")), net.ParseIP("2001:db8::2"), 6881, 1000)

	requester := NewHashID([]byte("requester__________"))
	peers, _, leechers := torrent.getPeers(requester, 50, false, 18)

	if len(peers) != 36 {
		t.Errorf("len(peers) = %d, want 36", len(peers))
	}
	if leechers != 3 {
		t.Errorf("leechers = %d, want 3 (total, not filtered)", leechers)
	}
}

func TestGetPeers_ExcludesRequester(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000)

	peers, _, _ := torrent.getPeers(peerID, 50, true, 6)

	// Requesting peer should be excluded
	if len(peers) != 0 {
		t.Errorf("len(peers) = %d, want 0 (requester excluded)", len(peers))
	}
}

func TestGetPeers_LimitsNumWant(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	for i := 0; i < 10; i++ {
		peerID := NewHashID([]byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
		torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), uint16(6881+i), 1000)
	}

	requester := NewHashID([]byte("requester__________"))
	peers, _, _ := torrent.getPeers(requester, 3, true, 6)

	// Should return only 3 peers (3 * 6 = 18 bytes)
	if len(peers) != 18 {
		t.Errorf("len(peers) = %d, want 18", len(peers))
	}
}

func TestGetOrCreateTorrent_New(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}

	hash := NewHashID([]byte("newtorrent__________"))
	torrent := tr.getOrCreateTorrent(hash)

	if torrent == nil {
		t.Fatal("torrent is nil")
	}
	if len(tr.torrents) != 1 {
		t.Errorf("torrents count = %d, want 1", len(tr.torrents))
	}
}

func TestGetOrCreateTorrent_Existing(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}

	hash := NewHashID([]byte("newtorrent__________"))
	t1 := tr.getOrCreateTorrent(hash)
	t2 := tr.getOrCreateTorrent(hash)

	if t1 != t2 {
		t.Error("should return same torrent instance")
	}
	if len(tr.torrents) != 1 {
		t.Errorf("torrents count = %d, want 1", len(tr.torrents))
	}
}

func TestGetTorrent_NotFound(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}

	hash := NewHashID([]byte("nonexistent_________"))
	torrent := tr.getTorrent(hash)

	if torrent != nil {
		t.Error("expected nil for non-existent torrent")
	}
}

func TestGetTorrent_Found(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}

	hash := NewHashID([]byte("existing____________"))
	tr.torrents[hash] = &Torrent{peers: make(map[HashID]*Peer)}

	torrent := tr.getTorrent(hash)

	if torrent == nil {
		t.Fatal("torrent is nil")
	}
}

func TestCheckRateLimit_FirstRequest(t *testing.T) {
	tr := &Tracker{
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	allowed, remaining := tr.checkRateLimit(addr)

	if !allowed {
		t.Error("first request should be allowed")
	}
	if remaining != 0 {
		t.Errorf("remaining = %v, want 0", remaining)
	}
}

func TestCheckRateLimit_WithinBurst(t *testing.T) {
	tr := &Tracker{
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	for i := 0; i < rateLimitBurst; i++ {
		allowed, _ := tr.checkRateLimit(addr)
		if !allowed {
			t.Errorf("request %d should be allowed (burst=%d)", i+1, rateLimitBurst)
		}
	}
}

func TestCheckRateLimit_ExceedsBurst(t *testing.T) {
	tr := &Tracker{
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	for i := 0; i < rateLimitBurst; i++ {
		tr.checkRateLimit(addr)
	}

	allowed, remaining := tr.checkRateLimit(addr)

	if allowed {
		t.Error("request beyond burst should be blocked")
	}
	if remaining <= 0 {
		t.Error("should return positive remaining time")
	}
}

func TestCheckRateLimit_DifferentIPs(t *testing.T) {
	tr := &Tracker{
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	addr1 := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	addr2 := &net.UDPAddr{IP: net.ParseIP("192.168.1.2"), Port: 6881}

	for i := 0; i < rateLimitBurst; i++ {
		tr.checkRateLimit(addr1)
	}

	allowed, _ := tr.checkRateLimit(addr2)

	if !allowed {
		t.Error("different IP should have separate limit")
	}
}

func TestAddPeer_SeederReannouncesAsSeeder(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 0) // first: seeder
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 0) // re-announce: still seeder

	if torrent.seeders != 1 {
		t.Errorf("seeders = %d, want 1", torrent.seeders)
	}
	if torrent.completed != 1 {
		t.Errorf("completed = %d, want 1 (should not double-count)", torrent.completed)
	}
}

func TestGetPeers_NumWantExceedsAvailable(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	torrent.addPeer(NewHashID([]byte("peer1_______________")), net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.addPeer(NewHashID([]byte("peer2_______________")), net.ParseIP("192.168.1.2"), 6881, 1000)

	requester := NewHashID([]byte("requester__________"))
	peers, seeders, leechers := torrent.getPeers(requester, 100, true, 6) // ask for 100, only 2 exist

	if len(peers) != 12 { // 2 peers * 6 bytes
		t.Errorf("len(peers) = %d, want 12", len(peers))
	}
	if leechers != 2 {
		t.Errorf("leechers = %d, want 2", leechers)
	}
	if seeders != 0 {
		t.Errorf("seeders = %d, want 0", seeders)
	}
}
