# KubeArchive Exporter - Design Documentation

> **For operational documentation, deployment instructions, and metrics reference, see [README.md](README.md)**

## Document Purpose

This document explains the architectural decisions, implementation details, and design trade-offs behind the KubeArchive exporter. It's intended for developers, architects, and maintainers who need to understand **why** the exporter works the way it does.

| Documentation | Audience | Focus |
|--------------|----------|-------|
| **README.md** | Operators, SREs, Konflux Users | WHAT it does, HOW to deploy/configure/use it |
| **DESIGN.md** (this doc) | O11Y, Architects | WHY design decisions were made, HOW it works internally |

## Overview

**Key Design Goals:**
- Achieve 30-day SLO accuracy from the moment the exporter starts (cold-start bootstrapping)
- Handle high-volume namespaces with up to 30,000 items per 30-day window
- Handle multi-tenant environments with hundreds of namespaces
- Minimize memory footprint and query load on KubeArchive
- Maintain thread-safe concurrent collection without blocking Prometheus scrapes

---

## Architecture Decisions

### 1. Cold-Start Bootstrapping

**Problem:** Prometheus exporters are typically stateless and compute metrics from recent observations. However, SLO requirements demand 30 days of historical data immediately upon startup.

**Solution:** On first boot, the exporter queries **720 hours (30 days)** of data to populate the full rolling window before serving `/metrics`.

**Implementation:**
```
Cold Start Settings:
- Query window: 720h (30 days)
- Collection timeout: 1,800s (30 minutes)
- Concurrency: 5 parallel requests
- Per-namespace item cap: 30,000

Steady State Settings:
- Query window: 36h (24h base + 50% safety margin)
- Collection timeout: 120s (2 minutes)
- Concurrency: 10 parallel requests
- Per-namespace item cap: 1,500
```

**Why 36h for steady state?**
- Base window of 24h ensures we capture recent completions
- 50% safety margin (24h → 36h) catches pipelines that complete slightly late
- Overlapping queries ensure no data loss between collection cycles

**Trade-offs:**
- **Pro:** Metrics are immediately accurate; no warm-up period
- **Pro:** Pod restarts don't leave SLO gaps (cold start rebuilds state in 10-15 minutes)
- **Con:** Longer startup time (10-15min vs instant)
- **Con:** Higher KubeArchive load during cold start (mitigated by reduced concurrency)

---

### 2. Gap-Filling for Busy Namespaces

**Problem:** Namespaces with >30,000 PipelineRuns in 30 days hit the cold-start item cap, resulting in truncated data.

**Solution:** Progressive gap-filling mechanism that retries truncated namespaces with narrower time windows.

**How It Works:**
1. During cold start, if a namespace hits the 30K limit, mark it as "truncated" and record the oldest fetched timestamp
2. Schedule a background gap-fill attempt with a narrower time window (query older data before the truncation point)
3. Limit gap-fill to **5 attempts per namespace** to prevent infinite retries (total coverage up to 150K items)
4. Track gap-fill attempts and exhaustion via Prometheus counters

**Trade-offs:**
- **Pro:** Captures more data for busy namespaces (instead of dropping 30-day history)
- **Pro:** Graceful degradation (5-attempt limit prevents runaway queries)
- **Con:** Adds complexity to bootstrap logic
- **Con:** May still miss some data for extremely busy namespaces

---

### 3. Per-Namespace Bootstrap State

**Problem:** In multi-tenant environments, not all namespaces need bootstrapping at the same time (new tenants may be added dynamically).

**Solution:** Track bootstrap state per namespace in an in-memory map.

**State Machine:**
```go
type BootstrapState struct {
    Bootstrapped      bool      // Has 30-day window been populated?
    TruncatedAt       string    // Oldest timestamp if truncated
    GapFillAttempts   int       // Number of gap-fill retries (max 5)
}

map[namespace]*BootstrapState
```

**Behavior:**
- New namespace → Bootstrap with 720h query
- Bootstrapped namespace → Steady-state 36h query
- Truncated namespace → Schedule gap-fill with narrower window
- Decommissioned namespace → Remove from map to avoid wasted gap-fill attempts

**Why This Matters:**
- Supports dynamic tenant onboarding without full exporter restart
- Prevents redundant 30-day queries for already-bootstrapped namespaces
- Cleans up state for deleted namespaces (avoids memory leaks)

---

### 4. Thread-Safe Collection with Lock-Free Data Fetching

**Problem:** Prometheus scrapes can occur concurrently with background collection cycles. Holding a write lock during KubeArchive HTTP fetches (~30-60s) would block scrapes.

**Solution:** **Lock-free data collection** followed by a short write-locked gauge update phase.

**Implementation:**
```go
// Background collection cycle
func (e *KAExporter) runCollection() {
    // Phase 1: Fetch data from KubeArchive WITHOUT holding lock (30-60s)
    // RollingStore has its own internal mutex for thread-safety
    releaseIdx, err := e.collectMetrics(ctx)

    // Phase 2: Acquire write lock ONLY for gauge updates (1-5ms)
    e.mu.Lock()
    defer e.mu.Unlock()

    e.buildSLO.updateGauges(e.rollingStore)
    e.integrationSLO.updateGauges(e.rollingStore)
    e.releaseSLO.updateGauges(e.rollingStore)
}
```

**Prometheus Scrape (Collect):**
```go
func (e *KAExporter) Collect(ch chan<- prometheus.Metric) {
    // Hold read lock to get consistent snapshot (1-5ms)
    e.mu.RLock()
    defer e.mu.RUnlock()

    e.buildSLO.Collect(ch)
    // ... emit gauge values
}
```

**Concurrency Guarantee:**
- `runCollection()` must ONLY be called from a single goroutine (started in `main.go`)
- The `coldStart` flag is read without mutex protection, which is safe because there's only one writer (the collection goroutine)
- A comment at the `Start()` method documents this constraint

**Trade-offs:**
- **Pro:** Prometheus scrapes are never blocked by KubeArchive HTTP fetches
- **Pro:** Write lock duration is minimal (1-5ms vs 30-60s)
- **Con:** Requires careful reasoning about concurrency invariants

---

### 5. 30-Day Rolling Window with Daily Buckets

**Problem:** Storing raw PipelineRun records for 30 days would consume excessive memory (millions of records).

**Solution:** Pre-aggregate observations into **daily buckets** and compute rolling metrics on-demand.

**Data Structure:**
```go
type DailyBucket struct {
    Day               string           // "2026-06-23"
    Count             int64            // Total completed (success + fail)
    SuccessCount      int64            // Only successful
    SuccessSumSeconds float64          // Duration sum (successful only)
    WaitSumSeconds    float64          // Queue time sum (all completed)
    FailureReasons    map[string]int64 // Failure count by reason
}

type MetricWindow struct {
    Buckets [30]DailyBucket // Fixed 30-day circular buffer
}
```

**Deduplication:**
- Each observation has a unique key: `namespace/name/uid/creationTimestamp`
- Keys are stored in `SeenKeys map[string]time.Time` with a retention window of 1.5× query window
- Prevents double-counting when overlapping queries fetch the same PipelineRun

**Memory Footprint:**
```
Per label combination:
- 30 buckets × ~100 bytes = 3 KB
- Dedupe keys: ~1,000 UIDs × 64 bytes = 64 KB

Typical deployment (500 label combinations):
- Total memory: ~33 MB for rolling windows + dedupe cache
```

**Stale Bucket Eviction:**
- During aggregation, buckets older than 30 days are skipped (not deleted, just ignored)
- `PruneSeenKeys()` runs periodically to clear old dedupe entries
- No explicit bucket deletion needed (circular buffer naturally overwrites old data)

---

## Key Constraints & Limitations

### 1. Long-Running Pipelines (>36h)

**Limitation:** Pipelines that take longer than the query window to complete may be missed in steady state.

**Why:** Queries filter by `creationTimestamp`, not `completionTimestamp`. If a PLR completes after falling out of the 36h window, it won't be captured.

**Mitigations:**
- Cold start captures these (30-day window)
- Increase `KA_WINDOW_HOURS` for clusters with long pipelines
- Future enhancement: Dual-query approach (both creation + completion timestamps)

---

### 2. Cold Start Item Cap (30,000 per query)

**Limitation:** Extremely busy namespaces with >30,000 PLRs in 30 days will be truncated during cold start.

**Mitigations:**
- Gap-fill mechanism retries with narrower time windows (up to 5 attempts = 150K total coverage)
- Truncation metrics track occurrences: `kaexporter_truncations_total{resource="pipelineruns"}`
- Per-namespace bootstrap state prevents repeated failures

**When This Happens:**
- Typical namespace: ~100-500 PLRs/day → no issue
- Busy namespace: 500-1,000 PLRs/day → may hit 30K limit over 30 days
- Very busy namespace: >1,000 PLRs/day → gap-fill may also truncate (uses additional 5 attempts)

---

### 3. No Historical Persistence

**Limitation:** Pod restart loses the 30-day rolling window state.

**Why:** Stateless design (no PVC, no external storage).

**Mitigations:**
- Cold start rebuilds state in 10-15 minutes
- Acceptable for SLO aggregates (not critical raw events)
- Future enhancement: Optional persistence to PVC or remote storage

---

### 4. Per-Namespace Timeout Behavior

**Problem:** A single slow namespace can block the entire collection cycle.

**Solution:** Use a **context with timeout** for the entire collection cycle, not per-namespace.

**Current Implementation:**
```go
// Collection timeout applies to ENTIRE cycle (all namespaces)
ctx, cancel := context.WithTimeout(context.Background(), timeout)
defer cancel()

err := e.collectMetrics(ctx) // Queries all namespaces in parallel
```

**Why Not Per-Namespace Timeouts?**
- Simpler implementation (fewer moving parts)
- Natural load-shedding: If KubeArchive is slow, skip the cycle rather than partial results
- Per-namespace timeouts would require tracking which namespaces succeeded

**Trade-offs:**
- **Pro:** Prevents cascading delays from slow namespaces
- **Con:** One slow namespace can cause all namespaces to skip a cycle
- **Mitigation:** Use generous timeout (30min cold start, 2min steady state)

---

## SLO Accuracy Requirements

**Target:** 30-day moving average with ≤1% error margin.

**How We Meet This:**
1. **Cold start:** Query full 30 days immediately → 0% error from day 1
2. **Deduplication:** UID-based keys prevent double-counting → no inflation
3. **Overlapping queries:** 36h window with 5min collection interval → no gaps
4. **Gap-filling:** Backfill truncated namespaces → reduce undercounting

**Validation:**
- Compare kaexporter metrics against raw KubeArchive queries
- Monitor `kaexporter_truncations_total` for data loss
- Alert on `kaexporter_scrape_errors_total{reason="collect"}` spikes


---

## Testing Strategy

**Unit Tests:**
- `metrics_test.go`: Observation recording, aggregation, deduplication
- `ka_client_test.go`: HTTP retry logic, pagination handling
- `rolling_store_test.go`: Bucket pruning, stale data eviction (NEW)

**Integration Tests:**
- End-to-end cold start simulation with mock KubeArchive API
- Gap-fill truncation scenarios
- Multi-namespace concurrent collection

**Production Validation:**
- Compare SLO metrics against Prometheus queries on raw KubeArchive data
- Monitor cold-start duration and truncation rates
- Verify memory usage stays within expected bounds (<100 MB per exporter pod)

---

## References

- **KubeArchive API Docs:**: https://kubearchive.github.io/kubearchive/main/reference/api.html
- **Konflux SLO Requirements:** https://redhat.atlassian.net/browse/PVO11Y-5274
