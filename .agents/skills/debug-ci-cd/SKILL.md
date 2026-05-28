---
name: debug-ci-cd
description: "Use when a Konflux PR or push pipeline has failed and you need to find the root cause and recover. Queries GitHub for check status, KubeArchive for archived pipeline runs, task runs, and step logs."
compatibility: "Requires gh (authenticated to GitHub), oc (authenticated to Konflux cluster), python3, and network access to the in-cluster KubeArchive API."
---

Diagnose and recover from Konflux CI pipeline failures for PR and push pipelines.

## When to use

- A pipeline check failed on a PR
- A push build failed after merge
- The user asks "why did CI fail"

## Prerequisites

- `gh` CLI authenticated to GitHub
- `oc` CLI authenticated to the Konflux cluster (for KubeArchive queries)

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

### Step 2: Find the pipeline run in KubeArchive

Pipeline runs are cleaned up from the cluster after completion. Use KubeArchive to query archived runs.

```bash
KUBEARCHIVE_URL="https://kubearchive-api-server-product-kubearchive.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com"
TOKEN=$(oc whoami -t)

# List recent pipeline runs (adjust limit as needed)
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-o11y-tenant/pipelineruns?limit=30" \
  | python3 -c "
import sys,json
d=json.load(sys.stdin)
for i in d.get('items',[]):
    name = i['metadata']['name']
    reason = i['status']['conditions'][0].get('reason','')
    created = i['metadata']['creationTimestamp']
    print(f'{created}  {reason:15s}  {name}')
"
```

### Step 3: Find the failing task

```bash
# Get the pipeline run and list its tasks
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-o11y-tenant/pipelineruns/<PIPELINERUN_NAME>" \
  | python3 -c "
import sys,json
d=json.load(sys.stdin)
for child in d.get('status',{}).get('childReferences',[]):
    print(child.get('pipelineTaskName',''), child.get('name',''))
"
```

Then check each task run's status:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/apis/tekton.dev/v1/namespaces/rhtap-o11y-tenant/taskruns/<TASKRUN_NAME>" \
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

### Step 4: Get the step logs

KubeArchive stores full container logs. The pod name is `<taskrun-name>-pod`:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$KUBEARCHIVE_URL/api/v1/namespaces/rhtap-o11y-tenant/pods/<TASKRUN_NAME>-pod/log?container=step-<STEP_NAME>"
```

Read the logs to identify the root cause.
