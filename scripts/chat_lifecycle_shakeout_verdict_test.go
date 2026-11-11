package scripts_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

func latencyFailureReport(cause chatlifecycle.VerdictCause) *chatlifecycle.Report {
	report := &chatlifecycle.Report{}
	report.Verdict.Outcome = chatlifecycle.VerdictProductFailure
	report.Verdict.Cause = cause
	report.Messages.Sent, report.Messages.SendAcknowledged = 13000, 13000
	return report
}

// A hosted runner may miss latency or first-attempt-rate limits without any
// product defect; a correctness failure must never be downgraded with them.
func TestShakeoutAdvisoryLatencyFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report *chatlifecycle.Report
		want   bool
	}{
		{"first-attempt rate with clean counters", latencyFailureReport(chatlifecycle.VerdictCauseOverallFirstAttemptRate), true},
		{"minute first-attempt rate", latencyFailureReport(chatlifecycle.VerdictCauseMinuteFirstAttemptRate), true},
		{"hot latency", latencyFailureReport(chatlifecycle.VerdictCauseHotLatency), true},
		{"sync latency", latencyFailureReport(chatlifecycle.VerdictCauseSyncLatency), true},
		{"message loss cause", latencyFailureReport(chatlifecycle.VerdictCauseMessageLoss), false},
		{"server crash cause", latencyFailureReport(chatlifecycle.VerdictCauseServerCrash), false},
		{"latency cause but a loss", func() *chatlifecycle.Report {
			r := latencyFailureReport(chatlifecycle.VerdictCauseHotLatency)
			r.Messages.Losses = 1
			return r
		}(), false},
		{"latency cause but a duplicate", func() *chatlifecycle.Report {
			r := latencyFailureReport(chatlifecycle.VerdictCauseHotLatency)
			r.Messages.Duplicates = 1
			return r
		}(), false},
		{"latency cause but a terminal send", func() *chatlifecycle.Report {
			r := latencyFailureReport(chatlifecycle.VerdictCauseOverallFirstAttemptRate)
			r.Messages.Terminal = 1
			return r
		}(), false},
		{"latency cause but unacknowledged sends", func() *chatlifecycle.Report {
			r := latencyFailureReport(chatlifecycle.VerdictCauseOverallFirstAttemptRate)
			r.Messages.SendAcknowledged = 12999
			return r
		}(), false},
		{"not a product failure", func() *chatlifecycle.Report {
			r := latencyFailureReport(chatlifecycle.VerdictCauseHotLatency)
			r.Verdict.Outcome = chatlifecycle.VerdictHarnessInvalid
			return r
		}(), false},
		{"no report", nil, false},
	} {
		if got := shakeoutAdvisoryLatencyFailure(tc.report); got != tc.want {
			t.Errorf("%s: shakeoutAdvisoryLatencyFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
}
