package chatlifecycle

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestVerifierFirstAttemptDeadlineUsesTransportObservation(t *testing.T) {
	for _, test := range []struct {
		name      string
		ackOffset time.Duration
		want      uint64
	}{
		{name: "on time", ackOffset: time.Second - time.Nanosecond},
		{name: "exact deadline before timeout work", ackOffset: time.Second, want: 1},
		{name: "late before timeout work", ackOffset: 2 * time.Second, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, verifier, logical, started := firstDeadlineFixture(t)
			if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(test.ackOffset)); err != nil {
				t.Fatal(err)
			}
			assertFirstDeadlineCounters(t, verifier.Snapshot(), 1, 1, test.want, 0)
		})
	}
}

func TestVerifierFirstAttemptDeadlineRetainsOriginalAfterRetryCompletion(t *testing.T) {
	for _, test := range []struct {
		name         string
		ackOffset    time.Duration
		expire       bool
		want         uint64
		wantResolved uint64
	}{
		{name: "late original ACK", ackOffset: 3 * time.Second, want: 1, wantResolved: 1},
		{name: "on time original ACK observed before retry but processed late", ackOffset: time.Second - time.Nanosecond, wantResolved: 1},
		{name: "missing original response expires bounded receipt unknown", expire: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, verifier, logical, started := firstDeadlineFixture(t)
			retry, err := newTestRetryPolicy(t, model).Attempt(logical, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifier.ObserveAttemptWithDeadline(logical, retry, 2, started.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 2), started.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := verifier.ReleaseSendAt(logical, started.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			assertFirstDeadlineCounters(t, verifier.Snapshot(), 1, 0, 0, 0)
			if test.expire {
				verifier.ExpireCorrelations(started.Add(12 * time.Second))
				if got := verifier.Snapshot().ReleasedAttemptCurrent; got != 0 {
					t.Fatalf("released receipts = %d", got)
				}
			} else if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(test.ackOffset)); err != nil {
				t.Fatal(err)
			}
			assertFirstDeadlineCounters(t, verifier.Snapshot(), 1, test.wantResolved, test.want, 0)
			if got := verifier.Snapshot().RetryAttempts; got != 1 {
				t.Fatalf("retry attempts = %d", got)
			}
		})
	}
}

func TestVerifierFirstAttemptDeadlinePreservesExplicitFailureMeaning(t *testing.T) {
	for _, test := range []struct {
		name         string
		local        bool
		legacyClock  bool
		rejected     bool
		offset       time.Duration
		wantResolved uint64
		wantTimeout  uint64
	}{
		{name: "early transport error", offset: time.Millisecond, wantResolved: 1},
		{name: "transport error at deadline", offset: time.Second, wantResolved: 1, wantTimeout: 1},
		{name: "local admission rejected before wire even if callback delayed", local: true, offset: 2 * time.Second, wantResolved: 1},
		{name: "legacy error clock remains unknown", legacyClock: true},
		{name: "early explicit rejection", rejected: true, offset: time.Millisecond, wantResolved: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, verifier, logical, started := firstDeadlineFixture(t)
			if test.rejected {
				ack := firstDeadlineAck(logical, 1)
				ack.MessageID, ack.MessageSeq, ack.ReasonCode = 0, 0, frame.ReasonSystemError
				if err := verifier.HandleSendackAt(ack, started.Add(test.offset)); err == nil {
					t.Fatal("expected explicit SENDACK rejection")
				}
			} else {
				at := started.Add(test.offset)
				if test.legacyClock {
					at = time.Time{}
				}
				resolve := verifier.ResolveAttemptErrorAt
				if test.local {
					resolve = verifier.ResolveAttemptLocalAdmissionErrorAt
				}
				if err := resolve(logical.ClientMsgNo, 1, at); err != nil {
					t.Fatal(err)
				}
			}
			retry, err := newTestRetryPolicy(t, model).Attempt(logical, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifier.ObserveAttemptWithDeadline(logical, retry, 2, started.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			assertFirstDeadlineCounters(t, verifier.Snapshot(), 1, test.wantResolved, test.wantTimeout, 1)
			wantLocal := uint64(0)
			if test.local {
				wantLocal = 1
			}
			if got := verifier.Snapshot().FirstAttemptLocalAdmissionFailures; got != wantLocal {
				t.Fatalf("local admission failures = %d, want %d", got, wantLocal)
			}
		})
	}
}

func TestVerifierFirstAttemptDeadlineUnknownAndCanceledStayBounded(t *testing.T) {
	_, verifier, logical, started := firstDeadlineFixture(t)
	assertFirstDeadlineCounters(t, verifier.Snapshot(), 1, 0, 0, 0)
	if err := verifier.abortSendHarness(logical); err != nil {
		t.Fatal(err)
	}
	assertFirstDeadlineCounters(t, verifier.Snapshot(), 1, 0, 0, 0)
	if got := verifier.Snapshot(); got.PendingCurrent != 0 || got.ReleasedAttemptCurrent != 0 || got.UnknownSendacks != 0 {
		t.Fatalf("canceled observation changed state: %+v", got)
	}

	model, legacy := newTestVerifier(t, 4, 4, 4, time.Second)
	old := mustLogicalSend(t, model, 0, 502, TrafficPerson, "sender", "recipient")
	if err := legacy.RegisterSend(old, started, SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	attempt, err := newTestRetryPolicy(t, model).Attempt(old, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.ObserveAttempt(old, attempt, 1); err != nil {
		t.Fatal(err)
	}
	if err := legacy.HandleSendackAt(firstDeadlineAck(old, 1), started.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertFirstDeadlineCounters(t, legacy.Snapshot(), 0, 0, 0, 0)
}

func firstDeadlineFixture(t *testing.T) (TrafficModel, *Verifier, LogicalSend, time.Time) {
	t.Helper()
	model, verifier := newTestVerifier(t, 4, 4, 4, 10*time.Second)
	logical := mustLogicalSend(t, model, 0, 501, TrafficPerson, "sender", "recipient")
	started := time.Unix(70_000, 0)
	if err := verifier.RegisterSend(logical, started, SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	attempt, err := newTestRetryPolicy(t, model).Attempt(logical, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.ObserveAttemptWithDeadline(logical, attempt, 1, started.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return model, verifier, logical, started
}

func firstDeadlineAck(logical LogicalSend, clientSeq uint64) *frame.SendackPacket {
	return &frame.SendackPacket{
		ClientMsgNo: logical.ClientMsgNo, ClientSeq: clientSeq,
		MessageID: 1, MessageSeq: 1, ReasonCode: frame.ReasonSuccess,
	}
}

func assertFirstDeadlineCounters(t *testing.T, got VerifierSnapshot, deadlines, resolved, timeouts, explicitFailures uint64) {
	t.Helper()
	if got.FirstAttemptDeadlines != deadlines || got.FirstAttemptDeadlineResolved != resolved ||
		got.FirstAttemptTimeouts != timeouts || got.FirstAttemptFailures != explicitFailures {
		t.Fatalf("first deadline counters = deadlines:%d resolved:%d timeouts:%d explicit_failures:%d; want %d/%d/%d/%d", got.FirstAttemptDeadlines, got.FirstAttemptDeadlineResolved, got.FirstAttemptTimeouts, got.FirstAttemptFailures, deadlines, resolved, timeouts, explicitFailures)
	}
	if got.FirstAttemptTimeouts > got.FirstAttemptDeadlineResolved || got.FirstAttemptDeadlineResolved > got.FirstAttemptDeadlines || got.FirstAttemptDeadlines > got.FirstAttempts {
		t.Fatalf("first deadline counter invariant violated: %+v", got)
	}
}
