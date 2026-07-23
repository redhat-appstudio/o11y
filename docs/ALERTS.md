## Alerting rules overview
Konflux uses Prometheus alert rules to send out alerts to teams. Alerts split into two tiers: **SLO alerts** (PagerDuty pages on-call, Slack message, feed the status page) and **non-SLO alerts** (Slack message). PagerDuty has limited licenses, so the team built a dual routing mechanism: SLO alerts use direct Slack team handles (`alert_team_handle`), while non-SLO alerts use an indirect routing key (`alert_routing_key`) mapped to team handles via an Alertmanager routing matrix. Each alert is also tested rigorously via promtool unit test files.

## PromQL rule validation and unit tests

```
make check-and-test
```
Runs `obsctl-reloader-rules-checker` container against alert and recording rules + their tests. No local tool install needed — promtool and pint are bundled in the container image.

### Selective rule testing (faster iteration)

After the environment split, rule files live under `rhobs/staging/` and `rhobs/production/`. Test against either environment:

```
make selective-check-and-test \
  RULE_FILES="rhobs/staging/alerting/data-plane/alerts/my_alert.yaml" \
  TEST_CASE_FILES="rhobs/staging/alerting/data-plane/tests/my_alert_test.yaml"
```

For KRD (Konflux Release Data) alerts:
```
make selective-check-and-test \
  RULE_FILES="rhobs/staging/alerting/konflux-release-data/alerts/my_alert.yaml" \
  TEST_CASE_FILES="rhobs/staging/alerting/konflux-release-data/tests/my_alert_test.yaml"
```

### Alert convention checks
```
make check-alert-conventions
```
Validates label conventions — e.g. `slo: "true"` must be paired with `severity: critical`. Runs in the same container image.

### YAML linting
```
make sync_pipenv   # first time only
make lint_yamls
```

## Architecture
Alert rules are evaluated by RHOBS (Red Hat Observability Service), not by in-cluster Prometheus. RHOBS has a dedicated Alertmanager instance which routes alerts into PagerDuty and Slack. SPRE holds the PagerDuty pager; AppSRE owns the RHOBS infrastructure. O11Y team owns the Alertmanager routing config in app-interface and alert rules in this repository. See [ALERTING_HISTORY.md](ALERTING_HISTORY.md) for the full design history and ownership split.

**Alert grouping:** Alertmanager groups alerts before sending notifications. The grouping strategy differs by tier:
- **SLO alerts** are grouped by all labels (`group_by: ['...']`). Each unique label combination produces its own Slack message and PagerDuty incident, so every distinct alert instance is reported individually.
- **Non-SLO alerts** are grouped by `alertname` and `cluster` (`group_by: [alertname, cluster]`). Multiple instances of the same alert on the same cluster are batched into a single Slack message with team routings preserved — e.g. if `PodNotReady` fires for 5 pods on the same cluster for multiple teams, they appear as one message containing several team-specific alerts.

**Metric forwarding:** Because alerts are evaluated by RHOBS (not in-cluster Prometheus), any metric used in an alert must first be forwarded from Konflux clusters to RHOBS via [infra-deployments](https://github.com/redhat-appstudio/infra-deployments). An alert referencing a metric that isn't forwarded will silently never fire. See [adding-alert SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alerting/adding-alert.md) for the full checklist.

**Exception — KRD alerts:** Konflux Release Data runner and pipeline metrics are not forwarded via infra-deployments. They are scraped and forwarded by an OTEL Collector defined in [krd-monitoring](https://gitlab.cee.redhat.com/konflux/o11y/krd-monitoring). ArgoCD `argocd_app_info` metrics are forwarded via the standard Prometheus remote-write path in [app-interface](https://gitlab.cee.redhat.com/service/app-interface/-/blob/dabd5d7a2dc8cc229941b4669bb9f2fd4413f3d7/data/services/observability/cicd/saas/saas-observability-per-cluster.yaml#L457). See the [KRD monitoring SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/konflux-release-data-monitoring.md) for full architecture and alert details.

## Deployment Model
Changes are deployed into RHOBS based on [references defined in app-interface](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml?ref_type=heads).

- **Staging**: auto-deploys from `main` branch via app-interface.
- **Production**: requires manually updating the commit reference. Always use the merge commit sha from `main` (not an individual branch commit) to ensure all changes are included. Check that the updated sha does not roll back already deployed changes.

### Environment split

Alert and recording rules are duplicated under `rhobs/staging/` and `rhobs/production/`. Each environment has its own directory tree that app-interface points at independently. This enables:

- **Staged rollouts**: land a new alert in staging first, validate it, then promote to production in a follow-up PR.
- **Environment-specific rules**: alerts that only apply to one environment (e.g. `absent()` alerts with per-env cluster lists, prod-only runner monitoring).
- **Environment-specific thresholds**: different `for` durations or severity levels per environment when SPREs recommend it.

A CI drift detection check compares the two directories on every PR and flags unacknowledged differences. See [ALERT_DRIFT.md](ALERT_DRIFT.md) for the full workflow, bypass mechanisms, and common scenarios.

## Conventions

### Alert types

Use the templates in [templates/](templates/) as a starting point for new alerts. Each field is documented with required/optional tags and usage comments.

**SLO alerts** page on-call via PagerDuty and post to `#konflux-slo-alerts`. They also feed the [Tactical Status Page](https://tsp.status.redhat.com). SLO alerts require [SOP runbook](https://gitlab.cee.redhat.com/konflux/docs/sop).

**Non-SLO alerts** (misc alerts) all route to [`#konflux-misc-alerts`](https://redhat.enterprise.slack.com/archives/C06GFP9JJBV). The `alert_routing_key` annotation is mapped to a Slack team handle via a routing matrix in Alertmanager, so the right team gets tagged within the channel. For general alerts, like quota or pod issues, this annotation can be set to the namespace name via template  `{{ $labels.namespace }}`. This allows general alert routing to the correct teams based on namespace ownership.

Example mappings:

| Routing Key | Team |
|------------|------|
| `pipelines`, `pipeline-service.*`, `tekton-*` | Pipelines |
| `o11y`, `appstudio-grafana`, `monitoring-workload.*` | O11y |

If a key doesn't match any mapping, the o11y team is notified as fallback. See the [alerting guide SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alerting/alerting_guide.md) for the full routing matrix.

**KRD (Konflux Release Data) alerts** are routed by the `component` label with a `krd-` prefix into a separate `#konflux-releng-alerts` Slack channel. See the [KRD monitoring SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/konflux-release-data-monitoring.md) for details.

**Severity levels:**
| Severity | Usage |
|----------|-------|
| critical | Required for SLO alerts (`slo: "true"`). Also allowed for high-priority non-SLO issues |
| high | Important service degradation, not SLO-bound |
| warning | Non-urgent, lower priority |
| info | Maintenance/informational events (rare) |

Note: The [adding-alert SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alerting/adding-alert.md) formally documents `critical` and `warning`; `high` and `info` are used in practice but not in the SOP.

**Availability alerts (`konflux_up` pattern):**

`konflux_up` is a standardized binary metric (0 = down, 1 = up) that every Konflux service should expose. It enables a unified SLO dashboard and the Tactical Status Page to show fleet-wide availability.

Required labels:
- `service` — component name (e.g. `build-service`, `grafana`)
- `check` — what's being verified (e.g. `replicas-available`, `probe`, `github`)
- `source_cluster` — added automatically by RHOBS forwarding

Optional labels:
- `severity` — set on the metric itself (not the alert rule) to override the default alert tier for a specific service. Valid value: `high`. When present, the probe alert for that service fires as `ProbeAlertHigh` (severity: high, slo: "false") instead of `ProbeAlert` (severity: critical, slo: "true"). This lets individual services opt out of SLO-grade paging while still receiving alerting. The label must be emitted by the upstream exporter or recording rule — see [SPRE-5323](https://issues.redhat.com/browse/SPRE-5323) for the probe exporter change that introduced it.

How `konflux_up` signals are created:
- **Recording rules** (most common): transform an existing metric into `konflux_up` via `label_replace` and clamp to 0/1. Example: `kube_deployment_status_replicas_available / kube_deployment_spec_replicas`. See `rhobs/recording/` for examples.
- **Custom Go exporters**: expose `konflux_up` directly via the Prometheus client library when availability checks require custom logic (HTTP probes, API calls). See `exporters/dsexporter/` for the reference implementation.

In both cases, the metric must be forwarded to RHOBS via infra-deployments before alerts can consume it.

Alerts built on `konflux_up` follow the pattern `konflux_up{namespace="...", check="...", service="..."} != 1`. A meta-alert (`KonfluxAlert`) also monitors for missing `konflux_up` signals by comparing against an `offset 1h` window and notifies o11y if a signal disappears.

### Converting between alert types

To promote a non-SLO alert to SLO:
1. Change `slo: ""` to `slo: "true"` and `severity` to `critical`
2. Replace `alert_routing_key` with `alert_team_handle` (map the routing key to the corresponding Slack subteam ID)
3. Create a SOP runbook and link it via `runbook_url`
4. Get approval in `#forum-konflux-sre`

To demote an SLO alert to non-SLO: reverse the above — replace `alert_team_handle` with `alert_routing_key`, set `slo: ""`, and adjust severity as appropriate.

### Potential Outcomes

Alerts that auto-resolve within 10 minutes are considered flapping. SPREs review alert history biweekly and notify teams when a significant number of service's alerts are flapping. Based on their analysis, SPREs may ask teams to (among other things):
- Increase the `for` duration (trades off detection speed)
- Use `keep_firing_for` to prevent premature resolution
- Re-evaluate alert threshold values
- Widen the PromQL time range (e.g. `[1h]` → `[1d]` for SLO calculations)

See the [flapping alerts SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alerting/flapping-alerts.md) for details.

### PromQL Test File Format

Each alert must have correspoing tests. Tests use promtool's unit test format. In addition to happy path tests, try to document possible edge cases when using more complex queries. The `rule_files` attribute references filenames (not paths) from the corresponding `rhobs/` directory.
