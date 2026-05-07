package multiraft

import (
	"testing"
	"time"
)

func TestNormalizeLogCompactionConfigDefaultsEnabled(t *testing.T) {
	got := NormalizeLogCompactionConfig(LogCompactionConfig{})
	if !got.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if got.TriggerEntries != defaultLogCompactionTriggerEntries {
		t.Fatalf("TriggerEntries = %d, want %d", got.TriggerEntries, defaultLogCompactionTriggerEntries)
	}
	if got.CheckInterval != defaultLogCompactionCheckInterval {
		t.Fatalf("CheckInterval = %v, want %v", got.CheckInterval, defaultLogCompactionCheckInterval)
	}
}

func TestNormalizeLogCompactionConfigPreservesExplicitDisabled(t *testing.T) {
	got := NormalizeLogCompactionConfig(LogCompactionConfig{Enabled: false, EnabledSet: true})
	if got.Enabled {
		t.Fatal("Enabled = true, want false")
	}
}

func TestValidateLogCompactionConfigRejectsEnabledInvalidValues(t *testing.T) {
	if err := ValidateLogCompactionConfig(LogCompactionConfig{Enabled: true, TriggerEntries: 0, CheckInterval: time.Second}); err == nil {
		t.Fatal("ValidateLogCompactionConfig zero trigger error = nil")
	}
	if err := ValidateLogCompactionConfig(LogCompactionConfig{Enabled: true, TriggerEntries: 1, CheckInterval: 0}); err == nil {
		t.Fatal("ValidateLogCompactionConfig zero interval error = nil")
	}
}
