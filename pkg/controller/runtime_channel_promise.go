package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

type (
	// ChannelReplicationCapability is one running node's Channel replication protocol answer.
	ChannelReplicationCapability = state.ChannelReplicationCapability
	// ChannelAuthorityPromiseState records one-way activation of Channel authority promises.
	ChannelAuthorityPromiseState = state.ChannelAuthorityPromiseState
	// ChannelPromiseCapabilityProbe asks one exact running member for its capability.
	ChannelPromiseCapabilityProbe func(context.Context, uint64, string) (ChannelReplicationCapability, error)
)

// ChannelAuthorityPromiseProtocolVersion is the protocol that fences
// authorities with durable promises instead of in-log barriers.
const ChannelAuthorityPromiseProtocolVersion = state.ChannelAuthorityPromiseProtocolVersion

// ErrChannelPromiseNotReady reports that activation or a join could not prove
// Channel authority promise support for every required member.
var ErrChannelPromiseNotReady = errors.New("controller: channel authority promise not ready")

// EnsureChannelAuthorityPromise one-way activates Channel authority promises
// once every non-removed member, Controller voters included, proves support.
// It returns whether activation is committed. Any probe failure or older
// member keeps the cluster in barrier mode; an active cluster is never probed
// again. Activation is a rare control operation, not part of any hot path.
func (r *Runtime) EnsureChannelAuthorityPromise(ctx context.Context) (bool, error) {
	ctx, cancel := presenceOperationContext(ctx)
	defer cancel()
	st, err := r.presenceCommittedState(ctx)
	if err != nil {
		return false, err
	}
	if st.ChannelAuthorityPromise != nil {
		return true, nil
	}
	proofs := make([]ChannelReplicationCapability, 0, len(st.Nodes))
	for _, node := range st.Nodes {
		if node.JoinState == state.NodeJoinStateRemoved {
			continue
		}
		proof, err := r.probeChannelPromiseCapability(ctx, node.NodeID, node.Addr)
		if err != nil {
			return false, fmt.Errorf("%w: node %d: %w", ErrChannelPromiseNotReady, node.NodeID, err)
		}
		proofs = append(proofs, proof)
	}
	if !st.ChannelPromiseProofsCover(proofs) {
		return false, ErrChannelPromiseNotReady
	}
	result, err := r.raftService().ProposeResult(ctx, command.Command{
		Kind: command.KindActivateChannelAuthorityPromise, IssuedAt: r.cfg.Now().UTC(),
		ChannelPromiseCapabilities: proofs,
	})
	if err != nil {
		return false, errors.Join(ErrChannelPromiseNotReady, err)
	}
	if result.Rejected {
		return false, ErrChannelPromiseNotReady
	}
	if err := r.publishFromState(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// probeChannelPromiseCapability asks one member and requires promise support.
func (r *Runtime) probeChannelPromiseCapability(ctx context.Context, nodeID uint64, addr string) (ChannelReplicationCapability, error) {
	if r.cfg.ChannelPromiseCapabilityProbe == nil {
		return ChannelReplicationCapability{}, ErrChannelPromiseNotReady
	}
	probeCtx, cancel := presenceOperationContext(ctx)
	defer cancel()
	proof, err := r.cfg.ChannelPromiseCapabilityProbe(probeCtx, nodeID, addr)
	if err != nil {
		return ChannelReplicationCapability{}, err
	}
	if proof.NodeID != nodeID || !proof.SupportsChannelAuthorityPromise() {
		return ChannelReplicationCapability{}, ErrChannelPromiseNotReady
	}
	return proof, nil
}
