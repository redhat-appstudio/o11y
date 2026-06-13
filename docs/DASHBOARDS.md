## Grafana dashboards overview

Konflux uses Grafana dashboards hosted in AppSRE Grafana instances to visualize metrics stored in RHOBS (Red Hat Observability Service). Dashboards are defined as JSON embedded in Kubernetes ConfigMap YAML files in the `dashboards/` directory. This repository serves as versioned storage — dashboards are typically created or edited in the Grafana UI, exported as JSON, wrapped in a ConfigMap, and committed here. There is no code generation.

Refer to the app-interface [monitoring documentation](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/docs/app-sre/monitoring.md#visualization-with-grafana) for the full guide on developing AppSRE dashboards.

## Creating a new dashboard

1. Build the dashboard in staging Grafana using the UI
2. Export the JSON and wrap it in a ConfigMap — see [Dashboard JSON export](#dashboard-json-export) and [ConfigMap structure](#configmap-structure)
3. Ensure conventions are met — see [Conventions](#conventions)
4. Verify the dashboard renders correctly in staging — see [Verification](#verification)
5. Open a PR (see [Definition of done](#definition-of-done))
6. After merge, promote to production — see [Deployment Model](#deployment-model)

## Verification

Dashboard changes must be manually verified in Grafana. There are no automated checks for dashboards at this time.

1. Open staging Grafana
2. Go to **Dashboards → New → Import**
3. Extract the JSON from the `data` field of the dashboard ConfigMap (the value under the `.json` key)
4. Paste the JSON into the **Import via dashboard JSON model** text box
5. Click **Load**, then **Import**
6. Verify panels load data, queries return expected results, and variables behave correctly

If it works in staging, it's good.

**Important:** Delete the test dashboard from staging before merging your PR. The staging instance auto-deploys from `main` after merge — if the test copy still exists with the same UID, it causes conflicts.

### Definition of done

A dashboard PR is ready to merge when:

- [ ] Dashboard has been imported into staging Grafana and verified: panels load data, variables work, no "No data" on expected metrics
- [ ] PR description includes a screenshot or link to stage dashboard

## Architecture

Dashboard ConfigMaps are deployed to AppSRE Grafana via [app-interface](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/docs/app-sre/monitoring.md#visualization-with-grafana). A Grafana sidecar watches for ConfigMaps with the `grafana_dashboard: "true"` label and automatically loads them. The `grafana-folder` annotation determines which folder the dashboard appears in.

**Datasource:** All dashboards query RHOBS as their Prometheus datasource. Only metrics that have been [forwarded to RHOBS](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/tshoot-missing-metrics.md) via [infra-deployments](https://github.com/redhat-appstudio/infra-deployments) are available. A panel referencing a metric that isn't forwarded will silently show "No data".

**Grafana instances:**
- Staging: https://grafana.stage.devshift.net
- Production: https://grafana.app-sre.devshift.net/

## Deployment Model

Changes are deployed based on [references defined in app-interface](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-stonesoup-dashboards.yml).

- **Staging**: auto-deploys from `main` branch via app-interface. Changes are visible in staging Grafana shortly after merge.
- **Production**: requires manually updating the commit reference in the app-interface saas file. Always use the merge commit SHA from `main`. Check that the updated SHA does not roll back already deployed changes.

**Promotion workflow:**
1. Verify the dashboard works in staging Grafana after merge to `main`
2. Copy the merge commit SHA from `main`
3. Update the [production reference](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-stonesoup-dashboards.yml) in app-interface
4. Verify in production Grafana after deployment

## Conventions

### ConfigMap structure

Use the [dashboard template](templates/grafana-dashboard-template.configmap.yaml) as a starting point.

Team-specific subfolders under RHTAP are allowed. Known folder assignments:

| Grafana folder | Owner / scope |
|---|---|
| `/grafana-dashboard-definitions/RHTAP` | General Konflux / O11Y dashboards |
| `/grafana-dashboard-definitions/RHTAP/Release-Service` | Release Service team dashboards |

If you are adding a dashboard for a new team, create a new subfolder under `RHTAP` named after the team. Use the same name consistently across all dashboards for that team.

### Datasource template variables

Dashboards should use a datasource template variable rather than hardcoding Grafana-internal datasource UIDs. Hardcoded UIDs (e.g. `P22466E8E7855F1E0`) are tied to a specific Grafana instance — if the datasource is recreated, or if the dashboard is imported into a different instance, panels break with "Datasource not found". The [dashboard template](templates/grafana-dashboard-template.configmap.yaml) includes the standard datasource and cluster variable definitions.

### Dashboard JSON export

1. Open the dashboard in Grafana
2. Click the **Export** button (top toolbar on right) → **Export as JSON**
3. Click **Copy to clipboard** or **Download file**
4. In the JSON, set `"id": null` — Grafana assigns instance-specific numeric IDs on import; baked-in IDs create noisy diffs
5. Verify `uid` is present and ≤ 40 characters
6. Paste the JSON as the value under the `.json` key in the ConfigMap, using the `|-` YAML block scalar indicator

Note: the `version` field increments on each UI save — it does not need to be reset.

### Metrics availability

Any PromQL query in a dashboard panel must reference metrics that are forwarded to RHOBS. If a metric is missing, the panel silently shows "No data" with no error message. See the [troubleshooting SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/tshoot-missing-metrics.md) for diagnosing missing metrics and labels.
