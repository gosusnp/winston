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

		a := New(s)
		results, err := a.Analyze(ctx, 7, time.Now())
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

		a := New(s)
		results, err := a.Analyze(ctx, 7, time.Now())
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

		a := New(s)
		results, err := a.Analyze(ctx, 7, time.Now())
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

		a := New(s)
		results, err := a.Analyze(ctx, 7, time.Now())
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

		a := New(s)
		results, err := a.Analyze(ctx, 7, time.Now())
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.ElementsMatch(t, []Profile{NoLimits, NoRequests}, results[0].Profiles)
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

		a := New(s)
		results, err := a.Analyze(ctx, 7, time.Now())
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

		a := New(s)
		results, err := a.Analyze(ctx, 7, time.Now())
		require.NoError(t, err)
		require.Len(t, results, 2)

		// DangerZone (w-danger) should come before OverProvisioned (w-over)
		assert.Equal(t, "w-danger", results[0].OwnerName)
		assert.Contains(t, results[0].Profiles, DangerZone)
		assert.Equal(t, "w-over", results[1].OwnerName)
		assert.Contains(t, results[1].Profiles, OverProvisioned)
	})
}
