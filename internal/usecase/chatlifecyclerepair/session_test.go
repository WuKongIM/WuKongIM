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
	return repair.Observation{
		Schema: repair.ObservationSchemaV1, RequestID: "chat-repair-a", LeaseID: "lease-a",
		Generation: 3, ObservedAt: at, Phase: repair.PhaseActive,
		Online: online, Sent: sent, SendAcknowledged: acknowledged,
	}
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
