package multiraft

import (
	"errors"
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

func TestNormalizeRaftOptionsDefaultsBackpressureLimits(t *testing.T) {
	got := NormalizeRaftOptions(RaftOptions{})
	if got.MaxQueuedRequests != defaultMaxQueuedRequests {
		t.Fatalf("MaxQueuedRequests = %d, want %d", got.MaxQueuedRequests, defaultMaxQueuedRequests)
	}
	if got.MaxQueuedControls != defaultMaxQueuedControls {
		t.Fatalf("MaxQueuedControls = %d, want %d", got.MaxQueuedControls, defaultMaxQueuedControls)
	}
	if got.MaxQueuedBackgroundControls != defaultMaxQueuedBackgroundControls {
		t.Fatalf("MaxQueuedBackgroundControls = %d, want %d", got.MaxQueuedBackgroundControls, defaultMaxQueuedBackgroundControls)
	}
	if got.MaxApplyingTasks != defaultMaxApplyingTasks {
		t.Fatalf("MaxApplyingTasks = %d, want %d", got.MaxApplyingTasks, defaultMaxApplyingTasks)
	}
}

func TestValidateRaftOptionsRejectsNegativeBackpressureLimits(t *testing.T) {
	base := NormalizeRaftOptions(RaftOptions{})
	for _, opts := range []RaftOptions{
		func() RaftOptions { next := base; next.MaxQueuedRequests = -1; return next }(),
		func() RaftOptions { next := base; next.MaxQueuedControls = -1; return next }(),
		func() RaftOptions { next := base; next.MaxQueuedBackgroundControls = -1; return next }(),
		func() RaftOptions { next := base; next.MaxApplyingTasks = -1; return next }(),
	} {
		if err := ValidateRaftOptions(opts); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("ValidateRaftOptions(%+v) error = %v, want %v", opts, err, ErrInvalidOptions)
		}
	}
}
