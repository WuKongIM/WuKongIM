package benchmark

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

func TestMetricsParseDiagnosticCannotChangeCLIOutcome(t *testing.T) {
	for _, field := range []string{
		"",
		`,"parse_reasons":["","","invalid_queue_utilization"]`,
		`,"parse_reasons":["","","private-token"]`,
		`,"parse_reasons":{"private":"token"}`,
	} {
		raw := []byte(`{"stage":"meta_checkpoint","reason":"metrics_fetch","observed_decision":"product_failure","observed_outcome":"product_failure","observed_cause":"message_loss","observed_terminal":true,"metrics_fetch":{"source_mask":4,"kinds":["","","parse"]` + field + `}}`)
		var diagnostic chatlifecycle.FinalizationFailure
		if err := json.Unmarshal(raw, &diagnostic); err != nil {
			t.Fatal(err)
		}
		result := chatlifecycle.CoordinatorResult{Outcome: chatlifecycle.CoordinatorHarnessInvalid, Code: chatlifecycle.CoordinatorCodeFinalize, FinalizationFailure: diagnostic.Bounded()}
		verdict := chatLifecycleCoordinatorVerdict(result)
		if verdict.Outcome != chatlifecycle.VerdictHarnessInvalid || verdict.Cause != chatlifecycle.VerdictCauseInvalidObservation || chatLifecycleVerdictExitCode(verdict) != exitChatLifecycleHarness {
			t.Fatal("optional parser reason changed exit8/no-final")
		}
		summary := chatLifecycleCoordinatorSummary(verdict, result, "unavailable")
		for _, part := range []string{"finalization_stage=meta_checkpoint", "finalization_reason=metrics_fetch", "report=unavailable", "observed_cause=message_loss"} {
			if !strings.Contains(summary, part) {
				t.Errorf("missing %s", part)
			}
		}
		if strings.Contains(summary, "private") {
			t.Fatal("optional parser reason exposed private text")
		}
	}
}
