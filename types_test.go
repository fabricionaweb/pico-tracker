package main

import (
	"bytes"
	"testing"
)

// per BEP 15, both info_hash and peer_id are 20 bytes
func TestHashID_NewHashID(t *testing.T) {
	t.Run("creates HashID from exactly 20 bytes", func(t *testing.T) {
		data := []byte("12345678901234567890") // 20 bytes
		h := NewHashID(data)

		if !bytes.Equal(h[:], []byte(data)) {
			t.Errorf("expected %v, got %v", data, h[:])
		}
	})

	t.Run("creates HashID from more than 20 bytes (uses first 20)", func(t *testing.T) {
		data := []byte("12345678901234567890extra")
		h := NewHashID(data)

		expected := []byte("12345678901234567890")
		if !bytes.Equal(h[:], expected) {
			t.Errorf("expected %v, got %v", expected, h[:])
		}
	})

	t.Run("creates HashID from exactly 20 zero bytes", func(t *testing.T) {
		data := make([]byte, 20)
		h := NewHashID(data)

		// Each byte should be 0x00
		for i := 0; i < 20; i++ {
			b := h[i]
			if b != 0 {
				t.Errorf("byte at index %d: expected 0x00, got 0x%02x", i, b)
			}
		}
	})

	t.Run("creates HashID from max byte value 0xFF", func(t *testing.T) {
		data := make([]byte, 20)
		for i := 0; i < 20; i++ {
			data[i] = 0xFF
		}
		h := NewHashID(data)

		for i := 0; i < 20; i++ {
			if h[i] != 0xFF {
				t.Errorf("byte at index %d: expected 0xFF, got 0x%02x", i, h[i])
			}
		}
	})

	t.Run("HashID has exactly 20 bytes", func(t *testing.T) {
		data := []byte("12345678901234567890")

		if len(NewHashID(data)) != 20 {
			t.Errorf("expected length 20, got %d", len(NewHashID(data)))
		}
	})
}

// 20 bytes -> 40 hex chars (each byte becomes 2 hex characters)
func TestHashID_String(t *testing.T) {
	t.Run("returns 40-character hex string", func(t *testing.T) {
		data := []byte("12345678901234567890")
		h := NewHashID(data)

		s := h.String()
		if len(s) != 40 {
			t.Errorf("expected length 40, got %d", len(s))
		}
	})

	t.Run("returns correct hex for known value", func(t *testing.T) {
		// All zeros should be 40 zeros in hex
		var h HashID
		s := h.String()

		expected := "0000000000000000000000000000000000000000"
		if s != expected {
			t.Errorf("expected %s, got %s", expected, s)
		}
	})

	t.Run("returns correct hex for non-zero bytes", func(t *testing.T) {
		// 0x01 -> "01", 0x02 -> "02", etc.
		data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a,
			0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14}
		h := NewHashID(data)

		s := h.String()
		expected := "0102030405060708090a0b0c0d0e0f1011121314"
		if s != expected {
			t.Errorf("expected %s, got %s", expected, s)
		}
	})

	t.Run("returns consistent hex for same input", func(t *testing.T) {
		data := []byte("12345678901234567890")
		h := NewHashID(data)

		s1 := h.String()
		s2 := h.String()
		if s1 != s2 {
			t.Errorf("expected consistent output, got %s and %s", s1, s2)
		}
	})

	t.Run("different inputs produce different hex", func(t *testing.T) {
		h1 := NewHashID([]byte("12345678901234567890"))
		h2 := NewHashID([]byte("abcdefghijklmnopqrst"))

		if h1.String() == h2.String() {
			t.Error("different inputs should produce different hex strings")
		}
	})
}
