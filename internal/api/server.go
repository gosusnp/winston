// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/gosusnp/winston/internal/analyzer"
	"github.com/gosusnp/winston/internal/report"
	"github.com/gosusnp/winston/internal/store"
)

type Server struct {
	store    *store.Store
	analyzer *analyzer.Analyzer
	staticFS fs.FS
}

func New(s *store.Store, a *analyzer.Analyzer, staticFS fs.FS) *Server {
	return &Server{
		store:    s,
		analyzer: a,
		staticFS: staticFS,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /exuberant", s.handleExuberant)

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
	// Lookback is fixed at 7 days as per requirements in server.go description
	results, err := s.analyzer.Analyze(r.Context(), 7)
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
