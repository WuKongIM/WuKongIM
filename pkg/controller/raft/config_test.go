package raft

import (
	"testing"
	"time"
)

func TestConfigDefaultsTickIntervalTo100Milliseconds(t *testing.T) {
	cfg := Config{}.normalized()
	if cfg.TickInterval != 100*time.Millisecond {
		t.Fatalf("TickInterval default = %s, want 100ms", cfg.TickInterval)
	}
}
