package worker

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// TaskKind identifies one blocking work category.
type TaskKind uint8

const (
	TaskFunc TaskKind = iota + 1
	TaskStoreAppend
	TaskStoreLoad
	TaskStoreApply
	TaskStoreReadLog
	TaskRPCPull
	TaskRPCAck
	TaskRPCNotify
	TaskStoreCheckpoint
	TaskRPCPullHint
	TaskStoreLookupMessage
	TaskStoreClose
)

// Task describes blocking work submitted to a bounded pool.
type Task struct {
	Kind  TaskKind
	Fence ch.Fence
	// Context cancels this task when the original caller gives up, in addition to pool shutdown.
	Context context.Context

	StoreAppend  *StoreAppendTask
	StoreLoad    *StoreLoadTask
	StoreReadLog *StoreReadLogTask
	// StoreLookupMessage asks storage for one durable message row by message id.
	StoreLookupMessage *StoreLookupMessageTask
	StoreApply         *StoreApplyTask
	// StoreCheckpoint persists a checkpoint before runtime eviction.
	StoreCheckpoint *StoreCheckpointTask
	// StoreClose releases a store handle after the reactor has detached it.
	StoreClose *StoreCloseTask
	RPCPull    *RPCPullTask
	RPCAck     *RPCAckTask
	// RPCNotify sends legacy transport compatibility nudges.
	RPCNotify *RPCNotifyTask
	// RPCPullHint sends a prompt pull nudge to a follower.
	RPCPullHint *RPCPullHintTask

	RunFunc func(context.Context) Result
}

// StoreLoadTask asks a worker to open and load a channel store before runtime activation.
type StoreLoadTask struct {
	ChannelID ch.ChannelID
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

// StoreLookupMessageTask asks a worker to look up one durable message by id.
type StoreLookupMessageTask struct {
	ChannelID ch.ChannelID
	MessageID uint64
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

// StoreCloseTask asks a worker to release a detached store handle.
type StoreCloseTask struct {
	// Store is the detached handle to close outside the reactor loop.
	Store store.ChannelStore
}

// RPCPullTask asks a remote leader for records.
type RPCPullTask struct {
	Node ch.NodeID
	// Timeout bounds the transport call after a worker starts executing the task; queue wait is excluded.
	Timeout time.Duration
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
	case TaskStoreLoad:
		res = runStoreLoad(ctx, deps, t)
	case TaskStoreReadLog:
		res = runStoreReadLog(ctx, deps, t)
	case TaskStoreLookupMessage:
		res = runStoreLookupMessage(ctx, deps, t)
	case TaskStoreApply:
		res = runStoreApply(ctx, deps, t)
	case TaskStoreCheckpoint:
		res = runStoreCheckpoint(ctx, deps, t)
	case TaskStoreClose:
		res = runStoreClose(ctx, deps, t)
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

func runStoreClose(ctx context.Context, deps Deps, t Task) Result {
	_ = ctx
	_ = deps
	payload := t.StoreClose
	if payload == nil || payload.Store == nil {
		return invalidResult(t)
	}
	err := payload.Store.Close()
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, StoreClose: &StoreCloseResult{}}
}

func runStoreLoad(ctx context.Context, deps Deps, t Task) Result {
	payload := t.StoreLoad
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
	initial, err := cs.Load(ctx)
	if err != nil {
		_ = cs.Close()
		return Result{Kind: t.Kind, Fence: t.Fence, Err: err}
	}
	return Result{Kind: t.Kind, Fence: t.Fence, StoreLoad: &StoreLoadResult{Store: cs, Initial: initial}}
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

func runStoreLookupMessage(ctx context.Context, deps Deps, t Task) Result {
	payload := t.StoreLookupMessage
	if payload == nil || deps.Stores == nil {
		return invalidResult(t)
	}
	if payload.MessageID == 0 {
		return Result{Kind: t.Kind, Fence: t.Fence, StoreLookupMessage: &StoreLookupMessageResult{}}
	}
	cs, err := deps.Stores.ChannelStore(t.Fence.ChannelKey, payload.ChannelID)
	if err != nil {
		return Result{Kind: t.Kind, Fence: t.Fence, Err: err}
	}
	if cs == nil {
		return invalidResult(t)
	}
	lookup, ok := cs.(store.MessageLookup)
	if !ok {
		return invalidResult(t)
	}
	msg, found, err := lookup.LookupMessageByID(ctx, payload.MessageID)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, StoreLookupMessage: &StoreLookupMessageResult{Message: msg, Found: found}}
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
	ctx, cancel := taskExecutionTimeout(ctx, payload.Timeout)
	defer cancel()
	resp, err := deps.Transport.Pull(ctx, payload.Node, payload.Request)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, RPCPull: &RPCPullResult{Response: resp}}
}

func taskExecutionTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
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
