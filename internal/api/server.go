// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/gosusnp/winston/internal/analyzer"
	"github.com/gosusnp/winston/internal/report"
	"github.com/gosusnp/winston/internal/store"
)

type metricsKey struct {
	profile   analyzer.Profile
	namespace string
	kind      string
	name      string
}

type Server struct {
	store             *store.Store
	analyzer          *analyzer.Analyzer
	staticFS          fs.FS
	defaultThresholds analyzer.Thresholds
}

func New(s *store.Store, a *analyzer.Analyzer, staticFS fs.FS, defaultThresholds analyzer.Thresholds) *Server {
	return &Server{
		store:             s,
		analyzer:          a,
		staticFS:          staticFS,
		defaultThresholds: defaultThresholds,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /exuberant", s.handleExuberant)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Default route for static files
	if s.staticFS != nil {
		mux.Handle("GET /", http.FileServer(http.FS(s.staticFS)))
	}

	return mux
}

func (s *Server) ListenAndServe(addr string) error {
	server := &http.Server{
		Addr:         addr,
		Handler:      s.Handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return server.ListenAndServe()
}
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.LatestRawPerContainer(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching latest stats: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = report.RenderStatsJSON(w, rows)
}

func (s *Server) handleExuberant(w http.ResponseWriter, r *http.Request) {
	thresholds := thresholdsFromRequest(r, s.defaultThresholds)
	results, err := s.analyzer.Analyze(r.Context(), 7, time.Now(), thresholds)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error analyzing workloads: %v", err), http.StatusInternalServerError)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_ = report.RenderExuberantMarkdown(w, results, "7d", time.Now())
	} else {
		w.Header().Set("Content-Type", "application/json")
		_ = report.RenderExuberantJSON(w, results, "7d", time.Now())
	}
}

// thresholdsFromRequest starts from the configured defaults and overrides any
// field present in the query string (min_op_cpu_m, min_op_mem_b, min_gl_cpu_m,
// min_gl_mem_b).
func thresholdsFromRequest(r *http.Request, defaults analyzer.Thresholds) analyzer.Thresholds {
	t := defaults
	q := r.URL.Query()
	if v, err := strconv.ParseInt(q.Get("min_op_cpu_m"), 10, 64); err == nil {
		t.OverProvisionedMinCPUM = v
	}
	if v, err := strconv.ParseInt(q.Get("min_op_mem_b"), 10, 64); err == nil {
		t.OverProvisionedMinMemB = v
	}
	if v, err := strconv.ParseInt(q.Get("min_gl_cpu_m"), 10, 64); err == nil {
		t.GhostLimitMinCPUM = v
	}
	if v, err := strconv.ParseInt(q.Get("min_gl_mem_b"), 10, 64); err == nil {
		t.GhostLimitMinMemB = v
	}
	return t
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	thresholds := thresholdsFromRequest(r, s.defaultThresholds)
	results, err := s.analyzer.Analyze(r.Context(), 7, time.Now(), thresholds)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error analyzing workloads: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintln(w, "# HELP winston_exuberant_workloads Workloads matching an exuberance profile (1 = present).")
	_, _ = fmt.Fprintln(w, "# TYPE winston_exuberant_workloads gauge")

	seen := make(map[metricsKey]struct{})
	for _, result := range results {
		for _, p := range result.Profiles {
			k := metricsKey{p, result.Namespace, result.OwnerKind, result.OwnerName}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			_, _ = fmt.Fprintf(w, "winston_exuberant_workloads{profile=%q,namespace=%q,kind=%q,name=%q} 1\n",
				string(p), result.Namespace, result.OwnerKind, result.OwnerName)
		}
	}
}
