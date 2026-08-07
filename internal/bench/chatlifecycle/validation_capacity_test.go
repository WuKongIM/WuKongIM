package chatlifecycle

import (
	"math"
	"testing"
	"time"
)

func TestCapacityModeRequiresFormalEvidenceAndExactStaircase(t *testing.T) {
	validCheckpoint := AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"local profile", func(c *Config) { c.Profile = ProfileLocal }, "profile: must be formal in capacity mode"},
		{"start rate", func(c *Config) { c.Capacity.StartRatePerSecond = 2_001 }, "capacity.start_rate_per_second: must equal formal default"},
		{"recovery rate", func(c *Config) { c.Capacity.RecoveryRatePerSecond = 2_001 }, "capacity.recovery_rate_per_second: must equal formal default"},
		{"step percent", func(c *Config) { c.Capacity.StepPercent = 26 }, "capacity.step_percent: must equal formal default"},
		{"maximum duration", func(c *Config) { c.Capacity.MaximumDuration = 9 * time.Hour }, "capacity.maximum_duration: must equal formal default"},
		{"recovery", func(c *Config) { c.Capacity.RecoveryDuration = 31 * time.Minute }, "capacity.recovery_duration: must equal formal default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeCapacity
			cfg.Capacity.AgedCheckpoint = validCheckpoint
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFormalSoakRequiresExactCapacityLeaves(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"start rate", func(c *Config) { c.Capacity.StartRatePerSecond = 2_001 }, "capacity.start_rate_per_second: must equal formal default"},
		{"recovery rate", func(c *Config) { c.Capacity.RecoveryRatePerSecond = 2_001 }, "capacity.recovery_rate_per_second: must equal formal default"},
		{"step percent", func(c *Config) { c.Capacity.StepPercent = 26 }, "capacity.step_percent: must equal formal default"},
		{"refine percent", func(c *Config) { c.Capacity.RefinePercent = 11 }, "capacity.refine_percent: must equal formal default"},
		{"stabilize", func(c *Config) { c.Capacity.Step.Stabilize = 11 * time.Minute }, "capacity.step.stabilize: must equal formal default"},
		{"measure", func(c *Config) { c.Capacity.Step.Measure = 21 * time.Minute }, "capacity.step.measure: must equal formal default"},
		{"maximum duration", func(c *Config) { c.Capacity.MaximumDuration = 7 * time.Hour }, "capacity.maximum_duration: must equal formal default"},
		{"recovery duration", func(c *Config) { c.Capacity.RecoveryDuration = 31 * time.Minute }, "capacity.recovery_duration: must equal formal default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			if cfg.Mode != ModeSoak {
				t.Fatalf("Mode = %q, want %q", cfg.Mode, ModeSoak)
			}
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidateCapacityAgedCheckpoint(t *testing.T) {
	validCheckpoint := AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
	tests := []struct {
		name       string
		checkpoint AgedCheckpoint
		want       string
	}{
		{"missing", AgedCheckpoint{}, "capacity.aged_checkpoint.reference: is required in capacity profile"},
		{"incomplete", AgedCheckpoint{Reference: "checkpoint", Passed: true, Duration: 72 * time.Hour}, "capacity.aged_checkpoint.completed: must be true in capacity profile"},
		{"failed", AgedCheckpoint{Reference: "checkpoint", Completed: true, Duration: 72 * time.Hour}, "capacity.aged_checkpoint.passed: must be true in capacity profile"},
		{"too short", AgedCheckpoint{Reference: "checkpoint", Completed: true, Passed: true, Duration: 71*time.Hour + 59*time.Minute}, "capacity.aged_checkpoint.duration: must be at least 72h0m0s in capacity profile"},
		{"valid", validCheckpoint, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeCapacity
			cfg.Capacity.AgedCheckpoint = tt.checkpoint
			err := cfg.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidateCapacityStaircase(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"zero step", func(c *Config) { c.Capacity.StepPercent = 0 }, "capacity.step_percent: must be in 1..100"},
		{"zero stabilize", func(c *Config) { c.Capacity.Step.Stabilize = 0 }, "capacity.step.stabilize: must be greater than zero"},
		{"wrong step total", func(c *Config) { c.Capacity.Step.Measure = 19 * time.Minute }, "capacity.step: stabilize plus measure must equal 30m0s"},
		{"zero recovery", func(c *Config) { c.Capacity.RecoveryDuration = 0 }, "capacity.recovery_duration: must be greater than zero"},
		{"zero maximum", func(c *Config) { c.Capacity.MaximumDuration = 0 }, "capacity.maximum_duration: must be greater than zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeCapacity
			cfg.Capacity.AgedCheckpoint = AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCapacityStepDurationRejectsOverflow(t *testing.T) {
	cfg := LocalConfig()
	cfg.Capacity.Step.Stabilize = time.Duration(math.MaxInt64)
	cfg.Capacity.Step.Measure = time.Nanosecond

	want := "capacity.step: stabilize plus measure exceeds supported duration"
	if err := cfg.Validate(); err == nil || err.Error() != want {
		t.Fatalf("Validate() error = %v, want %q", err, want)
	}
}
