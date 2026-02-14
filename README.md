# Pico Tracker: Portable BitTorrent Tracker (UDP)

A lightweight BitTorrent tracker implementing the UDP Tracker Protocol (BEP 15). Designed for self-hosting with minimal resource usage and maximum compatibility.

## Features

- **UDP Protocol** - Faster and more efficient than HTTP trackers
- **Full IPv4 and IPv6 support** - Returns appropriate peer lists based on client connection type (BEP 15 compliant)
- **Dual-stack** - Automatically listens on both IPv4 (0.0.0.0) and IPv6 ([::])
- **Automatic peer cleanup** - Removes inactive torrents every 30 minutes

## Usage

### Installation

```bash
# Go install
go install github.com/fabricionaweb/pico-tracker@latest

# Or build from source
go build -trimpath -ldflags="-s -w" .
```

### Running

```bash
# Run with defaults (port 1337)
./pico-tracker

# Custom port
./pico-tracker -port=6969

# Custom port using environment variable
PICO_TRACKER__PORT=6969 ./pico-tracker

# Enable debug logging
./pico-tracker -debug

# Get help
./pico-tracker -help
```

### Configuration

| Flag | Environment Variable | Default | Description |
|------|---------------------|---------|-------------|
| `-port` | `PICO_TRACKER__PORT` | `1337` | Port to listen on (binds to all interfaces) |
| `-debug` | `DEBUG` | `false` | Enable verbose debug logging |

**Note:** The tracker automatically try to bind to both:
- `0.0.0.0:<port>` for IPv4
- `[::]:<port>` for IPv6

### Tracker URL

Use this URL format in your torrent files or torrent clients:

```
udp://your-server:1337
```

**Important:** Unlike HTTP trackers, UDP trackers do not use a path (no trailing `/`). The tracker handles all requests on the UDP port directly.

## Protocol Implementation

This tracker implements the [UDP Tracker Protocol](http://bittorrent.org/beps/bep_0015.html) (BEP 15):

- **Connect** - Initial handshake to establish session
- **Announce** - Clients report their presence and request peer lists
- **Scrape** - Query statistics without joining the swarm

### IPv6 Support

Following BEP 15 specification:
- IPv4 clients receive IPv4 peers (6 bytes: 4 IP + 2 port)
- IPv6 clients receive IPv6 peers (18 bytes: 16 IP + 2 port)
- Both peer types are tracked and stored regardless of client type

## AI Collaboration

This project was developed using **Kimi 2.5** (AI agent) through iterative refinement, focusing on:
- Clean, maintainable code
- Standards compliance (BEP 15)
- Minimal dependencies
- Comprehensive documentation
