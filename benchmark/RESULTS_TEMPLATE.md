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

| Operation | Count | Avg Latency | P95 Latency | Expected Range |
|-----------|-------|-------------|-------------|----------------|
| Connect | 0 | 0ms | 0ms | 0.1-2ms |
| Announce | 0 | 0ms | 0ms | 0.5-5ms |
| Scrape | 0 | 0ms | 0ms | 0.2-3ms |

**What this means:**
- **Connect**: Initial handshake to get a connection ID (16 byte response)
- **Announce**: Peer registration and peer list retrieval (20+ byte response)
- **Scrape**: Torrent statistics request (20 byte response per hash)

**Performance Notes:**
- Connect should be fastest (no state lookup, just ID generation)
- Announce is heaviest (peer list generation, state updates)
- Scrape is medium (read-only stats lookup)
- Latency increases with torrent/peer count

### Latency by Operation

| Operation | Min | Avg | P50 | P95 | P99 | Max |
|-----------|-----|-----|-----|-----|-----|-----|
| Connect | 0ms | 0ms | 0ms | 0ms | 0ms | 0ms |
| Announce | 0ms | 0ms | 0ms | 0ms | 0ms | 0ms |
| Scrape | 0ms | 0ms | 0ms | 0ms | 0ms | 0ms |

**What this means:**
- **Connect**: Initial handshake to get a connection ID (16 byte response)
- **Announce**: Peer registration and peer list retrieval (20+ byte response)
- **Scrape**: Torrent statistics request (20 byte response per hash)

**Performance Notes:**
- Connect should be fastest (no state lookup, just ID generation)
- Announce is heaviest (peer list generation, state updates)
- Scrape is medium (read-only stats lookup)
- Latency increases with torrent/peer count

## Performance Analysis

### Bottlenecks Identified

1. **CPU-bound**: If latency increases linearly with concurrency
   - *Solution*: Optimize hot paths, reduce allocations, profile with pprof

2. **Lock contention**: If P99 >> P95 (high variance)
   - *Solution*: Reduce mutex scope, use RWMutex, implement sharding

3. **Network I/O**: If running remotely with high latency
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

3. **Scalability**:
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

## Historical Results

| Date | Version | Concurrency | RPS | P95 Latency | Error Rate | Notes |
|------|---------|-------------|-----|-------------|------------|-------|
| YYYY-MM-DD | v0.1.0 | 100 | 0 | 0ms | 0% | Initial benchmark |

**Trends to Watch:**
- RPS should stay stable or improve with optimizations
- Latency should not increase significantly
- Error rate should remain < 1%

## Quick Reference

### Is my tracker fast enough?

**For small deployments (< 1000 concurrent users):**
- RPS > 1,000
- P95 < 10ms
- Any modern hardware should achieve this easily

**For medium deployments (1K-10K users):**
- RPS > 10,000
- P95 < 20ms
- Requires dedicated CPU core

**For large deployments (10K+ users):**
- RPS > 50,000
- P95 < 50ms
- Requires multiple cores, may need load balancing

### When to worry:

1. **Error rate > 1%**: Clients will retry, increasing load
2. **P95 latency increasing with test duration**: Likely queue buildup
3. **RPS plateaus while CPU < 50%**: Lock contention or I/O bottleneck

### When to optimize:

1. **Before hitting limits**: Optimize when at 50% capacity, not 100%
2. **When latency affects users**: If P95 > 100ms for local, users will notice
3. **When scaling up**: Test at 2x expected load before deploying
4. **After code changes**: Always benchmark after significant changes
