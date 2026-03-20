// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gosusnp/winston/internal/analyzer"
	"github.com/gosusnp/winston/internal/api"
	"github.com/gosusnp/winston/internal/collector"
	"github.com/gosusnp/winston/internal/report"
	"github.com/gosusnp/winston/internal/store"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/metrics/pkg/client/clientset/versioned"
)

var version = "dev"

type config struct {
	DBPath          string
	Port            string
	CollectInterval time.Duration
	RetentionRawS   int
	Retention1HS    int
	Retention1DS    int
	PodTTLS         int
}

func main() {
	cfg := loadConfig()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "report":
			runReport(cfg)
		case "version":
			fmt.Println(version)
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			os.Exit(1)
		}
	} else {
		runServer(cfg)
	}
}

func loadConfig() config {
	return config{
		DBPath:          getEnv("WINSTON_DB_PATH", "/data/winston.db"),
		Port:            getEnv("WINSTON_PORT", "8080"),
		CollectInterval: time.Duration(getEnvInt("WINSTON_COLLECT_INTERVAL", 60)) * time.Second,
		RetentionRawS:   getEnvInt("WINSTON_RETENTION_RAW_S", 86400),
		Retention1HS:    getEnvInt("WINSTON_RETENTION_1H_S", 604800),
		Retention1DS:    getEnvInt("WINSTON_RETENTION_1D_S", 2592000),
		PodTTLS:         getEnvInt("WINSTON_POD_TTL_S", 3600),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return fallback
}

func runReport(cfg config) {
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open store: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := s.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "error closing store: %v\n", err)
		}
	}()

	a := analyzer.New(s, time.Duration(cfg.PodTTLS)*time.Second)
	lookbackDays := cfg.Retention1HS / 86400
	results, err := a.Analyze(context.Background(), lookbackDays, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "analysis error: %v\n", err)
		os.Exit(1)
	}

	window := fmt.Sprintf("%dd", lookbackDays)
	if err := report.RenderExuberantMarkdown(os.Stdout, results, window, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "report error: %v\n", err)
		os.Exit(1)
	}
}

func runServer(cfg config) {
	log.Printf("winston %s starting", version)

	s, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			log.Printf("error closing store: %v", err)
		}
	}()

	// Kubernetes client setup
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Fatalf("failed to build kubernetes config: %v", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Fatalf("failed to create kubernetes client: %v", err)
	}

	metricsClient, err := versioned.NewForConfig(restConfig)
	if err != nil {
		log.Fatalf("failed to create metrics client: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup

	// Start collector
	c := collector.New(clientset, metricsClient, s, cfg.CollectInterval)
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Run(ctx)
		log.Println("collector stopped")
	}()

	// Start compactor
	compactionCfg := store.CompactionConfig{
		RetentionRawS: cfg.RetentionRawS,
		Retention1HS:  cfg.Retention1HS,
		Retention1DS:  cfg.Retention1DS,
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		log.Println("compactor started (hourly ticker)")
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := s.Compact(ctx, now, compactionCfg); err != nil {
					log.Printf("compaction error: %v", err)
				}
			}
		}
	}()

	// Start API server
	a := analyzer.New(s, time.Duration(cfg.PodTTLS)*time.Second)
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("failed to sub static FS: %v", err)
	}
	srv := api.New(s, a, static)
	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      srv.Handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("API server listening on :%s", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	// Graceful shutdown for API server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("API server shutdown error: %v", err)
	}

	wg.Wait()
	log.Println("all components stopped")
}
