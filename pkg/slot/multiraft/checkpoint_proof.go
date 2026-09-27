package multiraft

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"

	"go.etcd.io/raft/v3/confchange"
	"go.etcd.io/raft/v3/raftpb"
	"go.etcd.io/raft/v3/tracker"
)

// CheckpointState is startup evidence from a durable FSM. Proof is absent when
// the engine seal, incarnation or atomic watermark does not permit reuse.
type CheckpointState struct {
	Enabled         bool
	LiveEpoch       uint64
	OwnershipDigest [32]byte
	Proof           []byte
}

// CheckpointStateMachine exposes certified recovery without changing the legacy
// StateMachine contract. Runtime data mutations carry Command.Checkpoint.
type CheckpointStateMachine interface {
	RecoveryState(context.Context) (CheckpointState, error)
	PersistRecoveryCheckpoint(context.Context, RecoveryCheckpoint) error
}

// MarshalBinary returns the bounded, versioned opaque proof stored by the FSM.
func (p RecoveryCheckpoint) MarshalBinary() ([]byte, error) { return encodeCheckpointProof(p) }

// RecoveryCheckpoint binds a durable FSM state to one exact Raft history and
// ownership set. The metadata adapter additionally binds the proof to its
// database incarnation and physical commit sequence. Matching indexes alone
// never authorize reuse.
type RecoveryCheckpoint struct {
	// LiveEpoch is process-local and never carried across engine opens.
	LiveEpoch          uint64 `json:"-"`
	Version            uint8
	ClusterID          string
	NodeID             NodeID
	SlotID             SlotID
	OwnershipDigest    [32]byte
	SnapshotIndex      uint64
	SnapshotTerm       uint64
	SnapshotDigest     [32]byte
	AppliedIndex       uint64
	AppliedTerm        uint64
	ConfigAppliedIndex uint64
	ConfState          raftpb.ConfState
	// LogDigest chains every entry, including configuration and empty entries.
	LogDigest [32]byte
}

func newCheckpointProof(clusterID string, nodeID NodeID, slotID SlotID, ownership [32]byte, snapshot raftpb.SnapshotMetadata, digest [32]byte, configIndex uint64) RecoveryCheckpoint {
	metadata, _ := snapshot.Marshal()
	h := sha256.New()
	h.Write(metadata)
	h.Write(digest[:])
	p := RecoveryCheckpoint{Version: 1, ClusterID: clusterID, NodeID: nodeID, SlotID: slotID, OwnershipDigest: ownership, SnapshotIndex: snapshot.Index, SnapshotTerm: snapshot.Term, SnapshotDigest: digest, AppliedIndex: snapshot.Index, AppliedTerm: snapshot.Term, ConfigAppliedIndex: configIndex, ConfState: cloneConfState(snapshot.ConfState)}
	copy(p.LogDigest[:], h.Sum(nil))
	return p
}

func advanceCheckpointProof(previous RecoveryCheckpoint, entry raftpb.Entry, conf raftpb.ConfState, configIndex uint64) (RecoveryCheckpoint, error) {
	if entry.Index != previous.AppliedIndex+1 || entry.Term < previous.AppliedTerm {
		return RecoveryCheckpoint{}, errors.New("non-contiguous checkpoint history")
	}
	var header [28]byte
	binary.BigEndian.PutUint64(header[0:8], entry.Index)
	binary.BigEndian.PutUint64(header[8:16], entry.Term)
	binary.BigEndian.PutUint32(header[16:20], uint32(entry.Type))
	binary.BigEndian.PutUint64(header[20:28], uint64(len(entry.Data)))
	h := sha256.New()
	h.Write(previous.LogDigest[:])
	h.Write(header[:])
	h.Write(entry.Data)
	previous.AppliedIndex = entry.Index
	previous.AppliedTerm = entry.Term
	previous.ConfigAppliedIndex = configIndex
	previous.ConfState = conf // Caller supplies an immutable configuration view.
	copy(previous.LogDigest[:], h.Sum(nil))
	return previous, nil
}

// verifyCheckpointProof validates the prefix through the checkpoint and also
// proves the retained committed suffix is contiguous. It never mutates Raft or
// the FSM; rejection selects the existing full snapshot recovery path.
func verifyCheckpointProof(base, proof RecoveryCheckpoint, entries []raftpb.Entry, commit uint64) bool {
	if proof.Version != 1 || proof.ClusterID == "" || proof.ClusterID != base.ClusterID || proof.NodeID != base.NodeID || proof.SlotID != base.SlotID || proof.OwnershipDigest != base.OwnershipDigest || proof.SnapshotIndex != base.SnapshotIndex || proof.SnapshotTerm != base.SnapshotTerm || proof.SnapshotDigest != base.SnapshotDigest || proof.AppliedIndex < base.AppliedIndex || proof.AppliedIndex > commit || commit < base.AppliedIndex {
		return false
	}
	progress := tracker.MakeProgressTracker(1, 0)
	if len(base.ConfState.Voters) > 0 || len(base.ConfState.VotersOutgoing) > 0 {
		cfg, prs, err := confchange.Restore(confchange.Changer{Tracker: progress, LastIndex: base.AppliedIndex}, base.ConfState)
		if err != nil {
			return false
		}
		progress.Config = cfg
		progress.Progress = prs
	}
	matched := checkpointBoundaryEqual(base, proof)
	current := base
	expected := base.AppliedIndex + 1
	for _, entry := range entries {
		if entry.Index > commit {
			break
		}
		if entry.Index != expected {
			return false
		}
		expected++
		if entry.Index > proof.AppliedIndex {
			continue
		}
		configIndex := current.ConfigAppliedIndex
		if isConfigChangeEntry(entry) {
			var change raftpb.ConfChangeV2
			if entry.Type == raftpb.EntryConfChange {
				var old raftpb.ConfChange
				if old.Unmarshal(entry.Data) != nil {
					return false
				}
				change = old.AsV2()
			} else if change.Unmarshal(entry.Data) != nil {
				return false
			}
			changer := confchange.Changer{Tracker: progress, LastIndex: entry.Index}
			var cfg tracker.Config
			var prs tracker.ProgressMap
			var err error
			if change.LeaveJoint() {
				cfg, prs, err = changer.LeaveJoint()
			} else if auto, ok := change.EnterJoint(); ok {
				cfg, prs, err = changer.EnterJoint(auto, change.Changes...)
			} else {
				cfg, prs, err = changer.Simple(change.Changes...)
			}
			if err != nil {
				return false
			}
			progress.Config = cfg
			progress.Progress = prs
			configIndex = entry.Index
		}
		var err error
		current, err = advanceCheckpointProof(current, entry, progress.ConfState(), configIndex)
		if err != nil {
			return false
		}
		if entry.Index == proof.AppliedIndex {
			matched = checkpointBoundaryEqual(current, proof)
		}
	}
	return matched && expected == commit+1
}

func checkpointBoundaryEqual(a, b RecoveryCheckpoint) bool {
	return a.AppliedIndex == b.AppliedIndex && a.AppliedTerm == b.AppliedTerm && a.ConfigAppliedIndex == b.ConfigAppliedIndex && a.LogDigest == b.LogDigest && sameConfState(a.ConfState, b.ConfState)
}

func encodeCheckpointProof(proof RecoveryCheckpoint) ([]byte, error) { return json.Marshal(proof) }

func decodeCheckpointProof(data []byte) (RecoveryCheckpoint, bool) {
	var proof RecoveryCheckpoint
	if len(data) == 0 || len(data) > 64<<10 {
		return proof, false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&proof) != nil || proof.Version != 1 || proof.NodeID == 0 || proof.SlotID == 0 || proof.AppliedIndex == 0 || len(proof.ClusterID) > 1024 {
		return RecoveryCheckpoint{}, false
	}
	if decoder.Decode(new(any)) != io.EOF {
		return RecoveryCheckpoint{}, false
	}
	return proof, true
}
