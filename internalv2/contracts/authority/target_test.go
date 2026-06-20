package authority

import "testing"

func TestTargetIsLocal(t *testing.T) {
	target := Target{HashSlot: 3, SlotID: 9, LeaderNodeID: 2, LeaderTerm: 13, ConfigEpoch: 17, RouteRevision: 7, AuthorityEpoch: 11}
	if !target.IsLocal(2) {
		t.Fatalf("IsLocal(2) = false, want true")
	}
	if target.IsLocal(1) {
		t.Fatalf("IsLocal(1) = true, want false")
	}
}

func TestTargetValidateRejectsMissingLeader(t *testing.T) {
	target := Target{HashSlot: 3, SlotID: 9, RouteRevision: 7, AuthorityEpoch: 11}
	if err := target.Validate(); err == nil {
		t.Fatalf("Validate() error = nil, want missing leader error")
	}
}

func TestTargetValidateAllowsZeroTermAndConfig(t *testing.T) {
	target := Target{HashSlot: 3, SlotID: 9, LeaderNodeID: 2, RouteRevision: 7, AuthorityEpoch: 11}
	if err := target.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil for v0 leader-only validation", err)
	}
}
