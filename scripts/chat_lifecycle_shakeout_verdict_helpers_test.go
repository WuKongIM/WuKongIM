package scripts_test

import "github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"

// shakeoutHardwareDependentCauses are verdict causes decided by latency or
// first-attempt deadlines. On a shared 4-vCPU hosted runner they measure the
// runner as much as the product, so the CI shakeout records them as advisory.
var shakeoutHardwareDependentCauses = map[chatlifecycle.VerdictCause]bool{
	chatlifecycle.VerdictCauseOverallFirstAttemptRate: true,
	chatlifecycle.VerdictCauseMinuteFirstAttemptRate:  true,
	chatlifecycle.VerdictCauseHotLatency:              true,
	chatlifecycle.VerdictCauseColdLatency:             true,
	chatlifecycle.VerdictCauseSyncLatency:             true,
}

// shakeoutAdvisoryLatencyFailure reports whether a terminal product failure is
// only a hardware-dependent latency/rate miss: every send was acknowledged and
// no loss, duplicate, corruption, sequence regression or terminal send exists.
// Any correctness signal keeps the failure gating.
func shakeoutAdvisoryLatencyFailure(report *chatlifecycle.Report) bool {
	if report == nil || report.Verdict.Outcome != chatlifecycle.VerdictProductFailure ||
		!shakeoutHardwareDependentCauses[report.Verdict.Cause] {
		return false
	}
	messages := report.Messages
	return messages.Sent > 0 && messages.SendAcknowledged == messages.Sent &&
		messages.Losses == 0 && messages.Duplicates == 0 && messages.Corruptions == 0 &&
		messages.SequenceRegressions == 0 && messages.Terminal == 0
}
