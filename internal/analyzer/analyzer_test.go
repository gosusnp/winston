// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/gosusnp/winston/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyze(t *testing.T) {
	ctx := context.Background()

	// Helper to seed data
	seed := func(t *testing.T, s *store.Store, meta store.PodMeta, agg store.AggBucket) {
		id, err := s.UpsertPodMetadata(ctx, meta)
		require.NoError(t, err)
		agg.PodID = id
		err = s.UpsertAggBucket(ctx, agg)
		require.NoError(t, err)
		// Insert a recent raw metric so PodsWithMissingConfig sees this pod as active.
		err = s.InsertRawMetric(ctx, id, time.Now().Unix(), 10, 1024)
		require.NoError(t, err)
	}

	t.Run("OverProvisioned", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		seed(t, s, store.PodMeta{
			Namespace: "ns1", PodName: "p1", ContainerName: "c1",
			OwnerKind: "Deployment", OwnerName: "w1",
			CPURequestM: 1000, CPULimitM: 2000,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 100, // 10% of request (1000)
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "ns1", results[0].Namespace)
		assert.Contains(t, results[0].Profiles, OverProvisioned)
	})

	t.Run("GhostLimit", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		seed(t, s, store.PodMeta{
			Namespace: "ns2", PodName: "p2", ContainerName: "c2",
			OwnerKind: "Deployment", OwnerName: "w2",
			CPURequestM: 100, CPULimitM: 1000,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUMaxM: 50, // 5% of limit (1000)
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Profiles, GhostLimit)
	})

	t.Run("DangerZone", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		seed(t, s, store.PodMeta{
			Namespace: "ns3", PodName: "p3", ContainerName: "c3",
			OwnerKind: "Deployment", OwnerName: "w3",
			CPURequestM: 100, CPULimitM: 1000,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP90M: 950, // 95% of limit (1000)
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Profiles, DangerZone)
	})

	t.Run("MultiProfile", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		seed(t, s, store.PodMeta{
			Namespace: "ns4", PodName: "p4", ContainerName: "c4",
			OwnerKind: "Deployment", OwnerName: "w4",
			CPURequestM: 1000, CPULimitM: 2000,
			MemRequestB: 1024, MemLimitB: 4096,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 100, // OverProvisioned CPU
			MemMaxB: 200, // GhostLimit Mem (approx 5%)
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.ElementsMatch(t, []Profile{OverProvisioned, GhostLimit}, results[0].Profiles)
	})

	t.Run("NoLimit_SkipsLimitProfiles", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		seed(t, s, store.PodMeta{
			Namespace: "ns5", PodName: "p5", ContainerName: "c5",
			OwnerKind: "Deployment", OwnerName: "w5",
			CPURequestM: 100, CPULimitM: 0, // No limit
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 50,   // 50% of request (healthy)
			CPUP90M: 1000, // Would be DangerZone if limit was set
			CPUMaxM: 5,    // Would be GhostLimit if limit was set
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.ElementsMatch(t, []Profile{NoLimits, NoRequests}, results[0].Profiles)
	})

	t.Run("NoLimits_HasAggStats", func(t *testing.T) {
		// A pod with no limit but existing agg data should have CPU/Mem stats populated.
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		seed(t, s, store.PodMeta{
			Namespace: "ns5b", PodName: "p5b", ContainerName: "c5b",
			OwnerKind: "Deployment", OwnerName: "w5b",
			CPULimitM: 0, // no limit
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 42,
			CPUAvgM: 80, CPUMaxM: 150,
			CPUP50M: 70, CPUP75M: 90, CPUP90M: 120, CPUP95M: 140, CPUP99M: 148,
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Profiles, NoLimits)
		assert.Equal(t, int64(42), results[0].SampleCount)
		assert.Equal(t, int64(80), results[0].CPU.AvgM)
		assert.Equal(t, int64(150), results[0].CPU.MaxM)
		assert.Equal(t, int64(120), results[0].CPU.P90M)
	})

	t.Run("NoMatch", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		seed(t, s, store.PodMeta{
			Namespace: "ns6", PodName: "p6", ContainerName: "c6",
			OwnerKind: "Deployment", OwnerName: "w6",
			CPURequestM: 100, CPULimitM: 200,
			MemRequestB: 1024, MemLimitB: 2048,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 50,   // 50% of request (healthy)
			CPUP90M: 80,   // 40% of limit (healthy)
			CPUMaxM: 100,  // 50% of limit (healthy)
			MemP95B: 512,  // 50% of request (healthy)
			MemP90B: 800,  // ~39% of limit (healthy)
			MemMaxB: 1024, // 50% of limit (healthy)
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{})
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("SortOrder", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		// OverProvisioned
		seed(t, s, store.PodMeta{
			Namespace: "ns", PodName: "p-over", ContainerName: "c1",
			OwnerKind: "Deployment", OwnerName: "w-over",
			CPURequestM: 1000,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 10,
		})

		// DangerZone
		seed(t, s, store.PodMeta{
			Namespace: "ns", PodName: "p-danger", ContainerName: "c1",
			OwnerKind: "Deployment", OwnerName: "w-danger",
			CPULimitM: 100,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP90M: 95,
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{})
		require.NoError(t, err)
		require.Len(t, results, 2)

		// DangerZone (w-danger) should come before OverProvisioned (w-over)
		assert.Equal(t, "w-danger", results[0].OwnerName)
		assert.Contains(t, results[0].Profiles, DangerZone)
		assert.Equal(t, "w-over", results[1].OwnerName)
		assert.Contains(t, results[1].Profiles, OverProvisioned)
	})

	t.Run("OverProvisioned_BelowMinRequest_Suppressed", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		// cpu_request=50m — qualifies on ratio (p95=5m < 20% of 50m) but below min threshold (100m).
		// All fields set to avoid triggering other profiles.
		seed(t, s, store.PodMeta{
			Namespace: "ns", PodName: "p", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w",
			CPURequestM: 50, CPULimitM: 200,
			MemRequestB: 67108864, MemLimitB: 134217728,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 5, CPUMaxM: 100, // CPUMaxM=50% of limit → no ghost_limit
			MemP95B: 33554432, MemMaxB: 67108864, // healthy mem
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{OverProvisionedMinCPUM: 100})
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("OverProvisioned_AboveMinRequest_Fires", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		// cpu_request=500m — qualifies on ratio (p95=50m < 20% of 500m) and meets min threshold (100m).
		seed(t, s, store.PodMeta{
			Namespace: "ns", PodName: "p", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w",
			CPURequestM: 500, CPULimitM: 1000,
			MemRequestB: 67108864, MemLimitB: 134217728,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 50, CPUMaxM: 500,
			MemP95B: 33554432, MemMaxB: 67108864,
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{OverProvisionedMinCPUM: 100})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Profiles, OverProvisioned)
	})

	t.Run("GhostLimit_BelowMinLimit_Suppressed", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		// cpu_limit=64m — qualifies on ratio (max=3m < 10% of 64m) but below min threshold (100m).
		// All fields set to avoid triggering other profiles.
		seed(t, s, store.PodMeta{
			Namespace: "ns", PodName: "p", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w",
			CPURequestM: 32, CPULimitM: 64,
			MemRequestB: 67108864, MemLimitB: 134217728,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 20, CPUMaxM: 3, // CPUP95M=62% of request → no over_provisioned
			MemP95B: 33554432, MemMaxB: 67108864,
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{GhostLimitMinCPUM: 100})
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("GhostLimit_AboveMinLimit_Fires", func(t *testing.T) {
		s, err := store.Open(":memory:")
		require.NoError(t, err)
		defer func() { _ = s.Close() }()
		now := time.Now().Unix()

		// cpu_limit=2000m — qualifies on ratio (max=50m < 10% of 2000m) and meets min threshold (100m).
		seed(t, s, store.PodMeta{
			Namespace: "ns", PodName: "p", ContainerName: "c",
			OwnerKind: "Deployment", OwnerName: "w",
			CPURequestM: 100, CPULimitM: 2000,
			MemRequestB: 67108864, MemLimitB: 134217728,
		}, store.AggBucket{
			Resolution: "1h", BucketStart: now, SampleCount: 1,
			CPUP95M: 80, CPUMaxM: 50,
			MemP95B: 33554432, MemMaxB: 67108864,
		})

		a := New(s, time.Hour)
		results, err := a.Analyze(ctx, 7, time.Now(), Thresholds{GhostLimitMinCPUM: 100})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Profiles, GhostLimit)
	})
}
