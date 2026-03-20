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
	if cfg.RetentionRawS != 86400 {
		t.Errorf("expected default RetentionRawS=86400, got %d", cfg.RetentionRawS)
	}
	if cfg.Retention1HS != 604800 {
		t.Errorf("expected default Retention1HS=604800, got %d", cfg.Retention1HS)
	}
	if cfg.Retention1DS != 2592000 {
		t.Errorf("expected default Retention1DS=2592000, got %d", cfg.Retention1DS)
	}
}

func TestLoadConfig_PodTTLS(t *testing.T) {
	t.Setenv("WINSTON_POD_TTL_S", "7200")
	cfg := loadConfig()
	if cfg.PodTTLS != 7200 {
		t.Errorf("expected PodTTLS=7200, got %d", cfg.PodTTLS)
	}
}

func TestLoadConfig_RetentionEnvVars(t *testing.T) {
	t.Setenv("WINSTON_RETENTION_RAW_S", "43200")
	t.Setenv("WINSTON_RETENTION_1H_S", "172800")
	t.Setenv("WINSTON_RETENTION_1D_S", "1296000")
	cfg := loadConfig()
	if cfg.RetentionRawS != 43200 {
		t.Errorf("expected RetentionRawS=43200, got %d", cfg.RetentionRawS)
	}
	if cfg.Retention1HS != 172800 {
		t.Errorf("expected Retention1HS=172800, got %d", cfg.Retention1HS)
	}
	if cfg.Retention1DS != 1296000 {
		t.Errorf("expected Retention1DS=1296000, got %d", cfg.Retention1DS)
	}
}
