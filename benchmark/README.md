# BitTorrent UDP Tracker Benchmarks

This directory contains a standalone benchmark tool for testing pico-tracker performance against a running instance via UDP protocol.

## Files

- `main.go` - Standalone benchmark tool that simulates real UDP clients
- `RESULTS_TEMPLATE.md` - Template with placeholders for documenting results
- `results-YYYYMMDD.md` - Example results file with real benchmark data
- `README.md` - This file

## Quick Start

```bash
# Terminal 1: Build and start the tracker
go run ./

# Terminal 2: Run benchmark
go run ./benchmark

# The benchmark runs for 30 seconds with 100 concurrent workers by default
```

> **Platform Note - macOS Users:** Benchmarking against **localhost on macOS is fundamentally broken** for high-throughput UDP testing. When multiple workers send packets simultaneously, the macOS UDP stack drops packets regardless of buffer size (even with 8MB+ buffers). This is a macOS kernel limitation, not a tracker issue.
>
> **The only reliable benchmark method is testing against a remote Linux server.**
>
> **Why macOS localhost fails:**
> - The benchmark starts all workers simultaneously, causing a packet burst
> - macOS UDP receive queue overflows before the tracker can read any packets
> - Increasing socket buffers (via `SetReadBuffer` or `sysctl`) does **not** resolve this
> - Docker on macOS has the same issue (runs Linux in a VM, but networking still goes through macOS)
>
> **What does work on macOS:**
> - Smoke testing: `-concurrency 1` to verify the tracker responds
> - Testing against remote Linux servers (e.g., `tracker.example.com:1337`) - this shows 4,000+ RPS with 0% failures
>

## What the Benchmark Measures

The benchmark tool simulates real BitTorrent clients by:
1. Creating UDP connections to the tracker
2. Sending a **connect** request once to get a connection ID
3. Sending **announce** requests repeatedly (register peers, get peer lists)
4. Sending **scrape** requests periodically (get torrent statistics)
5. Reconnecting every 2 minutes (connection IDs expire per BEP 15)

## Usage

```bash
go run benchmark/main.go [flags]

Flags:
  -target string      Tracker address (default "localhost:1337")
  -duration duration  Test duration (default 30s)
  -concurrency int    Number of parallel workers (default 100)
  -rate int          Rate limit per worker in req/s (0=unlimited)
  -hashes int        Info hashes per worker (default 5)
  -numwant int       Peers to request (default 50)
```

### Example Commands

```bash
# Quick smoke test (10 seconds, 50 workers)
go run benchmark/main.go -duration 10s -concurrency 50

# Extended load test (5 minutes, 1000 workers)
go run benchmark/main.go -duration 5m -concurrency 1000

# Rate-limited test (100 requests/second per worker)
go run benchmark/main.go -rate 100

# Heavy load with many torrents (500 workers, 20 hashes each)
go run benchmark/main.go -concurrency 500 -hashes 20

# Test remote tracker
go run benchmark/main.go -target tracker.example.com:1337 -duration 60s
```

## Understanding the Results

### Example Output

```
========================================
       BENCHMARK RESULTS
========================================
Duration: 30s
Concurrency: 100 workers

--- Request Statistics ---
Total Requests:     1,245,678
Successful:         1,245,000 (99.95%)
Failed:             678 (0.05%)
Requests/Second:    41,522.60

--- Request Breakdown ---
Connect:            ~100 (once per worker per 2 min)
Announce:           996,544
Scrape:             249,136

--- Latency Statistics (per operation) ---
Min:                123µs
Avg:                2.4ms
P50:                1.8ms
P95:                5.2ms
P99:                12.3ms
Max:                145.2ms

--- Latency by Operation ---
Connect (avg):      0.5ms
Announce (avg):     2.8ms
Scrape (avg):       1.2ms
========================================
```

### Interpreting Request Statistics

#### Total Requests
- **What it is**: Sum of all announce and scrape requests (+ connect at start)
- **Good value**: Depends on duration and concurrency, but higher is generally better
- **Example**: 100 workers × 30 seconds × ~350 req/s = ~1M requests
- **Note**: Connect requests are minimal (~100 for 100 workers) because connection IDs are reused for 2 minutes

#### Success Rate
- **What it is**: Percentage of requests that got valid responses
- **Good**: > 99.9%
- **Acceptable**: > 99%
- **Warning**: 95-99% (some clients will retry)
- **Critical**: < 95% (major issues)

#### RPS (Requests Per Second)
- **What it is**: Average throughput over the test
- **Good benchmarks**:
  - 10 workers: 50,000-70,000 RPS
  - 100 workers: 300,000-500,000 RPS
  - 1000 workers: 500,000-1,000,000 RPS (depending on hardware)
- **Rule of thumb**: Should scale roughly linearly with concurrency up to CPU limits

### Interpreting Latency Statistics

#### Latency by Operation

The benchmark tracks latency separately for each operation type:

- **Connect**: Generates connection ID (~0.1-1ms)
- **Announce**: Registers peers, generates peer lists (~1-5ms)
- **Scrape**: Read-only stats lookup (~0.5-2ms)

Each operation has its own min/avg/p50/p95/p99/max stats printed at the end.

#### Why Multiple Percentiles?

Different percentiles tell different stories:

- **Min**: Best-case scenario (warm caches, no contention)
- **Avg**: Overall average (can hide problems)
- **P50 (Median)**: Typical user experience
- **P95**: Almost all users (5% outliers)
- **P99**: Worst-case regular experience (catches issues)
- **Max**: Absolute worst case (often includes timeouts)

#### Latency Guidelines (Local Testing)

| Metric | Excellent | Good | Acceptable | Poor | Critical |
|--------|-----------|------|------------|------|----------|
| P50 | < 1ms | 1-5ms | 5-10ms | 10-50ms | > 50ms |
| P95 | < 2ms | 2-10ms | 10-50ms | 50-100ms | > 100ms |
| P99 | < 5ms | 5-20ms | 20-100ms | 100-500ms | > 500ms |

**What affects latency:**
- **Connect**: Should be fastest (~0.1-1ms), just generates connection ID
- **Announce**: Medium (~1-5ms), involves peer list generation
- **Scrape**: Fast (~0.5-2ms), read-only stats lookup
- **Peer count**: More peers = longer peer lists = higher latency
- **Concurrency**: Lock contention increases latency at high concurrency

#### Common Latency Patterns

1. **Flat line**: `P50 ≈ P95 ≈ P99`
   - **Meaning**: Stable performance, no bottlenecks
   - **Status**: Excellent

2. **Gradual increase**: `P50 < P95 < P99` (each 2-3x higher)
   - **Meaning**: Some variance, but acceptable
   - **Status**: Good

3. **Cliff**: `P50 good, P95 good, P99 very high`
   - **Meaning**: Occasional hiccups (GC pauses, cleanup, etc.)
   - **Status**: Usually acceptable, monitor if P99 > 100ms

4. **Spike**: `Max >> P99`
   - **Meaning**: Some timeouts or extreme outliers
   - **Status**: Check if consistent or one-off

5. **Increasing over time**: Latency grows throughout test
   - **Meaning**: Likely memory pressure or queue buildup
   - **Status**: Investigate memory usage and goroutine leaks

### When Is Performance "Good Enough"?

**For personal/hobby use:**
- RPS > 10,000
- P95 < 5ms
- Error rate < 1%

**For small communities (1K users):**
- RPS > 50,000
- P95 < 10ms
- Error rate < 0.1%

**For large trackers (10K+ users):**
- RPS > 200,000
- P95 < 20ms
- Error rate < 0.01%
- Consider horizontal scaling (multiple tracker instances)

## Troubleshooting Poor Performance

### Low RPS

**Symptoms**: RPS much lower than expected for hardware

**Possible causes**:
1. **Rate limiting**: Check if `-rate` flag is set on benchmark
2. **Tracker rate limiting**: Tracker allows 10 connect requests per 2 min per IP
3. **Network issues**: Test with localhost first
3. **CPU bottleneck**: Check CPU usage during test
4. **Lock contention**: Check if P99 >> P50

**Solutions**:
- Remove rate limits: `-rate 0`
- Test locally to eliminate network: `-target 127.0.0.1:1337`
- Profile tracker: `go tool pprof`

### High Error Rate

**Symptoms**: Failed requests > 1%

**Possible causes**:
1. **macOS localhost UDP drops**: The #1 cause of benchmark failures. When running against localhost on macOS with >1 worker, packets are dropped at the kernel level due to burst traffic. **Increasing socket buffers does not fix this** - we tested 4MB and 8MB buffers with no improvement.
2. **Tracker overloaded**: Concurrency too high
3. **Network timeouts**: Remote tracker with high latency
4. **Connection refused**: Tracker not running or wrong port
5. **Malformed responses**: Tracker bugs

**Solutions**:
- **macOS users**: Run benchmarks against a **remote Linux server**. macOS localhost is unreliable for >1 worker.
- For local smoke testing on macOS, use `-concurrency 1` only
- Verify tracker is running: `lsof -i :1337`
- Enable tracker debug mode: `DEBUG=1 go run main.go`

### High Latency

**Symptoms**: P95 > 50ms (local) or > 200ms (remote)

**Possible causes**:
1. **High CPU usage**: Tracker at capacity
2. **Many peers**: Large peer lists take time to generate
3. **Lock contention**: High concurrency with many writes
4. **GC pauses**: Go garbage collector pauses

**Solutions**:
- Reduce concurrency until latency improves
- Check peer count in tracker (enable debug logging)
- Profile to find hot paths
- Consider reducing `numwant` parameter

## Best Practices

### Before Benchmarking

1. **Warm up**: Run a short 10s test first to warm up caches
2. **Close other apps**: Minimize system load
3. **Local first**: Test localhost before remote to establish baseline
4. **Monitor resources**: Watch CPU, memory, and network during test

### During Benchmarking

1. **Progress updates**: Tool prints stats every 5 seconds
2. **Watch for errors**: If errors appear early, stop and investigate
3. **Check tracker logs**: Look for warnings or errors
4. **Multiple runs**: Run 3 times and use average for accuracy

### After Benchmarking

1. **Document results**: Copy RESULTS_TEMPLATE.md and fill in
2. **Compare baselines**: Compare to previous results
3. **Profile if needed**: Use pprof if performance changed unexpectedly
4. **Clean up**: Stop tracker and benchmark processes

## Example Benchmark Session

```bash
# 1. Build tracker for better performance
go build .
./pico-tracker &

# 2. Quick smoke test
echo "Quick test..."
go run benchmark/main.go -duration 10s -concurrency 10

# 3. Baseline measurement
echo "Baseline..."
go run benchmark/main.go -duration 30s -concurrency 100 > baseline.txt

# 4. Load test
echo "Load test..."
go run benchmark/main.go -duration 60s -concurrency 1000 > load_test.txt

# 5. Document results
cp benchmark/RESULTS_TEMPLATE.md benchmark/results-$(date +%Y%m%d).md
# Edit results file with your measurements

# 6. Clean up
kill %1  # Stop tracker
```

## Advanced Usage

### Profiling the Tracker

Add profiling to main.go:

```go
import _ "net/http/pprof"

func main() {
    go func() {
        log.Println(http.ListenAndServe("localhost:6060", nil))
    }()
    // ... rest of main
}
```

Then during benchmark:

```bash
# Get CPU profile
curl http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.prof
go tool pprof cpu.prof

# Get heap profile
curl http://localhost:6060/debug/pprof/heap > heap.prof
go tool pprof heap.prof
```

### Custom Metrics

Modify benchmark/main.go to track additional metrics:

```go
// Add to Stats struct
ResponseSizes []int

// Record in doAnnounce, doScrape, etc.
b.Stats.ResponseSizes = append(b.Stats.ResponseSizes, n)
```

## Tips

1. **Start small**: Begin with 10 workers, then scale up
2. **Watch latency**: RPS isn't everything - latency matters for user experience
3. **Test realistic scenarios**: Use `-hashes` and `-numwant` similar to production
4. **Monitor both sides**: Track both benchmark and tracker resource usage
5. **Automate**: Add benchmark to CI to catch performance regressions

## See Also

- `RESULTS_TEMPLATE.md` - Detailed template for documenting results
- `main.go` - Tracker source code
- `main_test.go` - Unit tests (if they exist)

---

## Prompt for AGENTS

When running benchmarks:

1. **Always run benchmarks sequentially** - NOT in parallel to avoid resource contention
2. **Use the tracker binary** - Build with `go build -o pico-tracker .` and run `./pico-tracker &`
3. **Stop tracker when done** - Kill the process after benchmarks complete
4. **Document everything** - Include all commands, parameters, and system info
5. **Compare with previous results** - Use the most recent results file as baseline

### Standard Benchmark Command Template

```bash
# Light Load (10 workers)
go run benchmark/main.go -target 127.0.0.1:1337 -duration 30s -concurrency 10 > /tmp/light_load.txt 2>&1

# Medium Load (100 workers)  
go run benchmark/main.go -target 127.0.0.1:1337 -duration 30s -concurrency 100 > /tmp/medium_load.txt 2>&1

# Heavy Load (1000 workers)
go run benchmark/main.go -target 127.0.0.1:1337 -duration 30s -concurrency 1000 > /tmp/heavy_load.txt 2>&1
```

### Running Against Remote Linux Instances

When benchmarking against remote Linux servers (not localhost):

1. **Ask for the instance details** - Request the IP:port of the remote tracker instance
2. **Replace target** - Update the `-target` flag with the provided IP:port
3. **Ensure connectivity** - Verify network connectivity before running benchmarks
4. **Document the environment** - Note that tests were run against remote instance

Example command for remote instance:
```bash
# Replace <IP:PORT> with the instance provided by the user
go run benchmark/main.go -target <IP:PORT> -duration 30s -concurrency 100
```
