package chatlifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestVerifierEngineAttemptJoinsOnlyExactAcknowledgedCompletion(t *testing.T) {
	model, verifier := newTestVerifier(t, 8, 8, 8, time.Minute)
	var logical LogicalSend
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		logical = mustLogicalSend(t, model, 0, ordinal, TrafficPerson, "sender", "recipient")
		sampled, err := verifier.ShouldCorrelate(logical)
		if err != nil {
			t.Fatal(err)
		}
		if sampled {
			break
		}
	}
	started := time.Unix(80_000, 0)
	if err := verifier.RegisterSend(logical, started, SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	policy := newTestRetryPolicy(t, model)
	first, err := policy.Attempt(logical, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.ObserveAttemptWithDeadline(logical, first, 1, started.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	retry, err := policy.Attempt(logical, 1)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := verifier.admitEngineAttemptWithDeadline(logical, retry, 2, started.Add(3*time.Second))
	if err != nil || completion == nil || completion.clientSeq != 1 || completion.messageID != 1 || completion.messageSeq != 1 {
		t.Fatalf("exact completion = %+v / %v", completion, err)
	}
	before := verifier.Snapshot()
	assertFirstDeadlineCounters(t, before, 1, 1, 1, 0)
	if before.Attempts != 1 || before.RetryAttempts != 0 || before.Acknowledged != 1 || before.CorrelationCurrent != 1 || before.HotSendackLatency.Count != 1 {
		t.Fatalf("joining completion changed attempt/latency/correlation evidence: %+v", before)
	}
	if err := verifier.ReleaseSendAt(logical, started.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := verifier.Snapshot(); got.PendingCurrent != 0 || got.CorrelationCurrent != 1 || got.SampledDelivered != 0 {
		t.Fatalf("logical settlement discarded sampled receive proof: %+v", got)
	}
	if err := verifier.HandleRecv(context.Background(), "recipient", mustRecvPacket(t, model, logical, 1, 1), discardRecvAcker{}); err != nil {
		t.Fatal(err)
	}
	if got := verifier.Snapshot(); got.CorrelationCurrent != 0 || got.SampledDelivered != 1 || got.Losses != 0 || got.Acknowledged != 1 || got.HotSendackLatency.Count != 1 {
		t.Fatalf("sampled receive did not close the preserved proof: %+v", got)
	}
}

func TestVerifierEngineAttemptRetainsStrictIdentityAndCompletionGuards(t *testing.T) {
	for _, test := range []string{"missing pending", "different logical", "wrong message identity", "duplicate sequence", "zero sequence", "attempt zero", "terminal", "invalid positive receipt", "unknown completion sequence", "completion still outstanding", "attempt slots exhausted"} {
		t.Run(test, func(t *testing.T) {
			model, verifier, logical, started := firstDeadlineFixture(t)
			policy := newTestRetryPolicy(t, model)
			retry, err := policy.Attempt(logical, 1)
			if err != nil {
				t.Fatal(err)
			}
			if test == "attempt slots exhausted" {
				for number := uint8(1); number < 4; number++ {
					attempt, err := policy.Attempt(logical, number)
					if err != nil {
						t.Fatal(err)
					}
					if err := verifier.ObserveAttemptWithDeadline(logical, attempt, uint64(number)+1, started.Add(time.Second)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if test == "terminal" {
				assertVerificationCode(t, verifier.CompleteTerminal(logical, TerminalSendNonRetriable), FailureCodeTerminalSend)
			} else if test == "missing pending" {
				if err := verifier.abortSendHarness(logical); err != nil {
					t.Fatal(err)
				}
			} else if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			clientSeq := uint64(2)
			switch test {
			case "different logical":
				logical.Target = "different-recipient"
			case "wrong message identity":
				retry.ClientMsgNo = "different-logical-message"
			case "duplicate sequence":
				clientSeq = 1
			case "zero sequence":
				clientSeq = 0
			case "attempt zero":
				retry.Attempt = 0
			case "invalid positive receipt":
				verifier.pending[logical.ClientMsgNo].messageID = 0
			case "unknown completion sequence":
				verifier.pending[logical.ClientMsgNo].completionClientSeq = 99
			case "completion still outstanding":
				verifier.pending[logical.ClientMsgNo].attempt(1).outstanding = true
			case "attempt slots exhausted":
				clientSeq = 5
			}
			completion, err := verifier.admitEngineAttemptWithDeadline(logical, retry, clientSeq, started.Add(3*time.Second))
			if completion != nil {
				t.Fatalf("invalid evidence acquired completion: %+v", completion)
			}
			assertVerificationCode(t, err, FailureCodeGeneratorInvariant)
		})
	}
}

func TestVerifierLegacyAttemptRegistrationRemainsStrictAfterAcknowledgement(t *testing.T) {
	model, verifier, logical, started := firstDeadlineFixture(t)
	if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	retry, err := newTestRetryPolicy(t, model).Attempt(logical, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertVerificationCode(t, verifier.ObserveAttemptWithDeadline(logical, retry, 2, started.Add(3*time.Second)), FailureCodeGeneratorInvariant)
}

func TestVerifierInvalidPositiveAckCannotBecomeCompletionReceipt(t *testing.T) {
	model, verifier, logical, started := firstDeadlineFixture(t)
	invalid := &frame.SendackPacket{ClientMsgNo: logical.ClientMsgNo, ClientSeq: 1, ReasonCode: frame.ReasonSuccess}
	assertVerificationCode(t, verifier.HandleSendackAt(invalid, started.Add(2*time.Second)), FailureCodeInvalidSendack)
	retry, err := newTestRetryPolicy(t, model).Attempt(logical, 1)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := verifier.admitEngineAttemptWithDeadline(logical, retry, 2, started.Add(3*time.Second))
	if completion != nil || err != nil {
		t.Fatalf("incomplete rejected ACK changed ordinary retry admission: %+v / %v", completion, err)
	}
	if got := verifier.Snapshot(); got.Acknowledged != 0 || got.Attempts != 2 || got.FirstAttemptFailures != 1 || got.Classification != SyncClassificationProductFailure {
		t.Fatalf("invalid ACK evidence was hidden by a completion receipt: %+v", got)
	}
}
