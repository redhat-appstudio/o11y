---
name: debug-rhobs-rules
description: "Use when Prometheus alerting or recording rules are not updating in RHOBS after merge, or when you need to verify what version of rules is deployed."
compatibility: "Requires obsctl (installed from main: go install github.com/observatorium/obsctl@main) and OIDC credentials for the Observatorium API."
---

Debug and verify Prometheus rule deployments in RHOBS (Red Hat Observability Service).

Currently deployed alert and recording rules can be viewed by any Red Hat employee in Grafana at https://grafana.stage.devshift.net/alerting/list. For deeper debugging (querying the raw API, testing rule validation), the workflow below requires OIDC credentials stored in https://vault.devshift.net/ and is limited to o11y team members.

## When to use

- Rules merged to main but alerts or recording rules not updating in RHOBS
- Need to verify which version of rules is deployed

## Prerequisites

- `obsctl` installed from main branch: `go install github.com/observatorium/obsctl@main` (must be main, not latest release, for the `--oidc.scopes` flag)
- OIDC client credentials for the rhtap tenant (client ID + secret), available in https://vault.devshift.net/

## Diagnostic workflow

### Step 1: Authenticate with obsctl

```bash
obsctl context api add --name='prod-api' --url='https://observatorium-mst.api.openshift.com'
obsctl login --api='prod-api' --oidc.audience='profile' \
  --oidc.client-id='<CLIENT_ID>' --oidc.client-secret='<SECRET>' \
  --oidc.issuer-url='https://sso.redhat.com/auth/realms/redhat-external' \
  --oidc.scopes profile --tenant='rhtap'
```

Must use `--oidc.scopes profile` -- Red Hat SSO does not support the default `openid offline_access` scopes.

For staging, use `--url='https://observatorium-mst.api.stage.openshift.com'`.

### Step 2: obsctl commands

#### Get raw rules (as configured)

```bash
obsctl metrics get rules.raw
```

Shows the rules as written to the API. The `html_url` annotation on each alert contains the commit SHA of the deployed version -- compare against the repo HEAD to check if updates are reaching RHOBS.

#### Get evaluated rules (with health status)

```bash
obsctl metrics get rules
```

Shows rules after evaluation by Thanos Ruler, including alert state (firing/pending/inactive) and last evaluation time.

#### Run PromQL queries

```bash
# Instant query
obsctl metrics query "up{job='some-service'}"

# Range query
obsctl metrics query --range --start='2026-05-28T00:00:00Z' --end='2026-05-29T00:00:00Z' --step='5m' "rate(http_requests_total[5m])"
```

#### Start Thanos Query UI

```bash
obsctl metrics ui
```

Opens a local proxy at http://localhost:8080 with the Thanos Query UI for interactive PromQL exploration against the tenant's RHOBS data.

#### Overwrite existing rules

--- DANGER ---
`obsctl metrics set` OVERWRITES ALL RULES for the entire tenant with the contents of the single file provided. This causes immediate loss of all alerts and recording rules until the obsctl-reloader re-syncs.
--- END DANGER ---

```bash
obsctl metrics set --rule.file=<path-to-rule-file>
```

## Deployment model

Rules in both staging and production RHOBS are controlled by the [saas-rhtap-rules](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml) saas file in app-interface. Changing the ref in this file is what triggers rule updates in RHOBS.

- **Staging**: refs `main` branch -- auto-deploys on merge to the o11y repo
- **Production**: refs a pinned commit SHA -- requires updating the ref in the saas file
- The **obsctl-reloader** syncs all rules in a single PUT per tenant -- one invalid rule blocks ALL updates (alerting and recording)
- obsctl-reloader injects `tenant_id` labels into rules during sync -- raw rule files from the repo will not have these

## Key references

- Observatorium prod API: `https://observatorium-mst.api.openshift.com`
- Observatorium staging API: `https://observatorium-mst.api.stage.openshift.com`
- RHOBS handbook: `https://rhobs-handbook.netlify.app/services/rhobs/rules-and-alerting.md/`
- obsctl repo: `https://github.com/observatorium/obsctl`
- Rule files: `rhobs/alerting/data_plane/`, `rhobs/recording/`
- Deployment config: [saas-rhtap-rules](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml) in app-interface
