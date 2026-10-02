package reactor

import (
	"bytes"
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func (r *Reactor) validateMQTTAnchorAuthority(rc *runtimeChannel, q ch.MQTTReplayAnchorRequest) error {
	if !q.Valid() {
		return ch.ErrInvalidConfig
	}
	if _, ok := r.cfg.QuorumLog.(ch.MQTTReplayAnchorCommitter); !ok {
		return ch.ErrInvalidConfig
	}
	capability, ok := r.cfg.Store.(store.MQTTReplayAnchorFactory)
	if !ok || !capability.SupportsMQTTReplayAnchors() {
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

func (r *Reactor) validateMQTTAnchorAdmission(ctx context.Context, rc *runtimeChannel, q ch.MQTTReplayAnchorRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validateMQTTAnchorAuthority(rc, q); err != nil {
		return err
	}
	if err := r.validateAppendEvent(ctx, rc, Event{Append: ch.AppendBatchRequest{ExpectedChannelEpoch: q.Meta.Epoch, ExpectedLeaderEpoch: q.Meta.LeaderEpoch}}); err != nil {
		return err
	}
	if q.Copy.After.Through > rc.state.HW {
		return ch.ErrLogConflict
	}
	return ctx.Err()
}

func (r *Reactor) validateMQTTAnchorControl(rc *runtimeChannel, event Event) error {
	q := *event.MQTTAnchor
	if err := r.validateMQTTAnchorAuthority(rc, q); err != nil {
		return err
	}
	a, err := q.Anchor()
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
	if q.Copy.After.Through > rc.state.HW {
		return ch.ErrLogConflict
	}
	return nil
}

func (r *Reactor) submitQuorumMQTTAnchor(fence ch.Fence, q ch.MQTTReplayAnchorRequest) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	// Once started, durability owns the effect independently of its observer.
	return r.cfg.Pools.Submit(context.Background(), worker.Task{Kind: worker.TaskQuorumMQTTAnchor, Fence: fence, Context: context.Background(), QuorumMQTTAnchor: &worker.QuorumMQTTAnchorTask{Request: q}})
}

func validMQTTAnchorCompletion(rc *runtimeChannel, q ch.MQTTReplayAnchorRequest, p ch.MQTTReplayAnchorProof) bool {
	if !p.Anchor.Valid() || !p.Manifest.StructurallyValid() || p.Manifest.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion || p.Anchor.Through >= p.Manifest.LastOffset {
		return false
	}
	if p.Manifest.LastOffset > rc.state.HW && (p.Manifest.ChannelEpoch != rc.state.Epoch || p.Manifest.LeaderTerm != rc.state.LeaderEpoch || p.Manifest.FenceVersion != rc.quorumAuthority.ID.FenceVersion) {
		return false
	}
	prefix := p.Prefix()
	if prefix == q.Copy.After {
		return true
	}
	// The only alternative is the sequencer's idle-anchor suppression result.
	return prefix == q.Copy.Before && prefix.Through+1 == p.Manifest.LastOffset && p.Manifest.LastOffset == q.Copy.After.Through && p.Manifest.LastOffset == rc.state.HW
}

func (r *Reactor) handleQuorumMQTTAnchorResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	batch := rc.appendInflight
	if batch == nil || batch.batchOpID != result.Fence.OpID || len(batch.requests) != 1 || batch.requests[0].mqttAnchor == nil {
		return
	}
	if result.Fence != batch.fence || result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch {
		return
	}
	request := batch.requests[0]
	q := *request.mqttAnchor
	commitErr := result.Err
	var proof ch.MQTTReplayAnchorProof
	if commitErr == nil {
		if err = r.validateMQTTAnchorAuthority(rc, q); err != nil {
			commitErr = err
		} else if result.QuorumMQTTAnchor == nil {
			commitErr = ch.ErrInvalidConfig
		} else {
			proof = result.QuorumMQTTAnchor.Proof
			if !validMQTTAnchorCompletion(rc, q, proof) {
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
		// Request records are not the committed row on historical or idle retries.
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
			out.Err = r.validateMQTTAnchorAdmission(request.ctx, rc, q)
			if out.Err == nil {
				out.MQTTAnchor = proof
			}
		}
		r.completeAppendFuture(rc, reply.OpID, future, out)
	}
	r.finishAppendInflightBatch(rc, commitErr, now)
}
