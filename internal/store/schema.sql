-- Copyright 2026 Jimmy Ma
-- SPDX-License-Identifier: MIT

CREATE TABLE IF NOT EXISTS pod_metadata (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace      TEXT    NOT NULL,
    pod_name       TEXT    NOT NULL,
    container_name TEXT    NOT NULL,
    owner_kind     TEXT,
    owner_name     TEXT,
    cpu_request_m  INTEGER NOT NULL,
    cpu_limit_m    INTEGER NOT NULL,
    mem_request_b  INTEGER NOT NULL,
    mem_limit_b    INTEGER NOT NULL,
    first_seen_at  INTEGER NOT NULL,
    last_seen_at   INTEGER NOT NULL,
    UNIQUE (namespace, pod_name, container_name)
);

CREATE TABLE IF NOT EXISTS metrics_raw (
    pod_id      INTEGER NOT NULL REFERENCES pod_metadata(id),
    captured_at INTEGER NOT NULL,
    cpu_m       INTEGER NOT NULL,
    mem_b       INTEGER NOT NULL,
    PRIMARY KEY (pod_id, captured_at)
);

CREATE INDEX IF NOT EXISTS idx_raw_captured_at ON metrics_raw (captured_at);

CREATE TABLE IF NOT EXISTS metrics_agg (
    pod_id        INTEGER NOT NULL REFERENCES pod_metadata(id),
    resolution    TEXT    NOT NULL,
    bucket_start  INTEGER NOT NULL,
    sample_count  INTEGER NOT NULL,

    -- CPU (milliCPU)
    cpu_avg_m     INTEGER NOT NULL,
    cpu_max_m     INTEGER NOT NULL,
    cpu_stddev_m  REAL    NOT NULL,
    cpu_p50_m     INTEGER NOT NULL,
    cpu_p75_m     INTEGER NOT NULL,
    cpu_p90_m     INTEGER NOT NULL,
    cpu_p95_m     INTEGER NOT NULL,
    cpu_p99_m     INTEGER NOT NULL,

    -- Memory (bytes)
    mem_avg_b     INTEGER NOT NULL,
    mem_max_b     INTEGER NOT NULL,
    mem_stddev_b  REAL    NOT NULL,
    mem_p50_b     INTEGER NOT NULL,
    mem_p75_b     INTEGER NOT NULL,
    mem_p90_b     INTEGER NOT NULL,
    mem_p95_b     INTEGER NOT NULL,
    mem_p99_b     INTEGER NOT NULL,

    PRIMARY KEY (pod_id, resolution, bucket_start)
);

CREATE INDEX IF NOT EXISTS idx_agg_resolution_bucket ON metrics_agg (resolution, bucket_start DESC);

-- Additive migrations: ignored if column already exists (handled in store.Open).
ALTER TABLE metrics_raw ADD COLUMN restart_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pod_metadata ADD COLUMN last_termination_reason TEXT NOT NULL DEFAULT '';
