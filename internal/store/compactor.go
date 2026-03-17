// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

type CompactionConfig struct {
	RetentionRawH   int // hours to keep raw rows (default 24)
	Retention1HDays int // days to keep 1h buckets (default 7)
	Retention1DDays int // days to keep 1d buckets (default 30)
}

func (s *Store) Compact(ctx context.Context, now time.Time, cfg CompactionConfig) error {
	if cfg.RetentionRawH <= 0 {
		cfg.RetentionRawH = 24
	}
	if cfg.Retention1HDays <= 0 {
		cfg.Retention1HDays = 7
	}
	if cfg.Retention1DDays <= 0 {
		cfg.Retention1DDays = 30
	}

	tx, err := s.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Create a transaction-aware Store for compaction steps
	txStore := s.WithTx(tx)

	// Step 1: Raw -> 1h
	if err := txStore.compactRawTo1H(ctx, now, cfg.RetentionRawH); err != nil {
		return fmt.Errorf("compacting raw to 1h: %w", err)
	}

	// Step 2: 1h -> 1d
	if err := txStore.compact1HTo1D(ctx, now, cfg.Retention1HDays); err != nil {
		return fmt.Errorf("compacting 1h to 1d: %w", err)
	}

	// Step 3: Prune old 1d rows
	cutoff1D := now.AddDate(0, 0, -cfg.Retention1DDays).Unix()
	if err := txStore.DeleteAggRowsBefore(ctx, "1d", cutoff1D); err != nil {
		return fmt.Errorf("pruning 1d rows: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}

func (s *Store) compactRawTo1H(ctx context.Context, now time.Time, retentionH int) error {
	cutoff := now.Add(time.Duration(-retentionH) * time.Hour).Unix()
	rows, err := s.RawRowsOlderThan(ctx, cutoff)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	type groupKey struct {
		podID      int64
		hourBucket int64
	}

	groups := make(map[groupKey][]RawRow)
	for _, r := range rows {
		hourBucket := r.CapturedAt / 3600 * 3600
		key := groupKey{podID: r.PodID, hourBucket: hourBucket}
		groups[key] = append(groups[key], r)
	}

	lastSeen := make(map[int64]int64)

	for key, groupRows := range groups {
		bucket := computeAggBucket(key.podID, "1h", key.hourBucket, groupRows)
		if err := s.UpsertAggBucket(ctx, bucket); err != nil {
			return err
		}

		maxCapturedAt := int64(0)
		for _, r := range groupRows {
			if r.CapturedAt > maxCapturedAt {
				maxCapturedAt = r.CapturedAt
			}
		}
		if maxCapturedAt > lastSeen[key.podID] {
			lastSeen[key.podID] = maxCapturedAt
		}
	}

	for podID, ls := range lastSeen {
		if err := s.UpdateLastSeenAt(ctx, podID, ls); err != nil {
			return err
		}
	}

	return s.DeleteRawRowsBefore(ctx, cutoff)
}

func computeAggBucket(podID int64, resolution string, bucketStart int64, rows []RawRow) AggBucket {
	count := int64(len(rows))
	var cpuSum, memSum int64
	var cpuSumSq, memSumSq float64
	var cpuMax, memMax int64

	cpus := make([]int64, count)
	mems := make([]int64, count)

	for i, r := range rows {
		cpuSum += r.CPUM
		memSum += r.MemB
		cpuSumSq += float64(r.CPUM * r.CPUM)
		memSumSq += float64(r.MemB * r.MemB)
		if r.CPUM > cpuMax {
			cpuMax = r.CPUM
		}
		if r.MemB > memMax {
			memMax = r.MemB
		}
		cpus[i] = r.CPUM
		mems[i] = r.MemB
	}

	sort.Slice(cpus, func(i, j int) bool { return cpus[i] < cpus[j] })
	sort.Slice(mems, func(i, j int) bool { return mems[i] < mems[j] })

	getPercentile := func(sorted []int64, p float64) int64 {
		idx := int(p * float64(len(sorted)-1))
		return sorted[idx]
	}

	cpuAvg := float64(cpuSum) / float64(count)
	memAvg := float64(memSum) / float64(count)

	cpuVar := (cpuSumSq / float64(count)) - (cpuAvg * cpuAvg)
	memVar := (memSumSq / float64(count)) - (memAvg * memAvg)

	// Ensure variance isn't negative due to precision
	if cpuVar < 0 {
		cpuVar = 0
	}
	if memVar < 0 {
		memVar = 0
	}

	return AggBucket{
		PodID:       podID,
		Resolution:  resolution,
		BucketStart: bucketStart,
		SampleCount: count,

		CPUAvgM:    int64(math.Round(cpuAvg)),
		CPUMaxM:    cpuMax,
		CPUSTDDevM: math.Sqrt(cpuVar),
		CPUP50M:    getPercentile(cpus, 0.50),
		CPUP75M:    getPercentile(cpus, 0.75),
		CPUP90M:    getPercentile(cpus, 0.90),
		CPUP95M:    getPercentile(cpus, 0.95),
		CPUP99M:    getPercentile(cpus, 0.99),

		MemAvgB:    int64(math.Round(memAvg)),
		MemMaxB:    memMax,
		MemSTDDevB: math.Sqrt(memVar),
		MemP50B:    getPercentile(mems, 0.50),
		MemP75B:    getPercentile(mems, 0.75),
		MemP90B:    getPercentile(mems, 0.90),
		MemP95B:    getPercentile(mems, 0.95),
		MemP99B:    getPercentile(mems, 0.99),
	}
}

func (s *Store) compact1HTo1D(ctx context.Context, now time.Time, retentionDays int) error {
	cutoff := now.AddDate(0, 0, -retentionDays).Unix()
	rows, err := s.AggRowsOlderThan(ctx, "1h", cutoff)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	type groupKey struct {
		podID     int64
		dayBucket int64
	}

	groups := make(map[groupKey][]AggRow)
	for _, r := range rows {
		dayBucket := r.BucketStart / 86400 * 86400
		key := groupKey{podID: r.PodID, dayBucket: dayBucket}
		groups[key] = append(groups[key], r)
	}

	lastSeen := make(map[int64]int64)

	for key, groupRows := range groups {
		bucket := computeWeightedAggBucket(key.podID, "1d", key.dayBucket, groupRows)
		if err := s.UpsertAggBucket(ctx, bucket); err != nil {
			return err
		}

		maxSeenAt := int64(0)
		for _, r := range groupRows {
			// A 1h bucket starting at BucketStart covers up to BucketStart + 3599
			endOfBucket := r.BucketStart + 3599
			if endOfBucket > maxSeenAt {
				maxSeenAt = endOfBucket
			}
		}
		if maxSeenAt > lastSeen[key.podID] {
			lastSeen[key.podID] = maxSeenAt
		}
	}

	for podID, ls := range lastSeen {
		if err := s.UpdateLastSeenAt(ctx, podID, ls); err != nil {
			return err
		}
	}

	return s.DeleteAggRowsBefore(ctx, "1h", cutoff)
}

func computeWeightedAggBucket(podID int64, resolution string, bucketStart int64, rows []AggRow) AggBucket {
	var totalCount int64
	var cpuSum, memSum float64
	var cpuMax, memMax int64

	for _, r := range rows {
		totalCount += r.SampleCount
		cpuSum += float64(r.CPUAvgM * r.SampleCount)
		memSum += float64(r.MemAvgB * r.SampleCount)
		if r.CPUMaxM > cpuMax {
			cpuMax = r.CPUMaxM
		}
		if r.MemMaxB > memMax {
			memMax = r.MemMaxB
		}
	}

	cpuAvg := cpuSum / float64(totalCount)
	memAvg := memSum / float64(totalCount)

	// stddev approximation: var = sum(count_i * (stddev_i² + (avg_i - global_avg)²)) / total_count
	var cpuVarSum, memVarSum float64
	for _, r := range rows {
		cpuDiff := float64(r.CPUAvgM) - cpuAvg
		cpuVarSum += float64(r.SampleCount) * (r.CPUSTDDevM*r.CPUSTDDevM + cpuDiff*cpuDiff)

		memDiff := float64(r.MemAvgB) - memAvg
		memVarSum += float64(r.SampleCount) * (r.MemSTDDevB*r.MemSTDDevB + memDiff*memDiff)
	}

	// Percentiles: weighted median of bucket percentiles (approximation)
	// We'll take the weighted average of each percentile.
	// Documentation note: Percentiles and StdDev at 1d tier are approximations.
	var cpuP50, cpuP75, cpuP90, cpuP95, cpuP99 float64
	var memP50, memP75, memP90, memP95, memP99 float64

	for _, r := range rows {
		w := float64(r.SampleCount) / float64(totalCount)
		cpuP50 += float64(r.CPUP50M) * w
		cpuP75 += float64(r.CPUP75M) * w
		cpuP90 += float64(r.CPUP90M) * w
		cpuP95 += float64(r.CPUP95M) * w
		cpuP99 += float64(r.CPUP99M) * w

		memP50 += float64(r.MemP50B) * w
		memP75 += float64(r.MemP75B) * w
		memP90 += float64(r.MemP90B) * w
		memP95 += float64(r.MemP95B) * w
		memP99 += float64(r.MemP99B) * w
	}

	return AggBucket{
		PodID:       podID,
		Resolution:  resolution,
		BucketStart: bucketStart,
		SampleCount: totalCount,

		CPUAvgM:    int64(math.Round(cpuAvg)),
		CPUMaxM:    cpuMax,
		CPUSTDDevM: math.Sqrt(cpuVarSum / float64(totalCount)),
		CPUP50M:    int64(math.Round(cpuP50)),
		CPUP75M:    int64(math.Round(cpuP75)),
		CPUP90M:    int64(math.Round(cpuP90)),
		CPUP95M:    int64(math.Round(cpuP95)),
		CPUP99M:    int64(math.Round(cpuP99)),

		MemAvgB:    int64(math.Round(memAvg)),
		MemMaxB:    memMax,
		MemSTDDevB: math.Sqrt(memVarSum / float64(totalCount)),
		MemP50B:    int64(math.Round(memP50)),
		MemP75B:    int64(math.Round(memP75)),
		MemP90B:    int64(math.Round(memP90)),
		MemP95B:    int64(math.Round(memP95)),
		MemP99B:    int64(math.Round(memP99)),
	}
}
