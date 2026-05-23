package reactor

import (
	"context"
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

const defaultReactorDrain = 128

// ReactorConfig wires one reactor.
type ReactorConfig struct {
	ID          int
	LocalNode   ch.NodeID
	Store       store.Factory
	MailboxSize int
}

// Reactor owns channel states for one hash partition.
type Reactor struct {
	cfg      ReactorConfig
	mailbox  *Mailbox
	channels map[ch.ChannelKey]*runtimeChannel
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

type runtimeChannel struct {
	state   *machine.ChannelState
	store   store.ChannelStore
	waiters map[ch.OpID]*Future
}

// NewReactor constructs a reactor.
func NewReactor(cfg ReactorConfig) *Reactor {
	return &Reactor{cfg: cfg, mailbox: NewMailbox(MailboxConfig{HighSize: cfg.MailboxSize, NormalSize: cfg.MailboxSize, LowSize: cfg.MailboxSize}), channels: make(map[ch.ChannelKey]*runtimeChannel), stop: make(chan struct{}), done: make(chan struct{})}
}

func (r *Reactor) start() { go r.loop() }

// Submit enqueues an event.
func (r *Reactor) Submit(priority Priority, event Event) error {
	select {
	case <-r.stop:
		return ch.ErrClosed
	default:
	}
	return r.mailbox.Submit(priority, event)
}

// SubmitCompletion blocks until a high-priority worker completion is enqueued or closed.
func (r *Reactor) SubmitCompletion(event Event) error {
	if r == nil || r.mailbox == nil {
		return ch.ErrClosed
	}
	select {
	case <-r.stop:
		return ch.ErrClosed
	default:
	}
	select {
	case r.mailbox.high <- event:
		return nil
	case <-r.stop:
		return ch.ErrClosed
	}
}

// Close stops the reactor and fails future work by closing the loop.
func (r *Reactor) Close() error {
	r.once.Do(func() { close(r.stop) })
	<-r.done
	return nil
}

func (r *Reactor) loop() {
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		events := r.mailbox.Drain(defaultReactorDrain)
		if len(events) == 0 {
			select {
			case <-r.stop:
				return
			case <-time.After(time.Millisecond):
			}
			continue
		}
		for _, event := range events {
			r.handle(event)
		}
	}
}

func (r *Reactor) handle(event Event) {
	switch event.Kind {
	case EventApplyMeta:
		r.handleApplyMeta(event)
	case EventAppend:
		r.handleAppend(event)
	case EventFetch:
		r.handleFetch(event)
	case EventPull:
		r.handlePull(event)
	case EventAck:
		r.handleAck(event)
	case EventApplyRecords:
		r.handleApplyRecords(event)
	case EventWorkerResult:
		r.handleWorkerResult(event)
	case EventTick:
		r.handleTick(event)
	case EventClose:
		r.handleClose(event)
	}
}

func (r *Reactor) handleWorkerResult(event Event) {
	// Task 2 only establishes completion routing; concrete result handling follows.
}

func (r *Reactor) handleTick(event Event) {
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) handleClose(event Event) {
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
	r.once.Do(func() { close(r.stop) })
}

func (r *Reactor) handleApplyMeta(event Event) {
	rc, err := r.ensureChannel(event.Meta)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	decision := rc.state.ApplyMeta(event.Meta)
	event.Future.Complete(Result{Err: decision.Err})
}

func (r *Reactor) ensureChannel(meta ch.Meta) (*runtimeChannel, error) {
	key := meta.Key
	if key == "" {
		key = ch.ChannelKeyForID(meta.ID)
	}
	if rc := r.channels[key]; rc != nil {
		return rc, nil
	}
	cs, err := r.cfg.Store.ChannelStore(key, meta.ID)
	if err != nil {
		return nil, err
	}
	initial, err := cs.Load(context.Background())
	if err != nil {
		return nil, err
	}
	state := machine.NewChannelState(key, r.cfg.LocalNode, 1)
	state.ID = meta.ID
	state.LEO = initial.LEO
	state.HW = initial.HW
	state.CheckpointHW = initial.CheckpointHW
	rc := &runtimeChannel{state: state, store: cs, waiters: make(map[ch.OpID]*Future)}
	r.channels[key] = rc
	return rc, nil
}

func (r *Reactor) lookup(key ch.ChannelKey) (*runtimeChannel, error) {
	if rc := r.channels[key]; rc != nil {
		return rc, nil
	}
	return nil, ch.ErrChannelNotFound
}

func (r *Reactor) handleAppend(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	records := make([]ch.Record, len(event.Append.Messages))
	for i, msg := range event.Append.Messages {
		records[i] = ch.Record{ID: msg.MessageID, Payload: append([]byte(nil), msg.Payload...), SizeBytes: len(msg.Payload)}
	}
	decision := rc.state.ProposeAppend(machine.AppendCommand{OpID: event.OpID, CommitMode: event.Append.CommitMode, Records: records})
	if decision.Err != nil {
		event.Future.Complete(Result{Err: decision.Err})
		return
	}
	if len(decision.Tasks) == 0 {
		event.Future.Complete(Result{})
		return
	}
	appendTask := decision.Tasks[0].StoreAppend
	stored, err := rc.store.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: appendTask.Records, Sync: appendTask.Sync})
	result := rc.state.ApplyAppendStored(machine.AppendStoredResult{Fence: decision.Tasks[0].Fence, BaseOffset: stored.BaseOffset, LastOffset: stored.LastOffset, Err: err})
	if len(result.Replies) == 0 {
		rc.waiters[event.OpID] = event.Future
		return
	}
	reply := result.Replies[0]
	batch := ch.AppendBatchResult{Items: reply.AppendItems}
	if len(batch.Items) == 0 && reply.Append.MessageSeq > 0 {
		batch.Items = []ch.AppendBatchItemResult{reply.Append}
	}
	event.Future.Complete(Result{AppendBatch: batch})
}

func (r *Reactor) handleFetch(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	decision := rc.state.BuildFetch(machine.FetchCommand{OpID: 1, FromSeq: event.Fetch.FromSeq, Limit: event.Fetch.Limit, MaxBytes: event.Fetch.MaxBytes})
	if decision.Err != nil {
		event.Future.Complete(Result{Err: decision.Err})
		return
	}
	if len(decision.Replies) > 0 {
		event.Future.Complete(Result{Fetch: decision.Replies[0].Fetch})
		return
	}
	readTask := decision.Tasks[0].ReadCommitted
	read, err := rc.store.ReadCommitted(context.Background(), store.ReadCommittedRequest{FromSeq: readTask.FromSeq, MaxSeq: readTask.MaxSeq, Limit: readTask.Limit, MaxBytes: readTask.MaxBytes})
	result := rc.state.ApplyReadCommitted(machine.ReadCommittedResult{Fence: decision.Tasks[0].Fence, Messages: read.Messages, NextSeq: read.NextSeq, Err: err})
	if len(result.Replies) == 0 {
		rc.waiters[event.OpID] = event.Future
		return
	}
	event.Future.Complete(Result{Fetch: result.Replies[0].Fetch})
}

func (r *Reactor) handlePull(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleLeader {
		event.Future.Complete(Result{Err: ch.ErrNotLeader})
		return
	}
	if event.Pull.Epoch != rc.state.Epoch || event.Pull.LeaderEpoch != rc.state.LeaderEpoch || !rc.state.IsReplica(event.Pull.Follower) {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	read, err := rc.store.ReadLog(context.Background(), store.ReadLogRequest{FromOffset: event.Pull.NextOffset, MaxOffset: rc.state.LEO, MaxBytes: event.Pull.MaxBytes})
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	event.Future.Complete(Result{Pull: transport.PullResponse{ChannelKey: rc.state.Key, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, LeaderHW: rc.state.HW, LeaderLEO: rc.state.LEO, Records: read.Records}})
}

func (r *Reactor) handleAck(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: event.Ack.Follower, MatchOffset: event.Ack.MatchOffset})
	for _, reply := range decision.Replies {
		future := rc.waiters[reply.OpID]
		if future == nil {
			continue
		}
		delete(rc.waiters, reply.OpID)
		future.Complete(Result{AppendBatch: ch.AppendBatchResult{Items: reply.AppendItems}, Err: reply.Err})
	}
	event.Future.Complete(Result{})
}

func (r *Reactor) handleApplyRecords(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleFollower {
		event.Future.Complete(Result{Err: ch.ErrNotReady})
		return
	}
	apply, err := rc.store.ApplyFollower(context.Background(), store.ApplyFollowerRequest{Records: event.PullResponse.Records, LeaderHW: event.PullResponse.LeaderHW})
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	rc.state.LEO = apply.LEO
	if event.PullResponse.LeaderHW < rc.state.LEO {
		rc.state.HW = event.PullResponse.LeaderHW
	} else {
		rc.state.HW = rc.state.LEO
	}
	event.Future.Complete(Result{ApplyLEO: apply.LEO})
}
