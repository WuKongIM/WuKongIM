package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
)

func TestPrepareControllerVoterRequiresExactPreservedMembership(t *testing.T) {
	validState := ClusterState{
		Controllers: []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}},
		Nodes: []Node{
			{NodeID: 1, Addr: "n1", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive},
			{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive},
		},
	}
	validVoters := []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}}
	if err := validatePrepareControllerVoterNextVoters(4, validVoters); err != nil {
		t.Fatalf("basic voter validation error = %v", err)
	}
	if err := validatePrepareControllerVoterNextVotersForState(4, "n4", validState, validVoters); err != nil {
		t.Fatalf("preserved voter validation error = %v", err)
	}

	basicInvalid := []struct {
		name   string
		voters []Voter
	}{
		{name: "empty"},
		{name: "zero node", voters: []Voter{{NodeID: 0, Addr: "n0"}, {NodeID: 4, Addr: "n4"}}},
		{name: "empty address", voters: []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4}}},
		{name: "duplicate", voters: []Voter{{NodeID: 4, Addr: "n4"}, {NodeID: 4, Addr: "n4-other"}}},
		{name: "missing local", voters: []Voter{{NodeID: 1, Addr: "n1"}}},
	}
	for _, tt := range basicInvalid {
		t.Run("basic "+tt.name, func(t *testing.T) {
			if err := validatePrepareControllerVoterNextVoters(4, tt.voters); err == nil {
				t.Fatal("validation error = nil")
			}
		})
	}

	stateInvalid := []struct {
		name      string
		localAddr string
		state     ClusterState
		voters    []Voter
	}{
		{name: "missing current controller", localAddr: "n4", state: validState, voters: []Voter{{NodeID: 4, Addr: "n4"}}},
		{name: "controller address drift", localAddr: "n4", state: validState, voters: []Voter{{NodeID: 1, Addr: "other"}, {NodeID: 4, Addr: "n4"}}},
		{name: "missing local voter", localAddr: "n4", state: validState, voters: []Voter{{NodeID: 1, Addr: "n1"}}},
		{name: "local node missing", localAddr: "n4", state: ClusterState{Controllers: validState.Controllers, Nodes: validState.Nodes[:1]}, voters: validVoters},
		{name: "local node inactive", localAddr: "n4", state: ClusterState{Controllers: validState.Controllers, Nodes: []Node{validState.Nodes[0], {NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateJoining}}}, voters: validVoters},
		{name: "local voter address drift", localAddr: "n4", state: validState, voters: []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "other"}}},
		{name: "runtime address drift", localAddr: "other", state: validState, voters: validVoters},
		{name: "unexpected voter", localAddr: "n4", state: validState, voters: []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}, {NodeID: 5, Addr: "n5"}}},
	}
	for _, tt := range stateInvalid {
		t.Run("state "+tt.name, func(t *testing.T) {
			if err := validatePrepareControllerVoterNextVotersForState(4, tt.localAddr, tt.state, tt.voters); err == nil {
				t.Fatal("validation error = nil")
			}
		})
	}
}

func TestPreservedMirrorStateSelectionNeverFallsBackFromCorruptionToAbsence(t *testing.T) {
	active := mirrorStateCandidate{path: "active", exists: true, valid: true, state: ClusterState{Revision: 9, Checksum: "active-9"}}
	backup := mirrorStateCandidate{path: "backup", exists: true, valid: true, state: ClusterState{Revision: 8, Checksum: "backup-8"}}
	selected, err := selectPreservedMirrorState(active, backup)
	if err != nil || selected.path != "active" {
		t.Fatalf("newer selection = %#v, %v", selected, err)
	}

	backup.state = ClusterState{Revision: 10, Checksum: "backup-10"}
	selected, err = selectPreservedMirrorState(active, backup)
	if err != nil || selected.path != "backup" {
		t.Fatalf("newer backup selection = %#v, %v", selected, err)
	}

	backup.state = ClusterState{Revision: 9, Checksum: "same"}
	active.state.Checksum = "different"
	selected, err = selectPreservedMirrorState(active, backup)
	if err != nil || selected.path != "active" {
		t.Fatalf("equal revision divergent selection = %#v, %v", selected, err)
	}

	corruptActive := mirrorStateCandidate{path: "active", exists: true, err: errors.New("invalid checksum")}
	missingBackup := mirrorStateCandidate{path: "backup"}
	if _, err := selectPreservedMirrorState(corruptActive, missingBackup); !errors.Is(err, corruptActive.err) {
		t.Fatalf("corrupt active error = %v", err)
	}
	corruptBackup := mirrorStateCandidate{path: "backup", exists: true, err: errors.New("truncated")}
	if _, err := selectPreservedMirrorState(corruptActive, corruptBackup); !errors.Is(err, corruptActive.err) {
		t.Fatalf("two corrupt candidates error = %v", err)
	}
	if _, err := selectPreservedMirrorState(mirrorStateCandidate{path: "active"}, missingBackup); err == nil {
		t.Fatal("two missing candidates error = nil")
	}
}

func TestControllerVoterProofUsesStrongestAppliedEvidence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status RaftStatus
		want   uint64
	}{
		{name: "applied", status: RaftStatus{AppliedIndex: 9, CommitIndex: 10, LastIndex: 11}, want: 9},
		{name: "commit fallback", status: RaftStatus{CommitIndex: 10, LastIndex: 11}, want: 10},
		{name: "last fallback", status: RaftStatus{LastIndex: 11}, want: 11},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := controllerRaftStatusProofIndex(tt.status); got != tt.want {
				t.Fatalf("proof index = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMirrorPromotionPreservesNewestDurableStateAndRestoresBackupOnMoveFailure(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "cluster-state.json")
	backupPath := filepath.Join(dir, mirrorBeforeControllerVoterPromotionFile)
	activeState := runtimeContractState(t, 9)
	backupState := runtimeContractState(t, 8)
	if err := statefile.New(activePath).Save(context.Background(), activeState); err != nil {
		t.Fatalf("save active: %v", err)
	}
	if err := statefile.New(backupPath).Save(context.Background(), backupState); err != nil {
		t.Fatalf("save backup: %v", err)
	}

	activeCandidate := loadMirrorStateCandidate(context.Background(), activePath)
	backupCandidate := loadMirrorStateCandidate(context.Background(), backupPath)
	selected, err := selectPreservedMirrorState(activeCandidate, backupCandidate)
	if err != nil || selected.path != activePath || selected.state.Revision != 9 {
		t.Fatalf("selected candidate = %#v, %v", selected, err)
	}
	if err := replaceBackupWithActive(activePath, backupPath, true); err != nil {
		t.Fatalf("replaceBackupWithActive() error = %v", err)
	}
	preserved := loadMirrorStateCandidate(context.Background(), backupPath)
	if !preserved.valid || preserved.state.Revision != 9 {
		t.Fatalf("preserved candidate = %#v", preserved)
	}
	if _, err := os.Stat(activePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active state still exists: %v", err)
	}

	if err := statefile.New(backupPath).Save(context.Background(), backupState); err != nil {
		t.Fatalf("restore backup fixture: %v", err)
	}
	missingActive := filepath.Join(dir, "missing-active.json")
	if err := replaceBackupWithActive(missingActive, backupPath, true); err == nil {
		t.Fatal("replace missing active error = nil")
	}
	restored := loadMirrorStateCandidate(context.Background(), backupPath)
	if !restored.valid || restored.state.Revision != 8 {
		t.Fatalf("backup after failed move = %#v", restored)
	}

	missing := loadMirrorStateCandidate(context.Background(), filepath.Join(dir, "missing.json"))
	if missing.exists || missing.valid || missing.err != nil {
		t.Fatalf("missing candidate = %#v", missing)
	}
	corruptPath := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corruptPath, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	corrupt := loadMirrorStateCandidate(context.Background(), corruptPath)
	if !corrupt.exists || corrupt.valid || corrupt.err == nil {
		t.Fatalf("corrupt candidate = %#v", corrupt)
	}
}
