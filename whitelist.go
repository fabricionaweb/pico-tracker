package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const whitelistRefreshInterval = 5 * time.Minute

// loadWhitelistFile reads the whitelist file and returns a map of allowed info_hashes
// Empty lines and lines starting with # are ignored
func loadWhitelistFile(path string) map[HashID]struct{} {
	//nolint:gosec // Path is controlled by admin
	file, err := os.Open(path)
	if err != nil {
		info("failed to open whitelist file: %v", err)
		return make(map[HashID]struct{}) // Fail-closed: return empty map to block all
	}
	//nolint:errcheck // File close errors ignored during read
	defer file.Close()

	hashes := make(map[HashID]struct{})
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if len(line) != 40 {
			info("whitelist line %d: invalid hash length (expected 40 hex chars), skipping", lineNum)
			continue
		}

		decoded, err := hex.DecodeString(line)
		if err != nil {
			info("whitelist line %d: invalid hex string, skipping", lineNum)
			continue
		}

		var hash HashID
		copy(hash[:], decoded)
		hashes[hash] = struct{}{}
	}

	if err := scanner.Err(); err != nil {
		info("error reading whitelist file: %v", err)
	}

	return hashes
}

// startWhitelistManager begins a goroutine that periodically reloads the whitelist
// The manager stops when the provided context is canceled
func startWhitelistManager(ctx context.Context, path string, whitelist *atomic.Pointer[map[HashID]struct{}]) {
	// Load immediately on startup
	data := loadWhitelistFile(path)
	whitelist.Store(&data)
	info("loaded %d hashes from whitelist", len(data))

	go func() {
		var lastMod time.Time
		if fi, err := os.Stat(path); err == nil {
			lastMod = fi.ModTime()
		}

		ticker := time.NewTicker(whitelistRefreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fi, err := os.Stat(path)
				if err != nil {
					info("failed to stat whitelist file: %v", err)
					continue
				}

				if fi.ModTime() != lastMod {
					data := loadWhitelistFile(path)
					whitelist.Store(&data)
					lastMod = fi.ModTime()
					info("reloaded whitelist: %d hashes", len(data))
				}
			}
		}
	}()
}

// isWhitelisted checks if an info_hash is in the whitelist
// If whitelist was never configured (pointer is nil), returns true (public mode)
// If whitelist is configured but empty (e.g., missing file), returns false (blocks all)
func (tr *Tracker) isWhitelisted(hash HashID) bool {
	m := tr.whitelist.Load()
	if m == nil {
		return true // Public mode - whitelist not configured
	}
	_, ok := (*m)[hash]
	return ok
}
