package channelplane

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

// PeerReactor batches remote channel-owner append effects by target peer.
type PeerReactor struct {
	opts PeerReactorOptions

	mu      sync.Mutex
	started bool
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
	lanes   map[peerLaneKey]*peerLane
}

// PeerReactorOptions controls remote append batching lanes.
type PeerReactorOptions struct {
	// Client sends one batched append RPC to a remote peer.
	Client PeerClient
	// LaneCount is the number of batching lanes per target peer.
	LaneCount int
	// MaxBatchWait bounds how long a partial lane batch may wait before flushing.
	MaxBatchWait time.Duration
	// MaxBatchRecords bounds one remote RPC by number of channel append batches.
	MaxBatchRecords int
	// MaxBatchBytes bounds one remote RPC by estimated serialized size.
	MaxBatchBytes int
	// MaxPending bounds queued and inflight appends per peer lane.
	MaxPending int
	// MaxInflightRPC bounds concurrent AppendBatches RPCs per peer lane.
	MaxInflightRPC int
	// RPCTimeout bounds one remote AppendBatches RPC. Zero leaves the caller or parent context deadline unchanged.
	RPCTimeout time.Duration
}

type peerLaneKey struct {
	nodeID channel.NodeID
	lane   int
}

type peerAppendTask struct {
	ctx        context.Context
	envelope   AppendBatchEnvelope
	done       chan peerAppendResult
	onComplete peerAppendCallback
	once       sync.Once
}

type peerAppendResult struct {
	result channel.AppendBatchResult
	err    error
}

type peerAppendCallback func(channel.AppendBatchResult, error)

type peerLane struct {
	parent *PeerReactor
	key    peerLaneKey
	inbox  chan *peerAppendTask
	// rpcQueue bounds flushed batches waiting for this lane's fixed RPC workers.
	rpcQueue chan []*peerAppendTask
	stopc    chan struct{}
	done     chan struct{}
	rpcWG    sync.WaitGroup

	submitMu sync.Mutex
	stopped  bool
	pending  atomic.Int64
}

// NewPeerReactor constructs the peer batching runtime. Call Start before use.
func NewPeerReactor(opts PeerReactorOptions) *PeerReactor {
	opts.setDefaults()
	return &PeerReactor{opts: opts, lanes: make(map[peerLaneKey]*peerLane)}
}

func (o *PeerReactorOptions) setDefaults() {
	if o.LaneCount <= 0 {
		o.LaneCount = defaultPeerLaneCount
	}
	if o.MaxBatchWait <= 0 {
		o.MaxBatchWait = defaultPeerBatchMaxWait
	}
	if o.MaxBatchRecords <= 0 {
		o.MaxBatchRecords = defaultPeerBatchMaxRecords
	}
	if o.MaxBatchBytes <= 0 {
		o.MaxBatchBytes = defaultPeerBatchMaxBytes
	}
	if o.MaxPending <= 0 {
		o.MaxPending = defaultPeerMaxPending
	}
	if o.MaxInflightRPC <= 0 {
		o.MaxInflightRPC = defaultPeerMaxInflightRPC
	}
}

// Start enables peer lanes to be created for remote append effects.
func (p *PeerReactor) Start() error {
	if p == nil || p.opts.Client == nil || p.opts.LaneCount <= 0 || p.opts.MaxBatchWait <= 0 || p.opts.MaxBatchRecords <= 0 || p.opts.MaxBatchBytes <= 0 || p.opts.MaxPending <= 0 || p.opts.MaxInflightRPC <= 0 {
		return channel.ErrInvalidConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if p.ctx == nil {
		p.ctx, p.cancel = context.WithCancel(context.Background())
	}
	p.started = true
	return nil
}

// Stop closes all peer lanes and fails queued append effects.
func (p *PeerReactor) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	if p.cancel != nil {
		p.cancel()
	}
	lanes := make([]*peerLane, 0, len(p.lanes))
	for _, lane := range p.lanes {
		lanes = append(lanes, lane)
	}
	p.mu.Unlock()

	for _, lane := range lanes {
		lane.stop()
	}
	for _, lane := range lanes {
		select {
		case <-lane.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, lane := range lanes {
		if err := lane.waitRPC(ctx); err != nil {
			return err
		}
	}
	return nil
}

// AppendRemoteBatch enqueues one remote-owner append and waits for its aligned result.
func (p *PeerReactor) AppendRemoteBatch(ctx context.Context, nodeID channel.NodeID, req channel.AppendBatchRequest, route ChannelRoute) (channel.AppendBatchResult, error) {
	done := make(chan peerAppendResult, 1)
	lane, task, err := p.appendRemoteBatch(ctx, nodeID, req, route, func(result channel.AppendBatchResult, err error) {
		done <- peerAppendResult{result: result, err: err}
	})
	if err != nil {
		return channel.AppendBatchResult{}, err
	}
	select {
	case result := <-done:
		return result.result, result.err
	case <-ctx.Done():
		lane.complete(task, channel.AppendBatchResult{}, ctx.Err())
		return channel.AppendBatchResult{}, ctx.Err()
	}
}

// AppendRemoteBatchAsync enqueues one remote-owner append and completes through onComplete.
func (p *PeerReactor) AppendRemoteBatchAsync(ctx context.Context, nodeID channel.NodeID, req channel.AppendBatchRequest, route ChannelRoute, onComplete peerAppendCallback) error {
	_, _, err := p.appendRemoteBatch(ctx, nodeID, req, route, onComplete)
	return err
}

func (p *PeerReactor) appendRemoteBatch(ctx context.Context, nodeID channel.NodeID, req channel.AppendBatchRequest, route ChannelRoute, onComplete peerAppendCallback) (*peerLane, *peerAppendTask, error) {
	if p == nil {
		return nil, nil, ErrNoRemoteAppender
	}
	if onComplete == nil {
		return nil, nil, channel.ErrInvalidConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	lane, err := p.lane(nodeID, req.ChannelID)
	if err != nil {
		return nil, nil, err
	}
	if !lane.reserve() {
		return nil, nil, ErrPeerBackpressured
	}
	task := &peerAppendTask{
		ctx: ctx,
		envelope: AppendBatchEnvelope{
			RouteEpoch: route.Epoch(),
			Request:    req,
		},
		onComplete: onComplete,
	}
	if err := lane.submit(ctx, task); err != nil {
		lane.release()
		return nil, nil, err
	}
	return lane, task, nil
}

func (p *PeerReactor) lane(nodeID channel.NodeID, id channel.ChannelID) (*peerLane, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	if !p.started {
		return nil, ErrNotStarted
	}
	key := peerLaneKey{nodeID: nodeID, lane: p.laneIndex(id)}
	lane := p.lanes[key]
	if lane == nil {
		lane = newPeerLane(p, key)
		p.lanes[key] = lane
		lane.start()
	}
	return lane, nil
}

func (p *PeerReactor) laneIndex(id channel.ChannelID) int {
	if p.opts.LaneCount == 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte{id.Type})
	_, _ = h.Write([]byte(id.ID))
	return int(h.Sum32() % uint32(p.opts.LaneCount))
}

func (p *PeerReactor) pendingForTest(nodeID channel.NodeID, laneIndex int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	lane := p.lanes[peerLaneKey{nodeID: nodeID, lane: laneIndex}]
	if lane == nil {
		return 0
	}
	return int(lane.pending.Load())
}

func newPeerLane(parent *PeerReactor, key peerLaneKey) *peerLane {
	return &peerLane{
		parent:   parent,
		key:      key,
		inbox:    make(chan *peerAppendTask, parent.opts.MaxPending),
		rpcQueue: make(chan []*peerAppendTask, parent.opts.MaxInflightRPC),
		stopc:    make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (l *peerLane) start() {
	l.rpcWG.Add(l.parent.opts.MaxInflightRPC)
	for i := 0; i < l.parent.opts.MaxInflightRPC; i++ {
		go l.runRPCWorker()
	}
	go l.run()
}

func (l *peerLane) stop() {
	l.submitMu.Lock()
	if l.stopped {
		l.submitMu.Unlock()
		return
	}
	l.stopped = true
	l.submitMu.Unlock()
	close(l.stopc)
}

func (l *peerLane) waitRPC(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		l.rpcWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *peerLane) reserve() bool {
	for {
		current := l.pending.Load()
		if current >= int64(l.parent.opts.MaxPending) {
			return false
		}
		if l.pending.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (l *peerLane) release() {
	l.pending.Add(-1)
}

func (l *peerLane) submit(ctx context.Context, task *peerAppendTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.submitMu.Lock()
	defer l.submitMu.Unlock()
	if l.stopped {
		return ErrClosed
	}
	select {
	case l.inbox <- task:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrPeerBackpressured
	}
}

func (l *peerLane) run() {
	defer func() {
		close(l.rpcQueue)
		close(l.done)
	}()
	var batch []*peerAppendTask
	var batchBytes int
	var timer *time.Timer
	var timerC <-chan time.Time
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerC = nil
	}
	startTimer := func() {
		if timer != nil {
			return
		}
		timer = time.NewTimer(l.parent.opts.MaxBatchWait)
		timerC = timer.C
	}
	flush := func() {
		if len(batch) == 0 {
			stopTimer()
			return
		}
		tasks := batch
		batch = nil
		batchBytes = 0
		stopTimer()
		l.enqueueFlush(tasks)
	}
	for {
		select {
		case task := <-l.inbox:
			if err := task.ctx.Err(); err != nil {
				l.complete(task, channel.AppendBatchResult{}, err)
				continue
			}
			taskBytes := estimateAppendBatchEnvelopeSize(task.envelope)
			if len(batch) > 0 && batchBytes+taskBytes > l.parent.opts.MaxBatchBytes {
				flush()
			}
			batch = append(batch, task)
			batchBytes += taskBytes
			if len(batch) == 1 {
				startTimer()
			}
			if len(batch) >= l.parent.opts.MaxBatchRecords || batchBytes >= l.parent.opts.MaxBatchBytes {
				flush()
			}
		case <-timerC:
			flush()
		case <-l.stopc:
			stopTimer()
			for _, task := range batch {
				l.complete(task, channel.AppendBatchResult{}, ErrClosed)
			}
			for {
				select {
				case task := <-l.inbox:
					l.complete(task, channel.AppendBatchResult{}, ErrClosed)
				default:
					return
				}
			}
		}
	}
}

func (l *peerLane) enqueueFlush(tasks []*peerAppendTask) {
	select {
	case l.rpcQueue <- tasks:
	case <-l.stopc:
		for _, task := range tasks {
			l.complete(task, channel.AppendBatchResult{}, ErrClosed)
		}
	default:
		for _, task := range tasks {
			l.complete(task, channel.AppendBatchResult{}, ErrPeerBackpressured)
		}
	}
}

func (l *peerLane) runRPCWorker() {
	defer l.rpcWG.Done()
	for tasks := range l.rpcQueue {
		select {
		case <-l.stopc:
			for _, task := range tasks {
				l.complete(task, channel.AppendBatchResult{}, ErrClosed)
			}
		default:
			l.flush(tasks)
		}
	}
}

func (l *peerLane) flush(tasks []*peerAppendTask) {
	req := AppendBatchesRequest{Batches: make([]AppendBatchEnvelope, 0, len(tasks))}
	kept := make([]*peerAppendTask, 0, len(tasks))
	for _, task := range tasks {
		if err := task.ctx.Err(); err != nil {
			l.complete(task, channel.AppendBatchResult{}, err)
			continue
		}
		req.Batches = append(req.Batches, task.envelope)
		kept = append(kept, task)
	}
	if len(kept) == 0 {
		return
	}
	rpcCtx := l.parent.ctx
	var cancel context.CancelFunc
	if l.parent.opts.RPCTimeout > 0 {
		rpcCtx, cancel = context.WithTimeout(rpcCtx, l.parent.opts.RPCTimeout)
	}
	if cancel != nil {
		defer cancel()
	}
	resp, err := l.parent.opts.Client.AppendBatches(rpcCtx, l.key.nodeID, req)
	if err != nil {
		for _, task := range kept {
			l.complete(task, channel.AppendBatchResult{}, err)
		}
		return
	}
	if len(resp.Results) != len(kept) {
		err := fmt.Errorf("channelplane: peer append result count mismatch: got %d want %d", len(resp.Results), len(kept))
		for _, task := range kept {
			l.complete(task, channel.AppendBatchResult{}, err)
		}
		return
	}
	for i, task := range kept {
		result, err := appendRemoteResult(resp.Results[i])
		l.complete(task, result, err)
	}
}

func (l *peerLane) complete(task *peerAppendTask, result channel.AppendBatchResult, err error) {
	task.once.Do(func() {
		l.release()
		if task.onComplete != nil {
			task.onComplete(result, err)
			return
		}
		if task.done != nil {
			task.done <- peerAppendResult{result: result, err: err}
		}
	})
}

func appendRemoteResult(result AppendBatchRemoteResult) (channel.AppendBatchResult, error) {
	switch result.Status {
	case RemoteAppendStatusOK:
		return result.Result, nil
	case RemoteAppendStatusNotLeader:
		return channel.AppendBatchResult{}, channel.ErrNotLeader
	case RemoteAppendStatusStaleRoute:
		return channel.AppendBatchResult{}, ErrStaleRoute
	case RemoteAppendStatusLeaseExpired:
		return channel.AppendBatchResult{}, channel.ErrLeaseExpired
	case RemoteAppendStatusWriteFenced:
		return channel.AppendBatchResult{}, channel.ErrWriteFenced
	case RemoteAppendStatusNotReady:
		return channel.AppendBatchResult{}, channel.ErrNotReady
	case RemoteAppendStatusBackpressure:
		return channel.AppendBatchResult{}, ErrPeerBackpressured
	case RemoteAppendStatusInvalid:
		return channel.AppendBatchResult{}, ErrInvalidRequest
	default:
		return channel.AppendBatchResult{}, fmt.Errorf("channelplane: unexpected remote append status %q", result.Status)
	}
}

func estimateAppendBatchEnvelopeSize(env AppendBatchEnvelope) int {
	size := 3 * 10
	size += len(env.Request.ChannelID.ID) + 1
	size += len(env.Request.TraceID)
	for _, msg := range env.Request.Messages {
		size += len(msg.ChannelID) + len(msg.ClientMsgNo) + len(msg.FromUID) + len(msg.Payload) + 96
	}
	return size
}
