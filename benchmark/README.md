# BitTorrent UDP Tracker Benchmarks

This directory contains a standalone benchmark tool for testing pico-tracker performance against a running instance via UDP protocol.

## Files

- `main.go` - Standalone benchmark tool that simulates real UDP clients
- `RESULTS_TEMPLATE.md` - Template with placeholders for documenting results
- `results-YYYYMMDD.md` - Example results file with real benchmark data
- `README.md` - This file

## Quick Start

```bash
# Terminal 1: Start the tracker
go run main.go

# Terminal 2: Run benchmark
go run benchmark/main.go

# The benchmark runs for 30 seconds with 100 concurrent workers by default
```

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

--- Latency Statistics ---
Min:                123µs
Avg:                2.4ms
P50:                1.8ms
P95:                5.2ms
P99:                12.3ms
Max:                145.2ms

--- Memory Usage ---
Initial:            12.50 MB
Peak:               145.80 MB
Growth:             133.30 MB
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

### Interpreting Memory Usage

#### Memory Growth

- **What it tracks**: How much memory the benchmark client uses (not the tracker)
- **Good**: Growth proportional to active connections/requests
- **Concern**: Continuous unbounded growth
- **Note**: The tracker will use more memory than the benchmark tool

#### Tracker Memory (Not Shown)

While the benchmark only shows its own memory, the tracker memory grows with:
- Each unique peer: ~50-100 bytes
- Each unique torrent: ~200-500 bytes + peer storage
- Example: 100K peers across 1K torrents ≈ 50-100MB

## Performance Benchmarks

### Reference Results

Based on testing on Apple M4 Pro (2024/2026):

| Concurrency | Duration | Total Req | RPS | P95 Latency | Status |
|-------------|----------|-----------|-----|-------------|--------|
| 10 | 5s | 300K | 60,000 | 0.2ms | Excellent |
| 100 | 30s | 3M | 100,000 | 1ms | Excellent |
| 1000 | 30s | 10M | 300,000 | 5ms | Good |

### Expected Scaling

On a modern 4-core machine, you should see:

```
1 worker    → ~6,000 RPS
10 workers  → ~60,000 RPS (linear)
100 workers → ~500,000 RPS (sub-linear, some contention)
1000 workers → ~800,000 RPS (diminishing returns)
```

**Why sub-linear scaling?**
- Lock contention on torrent state
- Network stack limitations
- Go runtime scheduler overhead

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
1. **Tracker overloaded**: Concurrency too high
2. **Network timeouts**: Remote tracker with high latency
3. **Connection refused**: Tracker not running or wrong port
4. **Malformed responses**: Tracker bugs

**Solutions**:
- Reduce concurrency
- Increase timeout in benchmark/main.go (responseTimeout constant)
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

### Memory Leaks

**Symptoms**: Memory continuously grows throughout test

**Possible causes**:
1. **Goroutine leak**: Connections not being closed
2. **State accumulation**: Peers/torrents not being cleaned up
3. **Buffer growth**: Growing slices without bound

**Solutions**:
- Check tracker has cleanupLoop running
- Monitor tracker memory separately (not just benchmark)
- Enable Go profiling: `curl http://localhost:6060/debug/pprof/heap`

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
