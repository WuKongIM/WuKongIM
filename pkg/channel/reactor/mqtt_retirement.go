package reactor

import (
	"bytes"
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

func (r *Reactor) validateMQTTRetirementAuthority(rc *runtimeChannel, q ch.MQTTReplayRetirementRequest) error {
	if !q.Valid() {
		return ch.ErrInvalidConfig
	}
	if _, ok := r.cfg.QuorumLog.(ch.MQTTReplayRetirementCommitter); !ok {
		return ch.ErrInvalidConfig
	}
	capability, ok := r.cfg.Store.(store.MQTTReplayRetirementFactory)
	if !ok || !capability.SupportsMQTTReplayRetirements() {
		return ch.ErrInvalidConfig
	}
	if err := r.validateMQTTLeaderCapability(rc, q.Meta.ID, q.Meta.RouteGeneration); err != nil {
		return err
	}
	authority, err := quorumAuthorityFromMeta(q.Meta)
	if err != nil {
		return err
	}
	if !sameQuorumAuthority(rc.quorumAuthority, authority) || rc.state.Status != q.Meta.Status {
		return ch.ErrStaleMeta
	}
	if rc.state.Role != ch.RoleLeader {
		return ch.ErrNotLeader
	}
	return nil
}

func (r *Reactor) validateMQTTRetirementAdmission(ctx context.Context, rc *runtimeChannel, q ch.MQTTReplayRetirementRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validateMQTTRetirementAuthority(rc, q); err != nil {
		return err
	}
	if err := r.validateAppendEvent(ctx, rc, Event{Append: ch.AppendBatchRequest{ExpectedChannelEpoch: q.Meta.Epoch, ExpectedLeaderEpoch: q.Meta.LeaderEpoch}}); err != nil {
		return err
	}
	if q.Captured.Manifest.LastOffset > rc.state.HW {
		return ch.ErrLogConflict
	}
	return ctx.Err()
}

func (r *Reactor) validateMQTTRetirementControl(rc *runtimeChannel, event Event) error {
	q := *event.MQTTRetirement
	if err := r.validateMQTTRetirementAuthority(rc, q); err != nil {
		return err
	}
	a, err := q.Retirement()
	if err != nil {
		return err
	}
	body, err := a.MarshalBinary()
	if err != nil {
		return ch.ErrInvalidConfig
	}
	req := event.Append
	if req.ChannelID != q.Meta.ID || req.ExpectedChannelEpoch != q.Meta.Epoch || req.ExpectedLeaderEpoch != q.Meta.LeaderEpoch || req.CommitMode != ch.CommitModeQuorum || len(req.Messages) != 1 {
		return ch.ErrInvalidConfig
	}
	m := req.Messages[0]
	if m.MessageID != q.MessageID || m.ServerTimestampMS != q.ServerTimestampMS || !m.SyncOnce || m.RedDot || m.Setting != 0 || m.Expire != 0 || m.FromUID != "" || m.ClientMsgNo != "" || len(m.PublicationMetadata) != 0 || !bytes.Equal(m.Payload, body) {
		return ch.ErrInvalidConfig
	}
	if q.Captured.Manifest.LastOffset > rc.state.HW {
		return ch.ErrLogConflict
	}
	return nil
}

func (r *Reactor) submitQuorumMQTTRetirement(fence ch.Fence, q ch.MQTTReplayRetirementRequest) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	// Once started, durability owns the effect independently of its observer.
	return r.cfg.Pools.Submit(context.Background(), worker.Task{Kind: worker.TaskQuorumMQTTRetirement, Fence: fence, Context: context.Background(), QuorumMQTTRetirement: &worker.QuorumMQTTRetirementTask{Request: q}})
}

// validMQTTRetirementCompletion accepts a historical or covering committed
// decision, but new durable positions must belong to this runtime's authority.
func validMQTTRetirementCompletion(rc *runtimeChannel, q ch.MQTTReplayRetirementRequest, p ch.MQTTReplayRetirementProof) bool {
	return q.AcceptsProof(p) && (p.Manifest.LastOffset <= rc.state.HW ||
		(p.Manifest.ChannelEpoch == rc.state.Epoch && p.Manifest.LeaderTerm == rc.state.LeaderEpoch && p.Manifest.FenceVersion == rc.quorumAuthority.ID.FenceVersion))
}

func (r *Reactor) handleQuorumMQTTRetirementResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	batch := rc.appendInflight
	if batch == nil || batch.batchOpID != result.Fence.OpID || len(batch.requests) != 1 || batch.requests[0].mqttRetirement == nil {
		return
	}
	if result.Fence != batch.fence || result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch {
		return
	}
	request := batch.requests[0]
	q := *request.mqttRetirement
	commitErr := result.Err
	var proof ch.MQTTReplayRetirementProof
	if commitErr == nil {
		if err = r.validateMQTTRetirementAuthority(rc, q); err != nil {
			commitErr = err
		} else if result.QuorumMQTTRetirement == nil {
			commitErr = ch.ErrInvalidConfig
		} else {
			proof = result.QuorumMQTTRetirement.Proof
			if !validMQTTRetirementCompletion(rc, q, proof) {
				commitErr = ch.ErrLogConflict
			}
		}
	}
	now := time.Now()
	oldHW := rc.state.HW
	r.observeAppendStoreCompleted(rc, *batch, now, proof.Manifest.LastOffset, commitErr)
	decision := rc.state.ApplyQuorumControlCommitted(machine.QuorumControlCommittedResult{Fence: result.Fence, CommittedThrough: proof.Manifest.LastOffset, Err: commitErr})
	if commitErr == nil {
		r.markAppendHWAdvanced(rc, oldHW, rc.state.HW, now)
		rc.state.CheckpointHW = max(rc.state.CheckpointHW, proof.Manifest.LastOffset)
		// Request records are not the committed row on historical or covered retries.
		// Never insert those caller identities into the recent-record cache.
		r.markAppendActivity(rc, now)
		rc.lifecycle.version = rc.state.LEO
		r.scheduleLifecycleFromState(rc, now)
	}
	for _, reply := range decision.Replies {
		future := rc.waiters[reply.OpID]
		delete(rc.waiters, reply.OpID)
		r.unregisterAppendCancelContext(rc, reply.OpID)
		out := Result{Err: reply.Err}
		if out.Err == nil {
			out.Err = r.validateMQTTRetirementAdmission(request.ctx, rc, q)
			if out.Err == nil {
				out.MQTTRetirement = proof
			}
		}
		r.completeAppendFuture(rc, reply.OpID, future, out)
	}
	r.finishAppendInflightBatch(rc, commitErr, now)
}
