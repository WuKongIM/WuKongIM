package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestChannelRuntimeProbeFailureKeepsCauseAndClosedReason(t *testing.T) {
	cause := errors.New("private runtime detail")
	for _, reason := range []ChannelRuntimeProbeFailureReason{
		ChannelRuntimeProbeFailureDeadline,
		ChannelRuntimeProbeFailureCanceled,
		ChannelRuntimeProbeFailureRuntimeUnavailable,
		ChannelRuntimeProbeFailureInvalidEvidence,
		ChannelRuntimeProbeFailureInternal,
	} {
		t.Run(string(reason), func(t *testing.T) {
			err := &ChannelRuntimeProbeFailure{Reason: reason, Cause: cause}
			if got := ChannelRuntimeProbeFailureReasonOf(fmt.Errorf("probe: %w", err)); got != reason {
				t.Fatalf("failure reason = %q, want %q", got, reason)
			}
			if !errors.Is(err, cause) {
				t.Fatal("probe failure did not retain its private cause")
			}
			if strings.Contains(err.Error(), cause.Error()) {
				t.Fatalf("safe diagnostic exposed private cause: %q", err.Error())
			}
		})
	}

	var nilFailure *ChannelRuntimeProbeFailure
	if got := nilFailure.Error(); got != "channel runtime probe failed: internal" {
		t.Fatalf("nil failure diagnostic = %q", got)
	}
	if got := nilFailure.Unwrap(); got != nil {
		t.Fatalf("nil failure unwrap = %v", got)
	}
	if got := ChannelRuntimeProbeFailureReasonOf(errors.New("other")); got != "" {
		t.Fatalf("unrelated error reason = %q", got)
	}
}

func TestFixedTrafficRetryBudget(t *testing.T) {
	want := 100*time.Millisecond + 500*time.Millisecond + 2*time.Second
	if got := TrafficRetryDelayBudget(); got != want {
		t.Fatalf("retry delay budget = %s, want %s", got, want)
	}
	if TrafficRetryMaximumAttempts != TrafficRetryMaximumRetries+1 {
		t.Fatalf("maximum attempts = %d, retries = %d", TrafficRetryMaximumAttempts, TrafficRetryMaximumRetries)
	}
}

func TestRangeLenUsesHalfOpenBounds(t *testing.T) {
	if got := (Range{Start: 11, End: 19}).Len(); got != 8 {
		t.Fatalf("range length = %d, want 8", got)
	}
	if got := (Range{Start: 11, End: 11}).Len(); got != 0 {
		t.Fatalf("empty range length = %d, want 0", got)
	}
}

func TestRateYAMLContract(t *testing.T) {
	var rate Rate
	if err := rate.UnmarshalYAML(func(value interface{}) error {
		pointer, ok := value.(*string)
		if !ok {
			return fmt.Errorf("unexpected target %T", value)
		}
		*pointer = " 25.5/s "
		return nil
	}); err != nil {
		t.Fatalf("UnmarshalYAML() error = %v", err)
	}
	if rate.PerSecond != 25.5 {
		t.Fatalf("decoded rate = %v/s, want 25.5/s", rate.PerSecond)
	}

	decodeErr := errors.New("yaml scalar required")
	if err := rate.UnmarshalYAML(func(interface{}) error { return decodeErr }); !errors.Is(err, decodeErr) {
		t.Fatalf("decode error = %v, want %v", err, decodeErr)
	}
	if err := rate.UnmarshalYAML(func(value interface{}) error {
		*(value.(*string)) = "invalid"
		return nil
	}); err == nil {
		t.Fatal("invalid rate unexpectedly decoded")
	}

	for _, raw := range []string{"/s", "not-a-number/s"} {
		if _, err := ParseRate(raw); err == nil {
			t.Fatalf("ParseRate(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestTCPSourcePoolValidationAndCapacity(t *testing.T) {
	valid := TCPSourceConfig{
		IPv4Addrs: []string{" 192.0.2.10 ", "198.51.100.20"},
		PortMin:   2000,
		PortMax:   2002,
	}
	if err := ValidateTCPSourceConfig(&valid); err != nil {
		t.Fatalf("valid source pool rejected: %v", err)
	}
	if got := TCPSourceCapacity(&valid); got != 6 {
		t.Fatalf("source capacity = %d, want 6", got)
	}

	tests := []struct {
		name string
		cfg  *TCPSourceConfig
	}{
		{name: "empty addresses", cfg: &TCPSourceConfig{PortMin: 2000, PortMax: 2001}},
		{name: "malformed address", cfg: &TCPSourceConfig{IPv4Addrs: []string{"invalid"}, PortMin: 2000, PortMax: 2001}},
		{name: "ipv6 address", cfg: &TCPSourceConfig{IPv4Addrs: []string{"2001:db8::1"}, PortMin: 2000, PortMax: 2001}},
		{name: "unspecified address", cfg: &TCPSourceConfig{IPv4Addrs: []string{"0.0.0.0"}, PortMin: 2000, PortMax: 2001}},
		{name: "duplicate address", cfg: &TCPSourceConfig{IPv4Addrs: []string{"192.0.2.1", " 192.0.2.1 "}, PortMin: 2000, PortMax: 2001}},
		{name: "privileged port", cfg: &TCPSourceConfig{IPv4Addrs: []string{"192.0.2.1"}, PortMin: 1023, PortMax: 2001}},
		{name: "port overflow", cfg: &TCPSourceConfig{IPv4Addrs: []string{"192.0.2.1"}, PortMin: 2000, PortMax: 65536}},
		{name: "reversed ports", cfg: &TCPSourceConfig{IPv4Addrs: []string{"192.0.2.1"}, PortMin: 2001, PortMax: 2000}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateTCPSourceConfig(test.cfg); err == nil {
				t.Fatal("invalid source pool unexpectedly accepted")
			}
		})
	}
	if err := ValidateTCPSourceConfig(nil); err != nil {
		t.Fatalf("omitted source pool rejected: %v", err)
	}
	for name, cfg := range map[string]*TCPSourceConfig{
		"nil":      nil,
		"empty":    {},
		"reversed": {IPv4Addrs: []string{"192.0.2.1"}, PortMin: 2001, PortMax: 2000},
	} {
		t.Run("zero capacity "+name, func(t *testing.T) {
			if got := TCPSourceCapacity(cfg); got != 0 {
				t.Fatalf("invalid source capacity = %d, want 0", got)
			}
		})
	}
}

func TestTerminalFenceFormattingAlwaysRedactsCapability(t *testing.T) {
	const capability = "secret-terminal-capability"
	grant := TerminalFenceGrant{
		Version:          TerminalFenceVersion,
		RunID:            "run-1",
		AssignmentID:     "assignment-2",
		ExpectedSessions: 3,
		Epoch:            4,
		Capability:       capability,
	}
	for name, diagnostic := range map[string]string{
		"string":    grant.String(),
		"go string": grant.GoString(),
		"fmt value": fmt.Sprintf("%v", grant),
		"fmt debug": fmt.Sprintf("%#v", grant),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(diagnostic, capability) || !strings.Contains(diagnostic, "capability:[redacted]") {
				t.Fatalf("unsafe terminal fence diagnostic: %q", diagnostic)
			}
		})
	}
}

func TestDigestScenarioRejectsNonFiniteEffectiveRate(t *testing.T) {
	scenario := Scenario{Objectives: ObjectivesConfig{IngressQPS: Rate{PerSecond: math.NaN()}}}
	if _, err := DigestScenario(scenario); err == nil || !strings.Contains(err.Error(), "marshal effective scenario") {
		t.Fatalf("DigestScenario() error = %v", err)
	}
}

func TestFanoutProofRejectsMalformedDigests(t *testing.T) {
	for name, digest := range map[string]string{
		"short":     "abc",
		"uppercase": strings.Repeat("A", fanoutDigestHexLen),
		"non-hex":   strings.Repeat("g", fanoutDigestHexLen),
	} {
		t.Run(name, func(t *testing.T) {
			proof := FanoutProofNotRequired()
			proof.Required = true
			proof.Expected = FanoutMultisetSummary{Count: 1, DigestA: digest, DigestB: fanoutZeroDigest}
			if proof.Complete() {
				t.Fatal("malformed fanout digest unexpectedly accepted")
			}
		})
	}
}
