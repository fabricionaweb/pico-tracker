package main

import (
	"crypto/sha256"
	"net"
	"testing"
	"time"
)

func setupSecretKey(t *testing.T) {
	t.Helper()
	h := sha256.New()
	h.Write([]byte("test-secret"))
	copy(secretKey[:], h.Sum(nil))
}

func TestGenerateConnectionID(t *testing.T) {
	setupSecretKey(t)

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	id := generateConnectionID(addr)

	timestamp := uint32(id >> 32)
	now := uint32(time.Now().Unix())
	if timestamp != now {
		t.Errorf("timestamp = %d, want %d (current time)", timestamp, now)
	}
}

func TestValidateConnectionID_Valid(t *testing.T) {
	setupSecretKey(t)

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	id := generateConnectionID(addr)

	if !validateConnectionID(id, addr) {
		t.Error("validateConnectionID returned false for valid ID")
	}
}

func TestValidateConnectionID_Expired(t *testing.T) {
	setupSecretKey(t)

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	id := generateConnectionID(addr)

	// BEP 15 specifies 2-minute expiration; use 3 minutes to safely exceed it
	expiredTimestamp := uint32(time.Now().Unix() - 3*60)
	expiredID := uint64(expiredTimestamp)<<32 | (id & 0xFFFFFFFF)

	if validateConnectionID(expiredID, addr) {
		t.Error("validateConnectionID returned true for expired ID")
	}
}

func TestValidateConnectionID_InvalidSignature(t *testing.T) {
	setupSecretKey(t)

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	id := generateConnectionID(addr)

	invalidSig := ^uint32(id) // flip all bits
	invalidID := (id & 0xFFFFFFFF00000000) | uint64(invalidSig)

	if validateConnectionID(invalidID, addr) {
		t.Error("validateConnectionID returned true for invalid signature")
	}
}

func TestValidateConnectionID_DifferentIP(t *testing.T) {
	setupSecretKey(t)

	addr1 := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	addr2 := &net.UDPAddr{IP: net.ParseIP("192.168.1.2"), Port: 6881}

	id := generateConnectionID(addr1)

	if validateConnectionID(id, addr2) {
		t.Error("validateConnectionID returned true for different IP")
	}
}

func TestConnectionID_IPv6(t *testing.T) {
	setupSecretKey(t)

	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6881}
	id := generateConnectionID(addr)

	if !validateConnectionID(id, addr) {
		t.Error("validateConnectionID returned false for valid IPv6 ID")
	}
}

func TestValidateConnectionID_WrongSecret(t *testing.T) {
	// Save original secretKey to restore after test
	originalKey := secretKey
	defer func() {
		secretKey = originalKey
	}()

	h := sha256.New()
	h.Write([]byte("secret-A"))
	copy(secretKey[:], h.Sum(nil))

	addr := &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 6881}
	id := generateConnectionID(addr)

	h = sha256.New()
	h.Write([]byte("secret-B"))
	copy(secretKey[:], h.Sum(nil))

	if validateConnectionID(id, addr) {
		t.Error("validateConnectionID returned true for wrong secret")
	}
}

func TestBufferPool(t *testing.T) {
	t.Run("getBuffer returns buffer with sufficient capacity", func(t *testing.T) {
		buf := getBuffer()
		if buf == nil {
			t.Fatal("getBuffer returned nil")
		}
		// Buffer should have at least maxPacketSize capacity
		// (sync.Pool may return larger buffers from other tests)
		if cap(*buf) < maxPacketSize {
			t.Errorf("buffer capacity = %d, want at least %d", cap(*buf), maxPacketSize)
		}
		putBuffer(buf)
	})

	t.Run("putBuffer resets slice length", func(t *testing.T) {
		buf := getBuffer()
		*buf = append(*buf, []byte("some data")...)
		if len(*buf) == 0 {
			t.Error("buffer should have data before put")
		}
		putBuffer(buf)

		// putBuffer should reset the slice to zero length
		// Note: we can't reliably test getBuffer returns len=0 because
		// other tests may put buffers back into the pool concurrently
		if len(*buf) != 0 {
			t.Errorf("buffer length after put = %d, want 0", len(*buf))
		}
	})

}
