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
