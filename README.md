# Pico Tracker: Lightweight BitTorrent Tracker (UDP)

A portable BitTorrent tracker implementing the UDP Tracker Protocol (BEP 15). Designed for self-hosting with minimal resource usage and maximum compatibility.

## Features

- **UDP Protocol** - Faster and more efficient than HTTP trackers
- **Full IPv4 and IPv6 support** - Returns appropriate peer lists based on client connection type (BEP 15 compliant)
- **Dual-stack** - Automatically listens on both IPv4 (0.0.0.0) and IPv6 ([::])
- **Rate limiting** - Prevents connect request abuse per IP:Port
- **Automatic peer cleanup** - Removes inactive torrents every 30 minutes
- **Zero-config** - No database required, everything stored in memory
- **Lightning fast** - All operations happen in RAM
- **Ephemeral by design** - Clean slate on every restart
- **Private/whitelist mode** - Optionally restrict to approved info hashes only

## Usage

### Installation

```bash
# Go install
go install github.com/fabricionaweb/pico-tracker@latest

# Or build from source (with version)
go build -trimpath -ldflags="-s -w -X main.version=1.0.0" -o pico-tracker

# Build without version (shows "dev")
go build -trimpath -ldflags="-s -w" -o pico-tracker
```

### Running

```bash
# Run with defaults (port 1337)
./pico-tracker

# Custom port
./pico-tracker -port=6969

# Custom port using environment variable
PICO_TRACKER__PORT=6969 ./pico-tracker

# Set secret key for production (recommended)
./pico-tracker -secret="your-random-secret-here"

# Set secret via environment variable
PICO_TRACKER__SECRET="your-random-secret-here" ./pico-tracker

# Enable debug logging
./pico-tracker -debug

# Run in private mode with whitelist
./pico-tracker -whitelist=/path/to/allowed_torrents.txt

# Whitelist via environment variable
PICO_TRACKER__WHITELIST=/path/to/allowed_torrents.txt ./pico-tracker

# Get help
./pico-tracker -help

# Show version
./pico-tracker -version
```

### Docker

```bash
# Pull the latest release
docker pull ghcr.io/fabricionaweb/pico-tracker:latest

# Run with defaults (note: UDP port)
docker run -p 1337:1337/udp ghcr.io/fabricionaweb/pico-tracker:latest

# Run with custom port
docker run -p 6969:6969/udp -e PICO_TRACKER__PORT=6969 ghcr.io/fabricionaweb/pico-tracker:latest

# Run with debug logs
docker run -p 1337:1337/udp -e DEBUG=1 ghcr.io/fabricionaweb/pico-tracker:latest

# Run with custom secret
docker run -p 1337:1337/udp -e PICO_TRACKER__SECRET=your-secret ghcr.io/fabricionaweb/pico-tracker:latest

# Run with whitelist (mount your whitelist file)
docker run -p 1337:1337/udp -v /path/to/whitelist.txt:/whitelist.txt -e PICO_TRACKER__WHITELIST=/whitelist.txt ghcr.io/fabricionaweb/pico-tracker:latest
```

### Configuration

| Flag | Environment Variable | Default | Description |
|------|---------------------|---------|-------------|
| `-port, -p` | `PICO_TRACKER__PORT` | `1337` | Port to listen on (binds to all interfaces) |
| `-secret, -s` | `PICO_TRACKER__SECRET` | *(auto)* | Secret key for connection ID signing (see Security below) |
| `-whitelist` | `PICO_TRACKER__WHITELIST` | *(none)* | Path to whitelist file for private tracker mode |
| `-debug, -d` | `DEBUG` | `false` | Enable verbose debug logging |
| `-version, -v` | - | - | Print version |

### Whitelist Mode (Private Tracker)

When running in whitelist mode, only approved torrents can be tracked:

**Whitelist file format:**
```
# Comments start with #
a1b2c3d4e5f6789012345678901234567890abcd
fedcba0987654321098765432109876543210fedc

e5d4c3b2a1987654321098765432109876543210
```

- One 40-character hexadecimal info hash per line
- Empty lines and lines starting with `#` are ignored
- The file is automatically reloaded every 5 minutes when modified
- Missing or empty file = block all torrents (fail-closed)
- Enabling whitelist mode rejects all requests for unlisted info hashes

**Note:** The tracker automatically try to bind to both:
- `0.0.0.0:<port>` for IPv4
- `[::]:<port>` for IPv6

### Tracker URL

Use this URL format in your torrent files or torrent clients:

```
udp://<dns or ip>:1337
```

**Note:** Unlike HTTP trackers, UDP trackers don't use URL paths. You can add `/announce` to the URL (e.g., `udp://tracker:1337/announce`), but it will be ignored by clients. All requests are handled directly on the UDP port.

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

### BEP 15 Compliance

This tracker fully implements the UDP Tracker Protocol security requirements:

- **Connection ID validation** - All announce and scrape requests are validated against cryptographically generated connection IDs using a stateless syn-cookie approach (per BEP 15 section "UDP connections / spoofing")
- **Connection expiration** - Connection IDs expire after 2 minutes (validated cryptographically, no storage required)
- **Port validation** - Rejects invalid port 0 to prevent malformed peer entries
- **IP consistency** - Client IP is correctly propagated through all request handlers

### Security

**Syn-Cookie Implementation:** This tracker uses a stateless syn-cookie approach for connection ID management instead of storing connections in memory:

- Connection IDs are cryptographically signed using HMAC-SHA256 with a secret key
- Format: `[32-bit timestamp][32-bit signature]`
- Zero memory overhead - no connection storage needed
- No cleanup required - IDs are validated mathematically
- Protection against IP spoofing attacks

**Secret Key:**
- If no secret is provided, a hardcoded default is used (with a startup warning)
- **For production, always set a custom secret** via `-secret` flag or `PICO_TRACKER__SECRET` environment variable
- The secret should be a random string of at least 32 characters
- All instances in a cluster should share the same secret for consistent validation

## AI Collaboration

This project was developed using **Kimi K2.5** and **MiniMax M2.5** (AI agents) through iterative refinement, focusing on:
- Clean, maintainable code
- Standards compliance (BEP 15)
- Minimal dependencies
- Comprehensive documentation
