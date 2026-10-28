package chatlifecycle

import (
	"testing"
	"time"
	"unsafe"
)

// F06: first16 is not the whole new-state budget. Attempt and late-receipt
// metadata are separately bounded by the preexisting capacities.
func TestAttemptLinkMetadataHasFixedPerOwnerCost(t *testing.T) {
	if got := unsafe.Sizeof(firstAttemptDeadline{}); got > 96 {
		t.Fatalf("original deadline metadata grew beyond fixed 96B bound: %d", got)
	}
	if got := unsafe.Sizeof(sendAttemptIdentity{}); got > 128 {
		t.Fatalf("attempt slot grew beyond fixed 128B bound: %d", got)
	}
	if got := unsafe.Sizeof(releasedAttempt{}); got > 192 {
		t.Fatalf("bounded released receipt grew beyond 192B: %d", got)
	}
	if got := unsafe.Sizeof(SessionFrameTiming{}); got > 96 {
		t.Fatalf("SDK timing queue element grew beyond96B: %d", got)
	}
	t.Logf("owner metadata: first_deadline=%d attempt_slot=%d released_receipt=%d session_timing=%d session_owner=%d; multiply by unchanged configured owner/queue bounds, not the 16-record prefix cap", unsafe.Sizeof(firstAttemptDeadline{}), unsafe.Sizeof(sendAttemptIdentity{}), unsafe.Sizeof(releasedAttempt{}), unsafe.Sizeof(SessionFrameTiming{}), unsafe.Sizeof(onlineSession{}))
}
func TestAttemptLinkReusesKnownSDKTimestampsAndRegressingSegmentsRemainUnknown(t *testing.T) {
	for _, regress := range []bool{false, true} {
		model, v := attemptLinkVerifierFixture(t, true)
		at := time.Unix(70000, 0)
		logical := attemptLinkRegister(t, model, v, 501, 1, at)
		timing := AttemptLinkResultEvidence{ConnectFingerprint: [16]byte{7}, Kind: "sendack_success", PendingStartedAt: at, WriteStartedAt: at.Add(250 * time.Millisecond)}
		if regress {
			timing.WriteStartedAt = at.Add(-time.Nanosecond)
		}
		if err := v.HandleSendackWithEvidenceAt(firstDeadlineAck(logical, 1), at.Add(2*time.Second), timing); err != nil {
			t.Fatal(err)
		}
		row := v.AttemptLinkDiagnostics(1).Records[0]
		if !regress && (row.SDKPendingToWrite != 250*time.Millisecond || row.SDKWriteToObserved != 1750*time.Millisecond || row.TimingMask != 3) {
			t.Fatalf("existing SDK boundaries changed: %+v", row)
		}
		if regress && row.TimingMask != 0 {
			t.Fatal("regressing SDK segment became measured timing")
		}
		assertFirstDeadlineCounters(t, v.Snapshot(), 1, 1, 1, 0)
	}
}
func TestAttemptLinkJoinedGenerationResetClearsOnlyOptionalPrefix(t *testing.T) {
	model, v := attemptLinkVerifierFixture(t, true)
	at := time.Unix(70000, 0)
	logical := attemptLinkRegister(t, model, v, 501, 1, at)
	_ = attemptLinkACK(v, logical, 1, at.Add(2*time.Second))
	old := v.AttemptLinkDiagnostics(1)
	// This isolated verifier owns no sender/drain goroutines; the reset has
	// the same joined-owner precondition required by the real generation.
	v.resetRuntime()
	fresh := v.AttemptLinkDiagnostics(2)
	if fresh.Generation != 2 || len(fresh.Records) != 0 || fresh.Dropped != 0 {
		t.Fatal("new generation inherited old diagnostic prefix")
	}
	if len(old.Records) != 1 || old.Generation != 1 {
		t.Fatal("reset mutated previously detached cut")
	}
}
