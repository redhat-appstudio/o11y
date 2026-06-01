---
name: debug-release
description: "Use when a Konflux release has failed for the o11y exporter container image and you need to find the root cause and recover. Queries release objects, traces to release pipeline runs via KubeArchive."
compatibility: "Requires oc (authenticated to Konflux cluster with access to rhtap-o11y-tenant namespace) with the kubectl-ka plugin (https://kubearchive.github.io/kubearchive/main/cli/installation.html)."
---

Diagnose and recover from failed Konflux releases for the o11y exporter container image. This is an o11y-team-internal workflow -- releases only affect the exporter image deployed via infra-deployments.

## When to use

- A release shows `Failed` status
- The user asks "why did the release fail"
- The exporter image is not updating after a merge

## Prerequisites

- `oc` CLI authenticated to the Konflux cluster with access to `rhtap-o11y-tenant` namespace
- `kubectl-ka` plugin installed ([installation guide](https://kubearchive.github.io/kubearchive/main/cli/installation.html))

## Diagnostic workflow

### Step 1: Get the failure reason

```bash
oc get release <RELEASE_NAME> -n rhtap-o11y-tenant -o yaml
```

Check `status.conditions` for the failure reason. The `ManagedPipelineProcessed` condition contains the failing task and step name. 

Also get the release pipeline run reference (runs in `rhtap-releng-tenant`, not the o11y namespace) under `.status.managedProcessing.pipelineRun`.

### Step 2: Find the failing task run

List the task runs for the pipeline run and match the failing task name from Step 1. This is needed for log lookup in the next step:

```bash
oc ka get taskrun -n rhtap-releng-tenant -l "tekton.dev/pipelineRun=<PIPELINERUN_NAME>"
```

### Step 3: Get the step logs

```bash
oc ka logs taskrun/<TASKRUN_NAME> -n rhtap-releng-tenant -c step-<STEP_NAME>
```

## Fallback: curl-based queries

If `kubectl-ka` is not installed and installation is not possible, query the KubeArchive API directly:

```bash
KUBEARCHIVE_URL="https://kubearchive-api-server-product-kubearchive.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com"
TOKEN=$(oc whoami -t)

# Get a pipeline run
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-releng-tenant/pipelineruns/<PIPELINERUN_NAME>"

# Get a task run
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-releng-tenant/taskruns/<TASKRUN_NAME>"

# Get step logs (pod name is <taskrun-name>-pod)
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/api/v1/namespaces/rhtap-releng-tenant/pods/<TASKRUN_NAME>-pod/log?container=step-<STEP_NAME>"
```
