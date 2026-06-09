package channelwrite

import (
	"context"
	"hash/fnv"
	"strconv"
	"sync"

	contract "github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
)

// ChannelID identifies a message channel.
type ChannelID = contract.ChannelID

// AuthorityTarget identifies the fenced channel authority for write admission.
type AuthorityTarget = contract.AuthorityTarget

// SendCommand is an entry-agnostic SEND request.
type SendCommand = contract.SendCommand

// SendResult is the client-facing SEND outcome.
type SendResult = contract.SendResult

// SendBatchItem carries one send command with its cancellation context.
type SendBatchItem = contract.SendBatchItem

// SendBatchItemResult aligns with one SendBatch item.
type SendBatchItemResult = contract.SendBatchItemResult

var (
	// ErrNotChannelAuthority reports that the local node is not the channel authority.
	ErrNotChannelAuthority = contract.ErrNotChannelAuthority
	// ErrBackpressured reports bounded runtime pressure or closed admission.
	ErrBackpressured = contract.ErrBackpressured
)

// Group owns a set of channel-hashed local authority write reactors.
type Group struct {
	opts     Options
	reactors []*reactor

	mu       sync.RWMutex
	started  bool
	stopping bool
	stopped  bool
}

// New creates a channel write reactor group with conservative defaults.
func New(opts Options) *Group {
	opts = applyDefaults(opts)
	group := &Group{opts: opts}
	limits := stateLimitsFromOptions(opts)
	for i := 0; i < opts.ReactorCount; i++ {
		group.reactors = append(group.reactors, newReactor(i, opts.MailboxSize, limits, opts.Clock))
	}
	return group
}

// Start starts every reactor and opens local admission.
// A group that has already stopped is not restarted.
func (g *Group) Start(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping || g.stopped {
		return ErrBackpressured
	}
	if g.started && !g.stopped {
		return nil
	}
	for _, reactor := range g.reactors {
		reactor.start()
	}
	g.started = true
	g.stopping = false
	g.stopped = false
	return nil
}

// Stop closes admission and waits for accepted reactor events to drain.
func (g *Group) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	if !g.started || g.stopped {
		g.mu.Unlock()
		return nil
	}
	reactors := append([]*reactor(nil), g.reactors...)
	if !g.stopping {
		g.stopping = true
		for _, reactor := range reactors {
			reactor.close()
		}
	}
	g.mu.Unlock()

	for _, reactor := range reactors {
		if err := reactor.wait(ctx); err != nil {
			return err
		}
	}

	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
	return nil
}

// SubmitLocal admits a batch to the local channel-authority reactor.
func (g *Group) SubmitLocal(ctx context.Context, target AuthorityTarget, items []SendBatchItem) (*Future, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if target.LeaderNodeID != g.opts.LocalNodeID {
		return nil, ErrNotChannelAuthority
	}

	copiedItems := cloneSendBatchItems(items)
	future := newFuture(len(copiedItems))

	g.mu.RLock()
	if !g.started || g.stopping || g.stopped {
		g.mu.RUnlock()
		return nil, ErrBackpressured
	}
	reactor := g.reactorForTarget(target)
	ack, err := reactor.enqueue(ctx, target, copiedItems, future)
	g.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if err := <-ack; err != nil {
		return nil, err
	}
	return future, nil
}

func (g *Group) reactorForTarget(target AuthorityTarget) *reactor {
	key := targetKey(target)
	idx := int(hashString64(key) % uint64(len(g.reactors)))
	return g.reactors[idx]
}

func targetKey(target AuthorityTarget) string {
	if target.ChannelKey != "" {
		return target.ChannelKey
	}
	return channelKey(target.ChannelID)
}

func channelKey(channelID ChannelID) string {
	return strconv.Itoa(int(channelID.Type)) + ":" + channelID.ID
}

func hashString64(value string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum64()
}

func cloneSendBatchItems(items []SendBatchItem) []SendBatchItem {
	if len(items) == 0 {
		return nil
	}
	copied := make([]SendBatchItem, len(items))
	for i := range items {
		copied[i] = items[i].Clone()
	}
	return copied
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
