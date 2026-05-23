package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// TaskKind identifies one blocking work category.
type TaskKind uint8

const (
	TaskFunc TaskKind = iota + 1
	TaskStoreAppend
	TaskStoreApply
	TaskStoreReadCommitted
	TaskStoreReadLog
	TaskRPCPull
	TaskRPCAck
)

// Task describes blocking work submitted to a bounded pool.
type Task struct {
	Kind  TaskKind
	Fence ch.Fence

	StoreAppend        *StoreAppendTask
	StoreReadCommitted *StoreReadCommittedTask
	StoreReadLog       *StoreReadLogTask
	StoreApply         *StoreApplyTask
	RPCPull            *RPCPullTask
	RPCAck             *RPCAckTask

	RunFunc func(context.Context) Result
}

// StoreAppendTask asks a worker to durably append leader records.
type StoreAppendTask struct {
	ChannelID ch.ChannelID
	Records   []ch.Record
	Sync      bool
}

// StoreReadCommittedTask asks a worker to read committed messages.
type StoreReadCommittedTask struct {
	ChannelID ch.ChannelID
	FromSeq   uint64
	MaxSeq    uint64
	Limit     int
	MaxBytes  int
}

// StoreReadLogTask asks a worker to read raw records for replication.
type StoreReadLogTask struct {
	ChannelID  ch.ChannelID
	FromOffset uint64
	MaxOffset  uint64
	MaxBytes   int
}

// StoreApplyTask asks a worker to persist follower records.
type StoreApplyTask struct {
	ChannelID ch.ChannelID
	Records   []ch.Record
	LeaderHW  uint64
}

// RPCPullTask asks a remote leader for records.
type RPCPullTask struct {
	Node    ch.NodeID
	Request transport.PullRequest
}

// RPCAckTask reports follower progress to the remote leader.
type RPCAckTask struct {
	Node    ch.NodeID
	Request transport.AckRequest
}

// Run executes the task payload with the provided blocking dependencies.
func (t Task) Run(ctx context.Context, deps Deps) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	switch t.Kind {
	case TaskFunc:
		if t.RunFunc == nil {
			return invalidResult(t)
		}
		res := t.RunFunc(ctx)
		res.Kind = t.Kind
		if res.Fence == (ch.Fence{}) {
			res.Fence = t.Fence
		}
		return res
	case TaskStoreAppend:
		return runStoreAppend(ctx, deps, t)
	case TaskStoreReadCommitted:
		return runStoreReadCommitted(ctx, deps, t)
	case TaskStoreReadLog:
		return runStoreReadLog(ctx, deps, t)
	case TaskStoreApply:
		return runStoreApply(ctx, deps, t)
	case TaskRPCPull:
		return runRPCPull(ctx, deps, t)
	case TaskRPCAck:
		return runRPCAck(ctx, deps, t)
	default:
		return invalidResult(t)
	}
}

func runStoreAppend(ctx context.Context, deps Deps, t Task) Result {
	payload := t.StoreAppend
	if payload == nil || deps.Stores == nil {
		return invalidResult(t)
	}
	cs, err := deps.Stores.ChannelStore(t.Fence.ChannelKey, payload.ChannelID)
	if err != nil {
		return Result{Kind: t.Kind, Fence: t.Fence, Err: err}
	}
	if cs == nil {
		return invalidResult(t)
	}
	stored, err := cs.AppendLeader(ctx, store.AppendLeaderRequest{Records: payload.Records, Sync: payload.Sync})
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, StoreAppend: &StoreAppendResult{BaseOffset: stored.BaseOffset, LastOffset: stored.LastOffset}}
}

func runStoreReadCommitted(ctx context.Context, deps Deps, t Task) Result {
	payload := t.StoreReadCommitted
	if payload == nil || deps.Stores == nil {
		return invalidResult(t)
	}
	cs, err := deps.Stores.ChannelStore(t.Fence.ChannelKey, payload.ChannelID)
	if err != nil {
		return Result{Kind: t.Kind, Fence: t.Fence, Err: err}
	}
	if cs == nil {
		return invalidResult(t)
	}
	read, err := cs.ReadCommitted(ctx, store.ReadCommittedRequest{FromSeq: payload.FromSeq, MaxSeq: payload.MaxSeq, Limit: payload.Limit, MaxBytes: payload.MaxBytes})
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, StoreReadCommitted: &StoreReadCommittedResult{Messages: read.Messages, NextSeq: read.NextSeq}}
}

func runStoreReadLog(ctx context.Context, deps Deps, t Task) Result {
	payload := t.StoreReadLog
	if payload == nil || deps.Stores == nil {
		return invalidResult(t)
	}
	cs, err := deps.Stores.ChannelStore(t.Fence.ChannelKey, payload.ChannelID)
	if err != nil {
		return Result{Kind: t.Kind, Fence: t.Fence, Err: err}
	}
	if cs == nil {
		return invalidResult(t)
	}
	read, err := cs.ReadLog(ctx, store.ReadLogRequest{FromOffset: payload.FromOffset, MaxOffset: payload.MaxOffset, MaxBytes: payload.MaxBytes})
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, StoreReadLog: &StoreReadLogResult{Records: read.Records}}
}

func runStoreApply(ctx context.Context, deps Deps, t Task) Result {
	payload := t.StoreApply
	if payload == nil || deps.Stores == nil {
		return invalidResult(t)
	}
	cs, err := deps.Stores.ChannelStore(t.Fence.ChannelKey, payload.ChannelID)
	if err != nil {
		return Result{Kind: t.Kind, Fence: t.Fence, Err: err}
	}
	if cs == nil {
		return invalidResult(t)
	}
	applied, err := cs.ApplyFollower(ctx, store.ApplyFollowerRequest{Records: payload.Records, LeaderHW: payload.LeaderHW})
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, StoreApply: &StoreApplyResult{LEO: applied.LEO}}
}

func runRPCPull(ctx context.Context, deps Deps, t Task) Result {
	payload := t.RPCPull
	if payload == nil || deps.Transport == nil {
		return invalidResult(t)
	}
	resp, err := deps.Transport.Pull(ctx, payload.Node, payload.Request)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, RPCPull: &RPCPullResult{Response: resp}}
}

func runRPCAck(ctx context.Context, deps Deps, t Task) Result {
	payload := t.RPCAck
	if payload == nil || deps.Transport == nil {
		return invalidResult(t)
	}
	err := deps.Transport.Ack(ctx, payload.Node, payload.Request)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, RPCAck: &RPCAckResult{}}
}

func invalidResult(t Task) Result {
	return Result{Kind: t.Kind, Fence: t.Fence, Err: ch.ErrInvalidConfig}
}
