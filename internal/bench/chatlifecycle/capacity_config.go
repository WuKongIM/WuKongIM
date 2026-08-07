package chatlifecycle

import (
	"strings"
	"time"
)

// PrepareCapacityConfig derives the only valid capacity configuration from an
// exact passing formal report. It changes no workload, observation, threshold,
// identity, or staircase value besides selecting capacity mode and binding the
// aged-checkpoint reference.
func PrepareCapacityConfig(formal Config, checkpoint Report, reference string) (Config, error) {
	if strings.TrimSpace(reference) == "" || formal.Validate() != nil || formal.Profile != ProfileFormal ||
		formal.Mode != ModeSoak || formal.Stage != StageFormal || validateReport(checkpoint) != nil ||
		checkpoint.Profile != ProfileFormal || checkpoint.Mode != ModeSoak || checkpoint.Stage != StageFormal ||
		checkpoint.Kind != CheckpointFinal || !checkpoint.Final || checkpoint.Continue ||
		!checkpoint.Verdict.Terminal || checkpoint.Verdict.Outcome != VerdictPass ||
		checkpoint.Verdict.Cause != VerdictCauseCompleted || checkpoint.Window.Elapsed < 72*time.Hour ||
		checkpoint.Capacity.Attempted {
		return Config{}, ErrCapacityAdmission
	}
	digest, err := digestCheckpointConfig(formal)
	if err != nil || digest != checkpoint.ConfigDigest {
		return Config{}, ErrCapacityAdmission
	}
	prepared := formal
	prepared.Mode = ModeCapacity
	prepared.Capacity.AgedCheckpoint = AgedCheckpoint{
		Reference: reference, Completed: true, Passed: true, Duration: checkpoint.Window.Elapsed,
	}
	if err := prepared.Validate(); err != nil {
		return Config{}, ErrCapacityAdmission
	}
	return prepared, nil
}
