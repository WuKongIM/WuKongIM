package chatlifecycle

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestEngineRetryAdmissionJoinsAckAlreadyVerifiedBeforeOwnerConsumption(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	// Install the public clock dependency before Start. Only the owner uses
	// this gate; the session transport clock remains the same underlying clock.
	gate := &retryAdmissionClock{clock: fixture.clock, entered: make(chan struct{}), release: make(chan struct{})}
	fixture.engine.clock = gate
	reads := make(chan struct{}, 8)
	fixture.factory.readCycles = reads
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 6}); err != nil {
		t.Fatal(err)
	}
	<-reads
	var intent TrafficIntent
	for ordinal := uint64(180); ordinal < 280; ordinal++ {
		intent = fixture.intent(t, uid, "retry-admission-race-group", ordinal, TrafficGroup)
		sampled, err := fixture.verifier.ShouldCorrelate(intent.Logical)
		if err != nil {
			t.Fatal(err)
		}
		if !sampled {
			break
		}
	}
	started := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, started); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.engine.Advance(started); err != nil {
		t.Fatal(err)
	}
	waitForEngineAdmissions(t, fixture.engine)
	now := started.Add(20 * time.Millisecond)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatal(err)
	}
	waitForEngineAdmissions(t, fixture.engine)
	retry, err := fixture.retry.Attempt(intent.Logical, 1)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(retry.Delay)
	fixture.clock.Set(now)
	gate.armed.Store(true)
	advanceDone := make(chan error, 1)
	go func() { _, err := fixture.engine.Advance(now); advanceDone <- err }()
	<-gate.entered
	closed := false
	defer func() {
		if !closed {
			close(gate.release)
		}
	}()
	packets := fixture.factory.sentPackets()
	if len(packets) != 1 {
		t.Fatalf("packets before retry admission = %d", len(packets))
	}
	fixture.factory.clients()[0].frames <- firstDeadlineAck(intent.Logical, packets[0].ClientSeq)
	// The real SessionPool drain verifies ACK then queues the Engine callback
	// before beginning the next read. The owner is still inside gated Now.
	<-reads
	if snapshot := fixture.verifier.Snapshot(); snapshot.Acknowledged != 1 || snapshot.PendingUnfinished != 0 {
		t.Fatalf("ACK was not verified before owner retry admission: %+v", snapshot)
	}
	close(gate.release)
	closed = true
	if err := <-advanceDone; err != nil {
		t.Errorf("valid late ACK became a harness failure: %v; evidence=%+v", err, fixture.evidence.Snapshot())
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	verification := fixture.verifier.Snapshot()
	if fixture.factory.sentCount() != 1 || verification.Attempts != 1 || verification.RetryAttempts != 0 ||
		verification.Acknowledged != 1 || verification.PendingCurrent != 0 || snapshot.InflightCurrent != 0 || snapshot.FinalFailures != 0 ||
		verification.Classification != "" {
		t.Errorf("completed original did not settle exactly once: engine=%+v verifier=%+v packets=%d", snapshot, verification, fixture.factory.sentCount())
	}
}

type retryAdmissionClock struct {
	clock   SessionClock
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (c *retryAdmissionClock) Now() time.Time {
	at := c.clock.Now()
	if c.armed.CompareAndSwap(true, false) {
		close(c.entered)
		<-c.release
	}
	return at
}
