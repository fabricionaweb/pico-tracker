# Pico Tracker: Portable BitTorrent Tracker

A lightweight BitTorrent tracker designed for self-hosting and temporary usage.

## Features

- Single endpoint (`/`) for all announce requests
- IPv4 and IPv6 support
- Reverse proxy compatible (X-Forwarded-For, X-Real-IP)
- Automatic peer cleanup

## Usage

```bash
# Go install
go install github.com/fabricionaweb/pico-tracker@latest

# Or Build from source
go build -trimpath -ldflags="-s -w" .

# Run with defaults (127.0.0.1:8080)
./pico-tracker

# Custom port (bind to 0.0.0.0)
./pico-tracker -addr=:9000

# Environment variable
PICO_TRACKER__ADDR=0.0.0.0:9000 ./pico-tracker

# Get help
./pico-tracker -help
```

**Tracker URL:** `http://your-server:8080/`

### AI Collaboration

This project was developed using **Kimi 2.5** (AI agent) through iterative refinement and focusing on simplicity.
