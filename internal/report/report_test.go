// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gosusnp/winston/internal/analyzer"
	"github.com/gosusnp/winston/internal/store"
)

func TestRenderStatsJSON_GroupedByNamespace(t *testing.T) {
	now := time.Now().Unix()
	rows := []store.LatestRawRow{
		{
			PodMeta: store.PodMeta{
				Namespace: "default", PodName: "pod1", ContainerName: "c1",
			},
			CapturedAt: now,
			CPUM:       10, MemB: 100,
		},
		{
			PodMeta: store.PodMeta{
				Namespace: "kube-system", PodName: "pod2", ContainerName: "c2",
			},
			CapturedAt: now,
			CPUM:       20, MemB: 200,
		},
	}

	var buf bytes.Buffer
	if err := RenderStatsJSON(&buf, rows); err != nil {
		t.Fatalf("RenderStatsJSON: %v", err)
	}

	var resp statsResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(resp.Namespaces) != 2 {
		t.Errorf("expected 2 namespaces, got %d", len(resp.Namespaces))
	}
	if len(resp.Namespaces["default"]) != 1 {
		t.Errorf("expected 1 item in default namespace")
	}
}

func TestRenderExuberantJSON_Shape(t *testing.T) {
	results := []analyzer.WorkloadResult{
		{
			Namespace: "default", OwnerKind: "Deployment", OwnerName: "app1", ContainerName: "c1",
			Profiles: []analyzer.Profile{analyzer.OverProvisioned},
			CPU:      analyzer.CPUStats{AvgM: 10, P95M: 20},
			Mem:      analyzer.MemStats{AvgB: 100, P95B: 200},
		},
	}

	var buf bytes.Buffer
	now := time.Now()
	if err := RenderExuberantJSON(&buf, results, "7d", now); err != nil {
		t.Fatalf("RenderExuberantJSON: %v", err)
	}

	var resp exuberantResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if resp.Window != "7d" {
		t.Errorf("expected window 7d, got %s", resp.Window)
	}
	if len(resp.Workloads) != 1 {
		t.Errorf("expected 1 workload")
	}
}

func TestRenderExuberantJSON_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderExuberantJSON(&buf, nil, "7d", time.Now()); err != nil {
		t.Fatalf("RenderExuberantJSON: %v", err)
	}

	if !strings.Contains(buf.String(), `"workloads": []`) {
		t.Errorf("expected empty workloads array, got %s", buf.String())
	}
}

func TestRenderExuberantMarkdown_Sections(t *testing.T) {
	results := []analyzer.WorkloadResult{
		{
			Namespace: "default", OwnerKind: "Deployment", OwnerName: "danger", ContainerName: "c1",
			Profiles: []analyzer.Profile{analyzer.DangerZone},
		},
		{
			Namespace: "default", OwnerKind: "Deployment", OwnerName: "over", ContainerName: "c2",
			Profiles: []analyzer.Profile{analyzer.OverProvisioned},
		},
		{
			Namespace: "default", OwnerKind: "Deployment", OwnerName: "ghost", ContainerName: "c3",
			Profiles: []analyzer.Profile{analyzer.GhostLimit},
		},
	}

	var buf bytes.Buffer
	if err := RenderExuberantMarkdown(&buf, results, "7d", time.Now()); err != nil {
		t.Fatalf("RenderExuberantMarkdown: %v", err)
	}

	out := buf.String()
	headers := []string{"## Danger Zone", "## Over-Provisioned", "## Ghost Limit"}
	for _, h := range headers {
		if !strings.Contains(out, h) {
			t.Errorf("missing header: %s", h)
		}
	}
}

func TestRenderExuberantMarkdown_EmptySection(t *testing.T) {
	results := []analyzer.WorkloadResult{
		{
			Namespace: "default", OwnerKind: "Deployment", OwnerName: "over", ContainerName: "c2",
			Profiles: []analyzer.Profile{analyzer.OverProvisioned},
		},
	}

	var buf bytes.Buffer
	if err := RenderExuberantMarkdown(&buf, results, "7d", time.Now()); err != nil {
		t.Fatalf("RenderExuberantMarkdown: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "## Danger Zone") {
		t.Errorf("should not contain Danger Zone")
	}
	if !strings.Contains(out, "## Over-Provisioned") {
		t.Errorf("should contain Over-Provisioned")
	}
}
