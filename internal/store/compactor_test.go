// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"testing"
	"time"
)

func TestCompact_RawTo1h(t *testing.T) {
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

	// Seed 30 raw rows spanning 2 hours (15 per hour)
	now := time.Unix(3600*10, 0) // Hour 10
	for i := 0; i < 15; i++ {
		// Hour 8: 3600 * 8
		if err := s.InsertRawMetric(ctx, podID, 3600*8+int64(i*60), 10, 100, 0); err != nil {
			t.Fatalf("insert raw metric failed: %v", err)
		}
		// Hour 9: 3600 * 9
		if err := s.InsertRawMetric(ctx, podID, 3600*9+int64(i*60), 20, 200, 0); err != nil {
			t.Fatalf("insert raw metric failed: %v", err)
		}
	}

	cfg := CompactionConfig{
		RetentionRawS: 3600, // Keep only 1 hour of raw data
	}

	// Compact at Hour 10. Rows in Hour 8 and Hour 9 should be compacted.
	// Hour 8 is 2 hours old, Hour 9 is 1 hour old.
	// Cutoff = 10 - 1h = 9. So everything before Hour 9 (i.e. Hour 8) should be compacted.
	// If now is Hour 10 and RetentionRawS is 3600, cutoff is Hour 9.
	// Hour 8 rows (captured_at < 9) are compacted.
	// Hour 9 rows (captured_at >= 9) are NOT compacted.

	if err := s.Compact(ctx, now, cfg); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	// Assert: raw rows for Hour 8 are deleted
	rawRows, err := s.RawRowsOlderThan(ctx, 3600*11) // All rows
	if err != nil {
		t.Fatalf("query raw rows failed: %v", err)
	}
	for _, r := range rawRows {
		if r.CapturedAt < 3600*9 {
			t.Errorf("found raw row that should have been deleted: %d", r.CapturedAt)
		}
	}

	// Assert: one 1h agg row exists for Hour 8
	aggRows, err := s.AggRowsForWindow(ctx, "1h", 0)
	if err != nil {
		t.Fatalf("query agg rows failed: %v", err)
	}

	foundHour8 := false
	for _, r := range aggRows {
		if r.BucketStart == 3600*8 {
			foundHour8 = true
			if r.SampleCount != 15 {
				t.Errorf("hour 8: expected sample_count 15, got %d", r.SampleCount)
			}
			if r.CPUAvgM != 10 {
				t.Errorf("hour 8: expected cpu_avg 10, got %d", r.CPUAvgM)
			}
		}
	}

	if !foundHour8 {
		t.Error("did not find agg row for hour 8")
	}
}

func TestCompact_UpdatesLastSeenAt(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	podID, err := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "default", PodName: "pod1", ContainerName: "c1", LastSeenAt: 0})
	if err != nil {
		t.Fatalf("upsert pod metadata failed: %v", err)
	}

	now := time.Unix(3600*10, 0)
	maxCapturedAt := int64(3600*8 + 14*60)
	for i := 0; i < 15; i++ {
		if err := s.InsertRawMetric(ctx, podID, 3600*8+int64(i*60), 10, 100, 0); err != nil {
			t.Fatalf("insert raw metric failed: %v", err)
		}
	}

	if err := s.Compact(ctx, now, CompactionConfig{RetentionRawS: 3600}); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	var lastSeenAt int64
	err = s.db.QueryRowContext(ctx, "SELECT last_seen_at FROM pod_metadata WHERE id = ?", podID).Scan(&lastSeenAt)
	if err != nil {
		t.Fatalf("query last_seen_at failed: %v", err)
	}

	if lastSeenAt != maxCapturedAt {
		t.Errorf("expected last_seen_at %d, got %d", maxCapturedAt, lastSeenAt)
	}
}

func TestCompact_UpdatesLastSeenAt_1hTo1d(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	podID, err := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "default", PodName: "pod1", ContainerName: "c1", LastSeenAt: 0})
	if err != nil {
		t.Fatalf("upsert pod metadata failed: %v", err)
	}

	now := time.Unix(86400*10, 0)
	bucketStart := int64(86400 * 2)
	expectedLastSeen := bucketStart + 3599
	bucket := AggBucket{
		PodID:       podID,
		Resolution:  "1h",
		BucketStart: bucketStart,
		SampleCount: 60,
	}
	if err := s.UpsertAggBucket(ctx, bucket); err != nil {
		t.Fatalf("upsert agg bucket failed: %v", err)
	}

	if err := s.Compact(ctx, now, CompactionConfig{Retention1HS: 604800}); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	var lastSeenAt int64
	err = s.db.QueryRowContext(ctx, "SELECT last_seen_at FROM pod_metadata WHERE id = ?", podID).Scan(&lastSeenAt)
	if err != nil {
		t.Fatalf("query last_seen_at failed: %v", err)
	}

	if lastSeenAt != expectedLastSeen {
		t.Errorf("expected last_seen_at %d, got %d", expectedLastSeen, lastSeenAt)
	}
}

func TestCompact_MonotonicLastSeenAt(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	futureTime := int64(1000000)
	podID, err := s.UpsertPodMetadata(ctx, PodMeta{Namespace: "default", PodName: "pod1", ContainerName: "c1", LastSeenAt: futureTime})
	if err != nil {
		t.Fatalf("upsert pod metadata failed: %v", err)
	}

	// Compact some old data
	now := time.Unix(3600*10, 0)
	for i := 0; i < 5; i++ {
		if err := s.InsertRawMetric(ctx, podID, 3600*8+int64(i*60), 10, 100, 0); err != nil {
			t.Fatalf("insert raw metric failed: %v", err)
		}
	}

	if err := s.Compact(ctx, now, CompactionConfig{RetentionRawS: 3600}); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	var lastSeenAt int64
	err = s.db.QueryRowContext(ctx, "SELECT last_seen_at FROM pod_metadata WHERE id = ?", podID).Scan(&lastSeenAt)
	if err != nil {
		t.Fatalf("query last_seen_at failed: %v", err)
	}

	if lastSeenAt != futureTime {
		t.Errorf("expected last_seen_at to remain %d, but it was changed to %d", futureTime, lastSeenAt)
	}
}

func TestCompact_1hTo1d(t *testing.T) {
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

	// Seed 1h agg rows spanning 8 days
	now := time.Unix(86400*10, 0) // Day 10
	// Day 2 has 24 hours
	for h := 0; h < 24; h++ {
		bucket := AggBucket{
			PodID:       podID,
			Resolution:  "1h",
			BucketStart: 86400*2 + int64(h*3600),
			SampleCount: 60,
			CPUAvgM:     10,
			CPUMaxM:     20,
		}
		if err := s.UpsertAggBucket(ctx, bucket); err != nil {
			t.Fatalf("upsert agg bucket failed: %v", err)
		}
	}

	if err := s.Compact(ctx, now, CompactionConfig{Retention1HS: 604800}); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	// Assert: 1h rows for Day 2 are deleted
	rows1h, err := s.AggRowsForWindow(ctx, "1h", 0)
	if err != nil {
		t.Fatalf("query 1h rows failed: %v", err)
	}
	for _, r := range rows1h {
		if r.BucketStart < 86400*3 {
			t.Errorf("found 1h row that should have been deleted: %d", r.BucketStart)
		}
	}

	// Assert: one 1d agg row exists for Day 2
	rows1d, err := s.AggRowsForWindow(ctx, "1d", 0)
	if err != nil {
		t.Fatalf("query 1d rows failed: %v", err)
	}

	foundDay2 := false
	for _, r := range rows1d {
		if r.BucketStart == 86400*2 {
			foundDay2 = true
			if r.SampleCount != 24*60 {
				t.Errorf("day 2: expected sample_count %d, got %d", 24*60, r.SampleCount)
			}
			if r.CPUAvgM != 10 {
				t.Errorf("day 2: expected cpu_avg 10, got %d", r.CPUAvgM)
			}
		}
	}

	if !foundDay2 {
		t.Error("did not find 1d agg row for day 2")
	}
}

func TestCompact_NoRows(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Compact(context.Background(), time.Now(), CompactionConfig{}); err != nil {
		t.Errorf("compact empty tables failed: %v", err)
	}
}

func TestCompact_PrunesOld1d(t *testing.T) {
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

	// Seed 1d row older than 30d
	now := time.Unix(86400*100, 0)
	bucket := AggBucket{
		PodID:       podID,
		Resolution:  "1d",
		BucketStart: 86400 * 50, // 50 days old
		SampleCount: 1440,
	}
	if err := s.UpsertAggBucket(ctx, bucket); err != nil {
		t.Fatalf("upsert 1d bucket failed: %v", err)
	}

	if err := s.Compact(ctx, now, CompactionConfig{Retention1DS: 2592000}); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	rows1d, err := s.AggRowsForWindow(ctx, "1d", 0)
	if err != nil {
		t.Fatalf("query 1d rows failed: %v", err)
	}

	if len(rows1d) != 0 {
		t.Errorf("expected 0 1d rows, got %d", len(rows1d))
	}
}
