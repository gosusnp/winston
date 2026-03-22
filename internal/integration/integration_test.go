// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gosusnp/winston/internal/analyzer"
	"github.com/gosusnp/winston/internal/api"
	"github.com/gosusnp/winston/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFullChain(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	now := time.Now().UTC()
	// Truncate seedStart to an hour boundary so all 26 compacted 1h buckets
	// get exactly 60 samples. Without this, the partial first bucket skews
	// avg(p90) below the DangerZone threshold when now falls mid-hour.
	seedStart := now.Add(-26 * time.Hour).Truncate(time.Hour)

	// 1. Seed pod_metadata
	// Container A: OverProvisioned (CPU p95 < 20% request)
	// Request: 200m, Limit: 500m. CPU p95 should be < 40m.
	idA, err := s.UpsertPodMetadata(ctx, store.PodMeta{
		Namespace:     "default",
		PodName:       "deploy-a-1",
		ContainerName: "app",
		OwnerKind:     "Deployment",
		OwnerName:     "deploy-a",
		CPURequestM:   200,
		CPULimitM:     500,
		MemRequestB:   128 * 1024 * 1024,
		MemLimitB:     512 * 1024 * 1024,
		FirstSeenAt:   seedStart.Unix(),
		LastSeenAt:    seedStart.Unix(),
	})
	require.NoError(t, err)

	// Container B: DangerZone (CPU p90 >= 90% limit)
	// Limit: 500m. CPU p90 >= 450m.
	idB, err := s.UpsertPodMetadata(ctx, store.PodMeta{
		Namespace:     "default",
		PodName:       "deploy-b-1", // Separate workload
		ContainerName: "app",
		OwnerKind:     "Deployment",
		OwnerName:     "deploy-b",
		CPURequestM:   200,
		CPULimitM:     500,
		MemRequestB:   128 * 1024 * 1024,
		MemLimitB:     512 * 1024 * 1024,
		FirstSeenAt:   seedStart.Unix(),
		LastSeenAt:    seedStart.Unix(),
	})
	require.NoError(t, err)

	// Container C: GhostLimit (Max < 10% limit)
	// Limit: 2000m. Max < 200m.
	idC, err := s.UpsertPodMetadata(ctx, store.PodMeta{
		Namespace:     "prod",
		PodName:       "stateful-c-0",
		ContainerName: "db",
		OwnerKind:     "StatefulSet",
		OwnerName:     "stateful-c",
		CPURequestM:   500,
		CPULimitM:     2000,
		MemRequestB:   1024 * 1024 * 1024,
		MemLimitB:     4096 * 1024 * 1024,
		FirstSeenAt:   seedStart.Unix(),
		LastSeenAt:    seedStart.Unix(),
	})
	require.NoError(t, err)

	// 2. Seed metrics_raw
	for i := 0; i < 26*60; i++ {
		ts := seedStart.Add(time.Duration(i) * time.Minute).Unix()

		// A: CPU consistently 30m
		err = s.InsertRawMetric(ctx, idA, ts, 30, 64*1024*1024)
		require.NoError(t, err)

		// B: CPU p90 at 460m
		cpuB := int64(460)
		if i%10 == 0 {
			cpuB = 100
		}
		err = s.InsertRawMetric(ctx, idB, ts, cpuB, 64*1024*1024)
		require.NoError(t, err)

		// C: CPU max 10m
		err = s.InsertRawMetric(ctx, idC, ts, 10, 512*1024*1024)
		require.NoError(t, err)
	}

	// 3. Compact
	cfg := store.CompactionConfig{
		RetentionRawS: 86400,
		Retention1HS:  604800,
		Retention1DS:  2592000,
	}
	err = s.Compact(ctx, now, cfg)
	require.NoError(t, err)

	// 4. Assertions on Store
	rawRows, err := s.RawRowsOlderThan(ctx, now.Add(-24*time.Hour).Unix())
	require.NoError(t, err)
	assert.Empty(t, rawRows)

	aggRows, err := s.AggRowsForWindow(ctx, "1h", seedStart.Unix())
	require.NoError(t, err)
	assert.NotEmpty(t, aggRows)

	// 5. Analyze
	az := analyzer.New(s, time.Hour)
	results, err := az.Analyze(ctx, 7, now, analyzer.Thresholds{})
	require.NoError(t, err)

	// 6. Assert Analysis Results
	var foundA, foundB, foundC bool
	var dangerIdx, overIdx int
	for i, w := range results {
		if w.OwnerName == "deploy-a" {
			for _, p := range w.Profiles {
				if p == analyzer.OverProvisioned {
					foundA = true
					overIdx = i
				}
			}
		}
		if w.OwnerName == "deploy-b" {
			for _, p := range w.Profiles {
				if p == analyzer.DangerZone {
					foundB = true
					dangerIdx = i
				}
			}
		}
		if w.OwnerName == "stateful-c" {
			for _, p := range w.Profiles {
				if p == analyzer.GhostLimit {
					foundC = true
				}
			}
		}
	}
	assert.True(t, foundA, "OverProvisioned not found")
	assert.True(t, foundB, "DangerZone not found")
	assert.True(t, foundC, "GhostLimit not found")
	assert.Less(t, dangerIdx, overIdx, "DangerZone should be sorted before OverProvisioned")

	// 7. API Tests
	srv := api.New(s, az, nil, analyzer.Thresholds{})
	handler := srv.Handler()

	// Test /exuberant (JSON)
	req := httptest.NewRequest("GET", "/exuberant", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var apiResponse struct {
		Workloads []analyzer.WorkloadResult `json:"workloads"`
	}
	err = json.Unmarshal(rr.Body.Bytes(), &apiResponse)
	require.NoError(t, err)
	assert.NotEmpty(t, apiResponse.Workloads)

	// Test /exuberant?format=markdown
	req = httptest.NewRequest("GET", "/exuberant?format=markdown", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "Danger Zone")
	assert.Contains(t, body, "Over-Provisioned")
	assert.Contains(t, body, "Ghost Limit")
}
