package chatlifecycle

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestEngineRetryExhaustionJoinsVerifiedAckBeforeTimeoutTerminal(t *testing.T) {
	fixture, intent, now, reads, ackGate := retryTerminalAckFixture(t)
	now = now.Add(20 * time.Millisecond)
	fixture.clock.Set(now)
	ackGate.armed.Store(true)
	fixture.factory.clients()[0].frames <- firstDeadlineAck(intent.Logical, fixture.factory.sentPackets()[0].ClientSeq)
	<-ackGate.verified
	_, advanceErr := fixture.engine.Advance(now)
	ackGate.open()
	<-reads
	if advanceErr != nil {
		t.Errorf("verified ACK lost to retry-exhausted timeout: %v; evidence=%+v", advanceErr, fixture.evidence.Snapshot())
	}
	assertRetryTerminalAckSettlement(t, fixture)
}

func TestEngineRetryExhaustionJoinsVerifiedAckBeforeAsyncErrorTerminal(t *testing.T) {
	fixture, intent, now, reads, ackGate := retryTerminalAckFixture(t)
	ownerEntered, ownerRelease := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	openOwner := func() { releaseOnce.Do(func() { close(ownerRelease) }) }
	defer openOwner()
	if err := fixture.engine.enqueue(engineCommand{run: func() {
		close(ownerEntered)
		<-ownerRelease
	}}); err != nil {
		t.Fatal(err)
	}
	<-ownerEntered
	packets := fixture.factory.sentPackets()
	client := fixture.factory.clients()[0]
	client.readErrors <- &engineFakeReadError{kind: wkproto.ReadErrorNonTerminal, clientSeq: packets[3].ClientSeq, clientMsgNo: intent.Logical.ClientMsgNo}
	<-reads // current-attempt error is now queued while the owner is paused
	ackGate.armed.Store(true)
	client.frames <- firstDeadlineAck(intent.Logical, packets[0].ClientSeq)
	<-ackGate.verified // the original ACK is verified, but not queued yet
	openOwner()
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatal(err)
	}
	ackGate.open()
	<-reads
	assertRetryTerminalAckSettlement(t, fixture)
}

func retryTerminalAckFixture(t *testing.T) (engineTestFixture, TrafficIntent, time.Time, <-chan struct{}, *verifiedAckCallbackGate) {
	t.Helper()
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	reads := make(chan struct{}, 16)
	fixture.factory.readCycles = reads
	ackGate := &verifiedAckCallbackGate{verified: make(chan struct{}), release: make(chan struct{})}
	original := fixture.pool.onSendack
	// This is the existing observer boundary: verification is complete before
	// the callback transfers that fact to the owner. No product hook is added.
	fixture.pool.onSendack = func(uid string, ack *frame.SendackPacket, err error) {
		if ackGate.armed.CompareAndSwap(true, false) {
			close(ackGate.verified)
			<-ackGate.release
		}
		original(uid, ack, err)
	}
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ackGate.open(); _ = fixture.engine.Stop() })
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 6}); err != nil {
		t.Fatal(err)
	}
	<-reads
	intent := fixture.intent(t, uid, "retry-terminal-ack-group", 180, TrafficGroup)
	now := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, now); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatal(err)
	}
	for number := uint8(1); number <= 3; number++ {
		now = now.Add(20 * time.Millisecond)
		fixture.clock.Set(now)
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatal(err)
		}
		retry, err := fixture.retry.Attempt(intent.Logical, number)
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(retry.Delay)
		fixture.clock.Set(now)
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatal(err)
		}
	}
	if got := fixture.factory.sentCount(); got != 4 {
		t.Fatalf("registered wire attempts = %d, want original + three retries", got)
	}
	return fixture, intent, now, reads, ackGate
}

func assertRetryTerminalAckSettlement(t *testing.T, fixture engineTestFixture) {
	t.Helper()
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	verification := fixture.verifier.Snapshot()
	if verification.Attempts != 4 || verification.RetryAttempts != 3 || verification.Acknowledged != 1 ||
		verification.Terminal != 0 || verification.DuplicateCompletions != 0 || verification.PendingCurrent != 0 ||
		verification.Classification != "" || snapshot.InflightCurrent != 0 || snapshot.FinalFailures != 0 {
		t.Errorf("exact ACK did not win owner terminal decision: attempts=%d retry=%d acknowledged=%d terminal=%d duplicates=%d pending=%d classification=%s inflight=%d finalFailures=%d evidence=%+v", verification.Attempts, verification.RetryAttempts, verification.Acknowledged, verification.Terminal, verification.DuplicateCompletions, verification.PendingCurrent, verification.Classification, snapshot.InflightCurrent, snapshot.FinalFailures, fixture.evidence.Snapshot())
	}
	assertFirstDeadlineCounters(t, verification, 1, 1, 1, 0)
	if verification.HotSendackLatency.Count != 1 {
		t.Errorf("verification latency counted %d times", verification.HotSendackLatency.Count)
	}
}

type verifiedAckCallbackGate struct {
	armed    atomic.Bool
	verified chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (g *verifiedAckCallbackGate) open() { g.once.Do(func() { close(g.release) }) }

func TestEngineNonRetriableTerminalJoinsVerifiedAckWithoutErasingPriorEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		invalidAck bool
	}{
		{name: "non-retriable NACK"},
		{name: "invalid positive ACK retains product failure", invalidAck: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, intent, now, reads, ackGate := retryTerminalAckFixture(t)
			ownerEntered, ownerRelease := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			openOwner := func() { releaseOnce.Do(func() { close(ownerRelease) }) }
			defer openOwner()
			if err := fixture.engine.enqueue(engineCommand{run: func() {
				close(ownerEntered)
				<-ownerRelease
			}}); err != nil {
				t.Fatal(err)
			}
			<-ownerEntered
			packets := fixture.factory.sentPackets()
			client := fixture.factory.clients()[0]
			rejected := &frame.SendackPacket{ClientMsgNo: intent.Logical.ClientMsgNo, ClientSeq: packets[3].ClientSeq, ReasonCode: frame.ReasonAuthFail}
			if test.invalidAck {
				rejected.ReasonCode = frame.ReasonSuccess // missing positive identity is a real product error
			}
			client.frames <- rejected
			<-reads // the actual session drain queued its current-attempt failure
			ackGate.armed.Store(true)
			client.frames <- firstDeadlineAck(intent.Logical, packets[0].ClientSeq)
			<-ackGate.verified
			openOwner()
			if _, err := fixture.engine.Advance(now); err != nil {
				t.Fatal(err)
			}
			ackGate.open()
			<-reads
			snapshot, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			verification := fixture.verifier.Snapshot()
			if verification.Acknowledged != 1 || verification.Attempts != 4 || verification.RetryAttempts != 3 ||
				verification.Terminal != 0 || verification.DuplicateCompletions != 0 || verification.PendingCurrent != 0 ||
				snapshot.InflightCurrent != 0 || snapshot.FinalFailures != 0 {
				t.Errorf("queued failure ignored exact acknowledged completion: acknowledged=%d attempts=%d retry=%d terminal=%d duplicate=%d pending=%d inflight=%d finalFailures=%d evidence=%+v", verification.Acknowledged, verification.Attempts, verification.RetryAttempts, verification.Terminal, verification.DuplicateCompletions, verification.PendingCurrent, snapshot.InflightCurrent, snapshot.FinalFailures, fixture.evidence.Snapshot())
			}
			if test.invalidAck {
				if verification.Classification != SyncClassificationProductFailure {
					t.Fatalf("prior invalid-ACK evidence was erased: %+v", verification)
				}
			} else if verification.SendackRejections != 1 || verification.Classification != "" {
				t.Fatalf("prior NACK count was erased or invented a failure: %+v", verification)
			}
			assertFirstDeadlineCounters(t, verification, 1, 1, 1, 0)
		})
	}
}
