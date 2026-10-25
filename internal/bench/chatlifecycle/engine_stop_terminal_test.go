package chatlifecycle

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestEngineStopRecordsSessionClosedWithoutGeneratorInvariant(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = fixture.engine.Stop() })
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 7}); err != nil {
		t.Fatalf("Login sender: %v", err)
	}
	intent := fixture.intent(t, uid, "pending-stop-group", 147, TrafficGroup)
	now := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, now); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := fixture.engine.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	verified := fixture.verifier.Snapshot()
	evidence := fixture.evidence.Snapshot()
	if got := evidenceCountForClass(evidence, FailureClassHarness); got != 0 {
		t.Fatalf("legitimate session-closed completion manufactured harness evidence: count=%d", got)
	}
	if verified.Terminal != 1 || verified.TerminalReasons != (TerminalSendSnapshot{SessionClosed: 1}) || verified.PendingCurrent != 0 || verified.PendingUnfinished != 0 || verified.FirstAttemptFailures != 1 {
		t.Fatalf("stopped SEND was not recorded and released once: %+v", verified)
	}
	if verified.FirstAttemptDeadlines != 1 || verified.FirstAttemptDeadlineResolved != 0 || verified.FirstAttemptTimeouts != 0 {
		t.Fatalf("Stop invented first-attempt transport coverage: %+v", verified)
	}
	if evidence.Classification != SyncClassificationProductFailure || evidenceCountForClass(evidence, FailureClassSend) != 1 {
		t.Fatalf("real session-closed product outcome was erased: %+v", evidence)
	}
	if err := fixture.engine.Stop(); err != nil {
		t.Fatalf("repeated Stop: %v", err)
	}
	if got := fixture.verifier.Snapshot(); !reflect.DeepEqual(got, verified) {
		t.Fatalf("repeated Stop counted completion twice: before=%+v after=%+v", verified, got)
	}
	if got := fixture.evidence.Snapshot(); !reflect.DeepEqual(got, evidence) {
		t.Fatalf("repeated Stop changed evidence: before=%+v after=%+v", evidence, got)
	}
}

func TestEngineStopKeepsMissingCompletedAndInvalidVerifierStateStrict(t *testing.T) {
	for _, name := range []string{"missing_pending", "already_acknowledged", "invalid_terminal_code"} {
		t.Run(name, func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			t.Cleanup(func() { _ = fixture.engine.Stop() })
			uid := fixture.identity.UID(7)
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 7}); err != nil {
				t.Fatalf("Login sender: %v", err)
			}
			intent := fixture.intent(t, uid, "strict-stop-group", 147, TrafficGroup)
			now := fixture.clock.Now()
			if err := fixture.engine.SubmitGranted(intent, now); err != nil {
				t.Fatalf("SubmitGranted: %v", err)
			}
			if _, err := fixture.engine.Advance(now); err != nil {
				t.Fatalf("Advance: %v", err)
			}
			switch name {
			case "missing_pending":
				assertVerificationCode(t, fixture.verifier.CompleteTerminal(intent.Logical, TerminalSendNonRetriable), FailureCodeTerminalSend)
				if err := fixture.verifier.ReleaseSendAt(intent.Logical, now); err != nil {
					t.Fatalf("public completed SEND release: %v", err)
				}
			case "already_acknowledged":
				packet := fixture.factory.sentPackets()[0]
				if err := fixture.verifier.HandleSendackAt(&frame.SendackPacket{ClientMsgNo: packet.ClientMsgNo, ClientSeq: packet.ClientSeq, MessageID: 1, MessageSeq: 1, ReasonCode: frame.ReasonSuccess}, now); err != nil {
					t.Fatalf("public ACK before owner settlement: %v", err)
				}
			case "invalid_terminal_code":
				assertVerificationCode(t, fixture.verifier.CompleteTerminal(intent.Logical, TerminalSendCode(255)), FailureCodeGeneratorInvariant)
			}
			before := fixture.verifier.Snapshot()
			if err := fixture.engine.Stop(); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			after := fixture.verifier.Snapshot()
			if evidenceCountForClass(fixture.evidence.Snapshot(), FailureClassHarness) == 0 {
				t.Fatal("strict public verifier violation was treated as successful cleanup")
			}
			if name == "invalid_terminal_code" {
				if after.Terminal != 1 || after.TerminalReasons != (TerminalSendSnapshot{SessionClosed: 1}) || evidenceCountForClass(fixture.evidence.Snapshot(), FailureClassHarness) != 1 {
					t.Fatalf("original invalid-code evidence changed during valid cleanup: %+v", after)
				}
			} else if after.Terminal != before.Terminal || after.TerminalReasons != before.TerminalReasons || after.Acknowledged != before.Acknowledged {
				t.Fatalf("invalid cleanup state manufactured a new product outcome: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestEngineStopPreservesOriginalRetryExhaustionBeforePendingCleanup(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: time.Second})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = fixture.engine.Stop() })
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 7}); err != nil {
		t.Fatalf("Login sender: %v", err)
	}
	intent := fixture.intent(t, uid, "original-timeout-group", 147, TrafficGroup)
	now := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, now); err != nil {
		t.Fatalf("SubmitGranted original: %v", err)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("initial Advance: %v", err)
	}
	for attempt := uint8(0); attempt <= 3; attempt++ {
		now = now.Add(time.Second)
		fixture.clock.Set(now)
		_, err := fixture.engine.Advance(now)
		if attempt == 3 {
			assertVerificationCode(t, err, FailureCodeTerminalSend)
			break
		}
		retry, err := fixture.retry.Attempt(intent.Logical, attempt+1)
		if err != nil {
			t.Fatalf("retry plan: %v", err)
		}
		now = now.Add(retry.Delay)
		fixture.clock.Set(now)
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("retry Advance: %v", err)
		}
	}
	pending := fixture.intent(t, uid, "pending-after-timeout-group", 148, TrafficGroup)
	if err := fixture.engine.SubmitGranted(pending, now); err != nil {
		t.Fatalf("SubmitGranted pending: %v", err)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("pending Advance: %v", err)
	}
	if err := fixture.engine.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	verified := fixture.verifier.Snapshot()
	want := TerminalSendSnapshot{RetryExhausted: RetryExhaustedSnapshot{Total: 1, AttemptTimeout: 1}, SessionClosed: 1}
	if verified.Terminal != 2 || verified.TerminalReasons != want || verified.FirstAttemptFailures != 2 || verified.PendingCurrent != 0 {
		t.Fatalf("Stop changed original retry-exhaustion evidence: %+v", verified)
	}
	if evidence := fixture.evidence.Snapshot(); evidenceCountForClass(evidence, FailureClassHarness) != 0 || evidenceCountForClass(evidence, FailureClassSend) != 2 || evidence.Classification != SyncClassificationProductFailure {
		t.Fatalf("Stop polluted or erased the original product outcome: %+v", evidence)
	}
}
