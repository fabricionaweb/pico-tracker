# BitTorrent UDP Tracker Benchmarks

Standalone benchmark tool for testing pico-tracker performance via UDP.

## Files

- `main.go` - Benchmark tool that simulates UDP clients
- `RESULTS_TEMPLATE.md` - Template for results
- `results-YYYYMMDD.md` - Benchmark results
- `README.md` - This file

## Quick Start

### Remote Server (Recommended)

```bash
go run ./benchmark -target <IP>:1337
```

### Local Testing

```bash
# Terminal 1: Start tracker
go run ./

# Terminal 2: Run benchmark
go run ./benchmark -target 127.0.0.1:1337
```

## Usage

```bash
go run benchmark/main.go [flags]

Flags:
  -target string      Tracker address (default "localhost:1337")
  -duration duration  Test duration (default 30s)
  -concurrency int    Number of parallel workers (default 100)
  -rate int           Rate limit per worker in req/s (0=unlimited)
  -hashes int         Info hashes per worker (default 5)
  -numwant int        Peers to request (default 50)
```

## Standard Commands

```bash
# Light Load (10 workers)
go run benchmark/main.go -target <IP:PORT> -duration 30s -concurrency 10

# Medium Load (100 workers)
go run benchmark/main.go -target <IP:PORT> -duration 30s -concurrency 100

# Heavy Load (1000 workers)
go run benchmark/main.go -target <IP:PORT> -duration 30s -concurrency 1000
```

## Interpreting Results

### Key Metrics

| Metric | Good | Poor |
|--------|------|------|
| Success Rate | > 99.9% | < 99% |
| RPS (100 workers) | > 10,000 | < 5,000 |
| P95 Latency | < 10ms | > 50ms |
| Error Rate | < 0.1% | > 1% |

### Latency Guidelines (Local Testing)

| Metric | Excellent | Acceptable | Poor |
|--------|-----------|------------|------|
| P50 | < 1ms | 5-10ms | > 50ms |
| P95 | < 2ms | 10-50ms | > 100ms |
| P99 | < 5ms | 20-100ms | > 500ms |

### Operations

- **Connect**: ~0.1-1ms - Generates connection ID
- **Announce**: ~1-5ms - Registers peers, gets peer lists
- **Scrape**: ~0.5-2ms - Read-only stats lookup

## Prompt for AGENTS

### ALWAYS Create Results File

**You MUST create a `results-YYYYMMDD.md` file after running benchmarks.**

1. Find the most recent `results-*.md` file
2. Copy and fill in the new results
3. Include all three benchmark runs (10, 100, 1000 workers)
4. Create comparison table with previous run
5. Save as `results-YYYYMMDD.md`

### ALWAYS Create Comparison Summary

**You MUST create a `COMPARISON-YYYYMMDD.md` file.**

Include:
- Header with dates being compared
- All load levels (10, 100, 1000 workers)
- Key metrics (RPS, P95, max latency, error rate)
- Verdict (improved/regressed/stable)
- Production readiness check

### Testing Mode

**Remote Testing:**
- Use provided IP:port
- Run benchmarks sequentially (NOT parallel)
- DO NOT start local tracker

**Local Testing:**
```bash
go build -o pico-tracker . && ./pico-tracker &
go run benchmark/main.go -target 127.0.0.1:1337 -duration 30s -concurrency 100
pkill pico-tracker
```

**Multiple Runs on Same Day:**
- Use version suffix: `results-20260220-v2.md`
- Compare to immediate previous run
- Document reason for re-run
