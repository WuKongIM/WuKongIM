package chatlifecycle

import (
	"testing"
	"time"
)

func TestVerifierEngineRetryExhaustionAtomicallyChoosesExactCompletion(t *testing.T) {
	model, verifier, logical, started := firstDeadlineFixture(t)
	policy := newTestRetryPolicy(t, model)
	for number := uint8(1); number <= 3; number++ {
		attempt, err := policy.Attempt(logical, number)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifier.ObserveAttemptWithDeadline(logical, attempt, uint64(number)+1, started.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	completion, err := verifier.completeEngineTerminalOrAcknowledged(logical, TerminalSendRetryExhausted, retryExhaustedAttemptTimeout)
	if err != nil || completion == nil || completion.clientSeq != 1 || completion.messageID != 1 || completion.messageSeq != 1 {
		t.Fatalf("four registered attempts lost their exact positive completion: %+v/%v", completion, err)
	}
	if got := verifier.Snapshot(); got.Terminal != 0 || got.DuplicateCompletions != 0 || got.Classification != "" || got.Acknowledged != 1 || got.Attempts != 4 {
		t.Fatalf("completion decision fabricated a terminal result: %+v", got)
	}
	assertFirstDeadlineCounters(t, verifier.Snapshot(), 1, 1, 1, 0)
}

func TestVerifierEngineRetryExhaustionPreservesUnacknowledgedTerminal(t *testing.T) {
	_, verifier, logical, _ := firstDeadlineFixture(t)
	completion, err := verifier.completeEngineTerminalOrAcknowledged(logical, TerminalSendRetryExhausted, retryExhaustedAttemptTimeout)
	if completion != nil {
		t.Fatalf("unacknowledged send acquired proof: %+v", completion)
	}
	assertVerificationCode(t, err, FailureCodeTerminalSend)
	if got := verifier.Snapshot(); got.Terminal != 1 || got.Acknowledged != 0 || got.FirstAttemptFailures != 1 || got.TerminalReasons.RetryExhausted.AttemptTimeout != 1 {
		t.Fatalf("real retry exhaustion was hidden: %+v", got)
	}
}

func TestVerifierEngineRetryExhaustionRetainsStrictUnknownAndInvalidGuards(t *testing.T) {
	for _, test := range []string{"missing", "different logical", "terminal", "invalid positive", "unknown completed sequence", "outstanding completed sequence", "invalid cause"} {
		t.Run(test, func(t *testing.T) {
			_, verifier, logical, started := firstDeadlineFixture(t)
			want := FailureCodeDuplicateCompletion
			cause := retryExhaustedAttemptTimeout
			switch test {
			case "missing":
				if err := verifier.abortSendHarness(logical); err != nil {
					t.Fatal(err)
				}
				want = FailureCodeGeneratorInvariant
			case "terminal":
				assertVerificationCode(t, verifier.CompleteTerminal(logical, TerminalSendNonRetriable), FailureCodeTerminalSend)
			default:
				if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			switch test {
			case "different logical":
				logical.Target = "different-target"
				want = FailureCodeGeneratorInvariant
			case "invalid positive":
				verifier.pending[logical.ClientMsgNo].messageSeq = 0
			case "unknown completed sequence":
				verifier.pending[logical.ClientMsgNo].completionClientSeq = 99
			case "outstanding completed sequence":
				verifier.pending[logical.ClientMsgNo].attempt(1).outstanding = true
			case "invalid cause":
				cause = retryExhaustedCause(255)
				want = FailureCodeGeneratorInvariant
			}
			completion, err := verifier.completeEngineTerminalOrAcknowledged(logical, TerminalSendRetryExhausted, cause)
			if completion != nil {
				t.Fatalf("invalid state acquired an exact completion: %+v", completion)
			}
			assertVerificationCode(t, err, want)
		})
	}
}

func TestVerifierPublicTerminalRemainsStrictAfterAcknowledgement(t *testing.T) {
	_, verifier, logical, started := firstDeadlineFixture(t)
	if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertVerificationCode(t, verifier.CompleteTerminal(logical, TerminalSendRetryExhausted), FailureCodeDuplicateCompletion)
}

func TestVerifierEngineTerminalDecisionRequiresClosedKindAndCause(t *testing.T) {
	for _, test := range []struct {
		name  string
		kind  TerminalSendCode
		cause retryExhaustedCause
		valid bool
	}{
		{"retry exhaustion", TerminalSendRetryExhausted, retryExhaustedAttemptTimeout, true},
		{"non-retriable", TerminalSendNonRetriable, 0, true},
		{"session closed", TerminalSendSessionClosed, 0, true},
		{"unknown terminal kind", TerminalSendCode(255), 0, false},
		{"foreign terminal cause", TerminalSendNonRetriable, retryExhaustedTransportError, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, verifier, logical, started := firstDeadlineFixture(t)
			if err := verifier.HandleSendackAt(firstDeadlineAck(logical, 1), started.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			completion, err := verifier.completeEngineTerminalOrAcknowledged(logical, test.kind, test.cause)
			if test.valid {
				if completion == nil || err != nil {
					t.Fatalf("known completion cannot settle a valid owner decision: %+v/%v", completion, err)
				}
			} else {
				if completion != nil {
					t.Fatalf("unknown terminal request acquired a success proof: %+v", completion)
				}
				assertVerificationCode(t, err, FailureCodeGeneratorInvariant)
			}
		})
	}
}
