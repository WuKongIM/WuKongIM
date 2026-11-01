package benchmark

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

// Missing final reports must not discard the rejected tick's fixed clock facts.
func TestChatLifecycleGrantTickSummaryKeepsCompatibilityAndBoundedFacts(t *testing.T) {
	result := chatlifecycle.CoordinatorResult{Outcome: chatlifecycle.CoordinatorHarnessInvalid, Code: chatlifecycle.CoordinatorCodeGrant, GrantFailure: chatlifecycle.CoordinatorGrantFailureTick}
	if err := json.Unmarshal([]byte(`{"GrantTickFailure":{"boundary":"scheduled_grant","reason":"stale","have_last_tick":true,"age_observed":true,"age_nanos":1300000000,"interval_observed":true,"interval_nanos":1000000000}}`), &result); err != nil {
		t.Fatal(err)
	}
	verdict := chatLifecycleCoordinatorVerdict(result)
	summary := chatLifecycleCoordinatorSummary(verdict, result, "unavailable")
	const original = "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=grant grant_failure_code=tick worker_runtime_code= observer_code= preflight_code= report=unavailable"
	lines := strings.Split(strings.TrimSuffix(summary, "\n"), "\n")
	if len(lines) != 2 || lines[0] != original || !strings.HasPrefix(lines[1], "chat-lifecycle grant_tick=") || len(lines[1])+1 > 1024 {
		t.Fatalf("tick evidence missing or compatibility changed: %q", summary)
	}
	var got struct {
		Boundary         string        `json:"boundary"`
		Reason           string        `json:"reason"`
		HaveLastTick     bool          `json:"have_last_tick"`
		AgeObserved      bool          `json:"age_observed"`
		AgeNanos         time.Duration `json:"age_nanos"`
		IntervalObserved bool          `json:"interval_observed"`
		IntervalNanos    time.Duration `json:"interval_nanos"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(lines[1], "chat-lifecycle grant_tick=")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Boundary != "scheduled_grant" || got.Reason != "stale" || !got.HaveLastTick || !got.AgeObserved || got.AgeNanos != 1300*time.Millisecond || !got.IntervalObserved || got.IntervalNanos != time.Second {
		t.Fatalf("observed tick evidence changed: %+v", got)
	}
	if chatLifecycleVerdictExitCode(verdict) != exitChatLifecycleHarness {
		t.Fatal("diagnostic changed exit")
	}
	result.Outcome, result.Code = chatlifecycle.CoordinatorProductFailure, chatlifecycle.CoordinatorCodeObserver
	verdict = chatLifecycleCoordinatorVerdict(result)
	summary = chatLifecycleCoordinatorSummary(verdict, result, "unavailable")
	if !strings.HasPrefix(summary, "chat-lifecycle outcome=product_failure cause=worker_product_failure coordinator_code=observer ") || !strings.Contains(summary, "\nchat-lifecycle grant_tick=") || chatLifecycleVerdictExitCode(verdict) != exitChatLifecycleProduct {
		t.Fatal("tick metadata changed concurrent product failure precedence")
	}
}

// Unknown strings and contradictory presence flags cannot become CLI evidence.
func TestChatLifecycleGrantTickSummaryRejectsUnsafeOrUnobservedFacts(t *testing.T) {
	const original = "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=grant grant_failure_code=tick worker_runtime_code= observer_code= preflight_code= report=unavailable\n"
	for _, raw := range []string{
		`{}`,
		`{"boundary":"CANARY_private_identity","reason":"stale","age_observed":true,"age_nanos":1300000000,"interval_observed":true,"interval_nanos":1000000000}`,
		`{"boundary":"scheduled_grant","reason":"CANARY_private_error","age_observed":true,"age_nanos":1300000000,"interval_observed":true,"interval_nanos":1000000000}`,
		`{"boundary":"scheduled_grant","reason":"stale","age_nanos":1300000000,"interval_observed":true,"interval_nanos":1000000000}`,
		`{"boundary":"scheduled_grant","reason":"future","age_observed":true,"age_nanos":1,"interval_observed":true,"interval_nanos":1000000000}`,
		`{"boundary":"scheduled_grant","reason":"zero_tick","age_observed":true,"age_nanos":0}`,
		`{"boundary":"scheduled_grant","reason":"first_late","have_last_tick":true,"age_observed":true,"interval_observed":true,"interval_nanos":2000000000}`,
		`{"boundary":"pending_status","reason":"stale","age_observed":true,"age_nanos":1300000000,"interval_observed":true,"interval_nanos":1000000000}`,
	} {
		var result chatlifecycle.CoordinatorResult
		result.Outcome, result.Code, result.GrantFailure = chatlifecycle.CoordinatorHarnessInvalid, chatlifecycle.CoordinatorCodeGrant, chatlifecycle.CoordinatorGrantFailureTick
		if err := json.Unmarshal([]byte(`{"GrantTickFailure":`+raw+`}`), &result); err != nil {
			t.Fatal(err)
		}
		if got := chatLifecycleCoordinatorSummary(chatLifecycleCoordinatorVerdict(result), result, "unavailable"); got != original {
			t.Fatalf("invalid/absent evidence gained a diagnostic line: %s", got)
		}
	}
}
