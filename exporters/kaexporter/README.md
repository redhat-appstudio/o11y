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

On first boot, the exporter queries **720 hours (30 days)** of historical data to populate the full rolling window before serving metrics.

| Setting | Cold start value | Steady-state value |
|---------|-----------------|-------------------|
| Query window | 720h (30 days) | `KA_WINDOW_HOURS` + 50% |
| Collection timeout | 600s | `KA_COLLECTION_TIMEOUT_SECONDS` |
| Concurrency | 5 | `KA_MAX_CONCURRENT` |
| Per-namespace item cap | 10,000 | 1,000 |

**Note:** `/metrics` endpoint is not served until cold start completes (~90-120 seconds). For architectural details on why this is necessary, see [DESIGN.md](DESIGN.md#1-cold-start-bootstrapping).

---

## Metrics

All metrics are **Gauges** over a rolling 30-day window of daily aggregated buckets.

### SLO Metrics (30-day rolling window)

| Metric | Phase | Labels |
|--------|-------|--------|
| `konflux_build_mean_duration_30d_seconds` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_mean_wait_30d_seconds` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_success_rate_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_failure_rate_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_total_count_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_success_count_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_failure_count_30d` | build | `cluster, namespace, application, component, build_type, event_type, reason` |
| `konflux_integration_mean_duration_30d_seconds` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_mean_wait_30d_seconds` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_success_rate_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_failure_rate_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_total_count_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_success_count_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_failure_count_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type, reason` |
| `konflux_release_cr_mean_duration_30d_seconds` | release | `cluster, namespace, application, component, automated` |
| `konflux_release_cr_mean_wait_30d_seconds` | release | `cluster, namespace, application, component, automated` |
| `konflux_release_cr_success_rate_30d` | release | `cluster, namespace, application, component, automated` |
| `konflux_release_cr_failure_rate_30d` | release | `cluster, namespace, application, component, automated` |
| `konflux_release_cr_total_count_30d` | release | `cluster, namespace, application, component, automated` |
| `konflux_release_cr_success_count_30d` | release | `cluster, namespace, application, component, automated` |
| `konflux_release_cr_failure_count_30d` | release | `cluster, namespace, application, component, automated, reason` |
| `konflux_release_cr_retry_count_30d` | release | `cluster, namespace, snapshot, release_plan, final_status` |

**Metric definitions**:
- **Duration metrics** (`mean_duration_30d_seconds`): Mean execution time for successful workloads (startTime to completionTime for PipelineRuns; startTime to completionTime for Releases)
- **Wait metrics** (`mean_wait_30d_seconds`): Mean waiting time before execution starts (creationTimestamp to startTime). Useful for identifying scheduling delays and resource constraints.
- **Success rate** (`success_rate_30d`): Ratio of successful workloads to total completed (0.0 to 1.0)
- **Error rate** (`failure_rate_30d`): Ratio of failed workloads to total completed (0.0 to 1.0). Inverse of success rate.
- **Total count** (`total_count_30d`): Count of all completed workloads (successful + failed) in the rolling window
- **Success count** (`success_count_30d`): Count of successful workloads in the rolling window. Enables correct volume-weighted aggregation across dimensions: `sum(success_count) / sum(total_count)`.
- **Failure count** (`failure_count_30d`): Count of failed workloads, broken down by failure reason. Useful for root cause analysis.
- **Retry count** (`konflux_release_cr_retry_count_30d`): Number of retries for each release intent (snapshot + releasePlan combination). Value is the count of additional attempts beyond the original (0 = no retries, 1 = one retry, etc.). Grouped by intent rather than individual Release CR to track how many times a specific release was retried.

**Failure Reasons**:

For PipelineRuns (builds and integration tests):
- `CouldntGetPipeline` - Failed to fetch pipeline definition
- `CouldntGetTask` - Failed to fetch task definition
- `CreateRunFailed` - Pipeline run creation failed
- `PipelineRunTimeout` - Execution exceeded timeout
- `Failed` - Generic pipeline failure
- `Unknown` - Failure with no reason specified

For Releases:
- `Failed` - Release failed
- `Skipped` - Release was skipped
- `Unknown` - Failure with no reason specified

**Note**: Releases with `Status="False"` and `Reason="Progressing"` are excluded from all metrics (not counted in total, success, or failure) as they represent in-progress releases, not completed ones.

**Label key** (phase-specific labels only; `cluster`, `namespace`, `application`, `component` are common to all):

| Label | Source | Values | Applies to |
|-------|--------|--------|------------|
| `build_type` | `tekton.dev/pipeline` label | `docker-builds`, `docker-multi-arch-builds`, `bundle-builds`, `operator-builds`, `operator-bundle-builds`, `fbc-builds`, `rpm-builds`, `standard-builds`, `custom-builds` | build only |
| `event_type` | `pipelinesascode.tekton.dev/event-type` (builds) / `pac.test.appstudio.openshift.io/event-type` (integration) | `push`, `pull_request`, `incoming`, `retest-comment`, `retest-all-comment` | build, integration only |
| `scenario` | `test.appstudio.openshift.io/scenario` | Integration test scenario name | integration only |
| `optional` | `test.appstudio.openshift.io/optional` | `true` (non-blocking), `false` (required) | integration only |
| `test_type` | Derived from pipeline labels | `ec` (Enterprise Contract), `integration` | integration only |
| `automated` | `release.appstudio.openshift.io/automated` | `true`, `false` | release only |
| `snapshot` | `release.appstudio.openshift.io/snapshot` label or `spec.snapshot` field | Snapshot name | release (retry count only) |
| `release_plan` | `spec.releasePlan` field | Release plan name | release (retry count only) |
| `final_status` | Derived from most recent attempt | `Succeeded`, `Failed`, `Unknown` | release (retry count only) |

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

## Endpoints

| Path | Description |
|------|-------------|
| `/metrics` | Prometheus metrics (instant read from cached state) |
| `/healthz` | Liveness check (always returns `200 OK`) |
| `/readyz` | Readiness check (returns `503` if last successful scrape is stale) |

---

## Troubleshooting

**Metrics not appearing after startup:**
- Check `/readyz` endpoint - it returns `503` until cold start completes (~90-120s)
- Check logs for `First collection complete in X.Xs` message

**Stale metrics (readiness probe failing):**
- Check `konflux_ka_exporter_scrape_errors_total` for collection errors
- Check `konflux_ka_exporter_last_scrape_success_timestamp_seconds` to see when last successful collection occurred
- Verify KubeArchive API is reachable and token is valid

**High truncation counts:**
- Monitor `konflux_ka_exporter_truncations_total{resource="pipelineruns"}`
- Namespaces with >10,000 PLRs in 30 days will truncate
- Gap-filling mechanism automatically retries (see [DESIGN.md](DESIGN.md#2-gap-filling-for-busy-namespaces))

**Memory usage growing:**
- Expected memory: ~50-100 MB depending on label cardinality
- Check number of unique label combinations (namespaces × applications × components)
- Consider filtering to specific namespaces via `TENANT_NAMESPACE`

---

## Architecture

For detailed architecture decisions, internal implementation details, and design rationale, see [DESIGN.md](DESIGN.md).
