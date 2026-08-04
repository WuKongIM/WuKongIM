package chatlifecycle

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestRetryAttemptsReuseIdentityAndExactBases(t *testing.T) {
	cfg := FormalConfig()
	model := newTestTrafficModel(t, cfg)
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	policy, err := NewRetryPolicy(identity, cfg.Workload.Retry)
	if err != nil {
		t.Fatalf("NewRetryPolicy() error = %v", err)
	}
	logical, err := model.NewLogicalSend(1, 123, TrafficPerson, "sender", "target")
	if err != nil {
		t.Fatalf("NewLogicalSend() error = %v", err)
	}

	wantBases := []time.Duration{0, 100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
	for attempt := uint8(0); attempt <= 3; attempt++ {
		plan, err := policy.Attempt(logical, attempt)
		if err != nil {
			t.Fatalf("Attempt(%d) error = %v", attempt, err)
		}
		if plan.ClientMsgNo != logical.ClientMsgNo {
			t.Fatalf("attempt %d client_msg_no = %q, want %q", attempt, plan.ClientMsgNo, logical.ClientMsgNo)
		}
		if plan.BaseDelay != wantBases[attempt] {
			t.Fatalf("attempt %d base = %s, want %s", attempt, plan.BaseDelay, wantBases[attempt])
		}
		if attempt == 0 && (plan.Jitter != 0 || plan.Delay != 0) {
			t.Fatalf("attempt zero timing = %+v, want no retry delay", plan)
		}
		if attempt > 0 {
			if plan.Jitter < 0 || plan.Jitter > plan.BaseDelay/5 {
				t.Fatalf("attempt %d jitter = %s, want nonnegative <= %s", attempt, plan.Jitter, plan.BaseDelay/5)
			}
			if plan.Delay != plan.BaseDelay+plan.Jitter {
				t.Fatalf("attempt %d delay = %s, want base+jitter %s", attempt, plan.Delay, plan.BaseDelay+plan.Jitter)
			}
			again, err := policy.Attempt(logical, attempt)
			if err != nil || again != plan {
				t.Fatalf("Attempt(%d) replay = %+v, %v; want %+v", attempt, again, err, plan)
			}
		}
	}
	if _, err := policy.Attempt(logical, 4); !errors.Is(err, errRetryAttempt) {
		t.Fatalf("Attempt(4) error = %v, want %v", err, errRetryAttempt)
	}
}

func TestRetryPolicyRejectsInvalidInputsAndDurationOverflow(t *testing.T) {
	cfg := FormalConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	if _, err := NewRetryPolicy(nil, cfg.Workload.Retry); !errors.Is(err, errRetryIdentityRequired) {
		t.Fatalf("NewRetryPolicy(nil) error = %v, want %v", err, errRetryIdentityRequired)
	}
	bad := cfg.Workload.Retry
	bad.Delays = append([]time.Duration(nil), bad.Delays...)
	bad.Delays[0]++
	if _, err := NewRetryPolicy(identity, bad); !errors.Is(err, errRetryConfig) {
		t.Fatalf("NewRetryPolicy(bad delay) error = %v, want %v", err, errRetryConfig)
	}
	policy, err := NewRetryPolicy(identity, cfg.Workload.Retry)
	if err != nil {
		t.Fatalf("NewRetryPolicy() error = %v", err)
	}
	if _, err := policy.Attempt(LogicalSend{}, 0); !errors.Is(err, errRetryLogicalIdentity) {
		t.Fatalf("Attempt(empty logical) error = %v, want %v", err, errRetryLogicalIdentity)
	}
	if _, err := checkedRetryDelay(time.Duration(math.MaxInt64), 1); !errors.Is(err, errRetryDelayOverflow) {
		t.Fatalf("checkedRetryDelay(overflow) error = %v, want %v", err, errRetryDelayOverflow)
	}
}
