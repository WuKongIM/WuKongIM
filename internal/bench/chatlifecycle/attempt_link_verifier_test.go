package chatlifecycle

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func attemptLinkVerifierFixture(t *testing.T, enabled bool) (TrafficModel, *Verifier) {
	t.Helper()
	model := newTestTrafficModel(t, FormalConfig())
	evidence, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(model, VerifierConfig{PendingCapacity: 64, SequenceCapacity: 64, CorrelationCapacity: 64, CorrelationDeadline: 10 * time.Second, EnableAttemptLinkDiagnostics: enabled}, evidence)
	if err != nil {
		t.Fatal(err)
	}
	return model, v
}
func attemptLinkRegister(t *testing.T, model TrafficModel, v *Verifier, index uint64, seq uint64, at time.Time) LogicalSend {
	t.Helper()
	logical := mustLogicalSend(t, model, 0, index, TrafficPerson, "private-sender", "private-recipient")
	if err := v.RegisterSend(logical, at, SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	retry, err := newTestRetryPolicy(t, model).Attempt(logical, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.ObserveAttemptWithDeadlineEvidence(logical, retry, seq, at.Add(time.Second), AttemptLinkRegistration{ConnectFingerprint: [16]byte{7}}); err != nil {
		t.Fatal(err)
	}
	return logical
}
func attemptLinkACK(v *Verifier, logical LogicalSend, seq uint64, at time.Time) error {
	return v.HandleSendackWithEvidenceAt(firstDeadlineAck(logical, seq), at, AttemptLinkResultEvidence{ConnectFingerprint: [16]byte{7}, Kind: "sendack_success"})
}
func TestAttemptLinkBoundaryChangesOnlyOptionalEvidence(t *testing.T) {
	for _, offset := range []time.Duration{time.Second - time.Nanosecond, time.Second, time.Second + time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			var native [2]VerifierSnapshot
			for i, enabled := range []bool{false, true} {
				model, v := attemptLinkVerifierFixture(t, enabled)
				at := time.Unix(70000, 0)
				logical := attemptLinkRegister(t, model, v, 501, 1, at)
				if err := attemptLinkACK(v, logical, 1, at.Add(offset)); err != nil {
					t.Fatal(err)
				}
				native[i] = v.Snapshot()
				d := v.AttemptLinkDiagnostics(1)
				want := 0
				if enabled && offset >= time.Second {
					want = 1
				}
				if !enabled {
					if d != nil {
						t.Fatal("disabled path retained optional evidence")
					}
					continue
				}
				if d == nil || len(d.Records) != want {
					t.Fatalf("late prefix=%+v want%d", d, want)
				}
				if want == 1 {
					r := d.Records[0]
					if r.Key.ClientSeq != 1 || r.Key.MessageFingerprint != sendtrace.MessageFingerprint(logical.ClientMsgNo) || r.Key.ConnectFingerprint != [16]byte{7} || r.ObservedAfterRegistration != offset || r.DeadlineAfterRegistration != time.Second {
						t.Fatalf("wrong exact boundary/key: %+v", r)
					}
				}
			}
			if !reflect.DeepEqual(native[0], native[1]) {
				t.Fatalf("opt-in changed native state: off=%+v on=%+v", native[0], native[1])
			}
		})
	}
}
func TestAttemptLinkRetainsOriginalAfterRetryAndIgnoresDuplicate(t *testing.T) {
	model, v := attemptLinkVerifierFixture(t, true)
	at := time.Unix(70000, 0)
	logical := attemptLinkRegister(t, model, v, 501, 1, at)
	retry, err := newTestRetryPolicy(t, model).Attempt(logical, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.ObserveAttemptWithDeadlineEvidence(logical, retry, 2, at.Add(3*time.Second), AttemptLinkRegistration{ConnectFingerprint: [16]byte{7}}); err != nil {
		t.Fatal(err)
	}
	if err = attemptLinkACK(v, logical, 2, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = v.ReleaseSendAt(logical, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if d := v.AttemptLinkDiagnostics(1); len(d.Records) != 0 {
		t.Fatal("retry result claimed the original deadline")
	}
	if err = attemptLinkACK(v, logical, 1, at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	_ = attemptLinkACK(v, logical, 1, at.Add(4*time.Second))
	d := v.AttemptLinkDiagnostics(1)
	if len(d.Records) != 1 || d.Records[0].Key.ClientSeq != 1 || d.Records[0].Ordinal != 1 {
		t.Fatalf("retry/duplicate contaminated late prefix: %+v", d)
	}
	assertFirstDeadlineCounters(t, v.Snapshot(), 1, 1, 1, 0)
}
func TestAttemptLinkUnknownClockLocalAdmissionAndExpiredReceiptDoNotInventLate(t *testing.T) {
	for _, mode := range []string{"zero_clock", "regressing_clock", "local_admission", "canceled", "expired_receipt"} {
		t.Run(mode, func(t *testing.T) {
			model, v := attemptLinkVerifierFixture(t, true)
			at := time.Unix(70000, 0)
			logical := attemptLinkRegister(t, model, v, 501, 1, at)
			switch mode {
			case "zero_clock":
				_ = attemptLinkACK(v, logical, 1, time.Time{})
			case "regressing_clock":
				_ = attemptLinkACK(v, logical, 1, at.Add(-time.Nanosecond))
			case "local_admission":
				_ = v.ResolveAttemptLocalAdmissionErrorAt(logical.ClientMsgNo, 1, at.Add(2*time.Second))
			case "canceled":
				_ = v.abortSendHarness(logical)
			case "expired_receipt":
				retry, _ := newTestRetryPolicy(t, model).Attempt(logical, 1)
				_ = v.ObserveAttemptWithDeadline(logical, retry, 2, at.Add(3*time.Second))
				_ = attemptLinkACK(v, logical, 2, at.Add(2*time.Second))
				_ = v.ReleaseSendAt(logical, at.Add(2*time.Second))
				v.ExpireCorrelations(at.Add(12 * time.Second))
			}
			if d := v.AttemptLinkDiagnostics(1); len(d.Records) != 0 {
				t.Fatalf("%s manufactured a confirmed-late record: %+v", mode, d)
			}
		})
	}
}
func TestAttemptLinkResultKindsAndConnectionMismatchPreserveNativeCounter(t *testing.T) {
	for _, kind := range []string{"sendack_success", "sendack_rejected", "transport_error", "connection_mismatch"} {
		t.Run(kind, func(t *testing.T) {
			model, v := attemptLinkVerifierFixture(t, true)
			at := time.Unix(70000, 0)
			logical := attemptLinkRegister(t, model, v, 501, 1, at)
			evidence := AttemptLinkResultEvidence{ConnectFingerprint: [16]byte{7}, Kind: kind}
			switch kind {
			case "transport_error":
				v.ObserveAttemptErrorWithEvidenceAt(logical.ClientMsgNo, 1, at.Add(time.Second), evidence)
				_ = v.ResolveAttemptErrorAt(logical.ClientMsgNo, 1, at.Add(time.Second))
			default:
				ack := firstDeadlineAck(logical, 1)
				if kind == "sendack_rejected" {
					ack.ReasonCode = frame.ReasonSystemError
					ack.MessageID = 0
					ack.MessageSeq = 0
				}
				if kind == "connection_mismatch" {
					evidence.ConnectFingerprint = [16]byte{8}
					evidence.Kind = "sendack_success"
				}
				_ = v.HandleSendackWithEvidenceAt(ack, at.Add(time.Second), evidence)
			}
			d := v.AttemptLinkDiagnostics(1)
			if len(d.Records) != 1 {
				t.Fatalf("late result lost: %+v", d)
			}
			if kind == "connection_mismatch" && d.Records[0].Attribution != "connection_unknown" {
				t.Fatal("mismatched scope was promoted to exact")
			}
			if got := v.Snapshot().FirstAttemptTimeouts; got != 1 {
				t.Fatalf("diagnostic mismatch changed confirmed native count: %d", got)
			}
		})
	}
}
func TestAttemptLinkFixed16SaturatingDroppedAndDetachedCuts(t *testing.T) {
	model, v := attemptLinkVerifierFixture(t, true)
	at := time.Unix(70000, 0)
	for i := uint64(0); i < 18; i++ {
		logical := attemptLinkRegister(t, model, v, 501+i, i+1, at)
		_ = attemptLinkACK(v, logical, i+1, at.Add(2*time.Second))
		_ = v.ReleaseSendAt(logical, at.Add(2*time.Second))
	}
	live := v.AttemptLinkDiagnostics(1)
	if len(live.Records) != 16 || live.Dropped != 2 {
		t.Fatalf("prefix bound: %+v", live)
	}
	live.Records[0].Key.ClientSeq = 999
	next := v.AttemptLinkDiagnostics(1)
	if next.Records[0].Key.ClientSeq != 1 {
		t.Fatal("snapshot exposed mutable recorder backing")
	}
	v.sendMu.Lock()
	v.attemptLinkDiagnostics.dropped = math.MaxUint64
	v.sendMu.Unlock()
	logical := attemptLinkRegister(t, model, v, 600, 100, at)
	_ = attemptLinkACK(v, logical, 100, at.Add(2*time.Second))
	if got := v.AttemptLinkDiagnostics(1).Dropped; got != math.MaxUint64 {
		t.Fatalf("dropped wrapped: %d", got)
	}
	assertFirstDeadlineCounters(t, v.Snapshot(), 19, 19, 19, 0)
}
