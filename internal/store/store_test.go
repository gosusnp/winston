// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"testing"
)

func TestOpen(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Check if tables were created
	rows, err := s.db.QueryContext(context.Background(), "SELECT name FROM sqlite_master WHERE type='table';")
	if err != nil {
		t.Fatalf("failed to query sqlite_master: %v", err)
	}
	defer func() { _ = rows.Close() }()

	tables := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("failed to scan table name: %v", err)
		}
		tables[name] = true
	}

	expected := []string{"pod_metadata", "metrics_raw", "metrics_agg"}
	for _, table := range expected {
		if !tables[table] {
			t.Errorf("expected table %s not found", table)
		}
	}
}

func TestUpsertPodMetadata(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	meta := PodMeta{
		Namespace:     "default",
		PodName:       "nginx-abc",
		ContainerName: "nginx",
		OwnerKind:     "Deployment",
		OwnerName:     "nginx",
		CPURequestM:   100,
		CPULimitM:     200,
		MemRequestB:   1024,
		MemLimitB:     2048,
		FirstSeenAt:   1000,
		LastSeenAt:    1000,
	}

	id, err := s.UpsertPodMetadata(context.Background(), meta)
	if err != nil {
		t.Fatalf("failed to upsert pod metadata: %v", err)
	}

	if id == 0 {
		t.Error("expected non-zero id")
	}

	// Read back and verify
	var m PodMeta
	err = s.db.QueryRowContext(context.Background(), `
		SELECT namespace, pod_name, container_name, owner_kind, owner_name,
		       cpu_request_m, cpu_limit_m, mem_request_b, mem_limit_b,
		       first_seen_at, last_seen_at
		FROM pod_metadata WHERE id = ?`, id).Scan(
		&m.Namespace, &m.PodName, &m.ContainerName, &m.OwnerKind, &m.OwnerName,
		&m.CPURequestM, &m.CPULimitM, &m.MemRequestB, &m.MemLimitB,
		&m.FirstSeenAt, &m.LastSeenAt,
	)
	if err != nil {
		t.Fatalf("failed to read back pod metadata: %v", err)
	}

	if m != meta {
		t.Errorf("read back meta mismatch: %+v != %+v", m, meta)
	}
}

func TestUpsertPodMetadata_Idempotent(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	meta := PodMeta{
		Namespace:     "default",
		PodName:       "nginx-abc",
		ContainerName: "nginx",
		CPURequestM:   100,
		CPULimitM:     200,
		FirstSeenAt:   1000,
		LastSeenAt:    1000,
	}

	id1, err := s.UpsertPodMetadata(context.Background(), meta)
	if err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}

	// Update limits
	meta.CPULimitM = 300
	meta.LastSeenAt = 2000
	id2, err := s.UpsertPodMetadata(context.Background(), meta)
	if err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}

	if id1 != id2 {
		t.Errorf("expected same id, got %d and %d", id1, id2)
	}

	var cpuLimitM int64
	var lastSeenAt int64
	var firstSeenAt int64
	err = s.db.QueryRowContext(context.Background(), "SELECT cpu_limit_m, last_seen_at, first_seen_at FROM pod_metadata WHERE id = ?", id1).
		Scan(&cpuLimitM, &lastSeenAt, &firstSeenAt)
	if err != nil {
		t.Fatalf("failed to read back: %v", err)
	}

	if cpuLimitM != 300 {
		t.Errorf("expected CPU limit 300, got %d", cpuLimitM)
	}
	if lastSeenAt != 2000 {
		t.Errorf("expected last seen 2000, got %d", lastSeenAt)
	}
	if firstSeenAt != 1000 {
		t.Errorf("expected first seen 1000, got %d", firstSeenAt)
	}
}

func TestInsertRawMetric(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	podID, err := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "n", PodName: "p", ContainerName: "c"})
	if err != nil {
		t.Fatalf("upsert pod metadata failed: %v", err)
	}

	err = s.InsertRawMetric(ctx, podID, 1000, 50, 500)
	if err != nil {
		t.Fatalf("failed to insert raw metric: %v", err)
	}

	latest, err := s.LatestRawPerContainer(ctx)
	if err != nil {
		t.Fatalf("failed to get latest: %v", err)
	}

	if len(latest) != 1 {
		t.Fatalf("expected 1 row, got %d", len(latest))
	}

	if latest[0].CPUM != 50 || latest[0].MemB != 500 {
		t.Errorf("latest values mismatch: %+v", latest[0])
	}
}

func TestLatestRawPerContainer_MultiPod(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	p1, err := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "default", PodName: "pod1", ContainerName: "c1"})
	if err != nil {
		t.Fatalf("upsert p1 failed: %v", err)
	}
	p2, err := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "default", PodName: "pod2", ContainerName: "c1"})
	if err != nil {
		t.Fatalf("upsert p2 failed: %v", err)
	}

	// pod1 metrics
	if err := s.InsertRawMetric(ctx, p1, 100, 10, 100); err != nil {
		t.Fatalf("insert p1 metric 1 failed: %v", err)
	}
	if err := s.InsertRawMetric(ctx, p1, 200, 20, 200); err != nil {
		t.Fatalf("insert p1 metric 2 failed: %v", err)
	}

	// pod2 metrics
	if err := s.InsertRawMetric(ctx, p2, 150, 15, 150); err != nil {
		t.Fatalf("insert p2 metric failed: %v", err)
	}

	latest, err := s.LatestRawPerContainer(ctx)
	if err != nil {
		t.Fatalf("failed to get latest: %v", err)
	}

	if len(latest) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(latest))
	}

	foundP1 := false
	foundP2 := false
	for _, r := range latest {
		if r.PodID == p1 {
			foundP1 = true
			if r.CapturedAt != 200 {
				t.Errorf("pod1: expected captured_at 200, got %d", r.CapturedAt)
			}
		}
		if r.PodID == p2 {
			foundP2 = true
			if r.CapturedAt != 150 {
				t.Errorf("pod2: expected captured_at 150, got %d", r.CapturedAt)
			}
		}
	}

	if !foundP1 || !foundP2 {
		t.Errorf("did not find both pods: p1=%v, p2=%v", foundP1, foundP2)
	}
}

func TestUpsertAggBucket(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	podID, err := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "n", PodName: "p", ContainerName: "c"})
	if err != nil {
		t.Fatalf("upsert pod metadata failed: %v", err)
	}

	bucket := AggBucket{
		PodID:       podID,
		Resolution:  "1h",
		BucketStart: 1000,
		SampleCount: 60,
		CPUAvgM:     50,
		CPUMaxM:     100,
	}

	err = s.UpsertAggBucket(ctx, bucket)
	if err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}

	// Update bucket
	bucket.SampleCount = 61
	bucket.CPUAvgM = 55
	err = s.UpsertAggBucket(ctx, bucket)
	if err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}

	// Verify update
	rows, err := s.AggRowsForWindow(ctx, "1h", 0)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if rows[0].SampleCount != 61 || rows[0].CPUAvgM != 55 {
		t.Errorf("row mismatch: %+v", rows[0])
	}
}
