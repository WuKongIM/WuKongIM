package model

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Rate describes a normalized event rate in operations per second.
type Rate struct {
	// PerSecond is the number of operations scheduled for each second.
	PerSecond float64 `json:"per_second" yaml:"per_second"`
}

// ParseRate parses a per-second rate expression such as "5000/s".
func ParseRate(raw string) (Rate, error) {
	value := strings.TrimSpace(raw)
	if !strings.HasSuffix(value, "/s") {
		return Rate{}, fmt.Errorf("rate %q must use /s suffix", raw)
	}
	number := strings.TrimSpace(strings.TrimSuffix(value, "/s"))
	if number == "" {
		return Rate{}, fmt.Errorf("rate %q missing numeric value", raw)
	}
	perSecond, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return Rate{}, fmt.Errorf("parse rate %q: %w", raw, err)
	}
	if perSecond <= 0 || math.IsNaN(perSecond) || math.IsInf(perSecond, 0) {
		return Rate{}, fmt.Errorf("rate %q must be finite and greater than zero", raw)
	}
	return Rate{PerSecond: perSecond}, nil
}

// UnmarshalYAML accepts compact rate strings in scenario YAML files.
func (r *Rate) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	parsed, err := ParseRate(raw)
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}
