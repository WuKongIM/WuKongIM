package multiraft

import (
	"context"
	"crypto/sha256"

	"go.etcd.io/raft/v3/raftpb"
)

func selectRecoveryCheckpoint(ctx context.Context, nodeID NodeID, opts SlotOptions, state BootstrapState, snapshot raftpb.Snapshot, memory *loadedMemoryStorage, digest [32]byte, configIndex uint64, progress *recoveryReporter) (*RecoveryCheckpoint, bool, error) {
	fsm, ok := opts.StateMachine.(CheckpointStateMachine)
	if !ok || opts.ClusterID == "" {
		return nil, false, nil
	}
	info, err := fsm.RecoveryState(ctx)
	if err != nil {
		return nil, false, err
	}
	if !info.Enabled {
		return nil, false, nil
	}
	base := newCheckpointProof(opts.ClusterID, nodeID, opts.ID, info.OwnershipDigest, snapshot.Metadata, digest, configIndex)
	base.LiveEpoch = info.LiveEpoch
	last, err := memory.LastIndex()
	if err != nil {
		return nil, false, err
	}
	reason := "missing_certificate"
	if opts.StartupRecovery && len(info.Proof) > 0 {
		proof, valid := decodeCheckpointProof(info.Proof)
		if valid {
			var entries []raftpb.Entry
			if last > snapshot.Metadata.Index {
				entries, err = memory.Entries(snapshot.Metadata.Index+1, last+1, maxSizePerMsg(0))
				if err != nil {
					return nil, false, err
				}
			}
			if verifyCheckpointProof(base, proof, entries, state.HardState.Commit) {
				proof.LiveEpoch = info.LiveEpoch
				progress.report(RecoveryProgress{Stage: "checkpoint_reuse", SnapshotIndex: snapshot.Metadata.Index, Entries: int64(proof.AppliedIndex - snapshot.Metadata.Index), TotalEntries: int64(state.HardState.Commit - snapshot.Metadata.Index)})
				return &proof, true, nil
			}
		}
		reason = "incompatible_certificate"
	} else if !opts.StartupRecovery {
		reason = "live_open"
	}
	progress.report(RecoveryProgress{Stage: "checkpoint_fallback", SnapshotIndex: snapshot.Metadata.Index, Reason: reason})
	// A verified snapshot or a genuinely fresh log establishes a new anchor.
	// Snapshotless compatibility recovery does not certify unknown existing data.
	if snapshot.Metadata.Index > 0 || (state.HardState.Commit == 0 && last == 0) {
		return &base, false, nil
	}
	return nil, false, nil
}

// resetCheckpointAnchor follows durable snapshot publication and uses exactly
// the same bytes and membership. Failure leaves a mismatched old proof, which
// selects full recovery after restart instead of trusting a partial transition.
func (g *slot) resetCheckpointAnchor(ctx context.Context, snapshot raftpb.Snapshot, configIndex uint64, captured ...CheckpointState) error {
	fsm, ok := g.stateMachine.(CheckpointStateMachine)
	if !ok || g.clusterID == "" {
		return nil
	}
	state, err := fsm.RecoveryState(ctx)
	if err != nil {
		return err
	}
	if !state.Enabled {
		return nil
	}
	if len(captured) > 0 {
		state = captured[0]
	}
	proof := newCheckpointProof(g.clusterID, g.nodeID(), g.id, state.OwnershipDigest, snapshot.Metadata, sha256.Sum256(snapshot.Data), configIndex)
	proof.LiveEpoch = state.LiveEpoch
	if err := fsm.PersistRecoveryCheckpoint(ctx, proof); err != nil {
		return err
	}
	g.checkpoint = &proof
	return nil
}

func (g *slot) advanceCheckpoint(entry raftpb.Entry) (*RecoveryCheckpoint, error) {
	if g.checkpoint == nil {
		return nil, nil
	}
	proof, err := advanceCheckpointProof(*g.checkpoint, entry, g.storageView.memory.confState, g.configAppliedIndexForSnapshot(entry.Index))
	if err != nil {
		return nil, err
	}
	g.checkpoint = &proof
	return &proof, nil
}
