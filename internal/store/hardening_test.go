// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreshDB(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "winston-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tempDir) }()

	dbPath := filepath.Join(tempDir, "test-winston-fresh.db")

	// Ensure file doesn't exist
	_, err = os.Stat(dbPath)
	assert.True(t, os.IsNotExist(err))

	s, err := Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	// Verify file was created
	_, err = os.Stat(dbPath)
	assert.NoError(t, err)

	// Verify schema was migrated (try an insert)
	ctx := context.Background()
	_, err = s.UpsertPodMetadata(ctx, PodMeta{
		Namespace:     "default",
		PodName:       "p1",
		ContainerName: "c1",
	})
	assert.NoError(t, err)
}

func TestConcurrentReadsDuringCompaction(t *testing.T) {
	s, err := Open(":memory:")
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	podID, err := s.UpsertPodMetadata(ctx, PodMeta{
		Namespace:     "default",
		PodName:       "p1",
		ContainerName: "c1",
	})
	require.NoError(t, err)

	// Seed many rows
	now := time.Now()
	for i := 0; i < 5000; i++ {
		ts := now.Add(time.Duration(-i) * time.Minute).Unix()
		err := s.InsertRawMetric(ctx, podID, ts, 10, 100)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	// 1. Compactor goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 5; i++ {
			_ = s.Compact(ctx, now, CompactionConfig{RetentionRawH: 1})
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// 2. Reader goroutines
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				_, _ = s.LatestRawPerContainer(ctx)
				_, _ = s.AggStatsForWindow(ctx, "1h", now.Add(-7*24*time.Hour).Unix(), 0)
				time.Sleep(1 * time.Millisecond)
			}
		}()
	}

	close(start)
	wg.Wait()
}
