package backup

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCoordinatorFenceContextRequiresCompleteLeadershipIdentity(t *testing.T) {
	if fence, ok := CoordinatorFenceFromContext(nil); ok || fence != (CoordinatorFence{}) {
		t.Fatalf("nil context returned fence %#v, ok=%v", fence, ok)
	}
	if _, ok := CoordinatorFenceFromContext(context.Background()); ok {
		t.Fatal("background context unexpectedly contained a coordinator fence")
	}

	want := CoordinatorFence{NodeID: 7, Term: 19}
	ctx := WithCoordinatorFence(context.Background(), want.NodeID, want.Term)
	if got, ok := CoordinatorFenceFromContext(ctx); !ok || got != want {
		t.Fatalf("coordinator fence = %#v, ok=%v; want %#v, true", got, ok, want)
	}

	for _, incomplete := range []CoordinatorFence{
		{Term: want.Term},
		{NodeID: want.NodeID},
	} {
		ctx := WithCoordinatorFence(
			context.Background(), incomplete.NodeID, incomplete.Term,
		)
		if got, ok := CoordinatorFenceFromContext(ctx); ok || got != incomplete {
			t.Fatalf("incomplete fence = %#v, ok=%v; want %#v, false", got, ok, incomplete)
		}
	}
}

func TestSystemStateCloneDetachesMutableState(t *testing.T) {
	state := SystemState{
		Plan: &Plan{
			Store: StoreConfig{CredentialCiphertext: []byte("encrypted")},
			RepositoryVerification: &RepositoryVerification{
				Status: RepositoryVerificationVerified,
			},
		},
		ActiveBackup: &BackupJob{
			ID:    "backup-1",
			Slots: []SlotProgress{{HashSlot: 1, OwnerNodeID: 2}},
		},
		ActiveRestore: &RestoreJob{
			ID: "restore-1",
			Slots: []RestoreSlotProgress{{
				HashSlot:       1,
				ReplicaNodeIDs: []uint64{2, 3},
			}},
		},
		ActiveArchiveOperation: &ArchiveOperation{Token: "lease-1"},
		History:                []TaskRecord{{ID: "task-1"}},
	}

	cloned := state.Clone()
	cloned.Plan.Store.CredentialCiphertext[0] = 'E'
	cloned.Plan.RepositoryVerification.Status = RepositoryVerificationUnverified
	cloned.ActiveBackup.Slots[0].OwnerNodeID = 9
	cloned.ActiveRestore.Slots[0].ReplicaNodeIDs[0] = 9
	cloned.ActiveArchiveOperation.Token = "changed"
	cloned.History[0].ID = "changed"

	if got := string(state.Plan.Store.CredentialCiphertext); got != "encrypted" {
		t.Fatalf("source credential ciphertext changed to %q", got)
	}
	if got := state.Plan.RepositoryVerification.Status; got != RepositoryVerificationVerified {
		t.Fatalf("source verification status changed to %q", got)
	}
	if got := state.ActiveBackup.Slots[0].OwnerNodeID; got != 2 {
		t.Fatalf("source backup slot owner changed to %d", got)
	}
	if got := state.ActiveRestore.Slots[0].ReplicaNodeIDs[0]; got != 2 {
		t.Fatalf("source restore replica changed to %d", got)
	}
	if got := state.ActiveArchiveOperation.Token; got != "lease-1" {
		t.Fatalf("source archive operation token changed to %q", got)
	}
	if got := state.History[0].ID; got != "task-1" {
		t.Fatalf("source history changed to %q", got)
	}
}

func TestRepositoryAccessErrorBoundaryDiagnostics(t *testing.T) {
	var nilError *RepositoryAccessError
	if got := nilError.Error(); got != "backup repository access failed" {
		t.Fatalf("nil error diagnostic = %q", got)
	}
	if got := nilError.Unwrap(); got != nil {
		t.Fatalf("nil error unwrap = %v", got)
	}

	cause := errors.New("internal secret")
	accessErr := &RepositoryAccessError{
		Reason:       RepositoryAccessTimeout,
		Stage:        RepositoryAccessList,
		Provider:     StoreKindS3,
		ProviderCode: "  Slow\x00Down\x7f  ",
		RequestID:    strings.Repeat("r", 300),
		Cause:        cause,
	}
	message := accessErr.Error()
	if strings.ContainsAny(message, "\x00\x7f") {
		t.Fatalf("diagnostic retained control characters: %q", message)
	}
	if !strings.Contains(message, "provider_code=SlowDown") {
		t.Fatalf("diagnostic did not sanitize provider code: %q", message)
	}
	if strings.Contains(message, strings.Repeat("r", 257)) {
		t.Fatalf("request ID was not bounded: %q", message)
	}
	if !errors.Is(accessErr, cause) {
		t.Fatal("repository access error did not retain its internal cause")
	}
}
