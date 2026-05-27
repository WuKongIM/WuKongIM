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
	TaskStoreReadLog
	TaskRPCPull
	TaskRPCAck
	TaskRPCNotify
	TaskStoreCheckpoint
	TaskRPCPullHint
)

// Task describes blocking work submitted to a bounded pool.
type Task struct {
	Kind  TaskKind
	Fence ch.Fence
	// Context cancels this task when the original caller gives up, in addition to pool shutdown.
	Context context.Context

	StoreAppend  *StoreAppendTask
	StoreReadLog *StoreReadLogTask
	StoreApply   *StoreApplyTask
	// StoreCheckpoint persists a checkpoint before runtime eviction.
	StoreCheckpoint *StoreCheckpointTask
	RPCPull         *RPCPullTask
	RPCAck          *RPCAckTask
	// RPCNotify sends legacy transport compatibility nudges.
	RPCNotify *RPCNotifyTask
	// RPCPullHint sends a prompt pull nudge to a follower.
	RPCPullHint *RPCPullHintTask

	RunFunc func(context.Context) Result
}

// StoreAppendTask asks a worker to durably append leader records.
type StoreAppendTask struct {
	ChannelID ch.ChannelID
	Records   []ch.Record
	Sync      bool
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

// StoreCheckpointTask asks a worker to persist a channel checkpoint before eviction.
type StoreCheckpointTask struct {
	// ChannelID identifies the store that owns the checkpoint.
	ChannelID ch.ChannelID
	// Checkpoint is the durable committed frontier to persist.
	Checkpoint ch.Checkpoint
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

// RPCNotifyTask is the legacy compatibility payload for follower nudges.
type RPCNotifyTask struct {
	Node    ch.NodeID
	Request transport.NotifyRequest
}

// RPCPullHintTask asks a remote follower to pull leader progress promptly.
type RPCPullHintTask struct {
	// Node is the target follower node.
	Node ch.NodeID
	// Request is the pull hint payload sent to the follower.
	Request transport.PullHintRequest
}

// Run executes the task payload with the provided blocking dependencies.
func (t Task) Run(ctx context.Context, deps Deps) Result {
	ctx, cancel := taskContext(ctx, t.Context)
	defer cancel()
	var res Result
	switch t.Kind {
	case TaskFunc:
		if t.RunFunc == nil {
			res = invalidResult(t)
			break
		}
		res = t.RunFunc(ctx)
		res.Kind = t.Kind
		if res.Fence == (ch.Fence{}) {
			res.Fence = t.Fence
		}
	case TaskStoreAppend:
		res = runStoreAppend(ctx, deps, t)
	case TaskStoreReadLog:
		res = runStoreReadLog(ctx, deps, t)
	case TaskStoreApply:
		res = runStoreApply(ctx, deps, t)
	case TaskStoreCheckpoint:
		res = runStoreCheckpoint(ctx, deps, t)
	case TaskRPCPull:
		res = runRPCPull(ctx, deps, t)
	case TaskRPCAck:
		res = runRPCAck(ctx, deps, t)
	case TaskRPCNotify:
		res = runRPCNotify(ctx, deps, t)
	case TaskRPCPullHint:
		res = runRPCPullHint(ctx, deps, t)
	default:
		res = invalidResult(t)
	}
	return normalizeContextErr(ctx, res)
}

func taskContext(parent context.Context, task context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if task == nil || task.Done() == nil {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancelCause(parent)
	cancelTask := func() {
		cancel(contextFromTaskCause(task))
	}
	if task.Err() != nil {
		cancelTask()
		return ctx, func() { cancel(context.Canceled) }
	}
	go func() {
		select {
		case <-task.Done():
			cancelTask()
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}

func contextFromTaskCause(task context.Context) error {
	if cause := context.Cause(task); cause != nil {
		return cause
	}
	if err := task.Err(); err != nil {
		return err
	}
	return context.Canceled
}

func normalizeContextErr(ctx context.Context, res Result) Result {
	// WithCancelCause preserves why the bridge canceled; Err still reports Canceled.
	if res.Err == context.Canceled {
		if cause := context.Cause(ctx); cause != nil && cause != context.Canceled {
			res.Err = cause
		}
	}
	return res
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

func runStoreCheckpoint(ctx context.Context, deps Deps, t Task) Result {
	payload := t.StoreCheckpoint
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
	err = cs.StoreCheckpoint(ctx, payload.Checkpoint)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, StoreCheckpoint: &StoreCheckpointResult{}}
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

func runRPCNotify(ctx context.Context, deps Deps, t Task) Result {
	payload := t.RPCNotify
	if payload == nil || deps.Transport == nil {
		return invalidResult(t)
	}
	err := deps.Transport.Notify(ctx, payload.Node, payload.Request)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, RPCNotify: &RPCNotifyResult{}}
}

func runRPCPullHint(ctx context.Context, deps Deps, t Task) Result {
	payload := t.RPCPullHint
	if payload == nil || deps.Transport == nil {
		return invalidResult(t)
	}
	err := deps.Transport.PullHint(ctx, payload.Node, payload.Request)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, RPCPullHint: &RPCPullHintResult{}}
}

func invalidResult(t Task) Result {
	return Result{Kind: t.Kind, Fence: t.Fence, Err: ch.ErrInvalidConfig}
}
