// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package main

import (
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := loadConfig()
	if cfg.MisconfigWindowS != 3600 {
		t.Errorf("expected default MisconfigWindowS=3600, got %d", cfg.MisconfigWindowS)
	}
}

func TestLoadConfig_MisconfigWindowS(t *testing.T) {
	t.Setenv("WINSTON_MISCONFIG_WINDOW_S", "7200")
	cfg := loadConfig()
	if cfg.MisconfigWindowS != 7200 {
		t.Errorf("expected MisconfigWindowS=7200, got %d", cfg.MisconfigWindowS)
	}
}
