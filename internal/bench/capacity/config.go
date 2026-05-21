package capacity

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	// ProfilePerson measures one-to-one send path capacity.
	ProfilePerson = "person"
	// ProfileGroup measures group send path capacity.
	ProfileGroup = "group"
	// ProfileMixed splits offered ingress load across person and group traffic.
	ProfileMixed = "mixed"
)

// Config controls one capacity send search.
type Config struct {
	// APIAddrs are HTTP API base addresses for already-running target nodes.
	APIAddrs []string
	// GatewayTCPAddrs optionally overrides discovered WKProto TCP gateway addresses.
	GatewayTCPAddrs []string
	// BenchToken is an optional bearer token for bench API routes.
	BenchToken string
	// Profile selects person, group, or mixed traffic.
	Profile string
	// StartQPS is the first offered ingress QPS attempted.
	StartQPS float64
	// MaxQPS is the maximum offered ingress QPS attempted.
	MaxQPS float64
	// StepFactor multiplies passing ramp attempts.
	StepFactor float64
	// Duration is the measured run duration per attempt.
	Duration time.Duration
	// Warmup is the warmup duration per attempt.
	Warmup time.Duration
	// Cooldown is the cooldown duration per attempt.
	Cooldown time.Duration
	// StableP99 is the maximum allowed run-phase sendack p99 latency.
	StableP99 time.Duration
	// MinActualRatio is the minimum actual/offered QPS ratio for pass.
	MinActualRatio float64
	// MaxSendackErrorRate is the maximum allowed run send error rate.
	MaxSendackErrorRate float64
	// MaxConnectErrorRate is the maximum allowed connect error rate.
	MaxConnectErrorRate float64
	// BinarySearch enables refinement after a ramp failure brackets capacity.
	BinarySearch bool
	// BinarySearchMinDeltaRatio stops binary search when bracket width is small enough.
	BinarySearchMinDeltaRatio float64
	// GroupMembers is the number of members per generated group channel.
	GroupMembers int
	// ReportDir is the root directory for capacity reports.
	ReportDir string
}

// DefaultConfig returns laptop-safe defaults for capacity send searches.
func DefaultConfig() Config {
	return Config{
		Profile:                   ProfileMixed,
		StartQPS:                  100,
		MaxQPS:                    5000,
		StepFactor:                1.5,
		Duration:                  30 * time.Second,
		Warmup:                    10 * time.Second,
		Cooldown:                  3 * time.Second,
		StableP99:                 200 * time.Millisecond,
		MinActualRatio:            0.95,
		MaxSendackErrorRate:       0,
		MaxConnectErrorRate:       0,
		BinarySearch:              true,
		BinarySearchMinDeltaRatio: 0.05,
		GroupMembers:              10,
		ReportDir:                 "./tmp/wkbench-capacity",
	}
}

// Validate checks static capacity config before discovery or execution.
func (c Config) Validate() error {
	if len(nonEmptyStrings(c.APIAddrs)) == 0 {
		return fmt.Errorf("api addresses are required")
	}
	switch strings.TrimSpace(c.Profile) {
	case ProfilePerson, ProfileGroup, ProfileMixed:
	default:
		return fmt.Errorf("profile must be one of %s, %s, %s", ProfilePerson, ProfileGroup, ProfileMixed)
	}
	if !positiveFinite(c.StartQPS) {
		return fmt.Errorf("start-qps must be greater than zero")
	}
	if !positiveFinite(c.MaxQPS) {
		return fmt.Errorf("max-qps must be greater than zero")
	}
	if c.MaxQPS < c.StartQPS {
		return fmt.Errorf("max-qps must be greater than or equal to start-qps")
	}
	if !positiveFinite(c.StepFactor) || c.StepFactor <= 1 {
		return fmt.Errorf("step-factor must be greater than 1")
	}
	if c.Duration <= 0 {
		return fmt.Errorf("duration must be greater than zero")
	}
	if c.Warmup <= 0 {
		return fmt.Errorf("warmup must be greater than zero")
	}
	if c.Cooldown < 0 {
		return fmt.Errorf("cooldown must not be negative")
	}
	if c.StableP99 <= 0 {
		return fmt.Errorf("stable-p99 must be greater than zero")
	}
	if !positiveFinite(c.MinActualRatio) || c.MinActualRatio > 1 {
		return fmt.Errorf("min-actual-ratio must be in (0,1]")
	}
	if c.MaxSendackErrorRate < 0 || math.IsNaN(c.MaxSendackErrorRate) || math.IsInf(c.MaxSendackErrorRate, 0) {
		return fmt.Errorf("max-sendack-error-rate must not be negative")
	}
	if c.MaxConnectErrorRate < 0 || math.IsNaN(c.MaxConnectErrorRate) || math.IsInf(c.MaxConnectErrorRate, 0) {
		return fmt.Errorf("max-connect-error-rate must not be negative")
	}
	if !positiveFinite(c.BinarySearchMinDeltaRatio) {
		return fmt.Errorf("binary-search-min-delta-ratio must be greater than zero")
	}
	if c.GroupMembers <= 0 {
		return fmt.Errorf("group-members must be greater than zero")
	}
	return nil
}

func positiveFinite(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func nonEmptyStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
