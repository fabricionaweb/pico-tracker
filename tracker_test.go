package main

import (
	"net"
	"testing"
	"time"
)

func TestAddPeer_NewSeeder(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))

	peerID := NewHashID([]byte("peer1_______________"))
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 0) // left=0 means seeder

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.leechers != 0 {
		t.Errorf("leechers = %d, want 0", torrent.leechers)
	}
}

func TestRemovePeer_NonExistent(t *testing.T) {
	torrent := &Torrent{peers: make(map[HashID]*Peer)}

	peerID := NewHashID([]byte("nonexistent_________"))
	torrent.removePeer(peerID) // should not panic

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

func TestCheckRateLimit_WindowReset(t *testing.T) {
	tr := &Tracker{
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	// Use up all burst capacity
	for i := 0; i < rateLimitBurst; i++ {
		allowed, _ := tr.checkRateLimit(addr)
		if !allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// Manually set window start to past to simulate window expiration
	tr.rateLimiterMu.Lock()
	tr.rateLimiter[MakeRateLimitKey(addr)].windowStart = time.Now().Add(-3 * time.Minute)
	tr.rateLimiterMu.Unlock()

	// Should be allowed after window expires
	allowed, remaining := tr.checkRateLimit(addr)
	if !allowed {
		t.Errorf("should be allowed after window reset, remaining=%v", remaining)
	}

	// Verify counter was reset
	tr.rateLimiterMu.Lock()
	count := tr.rateLimiter[MakeRateLimitKey(addr)].count
	tr.rateLimiterMu.Unlock()
	if count != 1 {
		t.Errorf("count = %d, want 1 after window reset", count)
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

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
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

// TestCleanupStalePeers_Integration tests the full cleanup flow end-to-end.
func TestCleanupStalePeers_Integration(t *testing.T) {
	tr := &Tracker{
		torrents:    make(map[HashID]*Torrent),
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	// Create torrent with mix of stale and active peers
	torrent := tr.getOrCreateTorrent(NewHashID([]byte("12345678901234567890")))
	staleLeecher := NewHashID([]byte("stale_leecher_______"))
	staleSeeder := NewHashID([]byte("stale_seeder________"))
	activePeer := NewHashID([]byte("active_peer_________"))

	torrent.addPeer(staleLeecher, net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.addPeer(staleSeeder, net.ParseIP("192.168.1.2"), 6882, 0)
	torrent.addPeer(activePeer, net.ParseIP("192.168.1.3"), 6883, 1000)

	// Mark two peers as stale (70 minutes ago)
	staleTime := time.Now().Add(-70 * time.Minute)
	torrent.mu.Lock()
	torrent.peers[staleLeecher].LastAnnounced = staleTime
	torrent.peers[staleSeeder].LastAnnounced = staleTime
	torrent.mu.Unlock()

	// Run full cleanup
	tr.cleanupStalePeers()

	// Verify results
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()

	if _, exists := torrent.peers[staleLeecher]; exists {
		t.Error("stale leecher should have been removed")
	}
	if _, exists := torrent.peers[staleSeeder]; exists {
		t.Error("stale seeder should have been removed")
	}
	if _, exists := torrent.peers[activePeer]; !exists {
		t.Error("active peer should still exist")
	}
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1 (only active)", torrent.leechers)
	}
	if torrent.seeders != 0 {
		t.Errorf("seeders = %d, want 0", torrent.seeders)
	}
}

// TestCleanupStalePeers_RemovesEmptyTorrents verifies empty torrents are deleted.
func TestCleanupStalePeers_RemovesEmptyTorrents(t *testing.T) {
	tr := &Tracker{
		torrents:    make(map[HashID]*Torrent),
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	// Create torrent that will become empty after cleanup
	hash := NewHashID([]byte("will_become_empty___"))
	torrent := tr.getOrCreateTorrent(hash)
	stalePeer := NewHashID([]byte("stale_peer__________"))

	torrent.addPeer(stalePeer, net.ParseIP("192.168.1.1"), 6881, 1000)

	// Mark peer as stale
	torrent.mu.Lock()
	torrent.peers[stalePeer].LastAnnounced = time.Now().Add(-70 * time.Minute)
	torrent.mu.Unlock()

	// Run cleanup
	tr.cleanupStalePeers()

	// Verify torrent was removed (became empty)
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if _, exists := tr.torrents[hash]; exists {
		t.Error("empty torrent should have been removed")
	}
}

// TestCleanupStalePeers_EmptyTracker verifies cleanup handles empty tracker gracefully.
func TestCleanupStalePeers_EmptyTracker(t *testing.T) {
	tr := &Tracker{
		torrents:    make(map[HashID]*Torrent),
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	// Should not panic on empty tracker
	tr.cleanupStalePeers()

	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if len(tr.torrents) != 0 {
		t.Error("tracker should still be empty")
	}
}

// TestCleanupSingleTorrent_Unit tests the individual torrent cleanup function.
func TestCleanupSingleTorrent_Unit(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}

	hash := NewHashID([]byte("test_torrent________"))
	torrent := tr.getOrCreateTorrent(hash)

	// Add mix of peers
	staleLeecher := NewHashID([]byte("stale_leecher_______"))
	staleSeeder := NewHashID([]byte("stale_seeder________"))
	activePeer := NewHashID([]byte("active_peer_________"))

	torrent.addPeer(staleLeecher, net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.addPeer(staleSeeder, net.ParseIP("192.168.1.2"), 6882, 0)
	torrent.addPeer(activePeer, net.ParseIP("192.168.1.3"), 6883, 1000)

	// Mark stale peers
	staleTime := time.Now().Add(-70 * time.Minute)
	torrent.mu.Lock()
	torrent.peers[staleLeecher].LastAnnounced = staleTime
	torrent.peers[staleSeeder].LastAnnounced = staleTime
	torrent.mu.Unlock()

	// Test cleanupSingleTorrent directly
	deadline := time.Now().Add(-stalePeerThreshold)
	isEmpty := tr.cleanupSingleTorrent(hash, deadline)

	if isEmpty {
		t.Error("torrent should not be empty (has active peer)")
	}

	// Verify stale peers removed, active remains
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if len(torrent.peers) != 1 {
		t.Errorf("peer count = %d, want 1", len(torrent.peers))
	}
	if torrent.leechers != 1 || torrent.seeders != 0 {
		t.Errorf("counters: leechers=%d, seeders=%d, want 1,0", torrent.leechers, torrent.seeders)
	}
}

// TestCleanupSingleTorrent_EmptyResult verifies cleanupSingleTorrent returns true when empty.
func TestCleanupSingleTorrent_EmptyResult(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}

	hash := NewHashID([]byte("becomes_empty_______"))
	torrent := tr.getOrCreateTorrent(hash)
	stalePeer := NewHashID([]byte("stale_______________"))

	torrent.addPeer(stalePeer, net.ParseIP("192.168.1.1"), 6881, 1000)
	torrent.mu.Lock()
	torrent.peers[stalePeer].LastAnnounced = time.Now().Add(-70 * time.Minute)
	torrent.mu.Unlock()

	deadline := time.Now().Add(-stalePeerThreshold)
	isEmpty := tr.cleanupSingleTorrent(hash, deadline)

	if !isEmpty {
		t.Error("cleanupSingleTorrent should return true for empty torrent")
	}
}

// TestCleanupSingleTorrent_NonExistent verifies cleanup handles deleted torrents.
func TestCleanupSingleTorrent_NonExistent(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}

	// Try to cleanup non-existent torrent
	nonExistent := NewHashID([]byte("does_not_exist______"))
	deadline := time.Now().Add(-stalePeerThreshold)
	isEmpty := tr.cleanupSingleTorrent(nonExistent, deadline)

	if isEmpty {
		t.Error("cleanupSingleTorrent should return false for non-existent torrent")
	}
}

// TestRemoveEmptyTorrents_Unit tests the bulk deletion function.
func TestRemoveEmptyTorrents_Unit(t *testing.T) {
	tr := &Tracker{
		torrents: make(map[HashID]*Torrent),
	}

	// Create two torrents - one empty, one with peers
	emptyHash := NewHashID([]byte("empty_torrent_______"))
	nonEmptyHash := NewHashID([]byte("non_empty_torrent___"))

	tr.getOrCreateTorrent(emptyHash)
	nonEmptyTorrent := tr.getOrCreateTorrent(nonEmptyHash)

	// Add peer to non-empty torrent
	peerID := NewHashID([]byte("active_peer_________"))
	nonEmptyTorrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000)

	// Both appear empty initially
	emptyTorrents := []HashID{emptyHash, nonEmptyHash}
	tr.removeEmptyTorrents(emptyTorrents)

	// Verify only truly empty torrent was removed
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	if _, exists := tr.torrents[emptyHash]; exists {
		t.Error("empty torrent should have been removed")
	}
	if _, exists := tr.torrents[nonEmptyHash]; !exists {
		t.Error("non-empty torrent should still exist (double-check prevented deletion)")
	}
}

// TestCleanupRateLimiters_Unit tests the rate limiter cleanup function directly.
func TestCleanupRateLimiters_Unit(t *testing.T) {
	tr := &Tracker{
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	// Add entries at different ages
	deadline := time.Now().Add(-rateLimitCleanupThreshold)

	tr.rateLimiterMu.Lock()
	tr.rateLimiter["stale"] = &rateLimitEntry{
		count:       5,
		windowStart: deadline.Add(-time.Minute), // Before deadline - stale
	}
	tr.rateLimiter["fresh"] = &rateLimitEntry{
		count:       3,
		windowStart: deadline.Add(time.Minute), // After deadline - fresh
	}
	tr.rateLimiterMu.Unlock()

	// Run cleanup
	tr.cleanupRateLimiters(deadline)

	// Verify only stale removed
	tr.rateLimiterMu.Lock()
	defer tr.rateLimiterMu.Unlock()

	if _, exists := tr.rateLimiter["stale"]; exists {
		t.Error("stale entry should have been removed")
	}
	if _, exists := tr.rateLimiter["fresh"]; !exists {
		t.Error("fresh entry should still exist")
	}
}

// TestCleanupRateLimiters_Empty verifies cleanup handles empty map.
func TestCleanupRateLimiters_Empty(t *testing.T) {
	tr := &Tracker{
		rateLimiter: make(map[string]*rateLimitEntry),
	}

	deadline := time.Now().Add(-rateLimitCleanupThreshold)
	tr.cleanupRateLimiters(deadline) // Should not panic

	tr.rateLimiterMu.Lock()
	defer tr.rateLimiterMu.Unlock()
	if len(tr.rateLimiter) != 0 {
		t.Error("rate limiter should still be empty")
	}
}
