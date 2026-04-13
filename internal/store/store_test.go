// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"testing"
	"time"
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

	err = s.InsertRawMetric(ctx, podID, 1000, 50, 500, 0)
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
	if err := s.InsertRawMetric(ctx, p1, 100, 10, 100, 0); err != nil {
		t.Fatalf("insert p1 metric 1 failed: %v", err)
	}
	if err := s.InsertRawMetric(ctx, p1, 200, 20, 200, 0); err != nil {
		t.Fatalf("insert p1 metric 2 failed: %v", err)
	}

	// pod2 metrics
	if err := s.InsertRawMetric(ctx, p2, 150, 15, 150, 0); err != nil {
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

func TestTransact_RollbackOnError(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	podID, err := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "default", PodName: "pod1", ContainerName: "c1"})
	if err != nil {
		t.Fatalf("upsert pod metadata failed: %v", err)
	}

	injected := errors.New("injected error")
	err = s.transact(ctx, func(txs *Store) error {
		if err := txs.UpsertAggBucket(ctx, AggBucket{
			PodID:       podID,
			Resolution:  "1h",
			BucketStart: 3600,
			SampleCount: 10,
		}); err != nil {
			return err
		}
		return injected
	})

	if !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}

	rows, err := s.AggRowsForWindow(ctx, "1h", 0)
	if err != nil {
		t.Fatalf("query agg rows failed: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 agg rows after rollback, got %d", len(rows))
	}
}

func TestPodsWithMissingConfig_StandalonePods(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	now := time.Now().Unix()

	// Two standalone pods (no owner) with missing limits — must not collapse into one row.
	idA, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "solo-a", ContainerName: "app",
		CPURequestM: 100, CPULimitM: 0, // missing limit
		FirstSeenAt: 1000, LastSeenAt: 1000,
	})
	if err != nil {
		t.Fatalf("upsert solo-a failed: %v", err)
	}
	idB, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "solo-b", ContainerName: "app",
		CPURequestM: 100, CPULimitM: 0, // missing limit
		FirstSeenAt: 1000, LastSeenAt: 1000,
	})
	if err != nil {
		t.Fatalf("upsert solo-b failed: %v", err)
	}

	// Insert recent raw metrics so both pods are considered active.
	if err := s.InsertRawMetric(ctx, idA, now, 10, 1024, 0); err != nil {
		t.Fatalf("insert raw metric for solo-a failed: %v", err)
	}
	if err := s.InsertRawMetric(ctx, idB, now, 10, 1024, 0); err != nil {
		t.Fatalf("insert raw metric for solo-b failed: %v", err)
	}

	pods, err := s.PodsWithMissingConfig(ctx, now-300)
	if err != nil {
		t.Fatalf("PodsWithMissingConfig failed: %v", err)
	}

	if len(pods) != 2 {
		t.Errorf("expected 2 pods, got %d: %+v", len(pods), pods)
	}

	names := make(map[string]bool)
	for _, p := range pods {
		names[p.OwnerName] = true
	}
	if !names["solo-a"] || !names["solo-b"] {
		t.Errorf("expected both solo-a and solo-b, got: %v", names)
	}
}

func TestPodsWithMissingConfig_SupersededByRollingDeploy(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	now := time.Now().Unix()

	// Old pod: Deployment/my-api, no limits, terminated (last metric 2 ticks ago).
	oldID, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "my-api-abc", ContainerName: "app",
		OwnerKind: "Deployment", OwnerName: "my-api",
		CPURequestM: 100, CPULimitM: 0,
		MemRequestB: 1024, MemLimitB: 0,
		FirstSeenAt: now - 200, LastSeenAt: now - 200,
	})
	if err != nil {
		t.Fatalf("upsert old pod failed: %v", err)
	}
	if err := s.InsertRawMetric(ctx, oldID, now-120, 10, 1024, 0); err != nil {
		t.Fatalf("insert raw metric for old pod failed: %v", err)
	}

	// New pod: same Deployment, limits now set, active (last metric just now).
	newID, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "my-api-def", ContainerName: "app",
		OwnerKind: "Deployment", OwnerName: "my-api",
		CPURequestM: 100, CPULimitM: 500,
		MemRequestB: 1024, MemLimitB: 2048,
		FirstSeenAt: now - 30, LastSeenAt: now - 30,
	})
	if err != nil {
		t.Fatalf("upsert new pod failed: %v", err)
	}
	if err := s.InsertRawMetric(ctx, newID, now, 10, 1024, 0); err != nil {
		t.Fatalf("insert raw metric for new pod failed: %v", err)
	}

	// Old pod is within TTL but superseded — should not appear.
	pods, err := s.PodsWithMissingConfig(ctx, now-300)
	if err != nil {
		t.Fatalf("PodsWithMissingConfig failed: %v", err)
	}
	if len(pods) != 0 {
		t.Errorf("expected 0 pods (old pod superseded by new pod with limits), got %d: %+v", len(pods), pods)
	}

	// Now add a second Deployment whose new pod is also missing limits — it should still appear.
	oldID2, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "other-abc", ContainerName: "app",
		OwnerKind: "Deployment", OwnerName: "other",
		CPURequestM: 100, CPULimitM: 0,
		MemRequestB: 1024, MemLimitB: 0,
		FirstSeenAt: now - 200, LastSeenAt: now - 200,
	})
	if err != nil {
		t.Fatalf("upsert other old pod failed: %v", err)
	}
	if err := s.InsertRawMetric(ctx, oldID2, now-120, 10, 1024, 0); err != nil {
		t.Fatalf("insert raw metric for other old pod failed: %v", err)
	}
	newID2, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "other-def", ContainerName: "app",
		OwnerKind: "Deployment", OwnerName: "other",
		CPURequestM: 100, CPULimitM: 0, // still no limits on new pod
		MemRequestB: 1024, MemLimitB: 0,
		FirstSeenAt: now - 30, LastSeenAt: now - 30,
	})
	if err != nil {
		t.Fatalf("upsert other new pod failed: %v", err)
	}
	if err := s.InsertRawMetric(ctx, newID2, now, 10, 1024, 0); err != nil {
		t.Fatalf("insert raw metric for other new pod failed: %v", err)
	}

	pods, err = s.PodsWithMissingConfig(ctx, now-300)
	if err != nil {
		t.Fatalf("PodsWithMissingConfig failed: %v", err)
	}
	// Only the new "other" pod should appear (old is superseded, new still has no limits).
	if len(pods) != 1 {
		t.Errorf("expected 1 pod (new 'other' pod still missing limits), got %d: %+v", len(pods), pods)
	}
	if len(pods) == 1 && pods[0].OwnerName != "other" {
		t.Errorf("expected OwnerName=other, got %q", pods[0].OwnerName)
	}
}

func TestAggStatsForWindow_StandalonePods(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	now := time.Now().Unix()

	// Two standalone pods (no owner) — must not collapse into one row.
	p1, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "solo-a", ContainerName: "app",
		CPURequestM: 100, CPULimitM: 200, MemRequestB: 1024, MemLimitB: 2048,
		FirstSeenAt: 1000, LastSeenAt: 1000,
	})
	if err != nil {
		t.Fatalf("upsert solo-a failed: %v", err)
	}
	p2, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "solo-b", ContainerName: "app",
		CPURequestM: 100, CPULimitM: 200, MemRequestB: 1024, MemLimitB: 2048,
		FirstSeenAt: 1000, LastSeenAt: 1000,
	})
	if err != nil {
		t.Fatalf("upsert solo-b failed: %v", err)
	}

	bucket := AggBucket{Resolution: "1h", BucketStart: 3600, SampleCount: 10, CPUAvgM: 50, CPUMaxM: 80}
	bucket.PodID = p1
	if err := s.UpsertAggBucket(ctx, bucket); err != nil {
		t.Fatalf("upsert agg solo-a failed: %v", err)
	}
	bucket.PodID = p2
	if err := s.UpsertAggBucket(ctx, bucket); err != nil {
		t.Fatalf("upsert agg solo-b failed: %v", err)
	}

	// Insert recent raw metrics so both pods are considered active.
	if err := s.InsertRawMetric(ctx, p1, now, 50, 1024, 0); err != nil {
		t.Fatalf("insert raw metric solo-a failed: %v", err)
	}
	if err := s.InsertRawMetric(ctx, p2, now, 50, 1024, 0); err != nil {
		t.Fatalf("insert raw metric solo-b failed: %v", err)
	}

	stats, err := s.AggStatsForWindow(ctx, "1h", 0, now-300)
	if err != nil {
		t.Fatalf("AggStatsForWindow failed: %v", err)
	}

	if len(stats) != 2 {
		t.Errorf("expected 2 stats rows, got %d: %+v", len(stats), stats)
	}

	names := make(map[string]bool)
	for _, st := range stats {
		names[st.OwnerName] = true
	}
	if !names["solo-a"] || !names["solo-b"] {
		t.Errorf("expected both solo-a and solo-b, got: %v", names)
	}
}

func TestAggStatsForWindow_ExcludesInactivePods(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	now := time.Now().Unix()

	// active pod — has recent raw data.
	active, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "active", ContainerName: "app",
		CPURequestM: 100, CPULimitM: 200, MemRequestB: 1024, MemLimitB: 2048,
		FirstSeenAt: 1000, LastSeenAt: 1000,
	})
	if err != nil {
		t.Fatalf("upsert active failed: %v", err)
	}
	// inactive pod — has agg data but no recent raw data.
	inactive, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace: "default", PodName: "inactive", ContainerName: "app",
		CPURequestM: 100, CPULimitM: 200, MemRequestB: 1024, MemLimitB: 2048,
		FirstSeenAt: 1000, LastSeenAt: 1000,
	})
	if err != nil {
		t.Fatalf("upsert inactive failed: %v", err)
	}

	bucket := AggBucket{Resolution: "1h", BucketStart: 3600, SampleCount: 10, CPUAvgM: 50, CPUMaxM: 80}
	bucket.PodID = active
	if err := s.UpsertAggBucket(ctx, bucket); err != nil {
		t.Fatalf("upsert agg active failed: %v", err)
	}
	bucket.PodID = inactive
	if err := s.UpsertAggBucket(ctx, bucket); err != nil {
		t.Fatalf("upsert agg inactive failed: %v", err)
	}

	// Only the active pod gets a recent raw metric.
	if err := s.InsertRawMetric(ctx, active, now, 50, 1024, 0); err != nil {
		t.Fatalf("insert raw metric active failed: %v", err)
	}
	// inactive pod has a raw metric older than the activeSince cutoff.
	if err := s.InsertRawMetric(ctx, inactive, now-3600, 50, 1024, 0); err != nil {
		t.Fatalf("insert raw metric inactive failed: %v", err)
	}

	stats, err := s.AggStatsForWindow(ctx, "1h", 0, now-300)
	if err != nil {
		t.Fatalf("AggStatsForWindow failed: %v", err)
	}

	if len(stats) != 1 {
		t.Errorf("expected 1 stat row, got %d: %+v", len(stats), stats)
	}
	if len(stats) == 1 && stats[0].OwnerName != "active" {
		t.Errorf("expected active pod, got %q", stats[0].OwnerName)
	}
}

func TestPodsWithHighRestarts(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Unix()

	newStore := func(t *testing.T) *Store {
		t.Helper()
		s, err := Open(":memory:")
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}

	t.Run("DeltaAboveThreshold_Included", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "ns", PodName: "p", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w"})
		_ = s.InsertRawMetric(ctx, id, now-120, 10, 1024, 0)
		_ = s.InsertRawMetric(ctx, id, now-60, 10, 1024, 3)
		_ = s.InsertRawMetric(ctx, id, now, 10, 1024, 7)

		results, err := s.PodsWithHighRestarts(ctx, now-300, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].RestartDelta != 7 {
			t.Errorf("expected RestartDelta=7, got %d", results[0].RestartDelta)
		}
	})

	t.Run("DeltaBelowThreshold_Excluded", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "ns", PodName: "p", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w"})
		_ = s.InsertRawMetric(ctx, id, now-60, 10, 1024, 0)
		_ = s.InsertRawMetric(ctx, id, now, 10, 1024, 2)

		results, err := s.PodsWithHighRestarts(ctx, now-300, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("OOMKilled_PropagatedCorrectly", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "ns", PodName: "p", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w",
			LastTerminationReason: "OOMKilled"})
		_ = s.InsertRawMetric(ctx, id, now-60, 10, 1024, 0)
		_ = s.InsertRawMetric(ctx, id, now, 10, 1024, 6)

		results, err := s.PodsWithHighRestarts(ctx, now-300, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if !results[0].OOMKilled {
			t.Errorf("expected OOMKilled=true")
		}
	})

	t.Run("MultiReplica_MixedTerminationReasons_SingleResult", func(t *testing.T) {
		// Regression: last_termination_reason in GROUP BY caused duplicate rows when replicas
		// had different termination reasons. One OOMKilled, one clean — must collapse into one result.
		s := newStore(t)
		oomID, _ := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "ns", PodName: "p-oom", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w",
			LastTerminationReason: "OOMKilled"})
		cleanID, _ := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "ns", PodName: "p-clean", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w",
			LastTerminationReason: ""})
		_ = s.InsertRawMetric(ctx, oomID, now-60, 10, 1024, 0)
		_ = s.InsertRawMetric(ctx, oomID, now, 10, 1024, 8)
		_ = s.InsertRawMetric(ctx, cleanID, now-60, 10, 1024, 0)
		_ = s.InsertRawMetric(ctx, cleanID, now, 10, 1024, 6)

		results, err := s.PodsWithHighRestarts(ctx, now-300, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result (workload-level grouping), got %d", len(results))
		}
		if results[0].RestartDelta != 8 {
			t.Errorf("expected RestartDelta=8 (max across replicas), got %d", results[0].RestartDelta)
		}
		if !results[0].OOMKilled {
			t.Errorf("expected OOMKilled=true (any replica OOMKilled sets the flag)")
		}
	})
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
