# Winston

> I am Winston Wolfe, I solve problems.

Winston is a cluster-wide resource observer for k3s / Raspberry Pi clusters. It identifies pods that are wasting resources or living dangerously close to their limits — the **exuberant** ones.

A single binary. An embedded SQLite database. A small Helm chart. No external dependencies.

---

## What it does

Winston polls the Kubernetes metrics API every minute (matching the metrics server's own scrape interval), stores usage snapshots, and runs analysis against five profiles:

**Misconfiguration** — flagged immediately after the first collection tick:

| Profile | Signal | What it means |
|---|---|---|
| **No Limits** | No CPU or memory limit set | Pod can consume unbounded resources; noisy-neighbor risk |
| **No Requests** | No CPU or memory request set | Pod gets BestEffort QoS and is first evicted under pressure |

**Usage-based** — available ~1h after a pod is first seen:

| Profile | Signal | What it means |
|---|---|---|
| **Danger Zone** | p90 CPU/mem ≥ 90% of limit | Throttling or OOMKill is likely |
| **Over-Provisioned** | p95 CPU/mem < 20% of request | Squatting on resources other pods can't use |
| **Ghost Limit** | Absolute peak < 10% of limit | Limit is set so high it's meaningless |

A pod can match multiple profiles at once. Results are grouped by workload (Deployment, StatefulSet, etc.) so you see the full picture, not just individual pod noise.

---

## Interfaces

**Web UI** — open `http://<service>:8080` in a browser. Queries the JSON API and renders a live dashboard.

**JSON API** — for programmatic access or building your own tooling:
```
GET /stats       current usage snapshot, all namespaces
GET /exuberant   workloads matching any exuberance profile
GET /metrics     Prometheus metrics (exuberance profile presence gauges)
```

**Prometheus metrics** — scrape `/metrics` with Alloy, Prometheus, or any compatible agent:
```promql
count by (profile) (winston_exuberant_workloads)             # per profile
count by (namespace, profile) (winston_exuberant_workloads)  # cross-cut
```
Each exuberant workload emits a `winston_exuberant_workloads{profile, namespace, kind, name}` gauge with value `1`. Stale series disappear automatically when a workload is no longer flagged.

**Markdown report** — human-readable and agent-friendly:
```bash
# Via the API
curl http://<service>:8080/exuberant?format=markdown

# Or directly from the pod (no port-forward needed)
kubectl exec -n monitoring <winston-pod> -- /winston report
```

The Markdown output can be fed directly to an LLM to get proposed `requests` and `limits` adjustments.

---

## Deployment

```bash
helm install winston ./helm/winston \
  --namespace monitoring \
  --create-namespace \
  --set image.tag=latest
```

Winston needs a `ClusterRole` to read pods and pod metrics across all namespaces — the Helm chart handles RBAC.

Key values:

```yaml
image:
  repository: ghcr.io/gosusnp/winston
  tag: latest
  pullPolicy: IfNotPresent

collector:
  intervalSeconds: 60    # matches metrics.k8s.io scrape interval

retention:
  rawSeconds: 86400      # keep raw 1-min samples for this many seconds (default: 24h)
  oneHourSeconds: 604800 # keep 1h buckets for this many seconds (default: 7d)
  oneDaySeconds: 2592000 # keep 1d buckets for this many seconds (default: 30d)

analyzer:
  podTTLSeconds: 3600    # pods with no data within this TTL are excluded from all profiles; increase for infrequent cronjobs
  overProvisioned:
    minCPUMillis: 0      # skip over_provisioned if cpu_request < this; 0 = no minimum
    minMemBytes: 0       # skip over_provisioned if mem_request < this; 0 = no minimum
  ghostLimit:
    minCPUMillis: 0      # skip ghost_limit if cpu_limit < this; 0 = no minimum
    minMemBytes: 0       # skip ghost_limit if mem_limit < this; 0 = no minimum

storage:
  size: 1Gi              # PVC size; ~12MB used for 200 containers / 30 days
  storageClassName: ""   # leave empty to use the cluster default (e.g. local-path on k3s)

service:
  port: 8080

resources:
  requests:
    cpu: 10m
    memory: 32Mi
  limits:
    memory: 64Mi
```

---

## Building

```bash
# Local run (requires KUBECONFIG)
make run

# Cross-compile for arm64 (Raspberry Pi)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 make build

# Full check (fmt, lint, test)
make pre-commit
```

No CGo. Pure Go. Cross-compilation just works.

---

## Architecture

See [`docs/architecture.md`](docs/architecture.md) for the full design: storage schema, compaction strategy, exuberance query logic, and key design decisions.
