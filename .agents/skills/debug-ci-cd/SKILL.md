---
name: debug-ci-cd
description: "Use when a Konflux PR or push pipeline has failed and you need to find the root cause and recover. Queries GitHub for check status, KubeArchive for archived pipeline runs, task runs, and step logs."
compatibility: "Requires gh (authenticated to GitHub), oc (authenticated to Konflux cluster) with the kubectl-ka plugin (https://kubearchive.github.io/kubearchive/main/cli/installation.html)."
---

Diagnose and recover from Konflux CI pipeline failures for PR and push pipelines.

## When to use

- A pipeline check failed on a PR
- A push build failed after merge
- The user asks "why did CI fail"

## Prerequisites

- `gh` CLI authenticated to GitHub
- `oc` CLI authenticated to the Konflux cluster
- `kubectl-ka` plugin installed ([installation guide](https://kubearchive.github.io/kubearchive/main/cli/installation.html))

## Diagnostic workflow

### Step 1: Identify what failed

Check GitHub for the failure type:

```bash
# For a PR
gh pr checks <PR_NUMBER> -R redhat-appstudio/o11y

# For the latest push to main
gh api repos/redhat-appstudio/o11y/commits/main/check-runs \
  --jq '.check_runs[] | {name, status, conclusion}'
```

Match the check name to the pipeline type. The current names are derived from `.tekton/` pipeline definitions, e.g.:
- `o11y-on-pull-request` -- PR validation pipeline

### Step 2: Find the pipeline run and failing task

Pipeline runs are cleaned up from the cluster after completion. Use `oc ka` to query both in-cluster and archived runs. Filter by PR number or commit SHA to find the exact run:

```bash
# By PR number
oc ka get pipelinerun -n rhtap-o11y-tenant \
  -l "pipelinesascode.tekton.dev/pull-request=<PR_NUMBER>"

# By commit SHA (also catches on-push pipelines)
oc ka get pipelinerun -n rhtap-o11y-tenant \
  -l "pipelinesascode.tekton.dev/sha=<COMMIT_SHA>"
```

List task runs for the pipeline run and inspect the failing one:

```bash
oc ka get taskrun -n rhtap-o11y-tenant -l "tekton.dev/pipelineRun=<PIPELINERUN_NAME>"
```

Get the failing task run's YAML to find the failed step (look for a non-zero `terminated.exitCode` in `status.steps`):

```bash
oc ka get taskrun <TASKRUN_NAME> -n rhtap-o11y-tenant -o yaml
```

### Step 3: Get the step logs

```bash
oc ka logs taskrun/<TASKRUN_NAME> -n rhtap-o11y-tenant -c step-<STEP_NAME>
```

Read the logs to identify the root cause.

## Fallback: curl-based queries

If `kubectl-ka` is not installed, query the KubeArchive API directly:

```bash
KUBEARCHIVE_URL="https://kubearchive-api-server-product-kubearchive.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com"
TOKEN=$(oc whoami -t)

# List recent pipeline runs
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-o11y-tenant/pipelineruns?limit=30"

# Get a specific task run
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-o11y-tenant/taskruns/<TASKRUN_NAME>"

# Get step logs (pod name is <taskrun-name>-pod)
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/api/v1/namespaces/rhtap-o11y-tenant/pods/<TASKRUN_NAME>-pod/log?container=step-<STEP_NAME>"
```
