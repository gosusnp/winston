// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type dbExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db dbExecutor
}

func Open(path string) (*Store, error) {
	// DSN for modernc.org/sqlite: file:path?_journal_mode=WAL&_synchronous=NORMAL
	// If path is :memory:, it won't persist, which is good for tests.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	if path == ":memory:" {
		dsn = "file::memory:?cache=shared"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// For :memory: we might need to set pragmas explicitly if dsn doesn't work as expected
	if path == ":memory:" {
		if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
			return nil, fmt.Errorf("setting journal_mode: %w", err)
		}
		if _, err := db.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
			return nil, fmt.Errorf("setting synchronous: %w", err)
		}
	}

	// Run migrations
	for _, query := range strings.Split(schemaSQL, ";") {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		// Strip comment-only blocks (license headers, etc.)
		var lines []string
		for _, line := range strings.Split(query, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				lines = append(lines, line)
			}
		}
		query = strings.TrimSpace(strings.Join(lines, "\n"))
		if query == "" {
			continue
		}
		if _, err := db.Exec(query); err != nil {
			return nil, fmt.Errorf("running schema migration: %w", err)
		}
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if closer, ok := s.db.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// WithTx returns a new Store instance that uses the provided transaction.
func (s *Store) WithTx(tx *sql.Tx) *Store {
	return &Store{db: tx}
}

// BeginTx starts a new transaction and returns it.
func (s *Store) BeginTx(ctx context.Context) (*sql.Tx, error) {
	if db, ok := s.db.(*sql.DB); ok {
		return db.BeginTx(ctx, nil)
	}
	return nil, fmt.Errorf("store is not backed by *sql.DB")
}

// transact runs a function in a transaction. If s already has a transaction,
// it just runs fn(s). Otherwise it begins a new transaction and commits it.
func (s *Store) transact(ctx context.Context, fn func(*Store) error) error {
	if _, ok := s.db.(*sql.Tx); ok {
		return fn(s)
	}
	tx, err := s.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(s.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertPodMetadata(ctx context.Context, meta PodMeta) (int64, error) {
	query := `
		INSERT INTO pod_metadata (
			namespace, pod_name, container_name, owner_kind, owner_name,
			cpu_request_m, cpu_limit_m, mem_request_b, mem_limit_b,
			first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(namespace, pod_name, container_name) DO UPDATE SET
			owner_kind = excluded.owner_kind,
			owner_name = excluded.owner_name,
			cpu_request_m = excluded.cpu_request_m,
			cpu_limit_m = excluded.cpu_limit_m,
			mem_request_b = excluded.mem_request_b,
			mem_limit_b = excluded.mem_limit_b,
			last_seen_at = MAX(last_seen_at, excluded.last_seen_at)
		RETURNING id;
	`
	var id int64
	err := s.db.QueryRowContext(ctx, query,
		meta.Namespace, meta.PodName, meta.ContainerName, meta.OwnerKind, meta.OwnerName,
		meta.CPURequestM, meta.CPULimitM, meta.MemRequestB, meta.MemLimitB,
		meta.FirstSeenAt, meta.LastSeenAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upserting pod metadata: %w", err)
	}
	return id, nil
}

func (s *Store) InsertRawMetric(ctx context.Context, podID int64, capturedAt int64, cpuM, memB int64) error {
	query := `
		INSERT INTO metrics_raw (pod_id, captured_at, cpu_m, mem_b)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(pod_id, captured_at) DO UPDATE SET
			cpu_m = excluded.cpu_m,
			mem_b = excluded.mem_b;
	`
	_, err := s.db.ExecContext(ctx, query, podID, capturedAt, cpuM, memB)
	if err != nil {
		return fmt.Errorf("inserting raw metric: %w", err)
	}
	return nil
}

func (s *Store) LatestRawPerContainer(ctx context.Context) ([]LatestRawRow, error) {
	query := `
		WITH Latest AS (
			SELECT pod_id, MAX(captured_at) as max_captured_at
			FROM metrics_raw
			GROUP BY pod_id
		)
		SELECT 
			m.namespace, m.pod_name, m.container_name, m.owner_kind, m.owner_name,
			m.cpu_request_m, m.cpu_limit_m, m.mem_request_b, m.mem_limit_b,
			m.first_seen_at, m.last_seen_at,
			r.pod_id, r.captured_at, r.cpu_m, r.mem_b
		FROM Latest l
		JOIN metrics_raw r ON l.pod_id = r.pod_id AND l.max_captured_at = r.captured_at
		JOIN pod_metadata m ON r.pod_id = m.id;
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying latest raw metrics: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []LatestRawRow
	for rows.Next() {
		var r LatestRawRow
		err := rows.Scan(
			&r.Namespace, &r.PodName, &r.ContainerName, &r.OwnerKind, &r.OwnerName,
			&r.CPURequestM, &r.CPULimitM, &r.MemRequestB, &r.MemLimitB,
			&r.FirstSeenAt, &r.LastSeenAt,
			&r.PodID, &r.CapturedAt, &r.CPUM, &r.MemB,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning latest raw row: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error in latest raw metrics: %w", err)
	}
	return results, nil
}

func (s *Store) RawRowsOlderThan(ctx context.Context, before int64) ([]RawRow, error) {
	query := `
		SELECT pod_id, captured_at, cpu_m, mem_b
		FROM metrics_raw
		WHERE captured_at < ?
		ORDER BY pod_id, captured_at ASC;
	`
	rows, err := s.db.QueryContext(ctx, query, before)
	if err != nil {
		return nil, fmt.Errorf("querying raw rows older than: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []RawRow
	for rows.Next() {
		var r RawRow
		if err := rows.Scan(&r.PodID, &r.CapturedAt, &r.CPUM, &r.MemB); err != nil {
			return nil, fmt.Errorf("scanning raw row: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error in raw rows older than: %w", err)
	}
	return results, nil
}

func (s *Store) AggRowsOlderThan(ctx context.Context, resolution string, before int64) ([]AggRow, error) {
	query := `
		SELECT 
			a.pod_id, a.resolution, a.bucket_start, a.sample_count,
			a.cpu_avg_m, a.cpu_max_m, a.cpu_stddev_m, a.cpu_p50_m, a.cpu_p75_m, a.cpu_p90_m, a.cpu_p95_m, a.cpu_p99_m,
			a.mem_avg_b, a.mem_max_b, a.mem_stddev_b, a.mem_p50_b, a.mem_p75_b, a.mem_p90_b, a.mem_p95_b, a.mem_p99_b,
			m.namespace, m.owner_kind, m.owner_name, m.container_name,
			m.cpu_request_m, m.cpu_limit_m, m.mem_request_b, m.mem_limit_b
		FROM metrics_agg a
		JOIN pod_metadata m ON a.pod_id = m.id
		WHERE a.resolution = ? AND a.bucket_start < ?
		ORDER BY a.pod_id, a.bucket_start ASC;
	`
	rows, err := s.db.QueryContext(ctx, query, resolution, before)
	if err != nil {
		return nil, fmt.Errorf("querying agg rows older than: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []AggRow
	for rows.Next() {
		var r AggRow
		err := rows.Scan(
			&r.PodID, &r.Resolution, &r.BucketStart, &r.SampleCount,
			&r.CPUAvgM, &r.CPUMaxM, &r.CPUSTDDevM, &r.CPUP50M, &r.CPUP75M, &r.CPUP90M, &r.CPUP95M, &r.CPUP99M,
			&r.MemAvgB, &r.MemMaxB, &r.MemSTDDevB, &r.MemP50B, &r.MemP75B, &r.MemP90B, &r.MemP95B, &r.MemP99B,
			&r.Namespace, &r.OwnerKind, &r.OwnerName, &r.ContainerName,
			&r.CPURequestM, &r.CPULimitM, &r.MemRequestB, &r.MemLimitB,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning agg row: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error in agg rows older than: %w", err)
	}
	return results, nil
}

func (s *Store) UpsertAggBucket(ctx context.Context, b AggBucket) error {
	query := `
		INSERT INTO metrics_agg (
			pod_id, resolution, bucket_start, sample_count,
			cpu_avg_m, cpu_max_m, cpu_stddev_m, cpu_p50_m, cpu_p75_m, cpu_p90_m, cpu_p95_m, cpu_p99_m,
			mem_avg_b, mem_max_b, mem_stddev_b, mem_p50_b, mem_p75_b, mem_p90_b, mem_p95_b, mem_p99_b
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pod_id, resolution, bucket_start) DO UPDATE SET
			sample_count = excluded.sample_count,
			cpu_avg_m = excluded.cpu_avg_m,
			cpu_max_m = excluded.cpu_max_m,
			cpu_stddev_m = excluded.cpu_stddev_m,
			cpu_p50_m = excluded.cpu_p50_m,
			cpu_p75_m = excluded.cpu_p75_m,
			cpu_p90_m = excluded.cpu_p90_m,
			cpu_p95_m = excluded.cpu_p95_m,
			cpu_p99_m = excluded.cpu_p99_m,
			mem_avg_b = excluded.mem_avg_b,
			mem_max_b = excluded.mem_max_b,
			mem_stddev_b = excluded.mem_stddev_b,
			mem_p50_b = excluded.mem_p50_b,
			mem_p75_b = excluded.mem_p75_b,
			mem_p90_b = excluded.mem_p90_b,
			mem_p95_b = excluded.mem_p95_b,
			mem_p99_b = excluded.mem_p99_b;
	`
	_, err := s.db.ExecContext(ctx, query,
		b.PodID, b.Resolution, b.BucketStart, b.SampleCount,
		b.CPUAvgM, b.CPUMaxM, b.CPUSTDDevM, b.CPUP50M, b.CPUP75M, b.CPUP90M, b.CPUP95M, b.CPUP99M,
		b.MemAvgB, b.MemMaxB, b.MemSTDDevB, b.MemP50B, b.MemP75B, b.MemP90B, b.MemP95B, b.MemP99B,
	)
	if err != nil {
		return fmt.Errorf("upserting agg bucket: %w", err)
	}
	return nil
}

func (s *Store) DeleteRawRowsBefore(ctx context.Context, before int64) error {
	query := "DELETE FROM metrics_raw WHERE captured_at < ?;"
	_, err := s.db.ExecContext(ctx, query, before)
	return err
}

func (s *Store) DeleteAggRowsBefore(ctx context.Context, resolution string, before int64) error {
	query := "DELETE FROM metrics_agg WHERE resolution = ? AND bucket_start < ?;"
	_, err := s.db.ExecContext(ctx, query, resolution, before)
	return err
}

func (s *Store) UpdateLastSeenAt(ctx context.Context, podID int64, lastSeenAt int64) error {
	query := "UPDATE pod_metadata SET last_seen_at = MAX(last_seen_at, ?) WHERE id = ?;"
	_, err := s.db.ExecContext(ctx, query, lastSeenAt, podID)
	return err
}

func (s *Store) AggRowsForWindow(ctx context.Context, resolution string, since int64) ([]AggRow, error) {
	query := `
		SELECT 
			a.pod_id, a.resolution, a.bucket_start, a.sample_count,
			a.cpu_avg_m, a.cpu_max_m, a.cpu_stddev_m, a.cpu_p50_m, a.cpu_p75_m, a.cpu_p90_m, a.cpu_p95_m, a.cpu_p99_m,
			a.mem_avg_b, a.mem_max_b, a.mem_stddev_b, a.mem_p50_b, a.mem_p75_b, a.mem_p90_b, a.mem_p95_b, a.mem_p99_b,
			m.namespace, m.owner_kind, m.owner_name, m.container_name,
			m.cpu_request_m, m.cpu_limit_m, m.mem_request_b, m.mem_limit_b
		FROM metrics_agg a
		JOIN pod_metadata m ON a.pod_id = m.id
		WHERE a.resolution = ? AND a.bucket_start >= ?
		ORDER BY a.pod_id, a.bucket_start ASC;
	`
	rows, err := s.db.QueryContext(ctx, query, resolution, since)
	if err != nil {
		return nil, fmt.Errorf("querying agg rows for window: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []AggRow
	for rows.Next() {
		var r AggRow
		err := rows.Scan(
			&r.PodID, &r.Resolution, &r.BucketStart, &r.SampleCount,
			&r.CPUAvgM, &r.CPUMaxM, &r.CPUSTDDevM, &r.CPUP50M, &r.CPUP75M, &r.CPUP90M, &r.CPUP95M, &r.CPUP99M,
			&r.MemAvgB, &r.MemMaxB, &r.MemSTDDevB, &r.MemP50B, &r.MemP75B, &r.MemP90B, &r.MemP95B, &r.MemP99B,
			&r.Namespace, &r.OwnerKind, &r.OwnerName, &r.ContainerName,
			&r.CPURequestM, &r.CPULimitM, &r.MemRequestB, &r.MemLimitB,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning agg row: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error in agg rows for window: %w", err)
	}
	return results, nil
}

func (s *Store) AggStatsForWindow(ctx context.Context, resolution string, since int64) ([]AggStats, error) {
	query := `
		SELECT
			m.namespace, COALESCE(NULLIF(m.owner_kind, ''), ''), COALESCE(NULLIF(m.owner_name, ''), m.pod_name), m.container_name,
			m.cpu_request_m, m.cpu_limit_m, m.mem_request_b, m.mem_limit_b,
			CAST(SUM(a.sample_count) AS INTEGER) as sample_count,
			CAST(ROUND(AVG(a.cpu_avg_m)) AS INTEGER),
			CAST(MAX(a.cpu_max_m) AS INTEGER),
			AVG(a.cpu_stddev_m),
			CAST(ROUND(AVG(a.cpu_p50_m)) AS INTEGER),
			CAST(ROUND(AVG(a.cpu_p75_m)) AS INTEGER),
			CAST(ROUND(AVG(a.cpu_p90_m)) AS INTEGER),
			CAST(ROUND(AVG(a.cpu_p95_m)) AS INTEGER),
			CAST(ROUND(AVG(a.cpu_p99_m)) AS INTEGER),
			CAST(ROUND(AVG(a.mem_avg_b)) AS INTEGER),
			CAST(MAX(a.mem_max_b) AS INTEGER),
			AVG(a.mem_stddev_b),
			CAST(ROUND(AVG(a.mem_p50_b)) AS INTEGER),
			CAST(ROUND(AVG(a.mem_p75_b)) AS INTEGER),
			CAST(ROUND(AVG(a.mem_p90_b)) AS INTEGER),
			CAST(ROUND(AVG(a.mem_p95_b)) AS INTEGER),
			CAST(ROUND(AVG(a.mem_p99_b)) AS INTEGER)
		FROM metrics_agg a
		JOIN pod_metadata m ON a.pod_id = m.id
		WHERE a.resolution = ? AND a.bucket_start >= ?
		GROUP BY m.namespace, COALESCE(NULLIF(m.owner_kind, ''), ''), COALESCE(NULLIF(m.owner_name, ''), m.pod_name), m.container_name, m.cpu_request_m, m.cpu_limit_m, m.mem_request_b, m.mem_limit_b
		ORDER BY m.namespace, COALESCE(NULLIF(m.owner_name, ''), m.pod_name), m.container_name;
	`
	rows, err := s.db.QueryContext(ctx, query, resolution, since)
	if err != nil {
		return nil, fmt.Errorf("querying agg stats for window: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []AggStats
	for rows.Next() {
		var s AggStats
		err := rows.Scan(
			&s.Namespace, &s.OwnerKind, &s.OwnerName, &s.ContainerName,
			&s.CPURequestM, &s.CPULimitM, &s.MemRequestB, &s.MemLimitB,
			&s.SampleCount,
			&s.CPUAvgM, &s.CPUMaxM, &s.CPUSTDDevM,
			&s.CPUP50M, &s.CPUP75M, &s.CPUP90M, &s.CPUP95M, &s.CPUP99M,
			&s.MemAvgB, &s.MemMaxB, &s.MemSTDDevB,
			&s.MemP50B, &s.MemP75B, &s.MemP90B, &s.MemP95B, &s.MemP99B,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning agg stats row: %w", err)
		}
		results = append(results, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error in agg stats for window: %w", err)
	}
	return results, nil
}

// PodsWithMissingConfig returns all pods that have no CPU/memory limit or request set,
// along with the number of raw samples collected so far. Only pods with a raw metric
// at or after `since` are returned — this filters out stale rows for terminated pods
// (e.g. old pod instances replaced by a redeploy with a new pod name).
func (s *Store) PodsWithMissingConfig(ctx context.Context, since int64) ([]UnboundPod, error) {
	query := `
		SELECT m.namespace, COALESCE(NULLIF(m.owner_kind, ''), ''), COALESCE(NULLIF(m.owner_name, ''), m.pod_name), m.container_name,
		       m.cpu_request_m, m.cpu_limit_m, m.mem_request_b, m.mem_limit_b,
		       COUNT(r.pod_id) as raw_samples
		FROM pod_metadata m
		JOIN metrics_raw r ON r.pod_id = m.id AND r.captured_at >= ?
		WHERE m.cpu_limit_m = 0 OR m.mem_limit_b = 0 OR m.cpu_request_m = 0 OR m.mem_request_b = 0
		GROUP BY m.namespace, COALESCE(NULLIF(m.owner_kind, ''), ''), COALESCE(NULLIF(m.owner_name, ''), m.pod_name), m.container_name,
		         m.cpu_request_m, m.cpu_limit_m, m.mem_request_b, m.mem_limit_b
		ORDER BY m.namespace, COALESCE(NULLIF(m.owner_name, ''), m.pod_name), m.container_name;
	`
	rows, err := s.db.QueryContext(ctx, query, since)
	if err != nil {
		return nil, fmt.Errorf("querying pods with missing config: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []UnboundPod
	for rows.Next() {
		var p UnboundPod
		err := rows.Scan(
			&p.Namespace, &p.OwnerKind, &p.OwnerName, &p.ContainerName,
			&p.CPURequestM, &p.CPULimitM, &p.MemRequestB, &p.MemLimitB,
			&p.RawSamples,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning unbound pod row: %w", err)
		}
		results = append(results, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error in pods with missing config: %w", err)
	}
	return results, nil
}
