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
	return validateCapacity(c.Capacity, c.Profile, c.Mode)
}

func fieldError(path, reason string) error { return fmt.Errorf("%s: %s", path, reason) }
