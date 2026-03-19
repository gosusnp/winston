// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package main

import (
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := loadConfig()
	if cfg.PodTTLS != 3600 {
		t.Errorf("expected default PodTTLS=3600, got %d", cfg.PodTTLS)
	}
}

func TestLoadConfig_PodTTLS(t *testing.T) {
	t.Setenv("WINSTON_POD_TTL_S", "7200")
	cfg := loadConfig()
	if cfg.PodTTLS != 7200 {
		t.Errorf("expected PodTTLS=7200, got %d", cfg.PodTTLS)
	}
}
