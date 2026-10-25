package chatlifecycle

import (
	"testing"
	"time"
)

func TestShortLocalDiagnosticDoesNotRequirePositiveQualificationDuration(t *testing.T) {
	cfg := LocalConfig()
	cfg.Thresholds.Timeline = TimelineThresholds{LatencyWarmup: time.Minute, Warmup: time.Minute, Checkpoint: 2 * time.Minute, Final: 3 * time.Minute}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("short local diagnostic rejected before it can gather insufficient evidence: %v", err)
	}
}
