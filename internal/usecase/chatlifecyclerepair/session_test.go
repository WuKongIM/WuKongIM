package chatlifecyclerepair_test

import (
	"errors"
	"testing"
	"time"

	repair "github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerepair"
)

func TestSessionStopsActiveGenerationWhenMessageProgressStalls(t *testing.T) {
	started := time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC)
	config := testConfig()
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}

	state, decision, err := repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 100, 90))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("first observation = decision=%+v error=%v", decision, err)
	}
	state, decision, err = repair.Advance(config, state, observation(started.Add(15*time.Second), 10_000, 100, 90))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("pre-deadline observation = decision=%+v error=%v", decision, err)
	}
	_, decision, err = repair.Advance(config, state, observation(started.Add(20*time.Second), 10_000, 100, 90))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonMessageProgressStalled {
		t.Fatalf("stall decision = %+v", decision)
	}
}

func TestSessionStopsWhenSendsAdvanceButAcknowledgementsStall(t *testing.T) {
	started := time.Date(2026, 8, 22, 15, 30, 0, 0, time.UTC)
	config := testConfig()
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 100, 90))
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Advance(config, state, observation(started.Add(15*time.Second), 10_000, 200, 90))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("pre-deadline observation = decision=%+v error=%v", decision, err)
	}
	_, decision, err = repair.Advance(config, state, observation(started.Add(20*time.Second), 10_000, 300, 90))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonAcknowledgementProgressStalled {
		t.Fatalf("acknowledgement stall decision = %+v", decision)
	}
}

func TestSessionStopsWhenActiveOnlinePopulationStaysBelowFloor(t *testing.T) {
	started := time.Date(2026, 8, 22, 15, 45, 0, 0, time.UTC)
	config := testConfig()
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Advance(config, state, observation(started.Add(5*time.Second), 9_499, 100, 90))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("first low-online observation = decision=%+v error=%v", decision, err)
	}
	_, decision, err = repair.Advance(config, state, observation(started.Add(20*time.Second), 9_499, 200, 190))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonOnlineBelowFloor {
		t.Fatalf("low-online decision = %+v", decision)
	}
}

func TestSessionStopsWhenAnActiveGenerationFallsBackOutOfActive(t *testing.T) {
	started := time.Date(2026, 8, 22, 15, 47, 0, 0, time.UTC)
	config := testConfig()
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 10_000, 10_000))
	if err != nil {
		t.Fatal(err)
	}
	lost := observation(started.Add(10*time.Second), 0, 10_000, 10_000)
	lost.Phase = repair.PhaseWarmup
	state, decision, err := repair.Advance(config, state, lost)
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("first lost-active observation = decision=%+v error=%v", decision, err)
	}
	lost.ObservedAt = started.Add(25 * time.Second)
	for worker := range lost.Workers {
		lost.Workers[worker].Uptime = time.Duration(lost.ObservedAt.UnixNano())
	}
	_, decision, err = repair.Advance(config, state, lost)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonActivePhaseLost {
		t.Fatalf("lost-active decision = %+v", decision)
	}
}

func TestSessionStopsWhenActiveSendRateStaysBelowFloor(t *testing.T) {
	started := time.Date(2026, 8, 22, 15, 48, 0, 0, time.UTC)
	config := testConfig()
	config.MinimumSendRatePerSecond = 1_900
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 100, 100))
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Advance(config, state, observation(started.Add(10*time.Second), 10_000, 101, 101))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("first low-rate observation = decision=%+v error=%v", decision, err)
	}
	_, decision, err = repair.Advance(config, state, observation(started.Add(25*time.Second), 10_000, 102, 102))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonSendRateBelowFloor {
		t.Fatalf("low-rate decision = %+v", decision)
	}
}

func TestSessionEarlyBurstDoesNotHideAContinuouslyLowRecentSendRate(t *testing.T) {
	started := time.Date(2026, 8, 22, 15, 48, 30, 0, time.UTC)
	config := testConfig()
	config.MinimumSendRatePerSecond = 1_900
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	// The first active cut contains a large historical burst. It establishes the
	// local window baseline and must not subsidize later slow windows.
	state, _, err = repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 100_000, 100_000))
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Advance(config, state, observation(started.Add(10*time.Second), 10_000, 100_100, 100_100))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("first low recent window = decision=%+v error=%v", decision, err)
	}
	_, decision, err = repair.Advance(config, state, observation(started.Add(25*time.Second), 10_000, 100_200, 100_200))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonSendRateBelowFloor {
		t.Fatalf("burst-masked rate decision = %+v", decision)
	}
}

func TestSessionUsesWorkerUptimeForRateAcrossSlowMixedCaptureCuts(t *testing.T) {
	started := time.Date(2026, 8, 23, 11, 14, 0, 0, time.UTC)
	config := testConfig()
	config.MinimumSendRatePerSecond = 1_900
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}

	first := observation(started.Add(23*time.Second), 10_000, 278_008, 277_164)
	first.Workers = [3]repair.WorkerProgress{
		{WorkerID: 0, Uptime: 100 * time.Second, Sent: 92_669, SendAcknowledged: 92_388},
		{WorkerID: 1, Uptime: 102 * time.Second, Sent: 92_669, SendAcknowledged: 92_388},
		{WorkerID: 2, Uptime: 104 * time.Second, Sent: 92_670, SendAcknowledged: 92_388},
	}
	state, decision, err := repair.Advance(config, state, first)
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("first mixed cut = decision=%+v error=%v", decision, err)
	}

	// The aggregate counters advanced by only 34,668 across 24 seconds of local
	// collection time (1,444/s). Each worker's own monotonic interval proves the
	// generation actually sustained 2,039/s; serial SSH latency is not workload
	// time and must not begin a low-rate window.
	second := observation(started.Add(47*time.Second), 10_000, 312_676, 312_030)
	second.Workers = [3]repair.WorkerProgress{
		{WorkerID: 0, Uptime: 117 * time.Second, Sent: 104_225, SendAcknowledged: 104_010},
		{WorkerID: 1, Uptime: 119 * time.Second, Sent: 104_225, SendAcknowledged: 104_010},
		{WorkerID: 2, Uptime: 121 * time.Second, Sent: 104_226, SendAcknowledged: 104_010},
	}
	state, decision, err = repair.Advance(config, state, second)
	if err != nil || decision.Action != repair.ActionContinue || state.SendRateBelowSince != nil {
		t.Fatalf("second mixed cut = state=%+v decision=%+v error=%v", state, decision, err)
	}

	third := observation(started.Add(70*time.Second), 10_000, 350_009, 350_007)
	third.Workers = [3]repair.WorkerProgress{
		{WorkerID: 0, Uptime: 135 * time.Second, Sent: 116_669, SendAcknowledged: 116_669},
		{WorkerID: 1, Uptime: 137 * time.Second, Sent: 116_670, SendAcknowledged: 116_669},
		{WorkerID: 2, Uptime: 139 * time.Second, Sent: 116_670, SendAcknowledged: 116_669},
	}
	state, decision, err = repair.Advance(config, state, third)
	if err != nil || decision.Action != repair.ActionContinue || state.SendRateBelowSince != nil {
		t.Fatalf("third mixed cut = state=%+v decision=%+v error=%v", state, decision, err)
	}
}

func TestSessionRejectsWorkerUptimeOrCounterRegression(t *testing.T) {
	started := time.Date(2026, 8, 23, 11, 20, 0, 0, time.UTC)
	config := testConfig()
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 300, 300))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*repair.Observation){
		func(next *repair.Observation) { next.Workers[1].Uptime = state.LastWorkers[1].Uptime },
		func(next *repair.Observation) {
			next.Workers[1].Sent = state.LastWorkers[1].Sent - 1
			next.Workers[1].SendAcknowledged = state.LastWorkers[1].SendAcknowledged - 1
			next.Sent = next.Workers[0].Sent + next.Workers[1].Sent + next.Workers[2].Sent
			next.SendAcknowledged = next.Workers[0].SendAcknowledged + next.Workers[1].SendAcknowledged + next.Workers[2].SendAcknowledged
		},
	} {
		next := observation(started.Add(10*time.Second), 10_000, 600, 600)
		mutate(&next)
		if _, _, err := repair.Advance(config, state, next); !errors.Is(err, repair.ErrInvalidObservation) {
			t.Fatalf("regressed worker observation error = %v", err)
		}
	}
}

func TestSessionStopsWhenAcknowledgementBacklogStaysAboveBound(t *testing.T) {
	started := time.Date(2026, 8, 22, 15, 49, 0, 0, time.UTC)
	config := testConfig()
	config.MaximumAckBacklog = 100
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 100, 100))
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Advance(config, state, observation(started.Add(10*time.Second), 10_000, 1_000, 800))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("first backlog observation = decision=%+v error=%v", decision, err)
	}
	_, decision, err = repair.Advance(config, state, observation(started.Add(25*time.Second), 10_000, 2_000, 1_700))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonAcknowledgementBacklogExceeded {
		t.Fatalf("backlog decision = %+v", decision)
	}
}

func TestSessionQualifiesOnlyAfterContinuousHealthyActiveProgress(t *testing.T) {
	started := time.Date(2026, 8, 22, 15, 50, 0, 0, time.UTC)
	config := testConfig()
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	var decision repair.Decision
	for tick := 1; tick <= 25; tick++ {
		at := started.Add(time.Duration(tick) * 5 * time.Second)
		state, decision, err = repair.Advance(config, state, observation(at, 10_000, uint64(tick*100), uint64(tick*100-1)))
		if err != nil {
			t.Fatal(err)
		}
		if tick < 25 && decision.Action != repair.ActionContinue {
			t.Fatalf("tick %d qualified early: %+v", tick, decision)
		}
		if tick == 25 && (decision.Action != repair.ActionQualified || decision.Reason != repair.ReasonNone || state.TerminalAction != repair.ActionQualified) {
			t.Fatalf("terminal qualification = state=%+v decision=%+v", state, decision)
		}
	}
}

func TestSessionAcceptsOneHourStabilityWindow(t *testing.T) {
	config := testConfig()
	config.QualifyAfter = time.Hour
	started := time.Date(2026, 8, 22, 15, 55, 0, 0, time.UTC)
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 100, 100))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("first active observation = decision=%+v error=%v", decision, err)
	}
	state, decision, err = repair.Advance(config, state, observation(started.Add(time.Hour), 10_000, 360_000, 360_000))
	if err != nil || decision.Action != repair.ActionContinue {
		t.Fatalf("stability run qualified before one active hour: decision=%+v error=%v", decision, err)
	}
	_, decision, err = repair.Advance(config, state, observation(started.Add(time.Hour+5*time.Second), 10_000, 360_005, 360_005))
	if err != nil || decision.Action != repair.ActionQualified {
		t.Fatalf("one-hour stability qualification = decision=%+v error=%v", decision, err)
	}
}

func TestSessionFailureIsTerminalUntilANewGenerationBegins(t *testing.T) {
	started := time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)
	config := testConfig()
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = repair.Advance(config, state, observation(started.Add(5*time.Second), 10_000, 100, 90))
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Advance(config, state, observation(started.Add(20*time.Second), 10_000, 100, 90))
	if err != nil || decision.Action != repair.ActionStopAndDiagnose {
		t.Fatalf("failure decision = %+v error=%v", decision, err)
	}
	_, _, err = repair.Advance(config, state, observation(started.Add(25*time.Second), 10_000, 200, 190))
	if !errors.Is(err, repair.ErrGenerationTerminal) {
		t.Fatalf("advance after terminal failure error = %v", err)
	}
}

func TestSessionStopsWhenTrafficNeverBecomesActive(t *testing.T) {
	started := time.Date(2026, 8, 22, 17, 0, 0, 0, time.UTC)
	config := testConfig()
	config.WarmupTimeout = time.Minute
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	warmup := observation(started.Add(time.Minute), 0, 0, 0)
	warmup.Phase = repair.PhaseWarmup
	_, decision, err := repair.Advance(config, state, warmup)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonWarmupTimeout {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestSessionAbortSealsExternalMonitorFailure(t *testing.T) {
	started := time.Date(2026, 8, 22, 17, 30, 0, 0, time.UTC)
	config := testConfig()
	config.WarmupTimeout = time.Minute
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Abort(state, started.Add(time.Second), repair.ReasonServiceInactive)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonServiceInactive ||
		state.TerminalAction != repair.ActionStopAndDiagnose {
		t.Fatalf("abort = state=%+v decision=%+v", state, decision)
	}
	if _, _, err := repair.Abort(state, started.Add(2*time.Second), repair.ReasonObservationUnavailable); !errors.Is(err, repair.ErrGenerationTerminal) {
		t.Fatalf("second abort error = %v", err)
	}
}

func TestSessionAbortAcceptsRequestScopedOperatorStop(t *testing.T) {
	started := time.Date(2026, 8, 22, 17, 45, 0, 0, time.UTC)
	config := testConfig()
	config.WarmupTimeout = time.Minute
	state, err := repair.Begin(config, repair.Candidate{
		RequestID: "chat-repair-a", LeaseID: "lease-a", Generation: 3,
		SourceSHA: "1111111111111111111111111111111111111111", BundleDigest: digest('a'),
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := repair.Abort(state, started.Add(time.Second), repair.ReasonOperatorStop)
	if err != nil || decision.Action != repair.ActionStopAndDiagnose || decision.Reason != repair.ReasonOperatorStop || state.TerminalReason != repair.ReasonOperatorStop {
		t.Fatalf("operator stop abort = state=%+v decision=%+v error=%v", state, decision, err)
	}
}

func observation(at time.Time, online, sent, acknowledged uint64) repair.Observation {
	result := repair.Observation{
		Schema: repair.ObservationSchemaV2, RequestID: "chat-repair-a", LeaseID: "lease-a",
		Generation: 3, ObservedAt: at, Phase: repair.PhaseActive,
		Online: online, Sent: sent, SendAcknowledged: acknowledged,
	}
	for worker := range result.Workers {
		result.Workers[worker] = repair.WorkerProgress{
			WorkerID: uint64(worker), Uptime: time.Duration(at.UnixNano()),
			Sent: sent / 3, SendAcknowledged: acknowledged / 3,
		}
		if uint64(worker) < sent%3 {
			result.Workers[worker].Sent++
		}
		if uint64(worker) < acknowledged%3 {
			result.Workers[worker].SendAcknowledged++
		}
	}
	return result
}

func testConfig() repair.Config {
	return repair.Config{
		TargetOnline: 10_000, MinimumOnlinePercent: 95, WarmupTimeout: 5 * time.Minute,
		StallAfter: 15 * time.Second, QualifyAfter: 2 * time.Minute,
		MinimumSendRatePerSecond: 1, MaximumAckBacklog: 10_000,
	}
}

func digest(value byte) string {
	result := make([]byte, 64)
	for index := range result {
		result[index] = value
	}
	return "sha256:" + string(result)
}
