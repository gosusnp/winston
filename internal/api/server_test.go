// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gosusnp/winston/internal/analyzer"
	"github.com/gosusnp/winston/internal/store"
)

func TestStats_OK(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	podID, _ := s.UpsertPodMetadata(ctx, store.PodMeta{
		Namespace: "default", PodName: "pod1", ContainerName: "c1",
	})
	_ = s.InsertRawMetric(ctx, podID, time.Now().Unix(), 10, 100)

	srv := New(s, analyzer.New(s), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status: %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
		t.Errorf("content-type: %s", res.Header.Get("Content-Type"))
	}

	var data map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if _, ok := data["namespaces"]; !ok {
		t.Errorf("expected namespaces key")
	}
}

func TestExuberant_JSON(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	srv := New(s, analyzer.New(s), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/exuberant")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status: %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
		t.Errorf("content-type: %s", res.Header.Get("Content-Type"))
	}

	var data map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if v, ok := data["workloads"].([]interface{}); !ok || len(v) != 0 {
		t.Errorf("expected empty workloads array")
	}
}

func TestExuberant_Empty(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Analyzer with empty store
	srv := New(s, analyzer.New(s), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/exuberant")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status: %d", res.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if v, ok := data["workloads"].([]interface{}); !ok || len(v) != 0 {
		t.Errorf("expected empty workloads array, got %v", v)
	}
}

func TestExuberant_Markdown(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	srv := New(s, analyzer.New(s), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/exuberant?format=markdown")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status: %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "text/markdown") {
		t.Errorf("content-type: %s", res.Header.Get("Content-Type"))
	}
}

func TestStatic_Served(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer func() { _ = s.Close() }()

	staticFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>test</html>")},
	}

	srv := New(s, analyzer.New(s), staticFS)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status: %d", res.StatusCode)
	}

	// Check content
	var buf strings.Builder
	if err := res.Write(&buf); err != nil {
		t.Fatalf("res.Write: %v", err)
	}
	if !strings.Contains(buf.String(), "<html>test</html>") {
		t.Errorf("body missing expected content: %s", buf.String())
	}
}
