package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) runConfiguredLeaderReconcile(ctx context.Context, meta channel.Meta, source ReconcileProbeSource) error {
	if source == nil {
		return nil
	}
	local := r.Status()
	proofs, err := source.ProbeQuorum(ctx, meta, local)
	if err != nil {
		return err
	}
	for _, proof := range proofs {
		if err := r.ApplyReconcileProof(ctx, proof); err != nil {
			return err
		}
	}
	return r.runLocalLeaderReconcile(meta)
}

func (r *replica) ApplyReconcileProof(ctx context.Context, proof channel.ReplicaReconcileProof) error {
	result := r.submitLoopCommand(ctx, machineReconcileProofCommand{Proof: proof})
	if result.Err != nil {
		return result.Err
	}
	return r.executeLeaderReconcileEffects(result.Effects)
}

func (r *replica) runLocalLeaderReconcile(meta channel.Meta) error {
	result := r.submitLoopCommand(context.Background(), machineCompleteReconcileCommand{Meta: meta})
	if result.Err != nil {
		return result.Err
	}
	return r.executeLeaderReconcileEffects(result.Effects)
}
