# KubeArchive exporter (kaexporter)

Prometheus exporter that computes **30-day moving averages** for Konflux build, integration, and release pipelines from KubeArchive data.

Exposes mean duration and success rate metrics over a rolling 30-day window using in-memory daily pre-aggregated buckets. Designed to meet Konflux SLO requirements while working within KubeArchive query constraints and Prometheus cardinality limits.

**Note**: Metrics are computed from an in-memory rolling store and reset on pod restart.

---

## Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `KA_HOST` | Yes | — | KubeArchive API base URL |
| `KA_TOKEN` | Yes | — | Bearer token for KubeArchive API |
| `CLUSTER_NAME` | No | `unknown` | Cluster name label on metrics |
| `TENANT_NAMESPACE` | No | *(empty)* | Single-tenant mode (specific namespace); empty = multi-tenant (all namespaces with `konflux-ci.dev/type=tenant`) |
| `KA_WINDOW_HOURS` | No | `48` | Base look-back window for KubeArchive queries. **Actual query window = KA_WINDOW_HOURS + 50% safety margin** to capture long-running pipelines (e.g., 48h → 72h query). Default 48h covers weekends + typical pipeline durations. |
| `KA_COLLECT_INTERVAL_SECONDS` | No | `300` | How often background collection refreshes metrics |
| `KA_COLLECTION_TIMEOUT_SECONDS` | No | `120` | Collection timeout (must be < collect interval) |
| `KA_MAX_CONCURRENT` | No | `10` | Max parallel KubeArchive API calls |
| `KA_MAX_RETRIES` | No | `3` | Max retries for failed KubeArchive calls |
| `EXPORTER_PORT` | No | `9101` | HTTP listen port |

---

## Metrics

All metrics are **Gauges** computed from an in-memory rolling store of daily aggregated buckets (30 days).

### Build Metrics
**Labels**: `{cluster, namespace, application, component, build_type, event_type}`
- `build_type`: Build pipeline type (e.g., "docker-build", "build-rpm-package", "multi-arch-build-pipeline")
- `event_type`: PipelinesAsCode event type (e.g., "push", "Merge_Request", "retest-comment", "retest-all-comment")

| Metric | Description |
|--------|-------------|
| `konflux_build_mean_duration_30d_seconds` | Mean build duration (30d) — **successful builds only** |
| `konflux_build_success_rate_30d` | Build success rate (30d) |

### Integration Test Metrics
**Labels**: `{cluster, namespace, application, component, scenario, optional, test_type}`
- `scenario`: Test scenario name (e.g., "containers-hummingbird-conforma", "containers-hummingbird-k8s-test")
- `optional`: "true" if test can fail without blocking release, "false" if required to pass
- `test_type`: "ec" for Enterprise Contract validation, "integration" for regular integration tests

| Metric | Description |
|--------|-------------|
| `konflux_integration_mean_duration_30d_seconds` | Mean integration test duration (30d) per scenario — **successful tests only** |
| `konflux_integration_success_rate_30d` | Integration test success rate (30d) per scenario |

### Release Metrics
**Labels**: `{cluster, namespace, application, component}`

| Metric | Description |
|--------|-------------|
| `konflux_release_mean_duration_30d_seconds` | Mean release duration (30d) — **successful releases only** |
| `konflux_release_success_rate_30d` | Release success rate (30d) |

**Self-monitoring**:
| Metric | Description |
|--------|-------------|
| `konflux_ka_exporter_scrape_errors_total{cluster, phase}` | Scrape errors by phase |
| `konflux_ka_exporter_last_scrape_success_timestamp_seconds` | Last successful scrape (unix timestamp) |
| `konflux_ka_exporter_scrape_duration_seconds` | Collection cycle duration |
| `konflux_ka_exporter_truncations_total{cluster, resource, namespace}` | KubeArchive fetch truncations |
| `konflux_ka_exporter_retry_attempts_total{cluster, reason}` | Retry attempts by reason |
| `konflux_ka_exporter_retry_exhausted_total{cluster, reason}` | Failed requests after max retries |

**Example queries**:
```promql
# Components with build success rate < 90%
konflux_build_success_rate_30d < 0.90

# Top 5 slowest builds
topk(5, konflux_build_mean_duration_30d_seconds)

# Build success rate for Merge_Request events (PR builds)
konflux_build_success_rate_30d{event_type="Merge_Request"}

# Build success rate for RPM package builds
konflux_build_success_rate_30d{build_type="build-rpm-package"}

# Retry success rate: retest-comment vs retest-all-comment
avg(konflux_build_success_rate_30d{event_type="retest-comment"}) vs 
avg(konflux_build_success_rate_30d{event_type="retest-all-comment"})

# Enterprise Contract (EC) success rate (all EC tests)
konflux_integration_success_rate_30d{test_type="ec"}

# EC success rate for specific scenario
konflux_integration_success_rate_30d{test_type="ec", scenario=~".*-conforma"}

# Regular integration tests (non-EC)
konflux_integration_success_rate_30d{test_type="integration"}

# Optional test performance (can fail without blocking release)
konflux_integration_success_rate_30d{optional="true"}

# Required test performance (must pass to release)
konflux_integration_success_rate_30d{optional="false"}

# Top 5 slowest integration test scenarios
topk(5, konflux_integration_mean_duration_30d_seconds)

# Compare EC vs integration test success rates
avg(konflux_integration_success_rate_30d{test_type="ec"}) vs
avg(konflux_integration_success_rate_30d{test_type="integration"})
```


---

## Build and run

```bash
go build -o kaexporter -mod=mod ./exporters/kaexporter/
```

```bash
export KA_HOST="https://kubearchive-api-server.<cluster>"
export KA_TOKEN="<token>"
export CLUSTER_NAME="<cluster-id>"
./kaexporter
```

---

## Test

```bash
go test -mod=mod -count=1 ./exporters/kaexporter/...
```

---

## Deploy

Manifests: `config/exporters/monitoring/ka/base/`

```bash
oc apply -k config/exporters/monitoring/ka/base/
```

**Deployment requirements**:
- Environment variables (see above table)
- Background collection interval (`KA_COLLECT_INTERVAL_SECONDS`) should match Prometheus scrape interval (default: 300s)

**Startup behavior**:
- First collection runs synchronously before `/metrics` endpoint opens (~30s)
- Metrics populate from in-memory rolling store as data is collected
- Pod restart clears all metrics (30-day window rebuilds incrementally from KubeArchive)

HTTP server starts only after first collection completes.

---

## Endpoints

| Path | Description |
|------|-------------|
| `/metrics` | Prometheus metrics (instant read from cached state) |
| `/health` | Liveness check (always returns `200 OK`) |
