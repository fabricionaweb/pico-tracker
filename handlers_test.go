package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"
)

// mockPacketConn implements net.PacketConn for testing without real UDP sockets
type mockPacketConn struct {
	localAddr   net.Addr
	writtenData []byte
}

func (m *mockPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	return 0, nil, nil
}

func (m *mockPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	m.writtenData = make([]byte, len(p))
	copy(m.writtenData, p)
	return len(p), nil
}

func (m *mockPacketConn) Close() error                       { return nil }
func (m *mockPacketConn) LocalAddr() net.Addr                { return m.localAddr }
func (m *mockPacketConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockPacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockPacketConn) SetWriteDeadline(t time.Time) error { return nil }

// mockFailingPacketConn simulates a write failure for testing error paths
type mockFailingPacketConn struct {
	mockPacketConn
}

func (m *mockFailingPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return 0, fmt.Errorf("simulated write failure")
}

// setupTracker creates a Tracker with secret key initialized for connection ID generation/validation
func setupTracker(t *testing.T) *Tracker {
	t.Helper()
	h := sha256.New()
	h.Write([]byte("test-secret"))
	copy(secretKey[:], h.Sum(nil))

	return &Tracker{
		torrents:    make(map[HashID]*Torrent),
		rateLimiter: make(map[string]*rateLimitEntry),
	}
}

func TestHandleConnect_ResponseFormat(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	transactionID := uint32(12345)

	tr.handleConnect(conn, addr, transactionID)

	if len(mock.writtenData) != 16 {
		t.Fatalf("response length = %d, want 16", len(mock.writtenData))
	}

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionConnect {
		t.Errorf("action = %d, want %d", action, actionConnect)
	}

	txID := binary.BigEndian.Uint32(mock.writtenData[4:8])
	if txID != transactionID {
		t.Errorf("transaction_id = %d, want %d", txID, transactionID)
	}

	connectionID := binary.BigEndian.Uint64(mock.writtenData[8:16])
	if connectionID == 0 {
		t.Error("connection_id should not be zero")
	}
}

func TestHandleConnect_RateLimitExceeded(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	for i := 0; i < rateLimitBurst; i++ {
		tr.handleConnect(conn, addr, uint32(i))
	}

	tr.handleConnect(conn, addr, uint32(rateLimitBurst))

	if len(mock.writtenData) == 0 {
		t.Error("should have sent error response when rate limited")
	}

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d (error)", action, actionError)
	}

	// Verify error message contains rate limit info
	errorMsg := string(mock.writtenData[8:])
	if !bytes.Contains(mock.writtenData[8:], []byte("rate limit exceeded")) {
		t.Errorf("error message = %q, want to contain 'rate limit exceeded'", errorMsg)
	}
}

func TestHandleAnnounce_PacketTooShort(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	packet := make([]byte, 50)
	binary.BigEndian.PutUint64(packet[0:8], 12345)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 999)

	tr.handleAnnounce(conn, addr, packet, 999)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d", action, actionError)
	}

	// Verify error message
	errorMsg := string(mock.writtenData[8:])
	if !bytes.Contains(mock.writtenData[8:], []byte("invalid packet size")) {
		t.Errorf("error message = %q, want to contain 'invalid packet size'", errorMsg)
	}
}

func TestHandleAnnounce_PortZero(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)

	// Announce packet format (BEP 15):
	// [connection_id:8][action:4][transaction_id:4][info_hash:20][peer_id:20]
	// [downloaded:8][left:8][uploaded:8][event:4][IP:4][key:4][num_want:4][port:2]
	// Total: 98 bytes
	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "12345678901234567890")  // info_hash
	copy(packet[36:56], "peer1_______________")  // peer_id
	binary.BigEndian.PutUint16(packet[96:98], 0) // port = 0 (invalid)

	tr.handleAnnounce(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d", action, actionError)
	}

	// Verify error message
	errorMsg := string(mock.writtenData[8:])
	if !bytes.Contains(mock.writtenData[8:], []byte("port cannot be 0")) {
		t.Errorf("error message = %q, want to contain 'port cannot be 0'", errorMsg)
	}
}

func TestHandleAnnounce_IPv6WithNonZeroIPField(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881}

	connID := generateConnectionID(addr)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "12345678901234567890")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint32(packet[84:88], 0xC0A80101) // 192.168.1.1 - non-zero IP in packet (invalid for IPv6)
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d", action, actionError)
	}

	// Verify error message
	errorMsg := string(mock.writtenData[8:])
	if !bytes.Contains(mock.writtenData[8:], []byte("IP address must be 0 for IPv6")) {
		t.Errorf("error message = %q, want to contain 'IP address must be 0 for IPv6'", errorMsg)
	}
}

func TestHandleAnnounce_AddPeer(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint64(packet[64:72], 1000) // left > 0 (leecher)
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionAnnounce {
		t.Fatalf("action = %d, want %d", action, actionAnnounce)
	}

	torrent := tr.getTorrent(infoHash)
	if torrent == nil {
		t.Fatal("torrent should be created")
	}
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}
}

func TestHandleAnnounce_EventStopped(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))
	peerID := NewHashID([]byte("peer1_______________"))

	tr.getOrCreateTorrent(infoHash).addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint32(packet[80:84], eventStopped)
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionAnnounce {
		t.Fatalf("action = %d, want %d", action, actionAnnounce)
	}

	torrent := tr.getTorrent(infoHash)
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.leechers != 0 {
		t.Errorf("leechers = %d, want 0 after stopped", torrent.leechers)
	}
}

func TestHandleAnnounce_EventCompleted(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint64(packet[64:72], 0) // left = 0 (completed)
	binary.BigEndian.PutUint32(packet[80:84], eventCompleted)
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	torrent := tr.getTorrent(infoHash)
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.seeders != 1 {
		t.Errorf("seeders = %d, want 1", torrent.seeders)
	}
	if torrent.completed != 1 {
		t.Errorf("completed = %d, want 1", torrent.completed)
	}
}

func TestHandleAnnounce_NumWantClamped(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Add more peers than maxPeersPerPacketV4 (200)
	for i := 0; i < 250; i++ {
		peerID := NewHashID([]byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
		tr.getOrCreateTorrent(infoHash).addPeer(peerID, net.ParseIP("192.168.1.1"), uint16(6881+i), 1000)
	}

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint32(packet[92:96], 10000) // request more than max
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// Response should have maxPeersPerPacketV4 * 6 bytes of peers
	expectedPeers := maxPeersPerPacketV4 * 6
	actualPeers := len(mock.writtenData) - 20
	if actualPeers != expectedPeers {
		t.Errorf("peers = %d, want %d (maxPeersPerPacketV4)", actualPeers, expectedPeers)
	}
}

func TestHandleScrape_PacketTooShort(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	packet := make([]byte, 10)

	tr.handleScrape(conn, addr, packet, 999)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d", action, actionError)
	}
}

func TestHandleScrape_NoInfoHashes(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)

	packet := make([]byte, 16)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 12345)

	tr.handleScrape(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d", action, actionError)
	}
}

func TestHandleScrape_UnknownTorrent(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)

	// Scrape packet format (BEP 15):
	// [connection_id:8][action:4][transaction_id:4][info_hash:20]...
	// Minimum: 16 + 20 = 36 bytes per hash
	packet := make([]byte, 36)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "nonexistent_________") // info_hash

	tr.handleScrape(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionScrape {
		t.Fatalf("action = %d, want %d", action, actionScrape)
	}

	// All stats should be zero for unknown torrent
	seeders := binary.BigEndian.Uint32(mock.writtenData[8:12])
	if seeders != 0 {
		t.Errorf("seeders = %d, want 0", seeders)
	}
}

func TestHandleScrape_ExistingTorrent(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	torrent := tr.getOrCreateTorrent(infoHash)
	torrent.addPeer(NewHashID([]byte("peer1_______________")), net.ParseIP("192.168.1.1"), 6881, 0)
	torrent.addPeer(NewHashID([]byte("peer2_______________")), net.ParseIP("192.168.1.2"), 6881, 1000)

	packet := make([]byte, 36)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")

	tr.handleScrape(conn, addr, packet, 12345)

	seeders := binary.BigEndian.Uint32(mock.writtenData[8:12])
	leechers := binary.BigEndian.Uint32(mock.writtenData[16:20])

	if seeders != 1 {
		t.Errorf("seeders = %d, want 1", seeders)
	}
	if leechers != 1 {
		t.Errorf("leechers = %d, want 1", leechers)
	}
}

func TestHandlePacket_PacketTooShort(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	packet := make([]byte, 8)

	tr.handlePacket(conn, addr, packet)

	if len(mock.writtenData) != 0 {
		t.Error("should not respond to too-short packet")
	}
}

func TestHandlePacket_InvalidProtocolID(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	packet := make([]byte, 16)
	binary.BigEndian.PutUint64(packet[0:8], 0xDEADBEEF) // invalid protocol ID
	binary.BigEndian.PutUint32(packet[8:12], actionConnect)
	binary.BigEndian.PutUint32(packet[12:16], 12345)

	tr.handlePacket(conn, addr, packet)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d", action, actionError)
	}
}

func TestHandlePacket_InvalidConnectionID(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	packet := make([]byte, 16)
	binary.BigEndian.PutUint64(packet[0:8], 12345) // invalid connection ID
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)

	tr.handlePacket(conn, addr, packet)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d", action, actionError)
	}
}

func TestHandlePacket_UnknownAction(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	packet := make([]byte, 16)
	binary.BigEndian.PutUint64(packet[0:8], protocolID)
	binary.BigEndian.PutUint32(packet[8:12], 99) // unknown action
	binary.BigEndian.PutUint32(packet[12:16], 12345)

	tr.handlePacket(conn, addr, packet)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d", action, actionError)
	}
}

func TestHandlePacket_ConnectAction(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	packet := make([]byte, 16)
	binary.BigEndian.PutUint64(packet[0:8], protocolID)
	binary.BigEndian.PutUint32(packet[8:12], actionConnect)
	binary.BigEndian.PutUint32(packet[12:16], 12345)

	tr.handlePacket(conn, addr, packet)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionConnect {
		t.Errorf("action = %d, want %d", action, actionConnect)
	}
}

func TestHandlePacket_AnnounceAction(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handlePacket(conn, addr, packet)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionAnnounce {
		t.Errorf("action = %d, want %d", action, actionAnnounce)
	}
}

func TestHandlePacket_ScrapeAction(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)

	packet := make([]byte, 36)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")

	tr.handlePacket(conn, addr, packet)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionScrape {
		t.Errorf("action = %d, want %d", action, actionScrape)
	}
}

func TestHandleAnnounce_ResponseInterval(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	interval := binary.BigEndian.Uint32(mock.writtenData[8:12])
	expectedInterval := announceInterval * 60
	if interval != uint32(expectedInterval) {
		t.Errorf("interval = %d, want %d", interval, expectedInterval)
	}
}

func TestHandleAnnounce_IPv4WithCustomIP(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))
	peerID := NewHashID([]byte("peer1_______________"))

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint32(packet[84:88], 0xC0A80101) // 192.168.1.1 in packet
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	torrent := tr.getTorrent(infoHash)
	torrent.mu.RLock()
	p := torrent.peers[peerID]
	torrent.mu.RUnlock()
	if p.IP.String() != "192.168.1.1" {
		t.Errorf("peer IP = %s, want 192.168.1.1 (from packet)", p.IP)
	}
}

func TestHandleAnnounce_IPv6WithIPFieldZero(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881}

	connID := generateConnectionID(addr)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint32(packet[84:88], 0) // IP field must be 0 for IPv6
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionAnnounce {
		t.Errorf("action = %d, want %d", action, actionAnnounce)
	}
}

func TestHandleAnnounce_NumWantZero(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Add more than defaultNumWant (50) peers
	for i := 0; i < 60; i++ {
		peerID := NewHashID([]byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
		tr.getOrCreateTorrent(infoHash).addPeer(peerID, net.ParseIP("192.168.1.1"), uint16(6881+i), 1000)
	}

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint32(packet[92:96], 0) // 0 means "use defaultNumWant"
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// Should return defaultNumWant peers (50), not all 60
	expectedPeers := defaultNumWant * 6
	actualPeers := len(mock.writtenData) - 20
	if actualPeers != expectedPeers {
		t.Errorf("peers = %d, want %d (defaultNumWant)", actualPeers, expectedPeers)
	}
}

func TestHandleAnnounce_NumWantMaxUint32(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Add more than defaultNumWant (50) peers
	for i := 0; i < 60; i++ {
		peerID := NewHashID([]byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
		tr.getOrCreateTorrent(infoHash).addPeer(peerID, net.ParseIP("192.168.1.1"), uint16(6881+i), 1000)
	}

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint32(packet[92:96], 0xFFFFFFFF) // 0xFFFFFFFF means "use defaultNumWant"
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// Should return defaultNumWant peers (50), not all 60
	expectedPeers := defaultNumWant * 6
	actualPeers := len(mock.writtenData) - 20
	if actualPeers != expectedPeers {
		t.Errorf("peers = %d, want %d (defaultNumWant)", actualPeers, expectedPeers)
	}
}

func TestHandlePacket_LoopbackUnknownAction(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6881}

	packet := make([]byte, 16)
	binary.BigEndian.PutUint64(packet[0:8], protocolID)
	binary.BigEndian.PutUint32(packet[8:12], 99) // unknown action
	binary.BigEndian.PutUint32(packet[12:16], 12345)

	tr.handlePacket(conn, addr, packet)

	// Loopback should get plain text response, not error action
	if string(mock.writtenData) != "unknown action\n" {
		t.Errorf("loopback response = %q, want %q", string(mock.writtenData), "unknown action\n")
	}
}

func TestHandleScrape_MultipleInfoHashes(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash1 := NewHashID([]byte("torrent1___________"))
	infoHash2 := NewHashID([]byte("torrent2___________"))

	torrent1 := tr.getOrCreateTorrent(infoHash1)
	torrent1.addPeer(NewHashID([]byte("peer1_______________")), net.ParseIP("192.168.1.1"), 6881, 0)

	torrent2 := tr.getOrCreateTorrent(infoHash2)
	torrent2.addPeer(NewHashID([]byte("peer2_______________")), net.ParseIP("192.168.1.2"), 6881, 1000)

	// 2 info hashes = 40 bytes + 16 header = 56 bytes
	packet := make([]byte, 56)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent1___________")
	copy(packet[36:56], "torrent2___________")

	tr.handleScrape(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionScrape {
		t.Fatalf("action = %d, want %d", action, actionScrape)
	}

	// Should have 2 * 12 = 24 bytes of stats
	if len(mock.writtenData) != 8+24 {
		t.Errorf("response length = %d, want 32", len(mock.writtenData))
	}

	// First torrent: 1 seeder, 0 leechers
	seeders1 := binary.BigEndian.Uint32(mock.writtenData[8:12])
	leechers1 := binary.BigEndian.Uint32(mock.writtenData[16:20])
	if seeders1 != 1 || leechers1 != 0 {
		t.Errorf("torrent1: seeders=%d, leechers=%d, want 1,0", seeders1, leechers1)
	}

	// Second torrent: 0 seeders, 1 leecher
	seeders2 := binary.BigEndian.Uint32(mock.writtenData[20:24])
	leechers2 := binary.BigEndian.Uint32(mock.writtenData[28:32])
	if seeders2 != 0 || leechers2 != 1 {
		t.Errorf("torrent2: seeders=%d, leechers=%d, want 0,1", seeders2, leechers2)
	}
}

func TestHandleAnnounce_SeederReannouncesAsSeeder(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// First announce: seeder
	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint64(packet[64:72], 0) // left = 0
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// Second announce: still seeder (re-announce)
	tr.handleAnnounce(conn, addr, packet, 12346)

	torrent := tr.getTorrent(infoHash)
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.seeders != 1 {
		t.Errorf("seeders = %d, want 1", torrent.seeders)
	}
	if torrent.completed != 1 {
		t.Errorf("completed = %d, want 1 (should not double-count)", torrent.completed)
	}
}

func TestHandleAnnounce_LeecherReannouncesAsLeecher(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// First announce: leecher
	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint64(packet[64:72], 1000) // left > 0
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// Second announce: still leecher (left > 0)
	tr.handleAnnounce(conn, addr, packet, 12346)

	torrent := tr.getTorrent(infoHash)
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}
	if torrent.completed != 0 {
		t.Errorf("completed = %d, want 0", torrent.completed)
	}
}

func TestHandleAnnounce_ResponseSeedersLeechers(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Add 3 seeders, 2 leechers
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("seeder1____________")), net.ParseIP("192.168.1.1"), 6881, 0)
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("seeder2____________")), net.ParseIP("192.168.1.2"), 6882, 0)
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("seeder3____________")), net.ParseIP("192.168.1.3"), 6883, 0)
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("leecher1___________")), net.ParseIP("192.168.1.4"), 6884, 1000)
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("leecher2___________")), net.ParseIP("192.168.1.5"), 6885, 2000)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint64(packet[64:72], 5000) // left > 0 so requester is leecher
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	leechers := binary.BigEndian.Uint32(mock.writtenData[12:16])
	seeders := binary.BigEndian.Uint32(mock.writtenData[16:20])

	if leechers != 3 { // 2 existing + 1 requester
		t.Errorf("leechers in response = %d, want 3", leechers)
	}
	if seeders != 3 {
		t.Errorf("seeders in response = %d, want 3", seeders)
	}
}

func TestHandleAnnounce_IPv6PeerFormat(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("peer1_______________")), net.ParseIP("2001:db8::1"), 6881, 0)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// IPv6 peers are 18 bytes each (16 bytes IP + 2 bytes port)
	peersLen := len(mock.writtenData) - 20
	if peersLen != 18 {
		t.Errorf("IPv6 peers length = %d, want 18", peersLen)
	}
}

func TestHandleScrape_CompletedField(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	torrent := tr.getOrCreateTorrent(infoHash)
	torrent.addPeer(NewHashID([]byte("seeder1____________")), net.ParseIP("192.168.1.1"), 6881, 0)
	torrent.addPeer(NewHashID([]byte("leecher1___________")), net.ParseIP("192.168.1.2"), 6881, 1000)

	// Simulate completion (eventCompleted increments completed)
	torrent.mu.Lock()
	torrent.completed = 5
	torrent.mu.Unlock()

	packet := make([]byte, 36)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")

	tr.handleScrape(conn, addr, packet, 12345)

	completed := binary.BigEndian.Uint32(mock.writtenData[12:16])
	if completed != 5 {
		t.Errorf("completed = %d, want 5", completed)
	}
}

func TestHandleAnnounce_NumWantLessThanAvailable(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Add 10 peers
	for i := 0; i < 10; i++ {
		peerID := NewHashID([]byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
		tr.getOrCreateTorrent(infoHash).addPeer(peerID, net.ParseIP("192.168.1.1"), uint16(6881+i), 1000)
	}

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint32(packet[92:96], 5) // request only 5
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// Should return 5 peers * 6 bytes = 30 bytes
	expectedPeers := 5 * 6
	actualPeers := len(mock.writtenData) - 20
	if actualPeers != expectedPeers {
		t.Errorf("peers = %d, want %d", actualPeers, expectedPeers)
	}
}

func TestHandleAnnounce_IPv6PeersToIPv4Client(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881} // IPv4 client

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Add only IPv6 peers
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("ipv6peer1___________")), net.ParseIP("2001:db8::1"), 6881, 1000)
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("ipv6peer2___________")), net.ParseIP("2001:db8::2"), 6882, 1000)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// IPv4 client should get no peers (IPv6 peers filtered out)
	peersLen := len(mock.writtenData) - 20
	if peersLen != 0 {
		t.Errorf("IPv4 client got %d bytes, want 0 (IPv6 peers filtered)", peersLen)
	}
}

func TestHandleAnnounce_IPv4PeersToIPv6Client(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881} // IPv6 client

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Add only IPv4 peers
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("ipv4peer1___________")), net.ParseIP("192.168.1.1"), 6881, 1000)
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("ipv4peer2___________")), net.ParseIP("192.168.1.2"), 6882, 1000)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// IPv6 client should get no peers (IPv4 peers filtered out)
	peersLen := len(mock.writtenData) - 20
	if peersLen != 0 {
		t.Errorf("IPv6 client got %d bytes, want 0 (IPv4 peers filtered)", peersLen)
	}
}

func TestHandleAnnounce_EventStarted(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// eventStarted should add/update peer
	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint64(packet[64:72], 1000) // left > 0
	binary.BigEndian.PutUint32(packet[80:84], eventStarted)
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	torrent := tr.getTorrent(infoHash)
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}
}

func TestHandleAnnounce_EventNone(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// eventNone (0) is default - regular update, should add peer like eventStarted
	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint64(packet[64:72], 1000) // left > 0
	binary.BigEndian.PutUint32(packet[80:84], eventNone)
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionAnnounce {
		t.Fatalf("action = %d, want %d", action, actionAnnounce)
	}

	torrent := tr.getTorrent(infoHash)
	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}
}

func TestHandleAnnounce_PeerIPPortEncoding(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	connID := generateConnectionID(addr)
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Add a peer with known IP and port
	peerIP := net.ParseIP("192.168.1.100")
	peerPort := uint16(6889)
	tr.getOrCreateTorrent(infoHash).addPeer(NewHashID([]byte("existingpeer_______")), peerIP, peerPort, 0)

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], connID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "requester__________")
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handleAnnounce(conn, addr, packet, 12345)

	// Response should have 1 peer = 6 bytes
	peersStart := 20
	peerIPResp := net.IP(mock.writtenData[peersStart : peersStart+4])
	peerPortResp := binary.BigEndian.Uint16(mock.writtenData[peersStart+4 : peersStart+6])

	if peerIPResp.String() != "192.168.1.100" {
		t.Errorf("peer IP = %s, want 192.168.1.100", peerIPResp)
	}
	if peerPortResp != 6889 {
		t.Errorf("peer port = %d, want 6889", peerPortResp)
	}
}

// Security tests

func TestHandleAnnounce_InvalidConnectionID(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	// Create valid connection ID then modify it
	validConnID := generateConnectionID(addr)
	invalidConnID := validConnID ^ 0xFFFFFFFF // Flip all bits in signature

	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], invalidConnID)
	binary.BigEndian.PutUint32(packet[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")
	copy(packet[36:56], "peer1_______________")
	binary.BigEndian.PutUint16(packet[96:98], 6881)

	tr.handlePacket(conn, addr, packet)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d (error)", action, actionError)
	}

	errorMsg := string(mock.writtenData[8:])
	if !bytes.Contains(mock.writtenData[8:], []byte("invalid connection ID")) {
		t.Errorf("error message = %q, want to contain 'invalid connection ID'", errorMsg)
	}
}

func TestHandleScrape_InvalidConnectionID(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	// Create valid connection ID then modify it
	validConnID := generateConnectionID(addr)
	invalidConnID := validConnID ^ 0xFFFFFFFF // Flip all bits in signature

	packet := make([]byte, 36)
	binary.BigEndian.PutUint64(packet[0:8], invalidConnID)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 12345)
	copy(packet[16:36], "torrent12345678901")

	tr.handlePacket(conn, addr, packet)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionError {
		t.Errorf("action = %d, want %d (error)", action, actionError)
	}
}

// Integration tests

func TestFullFlow_ConnectAnnounceScrape(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	conn := mock
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	infoHash := NewHashID([]byte("torrent12345678901"))

	// Step 1: Connect
	connectPacket := make([]byte, 16)
	binary.BigEndian.PutUint64(connectPacket[0:8], protocolID)
	binary.BigEndian.PutUint32(connectPacket[8:12], actionConnect)
	binary.BigEndian.PutUint32(connectPacket[12:16], 10001)

	tr.handlePacket(conn, addr, connectPacket)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionConnect {
		t.Fatalf("connect failed: action = %d, want %d", action, actionConnect)
	}
	connectionID := binary.BigEndian.Uint64(mock.writtenData[8:16])
	if connectionID == 0 {
		t.Fatal("connection ID is zero")
	}

	// Step 2: Announce
	mock.writtenData = nil
	announcePacket := make([]byte, 98)
	binary.BigEndian.PutUint64(announcePacket[0:8], connectionID)
	binary.BigEndian.PutUint32(announcePacket[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(announcePacket[12:16], 10002)
	copy(announcePacket[16:36], "torrent12345678901")
	copy(announcePacket[36:56], "peer1_______________")
	binary.BigEndian.PutUint64(announcePacket[64:72], 1000)
	binary.BigEndian.PutUint16(announcePacket[96:98], 6881)

	tr.handlePacket(conn, addr, announcePacket)

	action = binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionAnnounce {
		t.Fatalf("announce failed: action = %d, want %d", action, actionAnnounce)
	}

	// Verify peer was added
	torrent := tr.getTorrent(infoHash)
	if torrent == nil {
		t.Fatal("torrent not created")
	}
	torrent.mu.RLock()
	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}
	torrent.mu.RUnlock()

	// Step 3: Scrape
	mock.writtenData = nil
	scrapePacket := make([]byte, 36)
	binary.BigEndian.PutUint64(scrapePacket[0:8], connectionID)
	binary.BigEndian.PutUint32(scrapePacket[8:12], actionScrape)
	binary.BigEndian.PutUint32(scrapePacket[12:16], 10003)
	copy(scrapePacket[16:36], "torrent12345678901")

	tr.handlePacket(conn, addr, scrapePacket)

	action = binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionScrape {
		t.Fatalf("scrape failed: action = %d, want %d", action, actionScrape)
	}

	seeders := binary.BigEndian.Uint32(mock.writtenData[8:12])
	leechers := binary.BigEndian.Uint32(mock.writtenData[16:20])
	if seeders != 0 || leechers != 1 {
		t.Errorf("scrape result: seeders=%d, leechers=%d, want 0,1", seeders, leechers)
	}

	// Verify peer was added using infoHash
	torrent = tr.getTorrent(infoHash)
	if torrent == nil {
		t.Fatal("torrent should exist")
	}
}

func TestFullFlow_AnnounceWithPeerExchange(t *testing.T) {
	tr := setupTracker(t)
	mock := &mockPacketConn{}
	addr1 := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	addr2 := &net.UDPAddr{IP: net.ParseIP("192.168.1.2"), Port: 6882}

	// Peer 1 announces
	connID1 := generateConnectionID(addr1)
	packet1 := make([]byte, 98)
	binary.BigEndian.PutUint64(packet1[0:8], connID1)
	binary.BigEndian.PutUint32(packet1[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet1[12:16], 10001)
	copy(packet1[16:36], "torrent12345678901")
	copy(packet1[36:56], "peer1_______________")
	binary.BigEndian.PutUint64(packet1[64:72], 1000)
	binary.BigEndian.PutUint16(packet1[96:98], 6881)
	tr.handleAnnounce(mock, addr1, packet1, 10001)

	// Peer 2 announces and should get Peer 1 in response
	mock.writtenData = nil
	connID2 := generateConnectionID(addr2)
	packet2 := make([]byte, 98)
	binary.BigEndian.PutUint64(packet2[0:8], connID2)
	binary.BigEndian.PutUint32(packet2[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(packet2[12:16], 10002)
	copy(packet2[16:36], "torrent12345678901")
	copy(packet2[36:56], "peer2_______________")
	binary.BigEndian.PutUint64(packet2[64:72], 2000)
	binary.BigEndian.PutUint16(packet2[96:98], 6882)
	tr.handleAnnounce(mock, addr2, packet2, 10002)

	action := binary.BigEndian.Uint32(mock.writtenData[0:4])
	if action != actionAnnounce {
		t.Fatalf("announce failed: action = %d, want %d", action, actionAnnounce)
	}

	// Verify Peer 2 received Peer 1 in response
	peersLen := len(mock.writtenData) - 20
	if peersLen != 6 { // 1 peer * 6 bytes
		t.Errorf("peers length = %d, want 6", peersLen)
	}

	// Verify peer IP and port are correct
	if peersLen >= 6 {
		peerIP := net.IP(mock.writtenData[20:24])
		peerPort := binary.BigEndian.Uint16(mock.writtenData[24:26])
		if peerIP.String() != "192.168.1.1" {
			t.Errorf("peer IP = %s, want 192.168.1.1", peerIP)
		}
		if peerPort != 6881 {
			t.Errorf("peer port = %d, want 6881", peerPort)
		}
	}

	// Verify stats include both peers
	leechers := binary.BigEndian.Uint32(mock.writtenData[12:16])
	if leechers != 2 {
		t.Errorf("leechers in response = %d, want 2", leechers)
	}
}

func TestSendError_WriteFailure(t *testing.T) {
	tr := setupTracker(t)

	// Create a mock that fails on write
	failingMock := &mockFailingPacketConn{}
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}

	// This should not panic even though WriteTo fails
	// The sendError function should log the error but not panic
	tr.sendError(failingMock, addr, 12345, "test error message")

	// Verify no data was written (since WriteTo failed)
	if len(failingMock.writtenData) != 0 {
		t.Errorf("writtenData length = %d, want 0 (WriteTo failed)", len(failingMock.writtenData))
	}
}

// Unit tests for helper functions

func TestParseAnnounceRequest_ValidPacket(t *testing.T) {
	// Create a valid announce packet (98 bytes)
	packet := make([]byte, 98)
	binary.BigEndian.PutUint64(packet[0:8], 12345)        // connection_id
	binary.BigEndian.PutUint32(packet[8:12], 1)           // action
	binary.BigEndian.PutUint32(packet[12:16], 999)        // transaction_id
	copy(packet[16:36], "infohash12345678901")            // info_hash (20 bytes)
	copy(packet[36:56], "peer_id1234567890123")           // peer_id (20 bytes)
	binary.BigEndian.PutUint64(packet[56:64], 1000000)    // downloaded
	binary.BigEndian.PutUint64(packet[64:72], 5000)       // left
	binary.BigEndian.PutUint64(packet[72:80], 100000)     // uploaded
	binary.BigEndian.PutUint32(packet[80:84], 2)          // event (completed)
	binary.BigEndian.PutUint32(packet[84:88], 0xC0A80101) // IP (192.168.1.1)
	binary.BigEndian.PutUint32(packet[88:92], 12345)      // key
	binary.BigEndian.PutUint32(packet[92:96], 50)         // num_want
	binary.BigEndian.PutUint16(packet[96:98], 6881)       // port

	req, ok := parseAnnounceRequest(packet)
	if !ok {
		t.Fatal("parseAnnounceRequest returned false for valid packet")
	}

	// Verify info_hash was extracted correctly (should be hex of the 20 bytes we copied)
	expectedInfoHashBytes := make([]byte, 20)
	copy(expectedInfoHashBytes, "infohash12345678901")
	if req.infoHash != NewHashID(expectedInfoHashBytes) {
		t.Errorf("info_hash mismatch: got %s, want %x", req.infoHash.String(), expectedInfoHashBytes)
	}

	// Verify peer_id was extracted correctly
	expectedPeerIDBytes := make([]byte, 20)
	copy(expectedPeerIDBytes, "peer_id1234567890123")
	if req.peerID != NewHashID(expectedPeerIDBytes) {
		t.Errorf("peer_id mismatch: got %s, want %x", req.peerID.String(), expectedPeerIDBytes)
	}
	if req.left != 5000 {
		t.Errorf("left = %d, want 5000", req.left)
	}
	if req.event != 2 {
		t.Errorf("event = %d, want 2 (completed)", req.event)
	}
	if req.ipAddr != 0xC0A80101 {
		t.Errorf("ip_addr = %x, want 0xC0A80101", req.ipAddr)
	}
	if req.numWant != 50 {
		t.Errorf("num_want = %d, want 50", req.numWant)
	}
	if req.port != 6881 {
		t.Errorf("port = %d, want 6881", req.port)
	}
}

func TestParseAnnounceRequest_PacketTooShort(t *testing.T) {
	// Test with packet that's too short
	packet := make([]byte, 50) // Less than minAnnouncePacketSize (98)

	req, ok := parseAnnounceRequest(packet)
	if ok {
		t.Error("parseAnnounceRequest should return false for short packet")
	}
	if req.infoHash != (HashID{}) {
		t.Error("should return zero values for short packet")
	}
}

func TestCalculateNumWant_DefaultValues(t *testing.T) {
	// Test 0 returns defaultNumWant
	result := calculateNumWant(0, 200)
	if result != defaultNumWant {
		t.Errorf("calculateNumWant(0) = %d, want %d", result, defaultNumWant)
	}

	// Test 0xFFFFFFFF returns defaultNumWant
	result = calculateNumWant(0xFFFFFFFF, 200)
	if result != defaultNumWant {
		t.Errorf("calculateNumWant(0xFFFFFFFF) = %d, want %d", result, defaultNumWant)
	}
}

func TestCalculateNumWant_WithinLimit(t *testing.T) {
	// Test value within limit
	result := calculateNumWant(50, 200)
	if result != 50 {
		t.Errorf("calculateNumWant(50) = %d, want 50", result)
	}
}

func TestCalculateNumWant_ExceedsLimit(t *testing.T) {
	// Test value exceeding limit gets clamped
	result := calculateNumWant(500, 200)
	if result != 200 {
		t.Errorf("calculateNumWant(500, 200) = %d, want 200 (clamped)", result)
	}
}

func TestDetermineClientIP_IPv4NoCustomIP(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	clientIP, valid, errMsg := determineClientIP(addr, 0)

	if !valid {
		t.Errorf("expected valid, got invalid: %s", errMsg)
	}
	if clientIP.String() != "192.168.1.1" {
		t.Errorf("clientIP = %s, want 192.168.1.1", clientIP)
	}
}

func TestDetermineClientIP_IPv4WithCustomIP(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 6881}
	// Use custom IP 192.168.1.100 (0xC0A80164)
	clientIP, valid, errMsg := determineClientIP(addr, 0xC0A80164)

	if !valid {
		t.Errorf("expected valid, got invalid: %s", errMsg)
	}
	if clientIP.String() != "192.168.1.100" {
		t.Errorf("clientIP = %s, want 192.168.1.100", clientIP)
	}
}

func TestDetermineClientIP_IPv6WithNonZeroIPField(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881}
	// IPv6 clients must send IP field as 0
	clientIP, valid, errMsg := determineClientIP(addr, 0xC0A80101)

	if valid {
		t.Error("expected invalid for IPv6 with non-zero IP field")
	}
	if errMsg != "IP address must be 0 for IPv6" {
		t.Errorf("error message = %q, want 'IP address must be 0 for IPv6'", errMsg)
	}
	if clientIP != nil {
		t.Error("clientIP should be nil for invalid case")
	}
}

func TestDetermineClientIP_IPv6WithZeroIPField(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881}
	// IPv6 with IP field = 0 is valid
	clientIP, valid, errMsg := determineClientIP(addr, 0)

	if !valid {
		t.Errorf("expected valid, got invalid: %s", errMsg)
	}
	if clientIP.String() != "2001:db8::1" {
		t.Errorf("clientIP = %s, want 2001:db8::1", clientIP)
	}
}

func TestGetPeerConfig_IPv4(t *testing.T) {
	peerSize, maxWant := getPeerConfig(true)
	if peerSize != 6 {
		t.Errorf("IPv4 peerSize = %d, want 6", peerSize)
	}
	if maxWant != maxPeersPerPacketV4 {
		t.Errorf("IPv4 maxWant = %d, want %d", maxWant, maxPeersPerPacketV4)
	}
}

func TestGetPeerConfig_IPv6(t *testing.T) {
	peerSize, maxWant := getPeerConfig(false)
	if peerSize != 18 {
		t.Errorf("IPv6 peerSize = %d, want 18", peerSize)
	}
	if maxWant != maxPeersPerPacketV6 {
		t.Errorf("IPv6 maxWant = %d, want %d", maxWant, maxPeersPerPacketV6)
	}
}

func TestUpdateTorrentPeer_EventStopped(t *testing.T) {
	tr := setupTracker(t)
	infoHash := NewHashID([]byte("torrent12345678901"))
	peerID := NewHashID([]byte("peer1_______________"))
	torrent := tr.getOrCreateTorrent(infoHash)

	// First add a peer
	torrent.addPeer(peerID, net.ParseIP("192.168.1.1"), 6881, 1000)
	if torrent.leechers != 1 {
		t.Fatalf("setup failed: expected 1 leecher, got %d", torrent.leechers)
	}

	// Now update with stopped event
	updateTorrentPeer(torrent, peerID, net.ParseIP("192.168.1.1"), 6881, eventStopped, 1000)

	if torrent.leechers != 0 {
		t.Errorf("after stopped: leechers = %d, want 0", torrent.leechers)
	}
}

func TestUpdateTorrentPeer_EventCompleted(t *testing.T) {
	tr := setupTracker(t)
	infoHash := NewHashID([]byte("torrent12345678901"))
	peerID := NewHashID([]byte("peer1_______________"))
	torrent := tr.getOrCreateTorrent(infoHash)

	// Update with completed event
	updateTorrentPeer(torrent, peerID, net.ParseIP("192.168.1.1"), 6881, eventCompleted, 0)

	torrent.mu.RLock()
	defer torrent.mu.RUnlock()
	if torrent.seeders != 1 {
		t.Errorf("seeders = %d, want 1", torrent.seeders)
	}
	if torrent.completed != 1 {
		t.Errorf("completed = %d, want 1", torrent.completed)
	}
}

func TestUpdateTorrentPeer_RegularUpdate(t *testing.T) {
	tr := setupTracker(t)
	infoHash := NewHashID([]byte("torrent12345678901"))
	peerID := NewHashID([]byte("peer1_______________"))
	torrent := tr.getOrCreateTorrent(infoHash)

	// Regular update (no event or eventNone)
	updateTorrentPeer(torrent, peerID, net.ParseIP("192.168.1.1"), 6881, eventNone, 1000)

	if torrent.leechers != 1 {
		t.Errorf("leechers = %d, want 1", torrent.leechers)
	}
}

func TestBuildAnnounceResponse_IPv4(t *testing.T) {
	// Create sample peers data (1 peer = 6 bytes)
	peers := make([]byte, 6)
	peers[0], peers[1], peers[2], peers[3] = 192, 168, 1, 100 // IP
	binary.BigEndian.PutUint16(peers[4:6], 6889)              // port

	response := buildAnnounceResponse(peers, 5, 10, 12345, true)

	// Check response size: header (20) + 1 peer (6) = 26
	if len(response) != 26 {
		t.Fatalf("response length = %d, want 26", len(response))
	}

	// Verify header fields
	action := binary.BigEndian.Uint32(response[0:4])
	if action != actionAnnounce {
		t.Errorf("action = %d, want %d", action, actionAnnounce)
	}

	txID := binary.BigEndian.Uint32(response[4:8])
	if txID != 12345 {
		t.Errorf("transaction_id = %d, want 12345", txID)
	}

	leechers := binary.BigEndian.Uint32(response[12:16])
	if leechers != 10 {
		t.Errorf("leechers = %d, want 10", leechers)
	}

	seeders := binary.BigEndian.Uint32(response[16:20])
	if seeders != 5 {
		t.Errorf("seeders = %d, want 5", seeders)
	}

	// Verify peer data
	peerIP := net.IP(response[20:24])
	peerPort := binary.BigEndian.Uint16(response[24:26])
	if peerIP.String() != "192.168.1.100" {
		t.Errorf("peer IP = %s, want 192.168.1.100", peerIP)
	}
	if peerPort != 6889 {
		t.Errorf("peer port = %d, want 6889", peerPort)
	}
}

func TestBuildAnnounceResponse_IPv6(t *testing.T) {
	// Create sample IPv6 peers data (1 peer = 18 bytes)
	peers := make([]byte, 18)
	copy(peers[0:16], net.ParseIP("2001:db8::1"))  // IPv6 address
	binary.BigEndian.PutUint16(peers[16:18], 6889) // port

	response := buildAnnounceResponse(peers, 3, 7, 54321, false)

	// Check response size: header (20) + 1 peer (18) = 38
	if len(response) != 38 {
		t.Fatalf("response length = %d, want 38", len(response))
	}

	// Verify header
	action := binary.BigEndian.Uint32(response[0:4])
	if action != actionAnnounce {
		t.Errorf("action = %d, want %d", action, actionAnnounce)
	}

	txID := binary.BigEndian.Uint32(response[4:8])
	if txID != 54321 {
		t.Errorf("transaction_id = %d, want 54321", txID)
	}
}

func TestBuildAnnounceResponse_EmptyPeers(t *testing.T) {
	// Test with no peers
	response := buildAnnounceResponse([]byte{}, 0, 0, 99999, true)

	// Should be just the header (20 bytes)
	if len(response) != 20 {
		t.Fatalf("response length = %d, want 20", len(response))
	}

	action := binary.BigEndian.Uint32(response[0:4])
	if action != actionAnnounce {
		t.Errorf("action = %d, want %d", action, actionAnnounce)
	}
}

func TestGetScrapeStats_EmptyTorrent(t *testing.T) {
	tr := setupTracker(t)
	infoHash := NewHashID([]byte("nonexistent_________"))

	stats := tr.getScrapeStats(infoHash)

	if stats.seeders != 0 || stats.completed != 0 || stats.leechers != 0 {
		t.Errorf("empty torrent stats: seeders=%d, completed=%d, leechers=%d, want all 0",
			stats.seeders, stats.completed, stats.leechers)
	}
}

func TestGetScrapeStats_WithPeers(t *testing.T) {
	tr := setupTracker(t)
	infoHash := NewHashID([]byte("torrent12345678901"))
	torrent := tr.getOrCreateTorrent(infoHash)

	// Add seeders and leechers
	torrent.addPeer(NewHashID([]byte("seeder1____________")), net.ParseIP("192.168.1.1"), 6881, 0)
	torrent.addPeer(NewHashID([]byte("seeder2____________")), net.ParseIP("192.168.1.2"), 6882, 0)
	torrent.addPeer(NewHashID([]byte("leecher1___________")), net.ParseIP("192.168.1.3"), 6883, 1000)

	// Set completed manually to test that field
	torrent.mu.Lock()
	torrent.completed = 42
	torrent.mu.Unlock()

	stats := tr.getScrapeStats(infoHash)

	if stats.seeders != 2 {
		t.Errorf("seeders = %d, want 2", stats.seeders)
	}
	if stats.completed != 42 {
		t.Errorf("completed = %d, want 42", stats.completed)
	}
	if stats.leechers != 1 {
		t.Errorf("leechers = %d, want 1", stats.leechers)
	}
}

func TestBuildScrapeResponse_SingleHash(t *testing.T) {
	tr := setupTracker(t)
	infoHash := NewHashID([]byte("torrent12345678901"))
	torrent := tr.getOrCreateTorrent(infoHash)
	torrent.addPeer(NewHashID([]byte("seeder1____________")), net.ParseIP("192.168.1.1"), 6881, 0)

	// Create scrape packet with one info_hash
	packet := make([]byte, 36)
	binary.BigEndian.PutUint64(packet[0:8], 12345)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 999)
	copy(packet[16:36], "torrent12345678901")

	response, numHashes := tr.buildScrapeResponse(packet, 999)

	if numHashes != 1 {
		t.Errorf("numHashes = %d, want 1", numHashes)
	}

	// Response should be: header (8) + 1 entry (12) = 20 bytes
	if len(response) != 20 {
		t.Fatalf("response length = %d, want 20", len(response))
	}

	// Verify header
	action := binary.BigEndian.Uint32(response[0:4])
	if action != actionScrape {
		t.Errorf("action = %d, want %d", action, actionScrape)
	}

	txID := binary.BigEndian.Uint32(response[4:8])
	if txID != 999 {
		t.Errorf("transaction_id = %d, want 999", txID)
	}

	// Verify stats
	seeders := binary.BigEndian.Uint32(response[8:12])
	completed := binary.BigEndian.Uint32(response[12:16])
	leechers := binary.BigEndian.Uint32(response[16:20])

	if seeders != 1 {
		t.Errorf("seeders = %d, want 1", seeders)
	}
	if leechers != 0 {
		t.Errorf("leechers = %d, want 0", leechers)
	}
	if completed != 1 {
		t.Errorf("completed = %d, want 1", completed)
	}
}

func TestBuildScrapeResponse_MultipleHashes(t *testing.T) {
	tr := setupTracker(t)

	// Create two torrents
	infoHash1 := NewHashID([]byte("torrent1___________"))
	infoHash2 := NewHashID([]byte("torrent2___________"))

	torrent1 := tr.getOrCreateTorrent(infoHash1)
	torrent1.addPeer(NewHashID([]byte("peer1______________")), net.ParseIP("192.168.1.1"), 6881, 0)

	torrent2 := tr.getOrCreateTorrent(infoHash2)
	torrent2.addPeer(NewHashID([]byte("peer2______________")), net.ParseIP("192.168.1.2"), 6882, 1000)

	// Create scrape packet with two info_hashes
	packet := make([]byte, 56) // 16 header + 40 bytes for 2 hashes
	binary.BigEndian.PutUint64(packet[0:8], 12345)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 888)
	copy(packet[16:36], "torrent1___________")
	copy(packet[36:56], "torrent2___________")

	response, numHashes := tr.buildScrapeResponse(packet, 888)

	if numHashes != 2 {
		t.Errorf("numHashes = %d, want 2", numHashes)
	}

	// Response should be: header (8) + 2 entries (24) = 32 bytes
	if len(response) != 32 {
		t.Fatalf("response length = %d, want 32", len(response))
	}

	// First torrent stats (bytes 8-19)
	seeders1 := binary.BigEndian.Uint32(response[8:12])
	leechers1 := binary.BigEndian.Uint32(response[16:20])
	if seeders1 != 1 || leechers1 != 0 {
		t.Errorf("torrent1: seeders=%d, leechers=%d, want 1,0", seeders1, leechers1)
	}

	// Second torrent stats (bytes 20-31)
	seeders2 := binary.BigEndian.Uint32(response[20:24])
	leechers2 := binary.BigEndian.Uint32(response[28:32])
	if seeders2 != 0 || leechers2 != 1 {
		t.Errorf("torrent2: seeders=%d, leechers=%d, want 0,1", seeders2, leechers2)
	}
}

func TestBuildScrapeResponse_NonExistentTorrent(t *testing.T) {
	tr := setupTracker(t)

	// Create scrape packet for non-existent torrent
	packet := make([]byte, 36)
	binary.BigEndian.PutUint64(packet[0:8], 12345)
	binary.BigEndian.PutUint32(packet[8:12], actionScrape)
	binary.BigEndian.PutUint32(packet[12:16], 777)
	copy(packet[16:36], "nonexistent_________")

	response, numHashes := tr.buildScrapeResponse(packet, 777)

	if numHashes != 1 {
		t.Errorf("numHashes = %d, want 1", numHashes)
	}

	// All stats should be 0 for non-existent torrent
	seeders := binary.BigEndian.Uint32(response[8:12])
	completed := binary.BigEndian.Uint32(response[12:16])
	leechers := binary.BigEndian.Uint32(response[16:20])

	if seeders != 0 || completed != 0 || leechers != 0 {
		t.Errorf("non-existent torrent: seeders=%d, completed=%d, leechers=%d, want all 0",
			seeders, completed, leechers)
	}
}
