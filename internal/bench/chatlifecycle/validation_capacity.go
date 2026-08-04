package chatlifecycle

import (
	"strings"
)

func validateCapacity(c CapacityConfig, profile Profile, mode Mode) error {
	if c.StartRatePerSecond <= 0 {
		return fieldError("capacity.start_rate_per_second", "must be greater than zero")
	}
	if c.RecoveryRatePerSecond <= 0 {
		return fieldError("capacity.recovery_rate_per_second", "must be greater than zero")
	}
	if c.StepPercent < 1 || c.StepPercent > 100 {
		return fieldError("capacity.step_percent", "must be in 1..100")
	}
	if c.RefinePercent < 1 || c.RefinePercent > 100 {
		return fieldError("capacity.refine_percent", "must be in 1..100")
	}
	if c.Step.Stabilize <= 0 {
		return fieldError("capacity.step.stabilize", "must be greater than zero")
	}
	if c.Step.Measure <= 0 {
		return fieldError("capacity.step.measure", "must be greater than zero")
	}
	if c.RecoveryDuration <= 0 {
		return fieldError("capacity.recovery_duration", "must be greater than zero")
	}
	if profile == ProfileFormal && mode != ModeCapacity {
		if err := validateFormalCapacity(c); err != nil {
			return err
		}
	}
	stepDuration, ok := checkedAddPositiveDuration(c.Step.Stabilize, c.Step.Measure)
	if !ok {
		return fieldError("capacity.step", "stabilize plus measure exceeds supported duration")
	}
	if stepDuration != capacityStepDuration {
		return fieldError("capacity.step", "stabilize plus measure must equal 30m0s")
	}
	if mode != ModeCapacity {
		return nil
	}
	if profile != ProfileFormal {
		return fieldError("profile", "must be formal in capacity mode")
	}
	if err := validateFormalCapacity(c); err != nil {
		return err
	}
	if strings.TrimSpace(c.AgedCheckpoint.Reference) == "" {
		return fieldError("capacity.aged_checkpoint.reference", "is required in capacity profile")
	}
	if !c.AgedCheckpoint.Completed {
		return fieldError("capacity.aged_checkpoint.completed", "must be true in capacity profile")
	}
	if !c.AgedCheckpoint.Passed {
		return fieldError("capacity.aged_checkpoint.passed", "must be true in capacity profile")
	}
	if c.AgedCheckpoint.Duration < formalCheckpointDuration {
		return fieldError("capacity.aged_checkpoint.duration", "must be at least 72h0m0s in capacity profile")
	}
	return nil
}

func validateFormalCapacity(c CapacityConfig) error {
	expected := FormalConfig().Capacity
	if c.StartRatePerSecond != expected.StartRatePerSecond {
		return formalError("capacity.start_rate_per_second")
	}
	if c.RecoveryRatePerSecond != expected.RecoveryRatePerSecond {
		return formalError("capacity.recovery_rate_per_second")
	}
	if c.StepPercent != expected.StepPercent {
		return formalError("capacity.step_percent")
	}
	if c.RefinePercent != expected.RefinePercent {
		return formalError("capacity.refine_percent")
	}
	if c.Step.Stabilize != expected.Step.Stabilize {
		return formalError("capacity.step.stabilize")
	}
	if c.Step.Measure != expected.Step.Measure {
		return formalError("capacity.step.measure")
	}
	if c.RecoveryDuration != expected.RecoveryDuration {
		return formalError("capacity.recovery_duration")
	}
	return nil
}
