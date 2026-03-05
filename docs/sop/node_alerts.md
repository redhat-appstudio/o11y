# Node Alerts Runbook

This runbook covers node-level alerts for Konflux data plane clusters, including worker and infra nodes.

---

## WorkerNodeHighCPUAndMemory

### Summary

A worker or infra node in the cluster has had both CPU above 80% and memory above 60% for the last 2 hours. The alert fires per node (one alert per overloaded instance) and includes the node instance name. The 2-hour window reduces noise and flapping.

### Alert details

| Field | Value |
|-------|--------|
| **Alert name** | `WorkerNodeHighCPUAndMemory` |
| **Severity** | high |
| **Routing** | `alert_routing_key: perfandspreandinfra` |
| **Pending** | 2 hours (`for: 2h`) |
| **Scope** | Worker and infra nodes (`role=~"worker|infra"`) |

**Condition:** For each (instance, source_cluster) with role worker or infra:

- CPU usage (from `rate(node_cpu_seconds_total{mode="idle"}[2h])`) > 80%
- Memory usage (from `(node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes) / node_memory_MemTotal_bytes`) > 60%

Both must be true for 2 hours.

### Impact

- Workloads on the affected node may be slow or at risk of OOM.
- Sustained pressure can lead to node conditions (MemoryPressure, DiskPressure), evictions, or instability.

### Prerequisites

- Access to the cluster (or RHOBS/Grafana) and permission to view node metrics.
- Ability to list and describe nodes (e.g. `oc get nodes`, `oc describe node <name>`).

### Procedure

1. **Confirm the alert**
   - Note `source_cluster` and `instance` from the alert (Alertmanager or Grafana).
   - The alert fires per node; `instance` is the overloaded worker or infra node.

2. **Identify the affected node**
   - In the cluster: `oc get nodes -l node-role.kubernetes.io/worker` and/or infra label as appropriate.
   - Match `instance` to the node name (or node address).
   - Run `oc describe node <name>` and check **Conditions** (MemoryPressure, DiskPressure) and resource usage.

3. **Assess and mitigate**
   - **Short-term:** Consider cordoning the node if needed: `oc adm cordon <node>`, then drain per process: `oc adm drain <node> --ignore-daemonsets --delete-emptydir-data`.
   - **Workloads:** List pods on the node: `oc get pods -A --field-selector spec.nodeName=<node> -o wide`. Look for high CPU/memory consumers; scale or move workloads if appropriate.
   - **Node health:** Check kubelet, kernel, or hardware issues. Node restart is a last resort and should follow change control.

4. **Resolve and follow-up**
   - Confirm in metrics that CPU and memory on the node drop below threshold and the alert clears.
   - If recurring, plan capacity (add nodes, resize, rebalance) and document in post-incident or capacity review.

### Related alerts

| Alert | Description |
|-------|-------------|
| **WorkerNodeHighCPU** | More than 10 worker nodes with CPU >95% (cluster-level). |
| **WorkerNodeHighMemory** | More than 10 worker nodes with memory >90% (cluster-level). |
| **MasterNodeHighMemory** | Single master node memory >90% (SLO). |
| **InfraNodeHighCPU** / **InfraNodeHighMemory** | Per infra node CPU/memory. |

### References

- Alert rule: [rhobs/alerting/data_plane/prometheus.node_alerts.yaml](../../rhobs/alerting/data_plane/prometheus.node_alerts.yaml) (WorkerNodeHighCPUAndMemory).
- Node alerts runbook (Konflux docs): [infra/sre/node_alerts.md](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/infra/sre/node_alerts.md).
