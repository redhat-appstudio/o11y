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
| `KA_CONFIG_FILE` | No | *(empty)* | Path to YAML config file. Controls namespace exclusions and per-tenant SLO threshold overrides. When unset, no namespaces are excluded and default SLO thresholds are used. |
| `EXPORTER_PORT` | No | `9101` | HTTP listen port. |

### Configuration file

When `KA_CONFIG_FILE` is set, the exporter reads a YAML file that specifies namespaces to exclude from metric collection. Entries containing `*` are treated as glob patterns (using Go's `path.Match`); all others are exact matches. The `*` wildcard matches any sequence of characters and can appear anywhere in the pattern — leading (`*-managed`), trailing (`managed-*`), or mid-string (`konflux-perfscale-*-tenant`).

```yaml
excludeNamespaces:
  - rhtap-releng-tenant
  - "managed-*"
  - "konflux-perfscale-*-tenant"
```

If the file is specified but cannot be read or parsed, the exporter fails to start. When `KA_CONFIG_FILE` is not set, no namespaces are excluded and default SLO thresholds are used.

#### Custom SLO thresholds

The same config file supports a `customSLO` section for per-tenant, per-application, or per-component SLO threshold overrides. Values cascade from the most specific level upward: component overrides application, application overrides tenant, tenant overrides built-in defaults (k=2 stddev, 5% breach percentage).

At each level, domain-specific keys can be set:

| Key | Description |
|-----|-------------|
| `build_duration_threshold_seconds` | Fixed build duration threshold in seconds. Replaces the computed mean + 2*stddev. |
| `build_duration_breach_percentage` | Fraction of daily means that must exceed the threshold to trigger breach (default: 0.05). |
| `integration_duration_threshold_seconds` | Fixed integration test duration threshold. |
| `integration_duration_breach_percentage` | Integration breach percentage override. |
| `release_duration_threshold_seconds` | Fixed release duration threshold. |
| `release_duration_breach_percentage` | Release breach percentage override. |

Each key accepts either a simple numeric value or an object with `default` and `matches` for label-specific precision:

```yaml
# Simple form (backward compatible):
build_duration_threshold_seconds: 7200

# Match-based form:
integration_duration_threshold_seconds:
  default: 1800
  matches:
    - scenario: ec-scan
      value: 300
    - event_type: pull_request
      value: 900
```

Match fields (`scenario`, `event_type`, `build_type`, `automated`) are compared against the metric's label set. Empty fields act as wildcards. Matches are evaluated in YAML order; the first match wins. When no match hits, `default` is used. When `default` is also absent, the value falls through to the parent hierarchy level or the built-in defaults.

When `duration_threshold_seconds` is set, the breach evaluation uses that fixed value directly instead of computing `mean + 2*stddev`. When omitted, the statistical baseline is used. Breach percentages must be in the range (0, 1]. Thresholds must be > 0. Invalid values are logged as warnings at startup and ignored -- the exporter starts normally and the affected overrides fall back to built-in defaults.

```yaml
excludeNamespaces:
  - rhtap-releng-tenant
  - "managed-*"

customSLO:
  tenants:
    a-team-tenant:
      integration_duration_threshold_seconds: 3600
      applications:
        heavy-app:
          integration_duration_breach_percentage: 0.10
          components:
            slow-builder:
              build_duration_threshold_seconds: 7200
            tested-component:
              integration_duration_threshold_seconds:
                default: 1800
                matches:
                  - scenario: enterprise-contract
                    value: 300
                  - scenario: long-running-integration
                    event_type: push
                    value: 5400
```

In this example:
- `slow-builder` uses a fixed 7200s build threshold and inherits the 10% integration breach percentage from `heavy-app`
- `tested-component` uses different integration thresholds depending on the scenario: 300s for EC checks, 5400s for a specific long-running scenario on push events, and 1800s for everything else
- All other components in `heavy-app` use the default stddev-based build threshold but the relaxed 10% integration breach percentage
- All components in `a-team-tenant` use a fixed 3600s integration threshold unless overridden at a lower level
- Tenants not listed use the built-in defaults

### Cold start behavior

On first boot, the exporter queries **720 hours (30 days)** of historical data to populate the full rolling window before serving metrics.

| Setting | Cold start value | Steady-state value |
|---------|-----------------|-------------------|
| Query window | 720h (30 days) | `KA_WINDOW_HOURS` + 50% |
| Collection timeout | 600s | `KA_COLLECTION_TIMEOUT_SECONDS` |
| Concurrency | 5 | `KA_MAX_CONCURRENT` |
| Per-namespace item cap | 30,000 | 1,500 |

**Note:** `/metrics` endpoint is not served until cold start completes (~90-120 seconds). For architectural details on why this is necessary, see [DESIGN.md](DESIGN.md#1-cold-start-bootstrapping).

---

## Metrics

All metrics are **Gauges** over a rolling 30-day window of daily aggregated buckets.

### SLO Metrics (30-day rolling window)

| Metric | Phase | Labels |
|--------|-------|--------|
| `konflux_build_mean_duration_seconds_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_mean_wait_seconds_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_total_count_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_success_count_30d` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_build_failure_count_30d` | build | `cluster, namespace, application, component, build_type, event_type, reason` |
| `konflux_build_duration_slo_breach` | build | `cluster, namespace, application, component, build_type, event_type` |
| `konflux_integration_mean_duration_seconds_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_mean_wait_seconds_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_total_count_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_success_count_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_integration_failure_count_30d` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type, reason` |
| `konflux_integration_duration_slo_breach` | integration | `cluster, namespace, application, component, scenario, optional, test_type, event_type` |
| `konflux_release_cr_mean_duration_seconds_30d` | release | `cluster, namespace, application, component, automated, event_type` |
| `konflux_release_cr_mean_wait_seconds_30d` | release | `cluster, namespace, application, component, automated, event_type` |
| `konflux_release_cr_total_count_30d` | release | `cluster, namespace, application, component, automated, event_type` |
| `konflux_release_cr_success_count_30d` | release | `cluster, namespace, application, component, automated, event_type` |
| `konflux_release_cr_failure_count_30d` | release | `cluster, namespace, application, component, automated, event_type, reason` |
| `konflux_release_cr_duration_slo_breach` | release | `cluster, namespace, application, component, automated, event_type` |

**Metric definitions**:
- **Duration metrics** (`mean_duration_seconds_30d`): Mean execution time for **successful workloads only** (startTime to completionTime for PipelineRuns; startTime to completionTime for Releases). Failed workloads are excluded from this average.
- **Wait metrics** (`mean_wait_seconds_30d`): Mean waiting time before execution starts (creationTimestamp to startTime) for **successful workloads only**. Useful for identifying scheduling delays and resource constraints. Failed workloads are excluded from this average.
- **Total count** (`total_count_30d`): Count of all completed workloads (successful + failed) in the rolling window
- **Success count** (`success_count_30d`): Count of successful workloads in the rolling window. Enables correct volume-weighted aggregation across dimensions: `sum(success_count) / sum(total_count)`.
- **Failure count** (`failure_count_30d`): Count of failed workloads, broken down by failure reason. Useful for root cause analysis.
- **Duration SLO breach** (`duration_slo_breach`): 1 if the component's duration SLO is breached, 0 otherwise. Not emitted when data is insufficient (success_count < 10 or days_with_data < 3). By default, a component is in breach when >=5% of its daily mean durations over the past 30 days exceed the 30-day mean + 2 standard deviations. Thresholds and breach percentages can be overridden per tenant/application/component via the config file (see [Custom SLO thresholds](#custom-slo-thresholds)).

**Derived metrics** (can be computed from the above):
- **Success rate**: `success_count_30d / total_count_30d` (or 0 when total_count_30d == 0)
- **Failure rate**: `(total_count_30d - success_count_30d) / total_count_30d` or `1 - success_rate`

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
| `event_type` | `pipelinesascode.tekton.dev/event-type` (builds) / `pac.test.appstudio.openshift.io/event-type` (integration, release) | `push`, `pull_request`, `incoming`, `retest-comment`, `retest-all-comment` | build, integration, release |
| `scenario` | `test.appstudio.openshift.io/scenario` | Integration test scenario name | integration only |
| `optional` | `test.appstudio.openshift.io/optional` | `true` (non-blocking), `false` (required) | integration only |
| `test_type` | Derived from pipeline labels | `ec` (Enterprise Contract), `integration` | integration only |
| `automated` | `release.appstudio.openshift.io/automated` | `true`, `false` | release only |

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
- Truncation is expected during cold-start bootstrap for high-volume tenants (>30,000 items in 30 days) and resolves automatically via gap-fill retries across subsequent collection cycles
- In steady state (36h window, 1,500 item cap), truncation is normal for the busiest tenants and does not affect 30-day accuracy since most data is already in the rolling store

**Memory usage growing:**
- Expected memory: ~50-100 MB in stage, ~1 GB in production depending on label cardinality
- Check number of unique label combinations (namespaces x applications x components)
- Consider filtering to specific namespaces via `TENANT_NAMESPACE`

---

## Architecture

For detailed architecture decisions, internal implementation details, and design rationale, see [DESIGN.md](DESIGN.md).
