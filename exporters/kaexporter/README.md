# KubeArchive exporter (kaexporter)

Prometheus exporter for **Konflux delivery metrics** built from archived pipeline data.
This exporter reads archived **PipelineRun**, **Snapshot**, and **Release** resources from the [KubeArchive](https://github.com/kubearchive/kubearchive) HTTP API and exposes Konflux delivery metrics as **Histograms** (duration distributions) and **Gauges** (point-in-time values).

---

## Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `KA_HOST` | Yes | — | KubeArchive API base URL |
| `KA_TOKEN` | Yes | — | Bearer token for the KubeArchive API |
| `CLUSTER_NAME` | No | `unknown` | Value of the `cluster` label on all series |
| `TENANT_NAMESPACE` | No | *(empty)* | Single-tenant mode when set; multi-tenant (all `konflux-ci.dev/type=tenant` namespaces) when empty |
| `MANAGED_RELEASE_PLR_NAMESPACES` | No | *(empty)* | Comma-separated list of managed namespaces to scrape for release PipelineRuns (e.g. `rhtap-releng-tenant`) |
| `KA_WINDOW_HOURS` | No | `48` | Look-back window in hours for all KubeArchive queries (`creationTimestampAfter`). KubeArchive has no automatic retention — without this filter each scrape scans 6+ months of history. 48 h covers weekends and off-hours builds. |
| `KA_SCRAPE_TIMEOUT_SECONDS` | No | `120` (code fallback) | Hard deadline in seconds for each `Collect()` call. All in-flight KubeArchive HTTP requests are cancelled when this fires. **Must be set below the Prometheus `scrapeTimeout`** configured in the ServiceMonitor. The code fallback of `120s` is safe but conservative — the deployment manifest sets `160s` explicitly (20s below `scrapeTimeout: 180s`), and that value is authoritative for production. If you deploy without this env var you get `120s`, which still works correctly but gives less headroom for slow KubeArchive responses. |
| `KA_MAX_CONCURRENT` | No | `10` | Maximum concurrent KubeArchive API calls (release fetch and namespace scraping). |
| `EXPORTER_PORT` | No | `9101` | HTTP listen port |

---

## Metrics

**Histograms** accumulate across scrapes and expose `_bucket`, `_sum`, and `_count` suffixes.
Use `histogram_quantile(0.95, rate(..._bucket[1h]))` for percentiles, or `rate(..._sum[1h]) / rate(..._count[1h])` for averages.

**Gauges** are point-in-time and reset each scrape (reflect the most recent completed run per label set).

### Build

| Metric | Type |
|--------|------|
| `konflux_build_pipelinerun_duration_seconds` | Histogram |
| `konflux_build_pipelinerun_wait_seconds` | Gauge |

### Integration

| Metric | Type |
|--------|------|
| `konflux_build_to_integration_gap_seconds` | Gauge |
| `konflux_integration_pipelinerun_duration_seconds` | Histogram |
| `konflux_integration_pipelinerun_wait_seconds` | Gauge |

### Release

| Metric | Type |
|--------|------|
| `konflux_release_duration_seconds` | Histogram |
| `konflux_release_pipelinerun_duration_seconds` | Histogram |
| `konflux_release_pipelinerun_wait_seconds` | Gauge |
| `konflux_release_pipelinerun_execution_duration_seconds` | Histogram |

### Operational

| Metric | Type |
|--------|------|
| `konflux_archived_completion_count` | Gauge |

---

## Exporter self-monitoring

- `konflux_ka_exporter_scrape_errors_total`
- `konflux_ka_exporter_last_scrape_success_timestamp_seconds`
- `konflux_ka_exporter_scrape_duration_seconds`
- `konflux_ka_exporter_truncations_total`

---

## Build and run

```bash
go build -o kaexporter -mod=mod ./exporters/kaexporter/
```

```bash
export KA_HOST="https://kubearchive-api-server.<cluster>"
export KA_TOKEN="<token>"
export CLUSTER_NAME="<cluster-id>"
export MANAGED_RELEASE_PLR_NAMESPACES="rhtap-releng-tenant"
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

**Scrape timeout alignment** — the following three values must stay consistent. The deployment manifest already sets them correctly; document any changes here if you adjust them:

| Setting | Location | Value | Rule |
|---------|----------|-------|------|
| `scrapeTimeout` | `ka-exporter-service-monitor.yaml` | `180s` | Outermost Prometheus deadline |
| `KA_SCRAPE_TIMEOUT_SECONDS` | `ka-exporter-service.yaml` (env var) | `160s` | Must be < `scrapeTimeout` |
| `interval` | `ka-exporter-service-monitor.yaml` | `300s` | Must be > `scrapeTimeout` to prevent overlapping scrapes |

The code default for `KA_SCRAPE_TIMEOUT_SECONDS` is `120s` (safe fallback), but the deployment manifest is the authoritative source. If you deploy outside of the provided manifests, set `KA_SCRAPE_TIMEOUT_SECONDS` explicitly.

---

## Endpoints

| Path | Description |
|------|-------------|
| `/metrics` | Prometheus metrics |
| `/health` | Liveness check (`OK`) |
