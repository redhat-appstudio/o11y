# KubeArchive exporter (kaexporter)

Prometheus exporter for **Konflux delivery metrics** built from archived pipeline data.
This exporter reads archived **PipelineRun**, **Snapshot**, and **Release** resources from the [KubeArchive](https://github.com/kubearchive/kubearchive) HTTP API and exposes Konflux delivery metrics as Gauges.

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
| `KA_SCRAPE_TIMEOUT_SECONDS` | No | `120` | Hard deadline in seconds for each Collect() call. All in-flight HTTP requests are cancelled when this fires. Set to slightly less than the Prometheus `scrape_timeout`. |
| `KA_MAX_CONCURRENT` | No | `10` | Maximum concurrent KubeArchive API calls (release fetch and namespace scraping). |
| `EXPORTER_PORT` | No | `9101` | HTTP listen port |

---

## Metrics

All metrics represent **durations in seconds** for the latest completed runs.

### Build

- `konflux_build_pipelinerun_duration_seconds`
- `konflux_build_pipelinerun_queue_seconds`

### Integration

- `konflux_build_to_integration_gap_seconds`
- `konflux_integration_pipelinerun_duration_seconds`

### Release

- `konflux_release_duration_seconds`
- `konflux_release_pipelinerun_duration_seconds`
- `konflux_release_pipelinerun_queue_seconds`
- `konflux_release_pipelinerun_execution_duration_seconds`

### Operational

- `konflux_archived_completion_count`

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

---

## Endpoints

| Path | Description |
|------|-------------|
| `/metrics` | Prometheus metrics |
| `/health` | Liveness check (`OK`) |
