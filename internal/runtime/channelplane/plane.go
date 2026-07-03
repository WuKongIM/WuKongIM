package channelplane

import (
	"context"
	"hash/fnv"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

// Plane is a synchronous append facade backed by channel-keyed reactors.
type Plane struct {
	// opts contains immutable dependencies and queue limits.
	opts Options

	mu       sync.Mutex
	started  bool
	closed   bool
	reactors []*reactor
	effects  *effectExecutor
	peer     *PeerReactor
}

// New constructs a channel append plane without starting reactor goroutines.
func New(opts Options) (*Plane, error) {
	opts.setDefaults()
	if err := opts.validate(); err != nil {
		return nil, err
	}
	p := &Plane{
		opts:     opts,
		reactors: make([]*reactor, opts.ReactorCount),
		effects:  newEffectExecutor(effectExecutorOptions{Workers: opts.EffectWorkerCount, QueueSize: opts.EffectQueueSize}),
	}
	for i := range p.reactors {
		p.reactors[i] = newReactor(p, i, opts.ReactorInboxSize)
	}
	if opts.PeerClient != nil {
		p.peer = NewPeerReactor(PeerReactorOptions{
			Client:          opts.PeerClient,
			LaneCount:       opts.PeerLaneCount,
			MaxBatchWait:    opts.PeerBatchMaxWait,
			RPCTimeout:      opts.PeerRPCTimeout,
			MaxBatchRecords: opts.PeerBatchMaxRecords,
			MaxBatchBytes:   opts.PeerBatchMaxBytes,
			MaxPending:      opts.PeerMaxPending,
		})
	}
	return p, nil
}

// Start launches all reactor shards.
func (p *Plane) Start() error {
	if p == nil {
		return channel.ErrInvalidConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if p.started {
		return nil
	}
	p.effects.start()
	if p.peer != nil {
		if err := p.peer.Start(); err != nil {
			return err
		}
	}
	for _, r := range p.reactors {
		r.start()
	}
	p.started = true
	return nil
}

// Stop shuts down all reactors and fails queued append futures.
func (p *Plane) Stop(ctx context.Context) error {
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
	reactors := append([]*reactor(nil), p.reactors...)
	p.mu.Unlock()

	for _, r := range reactors {
		r.stop()
	}
	for _, r := range reactors {
		select {
		case <-r.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if p.peer != nil {
		if err := p.peer.Stop(ctx); err != nil {
			return err
		}
	}
	if p.effects != nil {
		return p.effects.stop(ctx)
	}
	return nil
}

// AppendBatch appends messages to one channel under the current authoritative route.
func (p *Plane) AppendBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	if p == nil {
		return channel.AppendBatchResult{}, channel.ErrInvalidConfig
	}
	if err := validateAppendBatchRequest(req); err != nil {
		return channel.AppendBatchResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return channel.AppendBatchResult{}, err
	}
	p.mu.Lock()
	started, closed := p.started, p.closed
	p.mu.Unlock()
	if closed {
		return channel.AppendBatchResult{}, ErrClosed
	}
	if !started {
		return channel.AppendBatchResult{}, ErrNotStarted
	}
	future := newAppendFuture()
	cmd := &appendCommand{ctx: ctx, req: req, future: future}
	reactor := p.reactors[p.reactorIndex(req.ChannelID)]
	if err := reactor.submit(ctx, cmd); err != nil {
		return channel.AppendBatchResult{}, err
	}
	return future.wait(ctx)
}

func (p *Plane) reactorIndex(id channel.ChannelID) int {
	if len(p.reactors) == 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte{id.Type})
	_, _ = h.Write([]byte(id.ID))
	return int(h.Sum32() % uint32(len(p.reactors)))
}
