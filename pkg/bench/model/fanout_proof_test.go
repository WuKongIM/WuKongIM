package model

import "testing"

func TestFanoutProofSnapshotMatchesOnlyExactThreeWayMultiset(t *testing.T) {
	zero := "0000000000000000000000000000000000000000000000000000000000000000"
	want := FanoutMultisetSummary{Count: 9, DigestA: zero, DigestB: zero}
	snapshot := FanoutProofSnapshot{
		Version:          FanoutProofVersion,
		Required:         true,
		EvidenceComplete: true,
		LogicalSendACKs:  1,
		Expected:         want,
		Received:         want,
		RecvACKed:        want,
	}
	if !snapshot.Complete() {
		t.Fatal("complete identity-free snapshot reported incomplete")
	}
	if !snapshot.Matches() {
		t.Fatal("exact expected/received/recvacked multisets did not match")
	}

	snapshot.Received.DigestB = "1000000000000000000000000000000000000000000000000000000000000000"
	if snapshot.Matches() {
		t.Fatal("equal counts with a different identity digest matched")
	}
}

func TestFanoutProofNotRequiredIsTheOnlyCompleteOptionalShape(t *testing.T) {
	snapshot := FanoutProofNotRequired()
	if !snapshot.Complete() || !snapshot.Matches() {
		t.Fatalf("canonical not-required proof was rejected: %+v", snapshot)
	}

	snapshot.LogicalSendACKs = 1
	if snapshot.Complete() {
		t.Fatal("not-required proof with traffic was accepted")
	}
}

func TestFanoutProofSnapshotRejectsNonCanonicalEmptyDigest(t *testing.T) {
	snapshot := FanoutProofNotRequired()
	snapshot.Required = true
	snapshot.Expected.DigestA = "1000000000000000000000000000000000000000000000000000000000000000"
	if snapshot.Complete() {
		t.Fatal("zero-count multiset with nonzero digest was accepted")
	}
}
