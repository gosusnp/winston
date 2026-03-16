// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package store

type PodMeta struct {
	Namespace     string
	PodName       string
	ContainerName string
	OwnerKind     string
	OwnerName     string
	CPURequestM   int64
	CPULimitM     int64
	MemRequestB   int64
	MemLimitB     int64
	FirstSeenAt   int64
	LastSeenAt    int64
}

type LatestRawRow struct {
	PodMeta
	PodID      int64
	CapturedAt int64
	CPUM       int64
	MemB       int64
}

type RawRow struct {
	PodID      int64
	CapturedAt int64
	CPUM       int64
	MemB       int64
}

type AggBucket struct {
	PodID       int64
	Resolution  string
	BucketStart int64
	SampleCount int64

	CPUAvgM    int64
	CPUMaxM    int64
	CPUSTDDevM float64
	CPUP50M    int64
	CPUP75M    int64
	CPUP90M    int64
	CPUP95M    int64
	CPUP99M    int64

	MemAvgB    int64
	MemMaxB    int64
	MemSTDDevB float64
	MemP50B    int64
	MemP75B    int64
	MemP90B    int64
	MemP95B    int64
	MemP99B    int64
}

type AggRow struct {
	AggBucket
	Namespace     string
	OwnerKind     string
	OwnerName     string
	ContainerName string
	CPURequestM   int64
	CPULimitM     int64
	MemRequestB   int64
	MemLimitB     int64
}
