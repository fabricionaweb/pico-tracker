package main

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWhitelistFile(t *testing.T) {
	// Create temp dir for test files
	tempDir := t.TempDir()

	t.Run("valid file with comments and empty lines", func(t *testing.T) {
		content := `# This is a comment
a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0

# Another comment
d4e5f6a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6ef

`
		filePath := filepath.Join(tempDir, "valid.txt")
		if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		hashes := loadWhitelistFile(filePath)
		if len(hashes) != 2 {
			t.Errorf("expected 2 hashes, got %d", len(hashes))
		}

		// Check first hash exists
		decoded, _ := hex.DecodeString("a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0")
		var h1 HashID
		copy(h1[:], decoded)
		if _, ok := hashes[h1]; !ok {
			t.Error("expected first hash to be in whitelist")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		hashes := loadWhitelistFile(filepath.Join(tempDir, "nonexistent.txt"))
		// Fail-closed: missing file returns empty map (blocks all torrents)
		if hashes == nil {
			t.Error("expected empty map for nonexistent file (fail-closed), got nil")
		}
		if len(hashes) != 0 {
			t.Errorf("expected empty map for nonexistent file, got %d hashes", len(hashes))
		}
	})

	t.Run("empty file", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "empty.txt")
		if err := os.WriteFile(filePath, []byte{}, 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		hashes := loadWhitelistFile(filePath)
		if len(hashes) != 0 {
			t.Errorf("expected empty map, got %d hashes", len(hashes))
		}
	})

	t.Run("invalid hashes skipped", func(t *testing.T) {
		content := `a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0
invalid_hash_here
d4e5f6a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6ef
`
		filePath := filepath.Join(tempDir, "invalid.txt")
		if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		hashes := loadWhitelistFile(filePath)
		if len(hashes) != 2 {
			t.Errorf("expected 2 valid hashes, got %d", len(hashes))
		}
	})

	t.Run("case insensitive hex", func(t *testing.T) {
		content := `A1B2C3D4E5F6A7B8C9D0E1F2A3B4C5D6E7F8A9B0
a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0`
		filePath := filepath.Join(tempDir, "case.txt")
		if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		hashes := loadWhitelistFile(filePath)
		if len(hashes) != 1 {
			t.Errorf("expected 1 unique hash (case insensitive), got %d", len(hashes))
		}
	})
}

func TestIsWhitelisted(t *testing.T) {
	t.Run("nil whitelist allows all", func(t *testing.T) {
		tr := &Tracker{}
		hash := HashID{0x01, 0x02, 0x03}

		if !tr.isWhitelisted(hash) {
			t.Error("expected hash to be allowed when whitelist is nil")
		}
	})

	t.Run("empty whitelist blocks all", func(t *testing.T) {
		tr := &Tracker{}
		emptyMap := make(map[HashID]struct{})
		tr.whitelist.Store(&emptyMap)

		hash := HashID{0x01, 0x02, 0x03}
		if tr.isWhitelisted(hash) {
			t.Error("expected hash to be blocked when whitelist is empty")
		}
	})

	t.Run("whitelisted hash allowed", func(t *testing.T) {
		tr := &Tracker{}
		allowed := make(map[HashID]struct{})
		hash := HashID{0x01, 0x02, 0x03}
		allowed[hash] = struct{}{}
		tr.whitelist.Store(&allowed)

		if !tr.isWhitelisted(hash) {
			t.Error("expected whitelisted hash to be allowed")
		}
	})

	t.Run("non-whitelisted hash blocked", func(t *testing.T) {
		tr := &Tracker{}
		allowed := make(map[HashID]struct{})
		hash1 := HashID{0x01, 0x02, 0x03}
		hash2 := HashID{0x04, 0x05, 0x06}
		allowed[hash1] = struct{}{}
		tr.whitelist.Store(&allowed)

		if tr.isWhitelisted(hash2) {
			t.Error("expected non-whitelisted hash to be blocked")
		}
	})
}

func TestStartWhitelistManager(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "whitelist.txt")

	// Create initial file
	content := `a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0`
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := &Tracker{}
	startWhitelistManager(ctx, filePath, &tr.whitelist)

	// Check that whitelist was loaded
	m := tr.whitelist.Load()
	if m == nil {
		t.Fatal("expected whitelist to be loaded")
	}

	if len(*m) != 1 {
		t.Errorf("expected 1 hash in whitelist, got %d", len(*m))
	}
}
