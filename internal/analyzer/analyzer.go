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
	HighRestarts    Profile = "high_restarts"
	OOMKilled       Profile = "oom_killed"
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
	Restarts      int64
	Current       ResourceConfig
	CPU           CPUStats
	Mem           MemStats
}

// Thresholds defines minimum configured values required for over_provisioned and
// ghost_limit profiles to fire. A workload is skipped for a profile if its
// request (over_provisioned) or limit (ghost_limit) is below the threshold.
// Zero means no minimum — any value qualifies (default behavior).
type Thresholds struct {
	OverProvisionedMinCPUM int64
	OverProvisionedMinMemB int64
	GhostLimitMinCPUM      int64
	GhostLimitMinMemB      int64
	HighRestartThreshold   int64
}

type Analyzer struct {
	store  *store.Store
	podTTL time.Duration
}

func New(s *store.Store, podTTL time.Duration) *Analyzer {
	return &Analyzer{store: s, podTTL: podTTL}
}

func (a *Analyzer) Analyze(ctx context.Context, lookbackDays int, now time.Time, thresholds Thresholds) ([]WorkloadResult, error) {
	since := now.AddDate(0, 0, -lookbackDays).Unix()
	stats, err := a.store.AggStatsForWindow(ctx, "1h", since, now.Add(-a.podTTL).Unix())
	if err != nil {
		return nil, fmt.Errorf("getting agg stats: %w", err)
	}

	// Index all agg stats by workload key so we can populate usage stats for
	// no_limits/no_requests pods that don't match any usage-based profile.
	type resultKey struct{ namespace, ownerKind, ownerName, container string }
	statsIndex := make(map[resultKey]*store.AggStats, len(stats))
	for i := range stats {
		s := &stats[i]
		statsIndex[resultKey{s.Namespace, s.OwnerKind, s.OwnerName, s.ContainerName}] = s
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

		// Over-Provisioned: avg(p95) < 20% of request, gated on min request threshold.
		isOverProvisioned := false
		if s.CPURequestM >= thresholds.OverProvisionedMinCPUM && s.CPURequestM > 0 && s.CPUP95M < int64(float64(s.CPURequestM)*0.2) {
			isOverProvisioned = true
		} else if s.MemRequestB >= thresholds.OverProvisionedMinMemB && s.MemRequestB > 0 && s.MemP95B < int64(float64(s.MemRequestB)*0.2) {
			isOverProvisioned = true
		}
		if isOverProvisioned {
			profiles = append(profiles, OverProvisioned)
		}

		// Ghost Limit: max(max) < 10% of limit, gated on min limit threshold.
		isGhostLimit := false
		if s.CPULimitM >= thresholds.GhostLimitMinCPUM && s.CPULimitM > 0 && s.CPUMaxM < int64(float64(s.CPULimitM)*0.1) {
			isGhostLimit = true
		} else if s.MemLimitB >= thresholds.GhostLimitMinMemB && s.MemLimitB > 0 && s.MemMaxB < int64(float64(s.MemLimitB)*0.1) {
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
			// Pod has no usage-based profile — create a result with metadata.
			// Populate CPU/Mem stats from agg data if available so the report
			// shows actual consumption alongside the misconfiguration flag.
			wr := WorkloadResult{
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
			}
			if agg, ok := statsIndex[key]; ok {
				wr.SampleCount = agg.SampleCount
				wr.CPU = CPUStats{
					AvgM:    agg.CPUAvgM,
					MaxM:    agg.CPUMaxM,
					StddevM: agg.CPUSTDDevM,
					P50M:    agg.CPUP50M,
					P75M:    agg.CPUP75M,
					P90M:    agg.CPUP90M,
					P95M:    agg.CPUP95M,
					P99M:    agg.CPUP99M,
				}
				wr.Mem = MemStats{
					AvgB:    agg.MemAvgB,
					MaxB:    agg.MemMaxB,
					StddevB: agg.MemSTDDevB,
					P50B:    agg.MemP50B,
					P75B:    agg.MemP75B,
					P90B:    agg.MemP90B,
					P95B:    agg.MemP95B,
					P99B:    agg.MemP99B,
				}
			}
			results = append(results, wr)
			index[key] = len(results) - 1
		}
	}

	// Merge high_restarts / oom_killed from metrics_raw restart count delta.
	restartThreshold := thresholds.HighRestartThreshold
	if restartThreshold <= 0 {
		// Safety net for callers that pass a zero-value Thresholds (e.g. tests).
		// main.go always sets this from WINSTON_HIGH_RESTART_THRESHOLD (default 5).
		restartThreshold = 5
	}
	restartPods, err := a.store.PodsWithHighRestarts(ctx, now.Add(-a.podTTL).Unix(), restartThreshold)
	if err != nil {
		return nil, fmt.Errorf("getting pods with high restarts: %w", err)
	}

	for _, p := range restartPods {
		var profiles []Profile
		profiles = append(profiles, HighRestarts)
		if p.OOMKilled {
			profiles = append(profiles, OOMKilled)
		}

		key := resultKey{p.Namespace, p.OwnerKind, p.OwnerName, p.ContainerName}
		if i, ok := index[key]; ok {
			results[i].Profiles = append(results[i].Profiles, profiles...)
			results[i].Restarts = p.RestartDelta
		} else {
			wr := WorkloadResult{
				Namespace:     p.Namespace,
				OwnerKind:     p.OwnerKind,
				OwnerName:     p.OwnerName,
				ContainerName: p.ContainerName,
				Profiles:      profiles,
				Restarts:      p.RestartDelta,
				Current: ResourceConfig{
					CPURequestM: p.CPURequestM,
					CPULimitM:   p.CPULimitM,
					MemRequestB: p.MemRequestB,
					MemLimitB:   p.MemLimitB,
				},
			}
			if agg, ok := statsIndex[key]; ok {
				wr.SampleCount = agg.SampleCount
				wr.CPU = CPUStats{
					AvgM:    agg.CPUAvgM,
					MaxM:    agg.CPUMaxM,
					StddevM: agg.CPUSTDDevM,
					P50M:    agg.CPUP50M,
					P75M:    agg.CPUP75M,
					P90M:    agg.CPUP90M,
					P95M:    agg.CPUP95M,
					P99M:    agg.CPUP99M,
				}
				wr.Mem = MemStats{
					AvgB:    agg.MemAvgB,
					MaxB:    agg.MemMaxB,
					StddevB: agg.MemSTDDevB,
					P50B:    agg.MemP50B,
					P75B:    agg.MemP75B,
					P90B:    agg.MemP90B,
					P95B:    agg.MemP95B,
					P99B:    agg.MemP99B,
				}
			}
			results = append(results, wr)
			index[key] = len(results) - 1
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
	min := 99
	for _, p := range profiles {
		var s int
		switch p {
		case OOMKilled:
			s = 1
		case DangerZone:
			s = 2
		case HighRestarts:
			s = 3
		case NoLimits:
			s = 4
		case NoRequests:
			s = 5
		case OverProvisioned:
			s = 6
		case GhostLimit:
			s = 7
		default:
			s = 8
		}
		if s < min {
			min = s
		}
	}
	return min
}
