// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package analyzer

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gosusnp/winston/internal/store"
)

type Profile string

const (
	OverProvisioned Profile = "over_provisioned"
	GhostLimit      Profile = "ghost_limit"
	DangerZone      Profile = "danger_zone"
	NoLimits        Profile = "no_limits"
	NoRequests      Profile = "no_requests"
)

type CPUStats struct {
	AvgM    int64
	MaxM    int64
	StddevM float64
	P50M    int64
	P75M    int64
	P90M    int64
	P95M    int64
	P99M    int64
}

type MemStats struct {
	AvgB    int64
	MaxB    int64
	StddevB float64
	P50B    int64
	P75B    int64
	P90B    int64
	P95B    int64
	P99B    int64
}

type ResourceConfig struct {
	CPURequestM int64
	CPULimitM   int64
	MemRequestB int64
	MemLimitB   int64
}

type WorkloadResult struct {
	Namespace     string
	OwnerKind     string
	OwnerName     string
	ContainerName string
	Profiles      []Profile
	SampleCount   int64
	Current       ResourceConfig
	CPU           CPUStats
	Mem           MemStats
}

type Analyzer struct {
	store  *store.Store
	podTTL time.Duration
}

func New(s *store.Store, podTTL time.Duration) *Analyzer {
	return &Analyzer{store: s, podTTL: podTTL}
}

func (a *Analyzer) Analyze(ctx context.Context, lookbackDays int, now time.Time) ([]WorkloadResult, error) {
	since := now.AddDate(0, 0, -lookbackDays).Unix()
	stats, err := a.store.AggStatsForWindow(ctx, "1h", since, now.Add(-a.podTTL).Unix())
	if err != nil {
		return nil, fmt.Errorf("getting agg stats: %w", err)
	}

	var results []WorkloadResult
	for _, s := range stats {
		var profiles []Profile

		// Danger Zone: avg(p90) >= 90% of limit
		isDangerZone := false
		if s.CPULimitM > 0 && s.CPUP90M >= int64(float64(s.CPULimitM)*0.9) {
			isDangerZone = true
		} else if s.MemLimitB > 0 && s.MemP90B >= int64(float64(s.MemLimitB)*0.9) {
			isDangerZone = true
		}
		if isDangerZone {
			profiles = append(profiles, DangerZone)
		}

		// Over-Provisioned: avg(p95) < 20% of request
		isOverProvisioned := false
		if s.CPURequestM > 0 && s.CPUP95M < int64(float64(s.CPURequestM)*0.2) {
			isOverProvisioned = true
		} else if s.MemRequestB > 0 && s.MemP95B < int64(float64(s.MemRequestB)*0.2) {
			isOverProvisioned = true
		}
		if isOverProvisioned {
			profiles = append(profiles, OverProvisioned)
		}

		// Ghost Limit: max(max) < 10% of limit
		isGhostLimit := false
		if s.CPULimitM > 0 && s.CPUMaxM < int64(float64(s.CPULimitM)*0.1) {
			isGhostLimit = true
		} else if s.MemLimitB > 0 && s.MemMaxB < int64(float64(s.MemLimitB)*0.1) {
			isGhostLimit = true
		}
		if isGhostLimit {
			profiles = append(profiles, GhostLimit)
		}

		if len(profiles) > 0 {
			results = append(results, WorkloadResult{
				Namespace:     s.Namespace,
				OwnerKind:     s.OwnerKind,
				OwnerName:     s.OwnerName,
				ContainerName: s.ContainerName,
				Profiles:      profiles,
				SampleCount:   s.SampleCount,
				Current: ResourceConfig{
					CPURequestM: s.CPURequestM,
					CPULimitM:   s.CPULimitM,
					MemRequestB: s.MemRequestB,
					MemLimitB:   s.MemLimitB,
				},
				CPU: CPUStats{
					AvgM:    s.CPUAvgM,
					MaxM:    s.CPUMaxM,
					StddevM: s.CPUSTDDevM,
					P50M:    s.CPUP50M,
					P75M:    s.CPUP75M,
					P90M:    s.CPUP90M,
					P95M:    s.CPUP95M,
					P99M:    s.CPUP99M,
				},
				Mem: MemStats{
					AvgB:    s.MemAvgB,
					MaxB:    s.MemMaxB,
					StddevB: s.MemSTDDevB,
					P50B:    s.MemP50B,
					P75B:    s.MemP75B,
					P90B:    s.MemP90B,
					P95B:    s.MemP95B,
					P99B:    s.MemP99B,
				},
			})
		}
	}

	// Merge no_limits / no_requests from pod_metadata directly (available immediately after first collection).
	unbound, err := a.store.PodsWithMissingConfig(ctx, now.Add(-a.podTTL).Unix())
	if err != nil {
		return nil, fmt.Errorf("getting pods with missing config: %w", err)
	}

	// Build an index so we can add profiles to existing agg results without duplicating.
	type resultKey struct{ namespace, ownerKind, ownerName, container string }
	index := make(map[resultKey]int, len(results))
	for i, r := range results {
		index[resultKey{r.Namespace, r.OwnerKind, r.OwnerName, r.ContainerName}] = i
	}

	for _, p := range unbound {
		// profiles is always non-empty here: the store query filters on
		// (cpu_limit_m=0 OR mem_limit_b=0 OR cpu_request_m=0 OR mem_request_b=0),
		// so at least one of the two blocks below will always fire.
		var profiles []Profile
		if p.CPULimitM == 0 || p.MemLimitB == 0 {
			profiles = append(profiles, NoLimits)
		}
		if p.CPURequestM == 0 || p.MemRequestB == 0 {
			profiles = append(profiles, NoRequests)
		}

		key := resultKey{p.Namespace, p.OwnerKind, p.OwnerName, p.ContainerName}
		if i, ok := index[key]; ok {
			// Pod already has agg results — just add the profiles.
			results[i].Profiles = append(results[i].Profiles, profiles...)
		} else {
			// Pod has no agg data yet — create a result with metadata only.
			results = append(results, WorkloadResult{
				Namespace:     p.Namespace,
				OwnerKind:     p.OwnerKind,
				OwnerName:     p.OwnerName,
				ContainerName: p.ContainerName,
				Profiles:      profiles,
				SampleCount:   p.RawSamples,
				Current: ResourceConfig{
					CPURequestM: p.CPURequestM,
					CPULimitM:   p.CPULimitM,
					MemRequestB: p.MemRequestB,
					MemLimitB:   p.MemLimitB,
				},
			})
		}
	}

	// Sort: by namespace ASC, then severity (DangerZone first, then OverProvisioned, then GhostLimit), then owner_name ASC.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Namespace != results[j].Namespace {
			return results[i].Namespace < results[j].Namespace
		}

		sevI := profileSeverity(results[i].Profiles)
		sevJ := profileSeverity(results[j].Profiles)
		if sevI != sevJ {
			return sevI < sevJ // Smaller value means higher priority/severity
		}

		if results[i].OwnerName != results[j].OwnerName {
			return results[i].OwnerName < results[j].OwnerName
		}
		return results[i].ContainerName < results[j].ContainerName
	})

	return results, nil
}

func profileSeverity(profiles []Profile) int {
	min := 4
	for _, p := range profiles {
		var s int
		switch p {
		case DangerZone:
			s = 1
		case NoLimits:
			s = 2
		case NoRequests:
			s = 3
		case OverProvisioned:
			s = 4
		case GhostLimit:
			s = 5
		default:
			s = 6
		}
		if s < min {
			min = s
		}
	}
	return min
}
