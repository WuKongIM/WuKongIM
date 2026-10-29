//go:build integration

package chatlifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestHotAttemptBudgetRealFactoryACKBoundary(t *testing.T) {
	for _, test := range []struct {
		name    string
		elapsed time.Duration
		late    uint64
	}{
		{"one_and_half_seconds", 1500 * time.Millisecond, 0},
		{"before_two_seconds", 2*time.Second - time.Nanosecond, 0},
		{"exact_two_seconds", 2 * time.Second, 1},
		{"after_two_seconds", 2*time.Second + time.Nanosecond, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// This primary RED anchor uses the actual existing Formal defaults,
			// factory and verifier; no unknown new JSON field can short-circuit it.
			fixture := hotAttemptBudgetFactoryFixture(t, FormalConfig())
			uid, intent, started := hotAttemptBudgetStartSend(t, fixture, 147)
			packets := fixture.factory.sentPackets()
			ack := firstDeadlineAck(intent.Logical, packets[0].ClientSeq)
			fixture.clock.Set(started.Add(test.elapsed))
			err := fixture.verifier.HandleSendackAt(ack, fixture.clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.engine.ObserveSendack(uid, ack, err); err != nil {
				t.Fatal(err)
			}
			assertFirstDeadlineCounters(t, fixture.verifier.Snapshot(), 1, 1, test.late, 0)
			got := fixture.verifier.Snapshot()
			counts, err := histogramThresholdCounters(got.HotSendackLatency, FormalConfig().Thresholds.Latency.HotSendACK)
			if err != nil {
				t.Fatal(err)
			}
			if got.RetryAttempts != 0 || got.Terminal != 0 || counts.Count != 1 || counts.AboveP99 != 1 || counts.AboveP999 != 1 {
				t.Fatalf("new wait budget erased original400ms/1s latency breach or added retry: %+v / %+v", got, counts)
			}
		})
	}
}

func TestHotAttemptBudgetFactoryKeepsColdAndLegacyLocal(t *testing.T) {
	fixture := hotAttemptBudgetFactoryFixture(t, FormalConfig())
	if fixture.engine.attemptTimeoutFor(TrafficIntent{}) != 2*time.Second || fixture.engine.attemptTimeoutFor(TrafficIntent{MetaCreateCandidate: true}) != 5*time.Second || fixture.engine.attemptTimeoutFor(TrafficIntent{Domain: LogicalDomainRevisit}) != 5*time.Second {
		t.Fatal("factory did not separate hot2s from existing cold5s")
	}
	local := LocalConfig()
	local.Thresholds.Latency.HotSendACK.P999 = 750 * time.Millisecond
	local = hotAttemptBudgetConfig(t, local, nil)
	legacy := hotAttemptBudgetFactoryFixture(t, local)
	if legacy.engine.attemptTimeoutFor(TrafficIntent{}) != 750*time.Millisecond {
		t.Fatal("missing custom local field did not inherit its original P999 budget")
	}
}

func TestHotAttemptBudgetEveryRetryKeepsTwoSecondsAndBoundedCredit(t *testing.T) {
	fixture := hotAttemptBudgetFactoryFixture(t, FormalConfig())
	_, intent, now := hotAttemptBudgetStartSend(t, fixture, 148)
	bases := [3]time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
	for attempt := uint8(0); attempt <= 3; attempt++ {
		before := now.Add(2*time.Second - time.Nanosecond)
		fixture.clock.Set(before)
		if _, err := fixture.engine.Advance(before); err != nil {
			t.Fatal(err)
		}
		waitForEngineAdmissions(t, fixture.engine)
		snapshot, err := fixture.engine.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.RetryQueueDepth != 0 || snapshot.RetryAttempts != uint64(attempt) || snapshot.InflightCurrent != 1 || fixture.factory.sentCount() != int(attempt)+1 {
			t.Fatalf("hot attempt%d retried before its2s budget or grew credit: %+v", attempt, snapshot)
		}
		deadline := now.Add(2 * time.Second)
		fixture.clock.Set(deadline)
		_, advanceErr := fixture.engine.Advance(deadline)
		if attempt == 3 {
			assertVerificationCode(t, advanceErr, FailureCodeTerminalSend)
			break
		}
		if advanceErr != nil {
			t.Fatal(advanceErr)
		}
		snapshot, err = fixture.engine.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.RetryQueueDepth != 1 || snapshot.RetryAttempts != uint64(attempt) || snapshot.InflightCurrent != 1 || snapshot.QueueCurrent > 2 {
			t.Fatalf("timeout did not retain exactly one bounded retry: %+v", snapshot)
		}
		retry, err := fixture.retry.Attempt(intent.Logical, attempt+1)
		if err != nil {
			t.Fatal(err)
		}
		if retry.BaseDelay != bases[attempt] {
			t.Fatal("retry policy delay changed")
		}
		now = deadline.Add(retry.Delay)
		fixture.clock.Set(now)
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatal(err)
		}
		waitForEngineAdmissions(t, fixture.engine)
		packets := fixture.factory.sentPackets()
		if len(packets) != int(attempt)+2 || packets[len(packets)-1].ClientMsgNo != packets[0].ClientMsgNo || packets[len(packets)-1].ClientSeq == packets[len(packets)-2].ClientSeq {
			t.Fatal("retry minted another logical identity or exceeded owned wire attempts")
		}
	}
	got := fixture.verifier.Snapshot()
	if got.RetryAttempts != 3 || got.Terminal != 1 || got.FirstAttemptFailures != 1 || got.TerminalReasons.RetryExhausted != (RetryExhaustedSnapshot{Total: 1, AttemptTimeout: 1}) || got.PendingCurrent != 0 || fixture.factory.sentCount() != 4 {
		t.Fatalf("four-attempt terminal/credit contract changed: %+v", got)
	}
	assertFirstDeadlineCounters(t, got, 1, 0, 0, 1)
}

func TestHotAttemptBudgetLateOriginalKeepsOriginalTwoSecondFence(t *testing.T) {
	fixture := hotAttemptBudgetFactoryFixture(t, FormalConfig())
	uid, intent, started := hotAttemptBudgetStartSend(t, fixture, 149)
	deadline := started.Add(2 * time.Second)
	fixture.clock.Set(deadline)
	if _, err := fixture.engine.Advance(deadline); err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.retry.Attempt(intent.Logical, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := deadline.Add(retry.Delay)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatal(err)
	}
	waitForEngineAdmissions(t, fixture.engine)
	packets := fixture.factory.sentPackets()
	if len(packets) != 2 {
		t.Fatalf("wire attempts=%d", len(packets))
	}
	ack := firstDeadlineAck(intent.Logical, packets[1].ClientSeq)
	if err := fixture.verifier.HandleSendackAt(ack, now); err != nil {
		t.Fatal(err)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, nil); err != nil {
		t.Fatal(err)
	}
	assertFirstDeadlineCounters(t, fixture.verifier.Snapshot(), 1, 0, 0, 0)
	// This is after the original2s fence but before the retry's own2s fence.
	lateOriginal := firstDeadlineAck(intent.Logical, packets[0].ClientSeq)
	if err := fixture.verifier.HandleSendackAt(lateOriginal, started.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	got := fixture.verifier.Snapshot()
	assertFirstDeadlineCounters(t, got, 1, 1, 1, 0)
	if got.Acknowledged != 1 || got.RetryAttempts != 1 || got.Terminal != 0 {
		t.Fatalf("retry success rewrote original coverage: %+v", got)
	}
}

func TestHotAttemptBudgetCancellationAndStopKeepOriginalOwnership(t *testing.T) {
	t.Run("canceled_login_has_no_wire_or_deadline", func(t *testing.T) {
		fixture := hotAttemptBudgetFactoryFixture(t, FormalConfig())
		fixture.factory.connectStarted = make(chan context.Context, 1)
		fixture.factory.connectRelease = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		loginDone := make(chan error, 1)
		go func() {
			_, err := fixture.engine.Login(ctx, SessionLogin{UID: fixture.identity.UID(0), UserIndex: 0, LoginOrdinal: 0})
			loginDone <- err
		}()
		connectCtx := <-fixture.factory.connectStarted
		cancel()
		<-connectCtx.Done()
		if !errors.Is(context.Cause(connectCtx), context.Canceled) {
			t.Fatal("CONNECT context did not observe the cancellation")
		}
		failure := mustLoginSyncFailure(t, <-loginDone)
		if failure.Stage != LoginSyncStageConnect || failure.Reason != LoginSyncReasonCanceled {
			t.Fatalf("canceled login lost its closed classification: %+v", failure)
		}
		if fixture.factory.sentCount() != 0 {
			t.Fatal("cancellation emitted a SEND")
		}
		assertFirstDeadlineCounters(t, fixture.verifier.Snapshot(), 0, 0, 0, 0)
	})
	t.Run("stop_before_two_seconds_keeps_unknown_coverage", func(t *testing.T) {
		fixture := hotAttemptBudgetFactoryFixture(t, FormalConfig())
		_, _, started := hotAttemptBudgetStartSend(t, fixture, 150)
		fixture.clock.Set(started.Add(1500 * time.Millisecond))
		if _, err := fixture.engine.Advance(fixture.clock.Now()); err != nil {
			t.Fatal(err)
		}
		snapshot, err := fixture.engine.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.RetryQueueDepth != 0 || snapshot.RetryAttempts != 0 || snapshot.InflightCurrent != 1 {
			t.Fatalf("new budget changed preStop credit: %+v", snapshot)
		}
		if err := fixture.engine.Stop(); err != nil {
			t.Fatal(err)
		}
		got := fixture.verifier.Snapshot()
		assertFirstDeadlineCounters(t, got, 1, 0, 0, 1)
		if got.Terminal != 1 || got.TerminalReasons != (TerminalSendSnapshot{SessionClosed: 1}) || got.PendingCurrent != 0 || got.PendingUnfinished != 0 {
			t.Fatalf("Stop erased real sessionclosed or fabricated timeout coverage: %+v", got)
		}
		if err := fixture.engine.Stop(); err != nil {
			t.Fatal(err)
		}
		if after := fixture.verifier.Snapshot(); !reflect.DeepEqual(after, got) {
			t.Fatal("repeatStop changed terminal/coverage counters")
		}
	})
}

func hotAttemptBudgetFactoryFixture(t *testing.T, cfg Config) engineTestFixture {
	t.Helper()
	clock := &sessionFakeClock{now: time.Unix(1_700_000_000, 0)}
	sessions := &engineFakeFactory{}
	factory := engineWorkerGenerationFactory{
		clock:             clock,
		newSessionFactory: func(WorkerAssignment) (SessionClientFactory, error) { return sessions, nil },
		newSyncer:         func(WorkerAssignment) (ConversationSyncer, error) { return engineSyncer{}, nil },
	}
	worker, err := factory.New(WorkerAssignment{WorkerFence: WorkerFence{RunID: cfg.RunID, AssignmentID: "hot-budget", Generation: 1}, WorkerID: 0, WorkerCount: uint64(cfg.Workload.Workers), Config: cfg, CoordinatorGrants: true})
	if err != nil {
		t.Fatalf("actual worker factory: %v", err)
	}
	generation := worker.(*engineWorkerGeneration)
	e := generation.engine
	if err := e.StartGeneration(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Stop(); err != nil {
			t.Error(err)
		}
	})
	return engineTestFixture{identity: e.schedule.identity, schedule: e.schedule, graph: e.graph, traffic: e.traffic, retry: e.retry, verifier: e.verifier, evidence: e.evidence, clock: clock, factory: sessions, pool: e.sessions, engine: e}
}

func hotAttemptBudgetStartSend(t *testing.T, fixture engineTestFixture, ordinal uint64) (string, TrafficIntent, time.Time) {
	t.Helper()
	uid := fixture.identity.UID(0)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 0, LoginOrdinal: 0}); err != nil {
		t.Fatal(err)
	}
	intent := fixture.intent(t, uid, "hot-budget-group", ordinal, TrafficGroup)
	started := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, started); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.engine.Advance(started); err != nil {
		t.Fatal(err)
	}
	waitForEngineAdmissions(t, fixture.engine)
	if fixture.factory.sentCount() != 1 {
		t.Fatalf("initial SDK packets=%d", fixture.factory.sentCount())
	}
	return uid, intent, started
}
