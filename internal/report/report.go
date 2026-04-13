// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package report

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"time"

	"github.com/gosusnp/winston/internal/analyzer"
	"github.com/gosusnp/winston/internal/store"
)

// statsResponse matches the JSON shape for /stats
type statsResponse struct {
	CapturedAt time.Time                       `json:"captured_at"`
	Namespaces map[string][]statsContainerItem `json:"namespaces"`
}

type statsContainerItem struct {
	Pod         string `json:"pod"`
	Container   string `json:"container"`
	OwnerKind   string `json:"owner_kind,omitempty"`
	OwnerName   string `json:"owner_name,omitempty"`
	CPUM        int64  `json:"cpu_m"`
	MemB        int64  `json:"mem_b"`
	CPURequestM int64  `json:"cpu_request_m"`
	CPULimitM   int64  `json:"cpu_limit_m"`
	MemRequestB int64  `json:"mem_request_b"`
	MemLimitB   int64  `json:"mem_limit_b"`
}

// RenderStatsJSON renders the /stats response as JSON.
func RenderStatsJSON(w io.Writer, rows []store.LatestRawRow) error {
	resp := statsResponse{
		Namespaces: make(map[string][]statsContainerItem),
	}

	var maxCapturedAt int64
	for _, row := range rows {
		if row.CapturedAt > maxCapturedAt {
			maxCapturedAt = row.CapturedAt
		}

		item := statsContainerItem{
			Pod:         row.PodName,
			Container:   row.ContainerName,
			OwnerKind:   row.OwnerKind,
			OwnerName:   row.OwnerName,
			CPUM:        row.CPUM,
			MemB:        row.MemB,
			CPURequestM: row.CPURequestM,
			CPULimitM:   row.CPULimitM,
			MemRequestB: row.MemRequestB,
			MemLimitB:   row.MemLimitB,
		}
		resp.Namespaces[row.Namespace] = append(resp.Namespaces[row.Namespace], item)
	}

	if maxCapturedAt > 0 {
		resp.CapturedAt = time.Unix(maxCapturedAt, 0).UTC()
	}

	// Sort containers within each namespace for stable output
	for ns := range resp.Namespaces {
		sort.Slice(resp.Namespaces[ns], func(i, j int) bool {
			if resp.Namespaces[ns][i].Pod != resp.Namespaces[ns][j].Pod {
				return resp.Namespaces[ns][i].Pod < resp.Namespaces[ns][j].Pod
			}
			return resp.Namespaces[ns][i].Container < resp.Namespaces[ns][j].Container
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

// exuberantResponse matches the JSON shape for /exuberant
type exuberantResponse struct {
	Window      string                  `json:"window"`
	GeneratedAt time.Time               `json:"generated_at"`
	Workloads   []exuberantWorkloadItem `json:"workloads"`
}

type exuberantWorkloadItem struct {
	Namespace   string                  `json:"namespace"`
	OwnerKind   string                  `json:"owner_kind"`
	OwnerName   string                  `json:"owner_name"`
	Container   string                  `json:"container"`
	Profiles    []analyzer.Profile      `json:"profiles"`
	SampleCount int64                   `json:"sample_count"`
	Restarts    int64                   `json:"restarts,omitempty"`
	Current     exuberantResourceConfig `json:"current"`
	CPU         exuberantCPUStats       `json:"cpu"`
	Mem         exuberantMemStats       `json:"mem"`
}

type exuberantResourceConfig struct {
	CPURequestM int64 `json:"cpu_request_m"`
	CPULimitM   int64 `json:"cpu_limit_m"`
	MemRequestB int64 `json:"mem_request_b"`
	MemLimitB   int64 `json:"mem_limit_b"`
}

type exuberantCPUStats struct {
	AvgM    int64   `json:"avg_m"`
	MaxM    int64   `json:"max_m"`
	StddevM float64 `json:"stddev_m"`
	P50M    int64   `json:"p50_m"`
	P75M    int64   `json:"p75_m"`
	P90M    int64   `json:"p90_m"`
	P95M    int64   `json:"p95_m"`
	P99M    int64   `json:"p99_m"`
}

type exuberantMemStats struct {
	AvgB    int64   `json:"avg_b"`
	MaxB    int64   `json:"max_b"`
	StddevB float64 `json:"stddev_b"`
	P50B    int64   `json:"p50_b"`
	P75B    int64   `json:"p75_b"`
	P90B    int64   `json:"p90_b"`
	P95B    int64   `json:"p95_b"`
	P99B    int64   `json:"p99_b"`
}

// RenderExuberantJSON renders the /exuberant response as JSON.
func RenderExuberantJSON(w io.Writer, results []analyzer.WorkloadResult, window string, generatedAt time.Time) error {
	workloads := make([]exuberantWorkloadItem, 0, len(results))
	for _, res := range results {
		item := exuberantWorkloadItem{
			Namespace:   res.Namespace,
			OwnerKind:   res.OwnerKind,
			OwnerName:   res.OwnerName,
			Container:   res.ContainerName,
			Profiles:    res.Profiles,
			SampleCount: res.SampleCount,
			Restarts:    res.Restarts,
			Current: exuberantResourceConfig{
				CPURequestM: res.Current.CPURequestM,
				CPULimitM:   res.Current.CPULimitM,
				MemRequestB: res.Current.MemRequestB,
				MemLimitB:   res.Current.MemLimitB,
			},
			CPU: exuberantCPUStats{
				AvgM:    res.CPU.AvgM,
				MaxM:    res.CPU.MaxM,
				StddevM: res.CPU.StddevM,
				P50M:    res.CPU.P50M,
				P75M:    res.CPU.P75M,
				P90M:    res.CPU.P90M,
				P95M:    res.CPU.P95M,
				P99M:    res.CPU.P99M,
			},
			Mem: exuberantMemStats{
				AvgB:    res.Mem.AvgB,
				MaxB:    res.Mem.MaxB,
				StddevB: res.Mem.StddevB,
				P50B:    res.Mem.P50B,
				P75B:    res.Mem.P75B,
				P90B:    res.Mem.P90B,
				P95B:    res.Mem.P95B,
				P99B:    res.Mem.P99B,
			},
		}
		workloads = append(workloads, item)
	}

	resp := exuberantResponse{
		Window:      window,
		GeneratedAt: generatedAt.UTC(),
		Workloads:   workloads,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

// RenderExuberantMarkdown renders the /exuberant response as Markdown.
func RenderExuberantMarkdown(w io.Writer, results []analyzer.WorkloadResult, window string, generatedAt time.Time) error {
	if _, err := fmt.Fprintf(w, "# Winston: Exuberant Pods Report\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Window: %s | Generated: %s UTC\n\n", window, generatedAt.UTC().Format("2006-01-02 15:04")); err != nil {
		return err
	}

	// Danger Zone
	dangerZone := filterByProfile(results, analyzer.DangerZone)
	if len(dangerZone) > 0 {
		if _, err := fmt.Fprintf(w, "## Danger Zone — %d workload(s)\n", len(dangerZone)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Sustained usage ≥ 90%% of limit. Throttling or OOMKill risk.\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "| Namespace | Workload | Container | CPU p90 | CPU Limit | Mem p90 | Mem Limit |\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "|---|---|---|---|---|---|---|\n"); err != nil {
			return err
		}
		for _, r := range dangerZone {
			if _, err := fmt.Fprintf(w, "| %s | %s/%s | %s | %s | %s | %s | %s |\n",
				r.Namespace, r.OwnerKind, r.OwnerName, r.ContainerName,
				formatCPU(r.CPU.P90M), formatCPU(r.Current.CPULimitM),
				formatMem(r.Mem.P90B), formatMem(r.Current.MemLimitB)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return err
		}
	}

	// Over-Provisioned
	overProvisioned := filterByProfile(results, analyzer.OverProvisioned)
	if len(overProvisioned) > 0 {
		if _, err := fmt.Fprintf(w, "## Over-Provisioned — %d workload(s)\n", len(overProvisioned)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "p95 usage < 20%% of request. Safe to reduce requests.\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "| Namespace | Workload | Container | CPU p95 | CPU Request | Mem p95 | Mem Request |\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "|---|---|---|---|---|---|---|\n"); err != nil {
			return err
		}
		for _, r := range overProvisioned {
			if _, err := fmt.Fprintf(w, "| %s | %s/%s | %s | %s | %s | %s | %s |\n",
				r.Namespace, r.OwnerKind, r.OwnerName, r.ContainerName,
				formatCPU(r.CPU.P95M), formatCPU(r.Current.CPURequestM),
				formatMem(r.Mem.P95B), formatMem(r.Current.MemRequestB)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return err
		}
	}

	// No Limits
	noLimits := filterByProfile(results, analyzer.NoLimits)
	if len(noLimits) > 0 {
		if _, err := fmt.Fprintf(w, "## No Limits — %d workload(s)\n", len(noLimits)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "No CPU or memory limit set. Pod can consume unbounded resources.\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "| Namespace | Workload | Container | CPU Limit | Mem Limit | CPU Max | Mem Max |\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "|---|---|---|---|---|---|---|\n"); err != nil {
			return err
		}
		for _, r := range noLimits {
			if _, err := fmt.Fprintf(w, "| %s | %s/%s | %s | %s | %s | %s | %s |\n",
				r.Namespace, r.OwnerKind, r.OwnerName, r.ContainerName,
				formatCPU(r.Current.CPULimitM), formatMem(r.Current.MemLimitB),
				formatCPU(r.CPU.MaxM), formatMem(r.Mem.MaxB)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return err
		}
	}

	// No Requests
	noRequests := filterByProfile(results, analyzer.NoRequests)
	if len(noRequests) > 0 {
		if _, err := fmt.Fprintf(w, "## No Requests — %d workload(s)\n", len(noRequests)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "No CPU or memory request set. Pod has BestEffort QoS and is first to be evicted.\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "| Namespace | Workload | Container | CPU Request | Mem Request | CPU Avg | Mem Avg |\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "|---|---|---|---|---|---|---|\n"); err != nil {
			return err
		}
		for _, r := range noRequests {
			if _, err := fmt.Fprintf(w, "| %s | %s/%s | %s | %s | %s | %s | %s |\n",
				r.Namespace, r.OwnerKind, r.OwnerName, r.ContainerName,
				formatCPU(r.Current.CPURequestM), formatMem(r.Current.MemRequestB),
				formatCPU(r.CPU.AvgM), formatMem(r.Mem.AvgB)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return err
		}
	}

	// OOM Killed
	oomKilled := filterByProfile(results, analyzer.OOMKilled)
	if len(oomKilled) > 0 {
		if _, err := fmt.Fprintf(w, "## OOM Killed — %d workload(s)\n", len(oomKilled)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Last container exit was OOMKilled and restarts are ongoing. Memory limit is too low or there is a memory leak.\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "| Namespace | Workload | Container | Restarts | Mem Max | Mem Limit |\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "|---|---|---|---|---|---|\n"); err != nil {
			return err
		}
		for _, r := range oomKilled {
			if _, err := fmt.Fprintf(w, "| %s | %s/%s | %s | %d | %s | %s |\n",
				r.Namespace, r.OwnerKind, r.OwnerName, r.ContainerName,
				r.Restarts, formatMemOrNA(r.Mem.MaxB, r.SampleCount), formatMem(r.Current.MemLimitB)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return err
		}
	}

	// High Restarts
	highRestarts := filterByProfile(results, analyzer.HighRestarts)
	if len(highRestarts) > 0 {
		if _, err := fmt.Fprintf(w, "## High Restarts — %d workload(s)\n", len(highRestarts)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Restart count increased significantly in the observation window.\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "| Namespace | Workload | Container | Restarts | CPU Avg | Mem Avg |\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "|---|---|---|---|---|---|\n"); err != nil {
			return err
		}
		for _, r := range highRestarts {
			if _, err := fmt.Fprintf(w, "| %s | %s/%s | %s | %d | %s | %s |\n",
				r.Namespace, r.OwnerKind, r.OwnerName, r.ContainerName,
				r.Restarts, formatCPUOrNA(r.CPU.AvgM, r.SampleCount), formatMemOrNA(r.Mem.AvgB, r.SampleCount)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return err
		}
	}

	// Ghost Limit
	ghostLimit := filterByProfile(results, analyzer.GhostLimit)
	if len(ghostLimit) > 0 {
		if _, err := fmt.Fprintf(w, "## Ghost Limit — %d workload(s)\n", len(ghostLimit)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Absolute peak < 10%% of limit. Limit is functionally unconstrained.\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "| Namespace | Workload | Container | CPU Max | CPU Limit | Mem Max | Mem Limit |\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "|---|---|---|---|---|---|---|\n"); err != nil {
			return err
		}
		for _, r := range ghostLimit {
			if _, err := fmt.Fprintf(w, "| %s | %s/%s | %s | %s | %s | %s | %s |\n",
				r.Namespace, r.OwnerKind, r.OwnerName, r.ContainerName,
				formatCPU(r.CPU.MaxM), formatCPU(r.Current.CPULimitM),
				formatMem(r.Mem.MaxB), formatMem(r.Current.MemLimitB)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return err
		}
	}

	return nil
}

func filterByProfile(results []analyzer.WorkloadResult, p analyzer.Profile) []analyzer.WorkloadResult {
	var filtered []analyzer.WorkloadResult
	for _, r := range results {
		if slices.Contains(r.Profiles, p) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func formatCPU(m int64) string {
	if m == 0 {
		return "none"
	}
	return fmt.Sprintf("%dm", m)
}

func formatMem(b int64) string {
	if b == 0 {
		return "none"
	}
	return fmt.Sprintf("%dMi", b/1048576)
}

// formatCPUOrNA renders a CPU value, returning "—" when no agg data is available.
func formatCPUOrNA(m int64, sampleCount int64) string {
	if sampleCount == 0 {
		return "—"
	}
	return formatCPU(m)
}

// formatMemOrNA renders a memory value, returning "—" when no agg data is available.
func formatMemOrNA(b int64, sampleCount int64) string {
	if sampleCount == 0 {
		return "—"
	}
	return formatMem(b)
}
