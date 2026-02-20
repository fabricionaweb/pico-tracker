# Performance Comparison: v1.0.0 vs Current Branch (benchmark)

**Date:** 2026-02-20  
**Comparison:** v1.0.0 (tag) vs benchmark branch (latest main)

## Summary

The current branch shows **improved performance** across all load levels with better latency and zero errors under heavy load.

## Results Comparison

### Light Load (10 Workers)

| Metric | v1.0.0 | Current | Change |
|--------|--------|---------|--------|
| RPS | 48,144 | 49,144 | +2.1% |
| P95 Latency | 268µs | 255µs | -4.9% |
| Error Rate | 0.00% | 0.00% | = |
| Total Requests | 1,444,374 | 1,474,336 | +2.1% |

### Medium Load (100 Workers)

| Metric | v1.0.0 | Current | Change |
|--------|--------|---------|--------|
| RPS | 51,161 | 51,813 | +1.3% |
| P95 Latency | 2.32ms | 2.25ms | -3.0% |
| Error Rate | 0.00% | 0.00% | = |
| Total Requests | 1,534,912 | 1,554,477 | +1.3% |

### Heavy Load (1000 Workers)

| Metric | v1.0.0 | Current | Change |
|--------|--------|---------|--------|
| RPS | 47,056 | 47,467 | +0.9% |
| P95 Latency | 23.04ms | 22.90ms | -0.6% |
| Error Rate | 0.01% | 0.00% | **-100%** |
| Total Requests | 1,412,586 | 1,424,899 | +0.9% |

## Key Improvements

1. **Throughput**: +2.1% (light), +1.3% (medium), +0.9% (heavy)
2. **Latency**: Consistently lower P95 across all loads
3. **Reliability**: Zero errors under heavy load (vs 79 errors in v1.0.0)

## Verdict

**IMPROVED** - The current branch outperforms v1.0.0 in all metrics:
- Higher throughput
- Lower latency
- Better error handling under heavy load

## Production Readiness

Both versions are production-ready:
- Success rate > 99.99%
- P95 latency < 50ms under heavy load
- Excellent throughput (>10K RPS)

The current branch is **recommended** for production deployment.
