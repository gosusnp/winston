# Winston — Agent Instructions

## Project

Winston is a single-binary Go application that observes pod resource usage across a k3s cluster and identifies wasteful or at-risk workloads. It stores time-series snapshots in an embedded SQLite database and exposes results via a JSON/Markdown HTTP API and a CLI report command.

Full design rationale is in @docs/architecture.md . Read it before making non-trivial changes.

## Key Constraints

- **Pure Go only — no CGo.** SQLite is via `modernc.org/sqlite`. `CGO_ENABLED=0` must remain valid. Do not introduce any CGo dependency.
- **Target: `linux/arm64`** (Raspberry Pi k3s cluster). Cross-compilation must work in a single `go build` step.
- **Single binary.** The HTTP server, CLI report command, collector, and compactor all live in one process. Do not split into separate binaries.
- **No over-engineering.** This runs on a Pi. Simple, direct code over abstractions.

## Project Layout

```
cmd/winston/        # main.go — entrypoint, wires everything
internal/collector/ # k8s polling loop, writes to store
internal/store/     # SQLite: open, migrate, all queries
internal/analyzer/  # exuberance profile SQL queries
internal/report/    # JSON + Markdown renderers (shared by API and CLI)
internal/api/       # net/http handlers
static/             # embedded UI (go:embed), single index.html
helm/winston/       # Helm chart for k8s deployment
```

## Code Style

- Standard Go idioms. No frameworks.
- HTTP: `net/http` only — no Gin, Echo, Chi, etc.
- Error handling: explicit, no panic in library code.
 Config via environment variables, read at startup in `main.go`.
- SQL lives in `internal/store` — no raw SQL elsewhere.
- `internal/report` is the only place that formats output. Handlers and CLI call it, they don't format themselves.

## Database

- SQLite file at `/data/winston.db`.
- Three tables: `pod_metadata`, `metrics_raw`, `metrics_agg`. Schema in `internal/store/schema.sql` (embedded via `go:embed`).
- WAL mode + `synchronous=NORMAL` — do not change these pragmas.
- Compaction tiers: raw (24h) → 1h agg (7d) → 1d agg (30d). Compaction runs on a ticker, not at collection time.

## Testing

- Unit test the analyzer query logic and report renderers.
- Store tests should use an in-memory SQLite DB (`:memory:`).
- Do not mock the store in analyzer or API tests — use a real in-memory DB.

## Common Commands

```bash
# Run locally (needs KUBECONFIG)
make run

# CLI report (against a running pod)
kubectl exec -n <ns> <pod> -- /winston report

# Build for arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 make build

# Lint and Test
make pre-commit
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `WINSTON_DB_PATH` | `/data/winston.db` | SQLite file path |
| `WINSTON_COLLECT_INTERVAL` | `60` | Collection interval in seconds (matches metrics server scrape interval) |
| `WINSTON_RETENTION_RAW_H` | `24` | Raw data retention in hours |
| `WINSTON_RETENTION_1H_DAYS` | `7` | 1h bucket retention in days |
| `WINSTON_RETENTION_1D_DAYS` | `30` | 1d bucket retention in days |
| `WINSTON_POD_TTL_S` | `3600` | Pods with no raw metric within this TTL (seconds) are excluded from all profiles; increase for infrequent cronjobs |
| `WINSTON_PORT` | `8080` | HTTP server port |
| `KUBECONFIG` | — | Path to kubeconfig (in-cluster SA used if unset) |
