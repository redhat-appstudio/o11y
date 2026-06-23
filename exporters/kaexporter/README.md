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
| `CLUSTER_NAME` | No | `unknown` | Cluster name label applied to all metrics |
| `TENANT_NAMESPACE` | No | *(empty)* | Restrict scraping to a specific namespace. Empty = multi-tenant mode (discovers all namespaces with `konflux-ci.dev/type=tenant`) |
| `KA_WINDOW_HOURS` | No | `24` | Steady-state look-back window. A 50% safety margin is added internally (e.g., 24h → 36h actual query) to capture long-running pipelines. Only applies after cold start. |
| `KA_COLLECT_INTERVAL_SECONDS` | No | `300` | How often (seconds) background collection refreshes metrics. Should match the Prometheus scrape interval. |
| `KA_COLLECTION_TIMEOUT_SECONDS` | No | `120` | Per-cycle deadline for steady-state collections. Must be less than `KA_COLLECT_INTERVAL_SECONDS`. |
| `KA_MAX_CONCURRENT` | No | `10` | Max parallel KubeArchive API calls per steady-state cycle. |
| `KA_HTTP_TIMEOUT_SECONDS` | No | `60` | Per-request HTTP timeout for KubeArchive API calls. |
| `KA_MAX_RETRIES` | No | `3` | Max retries per failed KubeArchive request (exponential backoff). |
| `KA_INITIAL_RETRY_DELAY_MS` | No | `100` | Initial retry delay in milliseconds. |
| `KA_MAX_RETRY_DELAY_MS` | No | `5000` | Maximum retry delay cap in milliseconds. |
| `EXPORTER_PORT` | No | `9101` | HTTP listen port. |

### Cold start behavior

On first boot, the exporter automatically bootstraps the full 30-day rolling window before serving metrics. This phase uses hardcoded settings independent of the env vars above:

| Setting | Cold start value | Steady-state value |
|---------|-----------------|-------------------|
| Query window | 720h (30 days) | `KA_WINDOW_HOURS` + 50% |
| Collection timeout | 600s | `KA_COLLECTION_TIMEOUT_SECONDS` |
| Concurrency | 5 | `KA_MAX_CONCURRENT` |
| Per-namespace item cap | 10,000 | 1,000 |

After the first successful collection, the exporter switches to steady-state settings permanently (until next restart). `/metrics` is not served until cold start completes.

---

## Metrics

All metrics are **Gauges** over a rolling 30-day window of daily aggregated buckets.

| Metric | Phase | Labels |
|--------|-------|--------|
| `konflux_build_mean_duration_30d_seconds` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_success_rate_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_total_count_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_integration_mean_duration_30d_seconds` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_success_rate_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_total_count_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_release_cr_mean_duration_30d_seconds` | release | `cluster, namespace, application, component, event_type, automated` |
| `konflux_release_cr_success_rate_30d` | release | `cluster, namespace, application, component, event_type, automated` |
| `konflux_release_cr_total_count_30d` | release | `cluster, namespace, application, component, event_type, automated` |

**Label key** (phase-specific labels only; `cluster`, `namespace`, `application`, `component`, `event_type` are common to all):

| Label | Source | Values |
|-------|--------|--------|
| `build_type` | `tekton.dev/pipeline` label | `docker-builds`, `docker-multi-arch-builds`, `bundle-builds`, `operator-builds`, `operator-bundle-builds`, `fbc-builds`, `rpm-builds`, `standard-builds`, `custom-builds` |
| `event_type` | `pipelinesascode.tekton.dev/event-type` (builds) / `pac.test.appstudio.openshift.io/event-type` (tests, releases) | `push`, `pull_request`, `incoming`, `retest-comment`, `retest-all-comment` |
| `scenario` | `test.appstudio.openshift.io/scenario` | Integration test scenario name |
| `optional` | `test.appstudio.openshift.io/optional` | `true` (non-blocking), `false` (required) |
| `test_type` | Derived from pipeline labels | `ec` (Enterprise Contract), `integration` |
| `automated` | `release.appstudio.openshift.io/automated` | `true`, `false` |

**Self-monitoring**:

| Metric | Labels | Description |
|--------|--------|-------------|
| `konflux_ka_exporter_scrape_errors_total` | `cluster, phase` | Scrape errors by phase |
| `konflux_ka_exporter_last_scrape_success_timestamp_seconds` | `cluster` | Unix timestamp of last successful scrape |
| `konflux_ka_exporter_scrape_duration_seconds` | `cluster` | Collection cycle duration |
| `konflux_ka_exporter_truncations_total` | `cluster, resource, namespace` | KubeArchive fetch truncations (item cap hit) |
| `konflux_ka_exporter_retry_attempts_total` | `cluster, reason` | Retry attempts by reason |
| `konflux_ka_exporter_retry_exhausted_total` | `cluster, reason` | Requests exhausted after max retries |

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

Manifests: `config/exporters/monitoring/kaexporter/base/`

```bash
oc apply -k config/exporters/monitoring/kaexporter/base/
```

**Deployment requirements**:
- `KA_HOST` and `KA_TOKEN` must be set (see table above)
- `KA_COLLECT_INTERVAL_SECONDS` should match the Prometheus scrape interval

---

## Endpoints

| Path | Description |
|------|-------------|
| `/metrics` | Prometheus metrics (instant read from cached state) |
| `/health` | Liveness check (always returns `200 OK`) — deprecated, use `/healthz` |
| `/healthz` | Liveness check (always returns `200 OK`) |
| `/readyz` | Readiness check (returns `503` if last successful scrape is stale) |
