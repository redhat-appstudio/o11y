## Grafana dashboards overview

Konflux uses Grafana dashboards hosted in AppSRE Grafana instances to visualize metrics stored in RHOBS (Red Hat Observability Service). Dashboards are defined as JSON embedded in Kubernetes ConfigMap YAML files in the `dashboards/` directory. This repository serves as versioned storage — dashboards are typically created or edited in the Grafana UI, exported as JSON, wrapped in a ConfigMap, and committed here. There is no code generation.

Refer to the app-interface [monitoring documentation](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/docs/app-sre/monitoring.md#visualization-with-grafana) for the full guide on developing AppSRE dashboards.

## Creating a new dashboard

1. Build the dashboard in [staging Grafana](https://grafana.stage.devshift.net) using the UI
2. Export the JSON and wrap it in a ConfigMap — see [Dashboard JSON export](#dashboard-json-export) and [ConfigMap structure](#configmap-structure)
3. Ensure conventions are met — see [Conventions](#conventions)
4. Verify the dashboard renders correctly in staging — see [Verification](#verification)
5. Open a PR (see [Definition of done](#definition-of-done))
6. After merge, promote to production — see [Deployment Model](#deployment-model)

## Verification

Dashboard changes must be manually verified in Grafana. There are no automated checks for dashboards at this time.

A dashboard is correct if it renders properly in [staging Grafana](https://grafana.stage.devshift.net). Upload the dashboard to the staging instance and verify it works — panels load data, queries return expected results, and variables behave correctly. If it works in staging, it's good.


### Upload via Grafana UI

1. Open [staging Grafana](https://grafana.stage.devshift.net)
2. Go to **Dashboards → New → Import**
3. Extract the JSON from the `data` field of the dashboard ConfigMap (the value under the `.json` key)
4. Paste the JSON into the **Import via dashboard JSON model** text box
5. Click **Load**, then **Import**
6. Verify panels load data, queries return expected results, and variables behave correctly

### Definition of done

A dashboard PR is ready to merge when all of the following are true:

- [ ] File is named `grafana-dashboard-{name}.configmap.yaml` and placed in `dashboards/`
- [ ] ConfigMap has `grafana_dashboard: "true"` label and a valid `grafana-folder` annotation
- [ ] Embedded JSON has `uid`, `title`, and `panels`; `id` is `null`
- [ ] UID is unique across all files in `dashboards/` and ≤ 40 characters
- [ ] Datasource template variable is defined; no hardcoded datasource UIDs in panels
- [ ] Dashboard has been imported into staging Grafana and verified: panels load data, variables work, no "No data" on expected metrics
- [ ] PR description includes a link to the staging dashboard

## Architecture

Dashboard ConfigMaps are deployed to AppSRE Grafana via [app-interface](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/docs/app-sre/monitoring.md#visualization-with-grafana). A Grafana sidecar watches for ConfigMaps with the `grafana_dashboard: "true"` label and automatically loads them. The `grafana-folder` annotation determines which folder the dashboard appears in.

**Datasource:** All dashboards query RHOBS as their Prometheus datasource. Only metrics that have been [forwarded to RHOBS](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/tshoot-missing-metrics.md) via [infra-deployments](https://github.com/redhat-appstudio/infra-deployments) are available. A panel referencing a metric that isn't forwarded will silently show "No data" — this is a common source of confusion when building new dashboards.

**Ownership:** AppSRE owns the Grafana infrastructure. O11Y team owns the dashboard definitions in this repository and the app-interface saas configuration that deploys them.

**Grafana instances:**
- Staging: https://grafana.stage.devshift.net
- Production: https://grafana.app-sre.devshift.net/

## Deployment Model

Changes are deployed based on [references defined in app-interface](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-stonesoup-dashboards.yml).

- **Staging**: auto-deploys from `main` branch via app-interface. Changes are visible in staging Grafana shortly after merge.
- **Production**: requires manually updating the commit reference in the app-interface saas file. Always use the merge commit SHA from `main` (not an individual branch commit) to ensure all changes are included. Check that the updated SHA does not roll back already deployed changes.

Dashboards use a separate saas file (`saas-stonesoup-dashboards.yml`) from alerting/recording rules (`saas-rhtap-rules.yaml`), so production promotions are independent.

**Promotion workflow:**
1. Verify the dashboard works in [staging Grafana](https://grafana.stage.devshift.net/dashboards) after merge to `main`
2. Copy the merge commit SHA from `main`
3. Update the [production reference](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-stonesoup-dashboards.yml) in app-interface
4. Verify in [production Grafana](https://grafana.app-sre.devshift.net/) after deployment

## Conventions

### ConfigMap structure

Use the [dashboard template](templates/grafana-dashboard-template.configmap.yaml) as a starting point:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-dashboard-<name>.configmap
  labels:
    grafana_dashboard: "true"
  annotations:
    grafana-folder: /grafana-dashboard-definitions/RHTAP
data:
  grafana-dashboard-<name>.json: |-
    { ... exported dashboard JSON ... }
```

**Must:**
- Set `kind: ConfigMap`
- Set label `grafana_dashboard: "true"` — without this the Grafana sidecar ignores the ConfigMap
- Set annotation `grafana-folder` starting with `/grafana-dashboard-definitions/RHTAP`
- Embed valid JSON containing `uid`, `title`, and `panels` fields
- Set `"id": null` in the embedded JSON

**Must not:**
- Omit the `uid` field or use a UID longer than 40 characters
- Hardcode Grafana-internal datasource UIDs in panels — use `"${Datasource}"` instead
- Duplicate a UID already used by another dashboard in `dashboards/`

Team-specific subfolders under RHTAP are allowed. Known folder assignments:

| Grafana folder | Owner / scope |
|---|---|
| `/grafana-dashboard-definitions/RHTAP` | General Konflux / O11Y dashboards |
| `/grafana-dashboard-definitions/RHTAP/Release-Service` | Release Service team dashboards |

If you are adding a dashboard for a new team, create a new subfolder under `RHTAP` named after the team. Use the same name consistently across all dashboards for that team.

### File naming

Files must follow the pattern: `grafana-dashboard-{name}.configmap.yaml`

The `{name}` portion should be a short, descriptive kebab-case identifier for the dashboard (e.g. `konflux-kubearchive-slo`, `alerts-activity`, `cluster-capacity`).

### Dashboard UID

The `uid` field in the dashboard JSON must be unique across all dashboards in the repository and must not exceed 40 characters. Grafana uses UIDs to identify dashboards — duplicate UIDs cause Grafana to alternate between dashboards unpredictably.

Grafana auto-generates UIDs when you create a dashboard in the UI. These are typically short random strings (e.g. `beqtfn88x35kwd`) which are fine to keep.

To list all UIDs currently in use:
```
grep -rh '"uid":' dashboards/
```
To check whether a specific UID is already taken:
```
grep -r '"uid": "your-uid-here"' dashboards/
```
If any result is returned, the UID is already in use — choose a different one.

**Common pitfall:** When testing a dashboard in staging before committing, the staging instance creates its own copy with the same UID. After merge, staging auto-deploys the committed version and the test copy conflicts. Either delete the test dashboard from staging before merging, or change the UID in the committed version.

### Datasource template variables

Dashboards should use a datasource template variable rather than hardcoding Grafana-internal datasource UIDs. Hardcoded UIDs (e.g. `P22466E8E7855F1E0`) are tied to a specific Grafana instance — if the datasource is recreated, or if the dashboard is imported into a different instance, panels break with "Datasource not found". A template variable resolves the correct datasource dynamically at runtime and lets users switch between datasources in the UI.

Dashboards should define a datasource template variable. The standard pattern in `templating.list`:
```json
{
  "name": "Datasource",
  "type": "datasource",
  "query": "prometheus",
  "regex": "/rhtap*/",
  "refresh": 1
}
```

Panels then reference `"uid": "${Datasource}"` instead of a hardcoded UID.

A common companion variable is a cluster selector, which depends on the datasource variable:
```json
{
  "name": "cluster",
  "type": "query",
  "datasource": { "type": "prometheus", "uid": "${Datasource}" },
  "definition": "label_values(source_cluster)",
  "includeAll": true,
  "multi": true,
  "refresh": 1
}
```

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
