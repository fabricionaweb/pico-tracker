package main

import (
	"os"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Run("port from env var", func(t *testing.T) {
		os.Setenv("PICO_TRACKER__PORT", "8080")
		defer os.Unsetenv("PICO_TRACKER__PORT")

		cfg := parseFlags([]string{})

		if cfg.port != 8080 {
			t.Errorf("expected port 8080, got %d", cfg.port)
		}
	})

	t.Run("port from env var is ignored if invalid", func(t *testing.T) {
		os.Setenv("PICO_TRACKER__PORT", "invalid")
		defer os.Unsetenv("PICO_TRACKER__PORT")

		cfg := parseFlags([]string{})

		// Falls back to default 1337
		if cfg.port != 1337 {
			t.Errorf("expected port 1337, got %d", cfg.port)
		}
	})

	t.Run("port from env var is ignored if zero", func(t *testing.T) {
		os.Setenv("PICO_TRACKER__PORT", "0")
		defer os.Unsetenv("PICO_TRACKER__PORT")

		cfg := parseFlags([]string{})

		// Falls back to default 1337
		if cfg.port != 1337 {
			t.Errorf("expected port 1337, got %d", cfg.port)
		}
	})

	t.Run("port from flag overrides env var", func(t *testing.T) {
		os.Setenv("PICO_TRACKER__PORT", "8080")
		defer os.Unsetenv("PICO_TRACKER__PORT")

		cfg := parseFlags([]string{"-port", "9000"})

		if cfg.port != 9000 {
			t.Errorf("expected port 9000, got %d", cfg.port)
		}
	})

	t.Run("secret from env var", func(t *testing.T) {
		os.Setenv("PICO_TRACKER__SECRET", "my-secret-key")
		defer os.Unsetenv("PICO_TRACKER__SECRET")

		cfg := parseFlags([]string{})

		if cfg.secret != "my-secret-key" {
			t.Errorf("expected secret 'my-secret-key', got '%s'", cfg.secret)
		}
	})

	t.Run("secret from flag overrides env var", func(t *testing.T) {
		os.Setenv("PICO_TRACKER__SECRET", "env-secret")
		defer os.Unsetenv("PICO_TRACKER__SECRET")

		cfg := parseFlags([]string{"-secret", "flag-secret"})

		if cfg.secret != "flag-secret" {
			t.Errorf("expected secret 'flag-secret', got '%s'", cfg.secret)
		}
	})

	t.Run("debug mode from env", func(t *testing.T) {
		os.Setenv("DEBUG", "1")
		defer os.Unsetenv("DEBUG")

		parseFlags([]string{})

		if !debugMode {
			t.Error("expected debugMode to be true")
		}
	})

	t.Run("debug mode from flag", func(t *testing.T) {
		os.Unsetenv("DEBUG")

		parseFlags([]string{"-debug"})

		if !debugMode {
			t.Error("expected debugMode to be true")
		}
	})

	t.Run("secret fallback when not provided", func(t *testing.T) {
		os.Unsetenv("PICO_TRACKER__SECRET")

		cfg := parseFlags([]string{})

		if cfg.secret != fallbackSecret {
			t.Errorf("expected fallback secret, got '%s'", cfg.secret)
		}
	})
}
