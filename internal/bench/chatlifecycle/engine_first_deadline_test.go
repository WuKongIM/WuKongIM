package chatlifecycle

import (
	"context"
	"testing"
	"time"
)

func TestEngineFirstAttemptTimeoutSurvivesExecutedRetrySuccess(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 6}); err != nil {
		t.Fatal(err)
	}
	intent := fixture.intent(t, uid, "first-deadline-retry-group", 146, TrafficGroup)
	started := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, started); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.engine.Advance(started); err != nil {
		t.Fatal(err)
	}
	assertFirstDeadlineCounters(t, fixture.verifier.Snapshot(), 1, 0, 0, 0)
	now := started.Add(20 * time.Millisecond)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.retry.Attempt(intent.Logical, 1)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(retry.Delay)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatal(err)
	}
	packets := fixture.factory.sentPackets()
	if len(packets) != 2 {
		t.Fatalf("wire packets = %d, want executed original and retry", len(packets))
	}
	ack := firstDeadlineAck(intent.Logical, packets[1].ClientSeq)
	verificationErr := fixture.verifier.HandleSendackAt(ack, now)
	if verificationErr != nil {
		t.Fatal(verificationErr)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, verificationErr); err != nil {
		t.Fatal(err)
	}
	// The original remains unresolved until its bounded receipt resolves. The
	// successful retry must never turn that missing original into on-time ACK.
	assertFirstDeadlineCounters(t, fixture.verifier.Snapshot(), 1, 0, 0, 0)
	lateOriginal := firstDeadlineAck(intent.Logical, packets[0].ClientSeq)
	if err := fixture.verifier.HandleSendackAt(lateOriginal, now); err != nil {
		t.Fatal(err)
	}
	fixture.verifier.ExpireCorrelations(now.Add(time.Minute))
	assertFirstDeadlineCounters(t, fixture.verifier.Snapshot(), 1, 1, 1, 0)
	if got := fixture.verifier.Snapshot(); got.Acknowledged != 1 || got.RetryAttempts != 1 || got.Terminal != 0 {
		t.Fatalf("retry completion semantics changed: %+v", got)
	}
}

func TestEngineFirstAttemptLateAckBeforeTimeoutWorkCountsDeadline(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 6}); err != nil {
		t.Fatal(err)
	}
	intent := fixture.intent(t, uid, "first-deadline-late-group", 147, TrafficGroup)
	started := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, started); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.engine.Advance(started); err != nil {
		t.Fatal(err)
	}
	ack := firstDeadlineAck(intent.Logical, fixture.factory.sentPackets()[0].ClientSeq)
	fixture.clock.Set(started.Add(21 * time.Millisecond))
	verificationErr := fixture.verifier.HandleSendackAt(ack, fixture.clock.Now())
	if verificationErr != nil {
		t.Fatal(verificationErr)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, verificationErr); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.engine.Advance(fixture.clock.Now()); err != nil {
		t.Fatal(err)
	}
	assertFirstDeadlineCounters(t, fixture.verifier.Snapshot(), 1, 1, 1, 0)
	if got := fixture.verifier.Snapshot().RetryAttempts; got != 0 {
		t.Fatalf("retry attempts = %d; no timeout work was processed", got)
	}
}

func TestEngineFirstAttemptEarlyAsyncErrorPrecedesQueuedLateAck(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 6}); err != nil {
		t.Fatal(err)
	}
	intent := fixture.intent(t, uid, "first-deadline-queue-group", 148, TrafficGroup)
	started := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, started); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.engine.Advance(started); err != nil {
		t.Fatal(err)
	}
	ack := firstDeadlineAck(intent.Logical, fixture.factory.sentPackets()[0].ClientSeq)
	ownerEntered, ownerRelease := make(chan struct{}), make(chan struct{})
	if err := fixture.engine.enqueue(engineCommand{run: func() {
		close(ownerEntered)
		<-ownerRelease
	}}); err != nil {
		t.Fatal(err)
	}
	defer close(ownerRelease)
	<-ownerEntered
	fixture.clock.Set(started.Add(time.Millisecond))
	fixture.engine.sessionAsyncSendError(uid, ack.ClientSeq, ack.ClientMsgNo)
	// This is the same order as the serial session drain: the first transport
	// error precedes a later ACK, while owner completion processing is blocked.
	fixture.clock.Set(started.Add(21 * time.Millisecond))
	if err := fixture.verifier.HandleSendackAt(ack, fixture.clock.Now()); err != nil {
		t.Fatal(err)
	}
	assertFirstDeadlineCounters(t, fixture.verifier.Snapshot(), 1, 1, 0, 0)
}
