package reactor

import (
	"context"
	"sync"
	"sync/atomic"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
)

// Config wires a group of channel-keyed reactors.
type Config struct {
	LocalNode    ch.NodeID
	ReactorCount int
	MailboxSize  int
	Store        store.Factory
}

// Group owns all reactors and routes events by channel key.
type Group struct {
	cfg      Config
	router   Router
	reactors []*Reactor
	nextOp   atomic.Uint64
	closed   atomic.Bool
}

// NewGroup creates and starts a reactor group.
func NewGroup(cfg Config) (*Group, error) {
	if cfg.LocalNode == 0 || cfg.Store == nil {
		return nil, ch.ErrInvalidConfig
	}
	if cfg.ReactorCount <= 0 {
		cfg.ReactorCount = 1
	}
	if cfg.MailboxSize <= 0 {
		cfg.MailboxSize = 1024
	}
	router, err := NewRouter(cfg.ReactorCount)
	if err != nil {
		return nil, err
	}
	g := &Group{cfg: cfg, router: router, reactors: make([]*Reactor, cfg.ReactorCount)}
	for i := range g.reactors {
		r := NewReactor(ReactorConfig{ID: i, LocalNode: cfg.LocalNode, Store: cfg.Store, MailboxSize: cfg.MailboxSize})
		g.reactors[i] = r
		r.start()
	}
	return g, nil
}

// Submit routes an event to the owning reactor and returns its future.
func (g *Group) Submit(ctx context.Context, key ch.ChannelKey, event Event) (*Future, error) {
	if g == nil || g.closed.Load() {
		return nil, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	future := event.Future
	if future == nil {
		future = NewFuture()
		event.Future = future
	}
	if event.Key == "" {
		event.Key = key
	}
	reactor := g.reactors[g.router.PickIndex(key)]
	if err := reactor.Submit(eventPriority(event.Kind), event); err != nil {
		return nil, err
	}
	return future, nil
}

// NextOpID returns a monotonic operation id for fenced calls.
func (g *Group) NextOpID() ch.OpID {
	return ch.OpID(g.nextOp.Add(1))
}

// Close stops all reactors.
func (g *Group) Close() error {
	if g == nil {
		return nil
	}
	if g.closed.Swap(true) {
		return nil
	}
	var wg sync.WaitGroup
	for _, reactor := range g.reactors {
		wg.Add(1)
		go func(r *Reactor) {
			defer wg.Done()
			r.Close()
		}(reactor)
	}
	wg.Wait()
	return nil
}

func eventPriority(kind EventKind) Priority {
	switch kind {
	case EventApplyMeta, EventWorkerResult, EventClose:
		return PriorityHigh
	case EventTick:
		return PriorityLow
	default:
		return PriorityNormal
	}
}
