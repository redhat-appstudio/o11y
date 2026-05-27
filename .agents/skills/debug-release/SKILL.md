---
name: debug-release
description: "Use when a Konflux release has failed for the o11y exporter container image and you need to find the root cause and recover. Queries release objects, traces to release pipeline runs via KubeArchive."
compatibility: "Requires oc (authenticated to Konflux cluster with access to rhtap-o11y-tenant namespace), python3, and network access to the in-cluster KubeArchive API."
---

Diagnose and recover from failed Konflux releases for the o11y exporter container image. This is an o11y-team-internal workflow -- releases only affect the exporter image deployed via infra-deployments.

## When to use

- A release shows `Failed` status
- The user asks "why did the release fail"
- The exporter image is not updating after a merge

## Prerequisites

- `oc` CLI authenticated to the Konflux cluster with access to `rhtap-o11y-tenant` namespace

## Diagnostic workflow

### Step 1: List releases

```bash
oc get releases -n rhtap-o11y-tenant --sort-by=.metadata.creationTimestamp
```

### Step 2: Get the failure reason

```bash
oc get release <RELEASE_NAME> -n rhtap-o11y-tenant -o yaml
```

Check `status.conditions` for the failure reason. The `ManagedPipelineProcessed` condition typically contains the failing task and step name.

### Step 3: Trace to the release pipeline run

The release pipeline runs in `rhtap-releng-tenant`, not the o11y namespace. Get the reference:

```bash
oc get release <RELEASE_NAME> -n rhtap-o11y-tenant \
  -o jsonpath='{.status.managedProcessing.pipelineRun}'
```

This returns a reference like `rhtap-releng-tenant/managed-xxxxx`.

### Step 4: Find the failing task via KubeArchive

```bash
KUBEARCHIVE_URL="https://kubearchive-api-server-product-kubearchive.{CLUSTER_SPECIFIC_URL}.com"
TOKEN=$(oc whoami -t)

# List tasks in the release pipeline run
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-releng-tenant/pipelineruns/<PIPELINERUN_NAME>" \
  | python3 -c "
import sys,json
d=json.load(sys.stdin)
for child in d.get('status',{}).get('childReferences',[]):
    print(child.get('pipelineTaskName',''), child.get('name',''))
"
```

Check each task run's status:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-releng-tenant/taskruns/<TASKRUN_NAME>" \
  | python3 -c "
import sys,json
d=json.load(sys.stdin)
c = d.get('status',{}).get('conditions',[{}])[0]
print(f\"Reason: {c.get('reason','')}\")
print(f\"Message: {c.get('message','')}\")
for s in d.get('status',{}).get('steps',[]):
    term = s.get('terminated',{})
    if term.get('exitCode',0) != 0:
        print(f\"Failing step: {s.get('name','')} exit={term.get('exitCode','')}\")"
```

### Step 5: Get the step logs

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/api/v1/namespaces/rhtap-releng-tenant/pods/<TASKRUN_NAME>-pod/log?container=step-<STEP_NAME>"
```
