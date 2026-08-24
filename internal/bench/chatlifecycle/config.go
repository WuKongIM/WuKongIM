package chatlifecycle

import (
	"fmt"
	"strings"
)

// Validate checks static deterministic configuration before planning or I/O.
func (c Config) Validate() error {
	if strings.TrimSpace(c.RunID) == "" {
		return fieldError("run_id", "is required")
	}
	if c.Seed == 0 {
		return fieldError("seed", "must be nonzero")
	}
	if c.Profile != ProfileFormal && c.Profile != ProfileLocal {
		return fieldError("profile", "must be formal or local")
	}
	if c.Mode != ModeSoak && c.Mode != ModeCapacity {
		return fieldError("mode", "must be soak or capacity")
	}
	if err := validateWorkload(c.Workload, c.Profile); err != nil {
		return err
	}
	if err := validateObservation(c.Observation); err != nil {
		return err
	}
	if err := validateThresholds(c.Thresholds); err != nil {
		return err
	}
	if c.Profile == ProfileFormal {
		if err := validateFormalDefaults(c); err != nil {
			return err
		}
	} else if err := validateLocalObservationShape(c.Observation); err != nil {
		return err
	}
	if c.Workload.Workers != len(c.Observation.Workers) {
		return fieldError("workload.workers", "must equal observation worker count")
	}
	if err := validateCapacity(c.Capacity, c.Profile, c.Mode); err != nil {
		return err
	}
	switch c.Stage {
	case StageFormal:
		if c.Profile != ProfileFormal {
			return fieldError("stage", "formal requires the formal profile")
		}
	case StageRehearsal:
		if c.Profile != ProfileFormal || c.Mode != ModeSoak {
			return fieldError("stage", "rehearsal requires the formal soak profile")
		}
	case StageShakeout:
		if c.Profile != ProfileLocal || c.Mode != ModeSoak {
			return fieldError("stage", "shakeout requires the local soak profile")
		}
	default:
		return fieldError("stage", "must be formal, rehearsal, or shakeout")
	}
	if c.RunDuration != 0 {
		if c.Stage != StageRehearsal {
			return fieldError("run_duration", "is allowed only for a direct rehearsal")
		}
		if c.RunDuration < minDirectRunDuration || c.RunDuration > maxDirectRunDuration {
			return fieldError("run_duration", "must be between 16m and 72h15m")
		}
	}
	return nil
}

func fieldError(path, reason string) error { return fmt.Errorf("%s: %s", path, reason) }
