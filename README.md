# Winston

> Things got messy. The Wolf is here.

Winston is a cluster-wide resource observer for k3s / Raspberry Pi clusters. It identifies pods that are wasting resources or living dangerously close to their limits — the **exuberant** ones.

A single binary. An embedded SQLite database. A small Helm chart. No external dependencies.

---

## What it does

Winston polls the Kubernetes metrics API every minute (matching the metrics server's own scrape interval), stores usage snapshots, and runs analysis against three profiles:

| Profile | Signal | What it means |
|---|---|---|
| **Over-Provisioned** | p95 CPU/mem < 20% of request | Squatting on resources other pods can't use |
| **Ghost Limit** | Absolute peak < 10% of limit | Limit is set so high it's meaningless |
| **Danger Zone** | p90 CPU/mem ≥ 90% of limit | Throttling or OOMKill is likely |

Results are grouped by workload (Deployment, StatefulSet, etc.) so you see the full picture, not just individual pod noise.

---

## Interfaces

**Web UI** — open `http://<service>:8080` in a browser. Queries the JSON API and renders a live dashboard.

**JSON API** — for programmatic access or building your own tooling:
```
GET /stats       current usage snapshot, all namespaces
GET /exuberant   workloads matching any exuberance profile
```

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
  rawHours: 24           # keep raw 1-min samples for this many hours
  oneHourDays: 7         # keep 1h buckets for this many days
  oneDayDays: 30         # keep 1d buckets for this many days

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
