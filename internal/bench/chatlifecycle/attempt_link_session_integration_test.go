//go:build integration

package chatlifecycle

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type attemptLinkClock struct {
	mu    sync.Mutex
	base  *sessionFakeClock
	reads int
}

func (c *attemptLinkClock) Now() time.Time {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.base.Now()
}
func (c *attemptLinkClock) reset()     { c.mu.Lock(); c.reads = 0; c.mu.Unlock() }
func (c *attemptLinkClock) count() int { c.mu.Lock(); defer c.mu.Unlock(); return c.reads }

type attemptLinkScopeFactory struct{ base *sessionFakeFactory }

func (f attemptLinkScopeFactory) NewSession(ctx context.Context, uid, token string) (SessionClient, error) {
	client, err := f.base.NewSession(ctx, uid, token)
	if err != nil {
		return nil, err
	}
	return attemptLinkScopeClient{SessionClient: client}, nil
}

type attemptLinkScopeClient struct{ SessionClient }

func (attemptLinkScopeClient) ConnectFingerprint() [16]byte { return [16]byte{7} }
func (c attemptLinkScopeClient) ReadFrameTiming(ctx context.Context) (frame.Frame, SessionFrameTiming, error) {
	return c.SessionClient.(sessionFrameTimingObserver).ReadFrameTiming(ctx)
}

// F04/F06/F09: execute the real SessionPool drain and direct verifier ACK
// observation with a known SDK time. No diagnostic clock read may be added.
func TestAttemptLinkSessionDrainUsesOriginalSDKClockWithoutAdditionalRead(t *testing.T) {
	f := newSessionTestFixture(t)
	evidence, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(f.traffic, VerifierConfig{PendingCapacity: 128, SequenceCapacity: 128, CorrelationCapacity: 16, CorrelationDeadline: time.Minute, EnableAttemptLinkDiagnostics: true}, evidence)
	if err != nil {
		t.Fatal(err)
	}
	clock := &attemptLinkClock{base: f.clock}
	done := make(chan error, 1)
	pool, err := NewSessionPool(SessionPoolConfig{Identity: f.identity, Schedule: f.schedule, Catalog: f.catalog, Factory: attemptLinkScopeFactory{f.factory}, Syncer: f.syncer, Verifier: verifier, Clock: clock, DeviceID: "wkbench-lifecycle", StartingCapacity: 128, OnSendack: func(_ string, _ *frame.SendackPacket, err error) { done <- err }})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.CloseAll()
	at := f.clock.Now()
	uid := f.identity.UID(5)
	if _, err = pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 5, LoginOrdinal: 1}); err != nil {
		t.Fatal(err)
	}
	scope := pool.sendConnectionFingerprint(uid)
	if scope != [16]byte{7} {
		t.Fatal("accepted session did not retain actual optional provider digest")
	}
	logical := mustLogicalSend(t, f.traffic, 0, 501, TrafficPerson, uid, "private-recipient")
	if err = verifier.RegisterSend(logical, at, SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	attempt, err := newTestRetryPolicy(t, f.traffic).Attempt(logical, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifier.ObserveAttemptWithDeadlineEvidence(logical, attempt, 1, at.Add(time.Second), AttemptLinkRegistration{ConnectFingerprint: scope}); err != nil {
		t.Fatal(err)
	}
	client := f.factory.clients()[0]
	client.mu.Lock()
	client.timing = SessionFrameTiming{PendingStartedAt: at, WriteStartedAt: at.Add(250 * time.Millisecond), ObservedAt: at.Add(2 * time.Second), ConnectFingerprint: scope}
	client.mu.Unlock()
	clock.reset()
	client.frames <- firstDeadlineAck(logical, 1)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("real session drain did not verify ACK")
	}
	if got := clock.count(); got != 0 {
		t.Fatalf("known SDK result added pool/diagnostic clock reads: %d", got)
	}
	records := verifier.AttemptLinkDiagnostics(1).Records
	if len(records) != 1 || records[0].Key.ClientSeq != 1 || records[0].Key.ConnectFingerprint != scope || records[0].ObservedAfterRegistration != 2*time.Second {
		t.Fatalf("drain lost exact original observation: %+v", records)
	}
	assertFirstDeadlineCounters(t, verifier.Snapshot(), 1, 1, 1, 0)
}
