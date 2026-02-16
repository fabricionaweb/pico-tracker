# BitTorrent UDP Tracker Benchmark Results

## Test Environment

| Parameter | Value |
|-----------|-------|
| Date | YYYY-MM-DD |
| Tester | Name |
| Tracker Version | vX.X.X |
| Go Version | go1.XX.X |
| OS | macOS/Linux/Windows |
| Architecture | amd64/arm64 |
| CPU | Model |
| Memory | X GB |
| Network | Local/Remote |

## Configuration

| Parameter | Value |
|-----------|-------|
| Target | localhost:1337 |
| Duration | 30s |
| Concurrency | 100 workers |
| Rate Limit | 0 (unlimited) |
| Info Hashes per Worker | 5 |
| Num Want | 50 |

## Results Summary

### Request Statistics

| Metric | Value | Status |
|--------|-------|--------|
| Total Requests | 0 | - |
| Successful | 0 (0.00%) | - |
| Failed | 0 (0.00%) | - |
| Requests/Second | 0.00 | - |

**What this means:**
- **Total Requests**: The total number of UDP requests sent during the test (connect + announce + scrape)
- **Successful**: Requests that received valid responses within the timeout (5 seconds)
- **Failed**: Requests that timed out, received malformed responses, or encountered network errors
- **RPS (Requests Per Second)**: Overall throughput - higher is better

**Status Guide:**
- **Good**: RPS > 10,000, Success rate > 99.9%, Error rate < 0.1%
- **Acceptable**: RPS 5,000-10,000, Success rate > 99%, Error rate < 1%
- **Poor**: RPS < 5,000, Success rate < 99%, Error rate > 1%
- **Critical**: Error rate > 5% (indicates tracker overload or network issues)

### Request Breakdown

| Operation | Count | Avg Latency | Expected Range |
|-----------|-------|-------------|----------------|
| Connect | 0 | 0ms | 0.1-2ms |
| Announce | 0 | 0ms | 0.5-5ms |
| Scrape | 0 | 0ms | 0.2-3ms |

**What this means:**
- **Connect**: Initial handshake to get a connection ID (16 byte response)
- **Announce**: Peer registration and peer list retrieval (20+ byte response)
- **Scrape**: Torrent statistics request (20 byte response per hash)

**Performance Notes:**
- Connect should be fastest (no state lookup, just ID generation)
- Announce is heaviest (peer list generation, state updates)
- Scrape is medium (read-only stats lookup)
- Latency increases with torrent/peer count

### Latency Statistics

| Percentile | Latency | Status |
|------------|---------|--------|
| Min | 0ms | - |
| Avg | 0ms | - |
| P50 | 0ms | - |
| P95 | 0ms | - |
| P99 | 0ms | - |
| Max | 0ms | - |

**What this means:**
- **Min**: Fastest response time (best case)
- **Avg**: Mean latency (can be skewed by outliers)
- **P50 (Median)**: 50% of requests were faster than this
- **P95**: 95% of requests were faster than this (important for user experience)
- **P99**: 99% of requests were faster than this (catches worst cases)
- **Max**: Slowest response (usually includes timeouts or outliers)

**Latency Status Guide (Local Network):**
- **Excellent**: P95 < 1ms, P99 < 5ms
- **Good**: P95 < 5ms, P99 < 10ms
- **Acceptable**: P95 < 10ms, P99 < 50ms
- **Poor**: P95 > 50ms (indicates CPU bottleneck or lock contention)
- **Critical**: P95 > 100ms (tracker is overloaded)

**Latency Status Guide (Remote/Internet):**
- **Excellent**: P95 < 10ms, P99 < 50ms
- **Good**: P95 < 50ms, P99 < 100ms
- **Acceptable**: P95 < 100ms, P99 < 200ms
- **Poor**: P95 > 200ms (network or tracker issues)

### Memory Usage

| Metric | Value | Status |
|--------|-------|--------|
| Initial | 0.00 MB | - |
| Peak | 0.00 MB | - |
| Growth | 0.00 MB | - |

**What this means:**
- **Initial**: Memory used when benchmark started
- **Peak**: Highest memory usage during the test
- **Growth**: Net memory increase (Peak - Initial)

**Memory Status Guide:**
- **Good**: Growth < 100MB, stable or slowly growing
- **Acceptable**: Growth 100-500MB
- **Concern**: Growth > 500MB or continuous growth (possible memory leak)
- **Critical**: Growth > 1GB (likely memory leak or excessive peer/torrent storage)

**Memory Usage Factors:**
- Each peer consumes ~50-100 bytes
- Each torrent with peers consumes ~200-500 bytes
- 100K peers = ~10MB memory
- 10K torrents with 10 peers each = ~20-50MB

## Performance Analysis

### Bottlenecks Identified

1. **CPU-bound**: If latency increases linearly with concurrency but memory is stable
   - *Solution*: Optimize hot paths, reduce allocations, profile with pprof

2. **Memory-bound**: If memory grows rapidly or hits limits
   - *Solution*: Check for memory leaks, implement peer expiration, reduce cache sizes

3. **Lock contention**: If P99 >> P95 (high variance)
   - *Solution*: Reduce mutex scope, use RWMutex, implement sharding

4. **Network I/O**: If running remotely with high latency
   - *Solution*: This is expected, ensure tracker has enough bandwidth

### Optimization Recommendations

Based on results, consider these optimizations:

1. **Increase throughput**:
   - Enable debug mode to see if cleanup is running too frequently
   - Reduce logging verbosity in production
   - Pre-allocate peer slices

2. **Reduce latency**:
   - Shrink critical sections in torrent.mu
   - Batch peer updates
   - Use sync.Pool for temporary buffers

3. **Memory efficiency**:
   - Implement aggressive peer timeout (currently 30 min cleanup)
   - Limit max peers per torrent
   - Use cleanupLoop more frequently if memory is high

4. **Scalability**:
   - Run multiple tracker instances behind load balancer
   - Use Redis/external state for horizontal scaling

## Comparative Results

### Load Test Scenarios

| Scenario | Concurrency | Duration | RPS | P95 Latency | Status |
|----------|-------------|----------|-----|-------------|--------|
| Light Load | 10 | 30s | 0 | 0ms | - |
| Medium Load | 100 | 30s | 0 | 0ms | - |
| Heavy Load | 1000 | 30s | 0 | 0ms | - |
| Stress Test | 10000 | 60s | 0 | 0ms | - |

**Scenario Descriptions:**

- **Light Load (10 workers)**: Baseline performance, should show best latency
- **Medium Load (100 workers)**: Typical production load, good for regression testing
- **Heavy Load (1000 workers)**: Stress test, reveals bottlenecks and lock contention
- **Stress Test (10000 workers)**: Breaking point, identifies hard limits

**Expected Scaling:**
- RPS should scale nearly linearly up to CPU core count
- Latency should remain flat until ~50-70% CPU utilization
- Above 80% CPU, expect exponential latency increase

## Profiling Results

### CPU Profile
```
Top 10 functions by CPU time:
1.
2.
3.
```

**Analysis Guide:**
- If `runtime.mallocgc` is high: Too many allocations, use object pools
- If `sync.(*Mutex).Lock` is high: Lock contention, use more granular locks
- If `syscall` or `net` functions are high: Network bottleneck (unusual for local tests)

### Memory Profile
```
Top 10 allocators:
1.
2.
3.
```

**Analysis Guide:**
- If peer/map operations allocate heavily: Consider pre-allocation
- If slice growth causes allocations: Set capacity hints
- If string conversions allocate: Use []byte where possible

## Historical Results

| Date | Version | Concurrency | RPS | P95 Latency | Error Rate | Notes |
|------|---------|-------------|-----|-------------|------------|-------|
| YYYY-MM-DD | v0.1.0 | 100 | 0 | 0ms | 0% | Initial benchmark |

**Trends to Watch:**
- RPS should stay stable or improve with optimizations
- Latency should not increase significantly
- Error rate should remain < 1%
- Memory growth should be proportional to peer count, not test duration

## Quick Reference

### Is my tracker fast enough?

**For small deployments (< 1000 concurrent users):**
- RPS > 1,000
- P95 < 10ms
- Any modern hardware should achieve this easily

**For medium deployments (1K-10K users):**
- RPS > 10,000
- P95 < 20ms
- Requires dedicated CPU core, reasonable memory

**For large deployments (10K+ users):**
- RPS > 50,000
- P95 < 50ms
- Requires multiple cores, may need load balancing

### When to worry:

1. **Error rate > 1%**: Clients will retry, increasing load
2. **P95 latency increasing with test duration**: Likely memory leak or queue buildup
3. **RPS plateaus while CPU < 50%**: Lock contention or I/O bottleneck
4. **Memory grows > 100MB/min**: Memory leak or excessive state retention

### When to optimize:

1. **Before hitting limits**: Optimize when at 50% capacity, not 100%
2. **When latency affects users**: If P95 > 100ms for local, users will notice
3. **When scaling up**: Test at 2x expected load before deploying
4. **After code changes**: Always benchmark after significant changes
