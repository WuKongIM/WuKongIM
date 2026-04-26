package replica

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func (r *replica) Append(ctx context.Context, batch []channel.Record) (channel.CommitResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return channel.CommitResult{}, err
	}

	waiter := &appendWaiter{ch: make(chan appendCompletion, 1)}
	req := &appendRequest{}
	req.ctx = ctx
	req.batch = cloneRecords(batch)
	req.byteCount = appendRequestBytes(batch)
	req.commitMode = channel.CommitModeFromContext(ctx)
	req.waiter = waiter
	req.enqueuedAt = time.Now()
	if r != nil && r.now != nil {
		req.enqueuedAt = r.now()
	}
	req.waiter.enqueuedAt = req.enqueuedAt
	result := r.submitLoopCommand(context.Background(), machineAppendRequestCommand{Request: req, Now: req.enqueuedAt})
	if result.Err != nil {
		return channel.CommitResult{}, result.Err
	}

	select {
	case completion := <-req.waiter.ch:
		return completion.result, completion.err
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			r.logAppendTimeout(req)
		}
		_ = r.submitLoopCommand(context.Background(), machineAppendCancelCommand{Request: req, Err: ctx.Err()})
		completion := <-req.waiter.ch
		return completion.result, completion.err
	}
}

func (r *replica) logAppendTimeout(req *appendRequest) {
	if r == nil || req == nil || req.waiter == nil {
		return
	}
	r.mu.RLock()
	state := r.state
	meta := r.meta
	progress := make(map[uint64]uint64, len(r.progress))
	for replicaID, matchOffset := range r.progress {
		progress[uint64(replicaID)] = matchOffset
	}
	isr := make([]uint64, 0, len(meta.ISR))
	for _, replicaID := range meta.ISR {
		isr = append(isr, uint64(replicaID))
	}
	target := req.waiter.target
	rangeStart := req.waiter.rangeStart
	rangeEnd := req.waiter.rangeEnd
	r.mu.RUnlock()

	r.appendLogger().Debug("append wait timed out before quorum commit",
		wklog.Event("channel.replica.append.timeout"),
		wklog.NodeID(uint64(r.localNode)),
		wklog.LeaderNodeID(uint64(state.Leader)),
		wklog.String("channelKey", string(state.ChannelKey)),
		wklog.String("role", replicaRoleString(state.Role)),
		wklog.Uint64("epoch", state.Epoch),
		wklog.Bool("commitReady", state.CommitReady),
		wklog.Uint64("hw", state.HW),
		wklog.Uint64("leo", state.LEO),
		wklog.Uint64("checkpointHW", state.CheckpointHW),
		wklog.Int("minISR", meta.MinISR),
		wklog.Any("isr", isr),
		wklog.Any("progress", progress),
		wklog.Uint64("targetOffset", target),
		wklog.Uint64("rangeStart", rangeStart),
		wklog.Uint64("rangeEnd", rangeEnd),
		wklog.Error(context.DeadlineExceeded),
	)
}

func replicaRoleString(role channel.ReplicaRole) string {
	switch role {
	case channel.ReplicaRoleFollower:
		return "follower"
	case channel.ReplicaRoleLeader:
		return "leader"
	case channel.ReplicaRoleFencedLeader:
		return "fenced_leader"
	case channel.ReplicaRoleTombstoned:
		return "tombstoned"
	default:
		return "unknown"
	}
}

func appendRequestBytes(batch []channel.Record) int {
	total := 0
	for _, record := range batch {
		size := record.SizeBytes
		if size <= 0 {
			size = len(record.Payload)
		}
		total += size
	}
	return total
}
