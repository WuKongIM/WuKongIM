package app

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultConversationProjectorFlushInterval = 100 * time.Millisecond
	defaultConversationProjectorShardCount    = 64
)

type channelLatestBatchWriter interface {
	UpsertChannelLatestBatch(context.Context, []metadb.ChannelLatest) error
}

type conversationProjectorOptions struct {
	// writer persists coalesced latest rows outside the foreground send path.
	writer channelLatestBatchWriter
	// now supplies wall-clock time for projection timestamps.
	now func() time.Time
	// flushInterval controls the background flush cadence.
	flushInterval time.Duration
	// shardCount bounds lock contention when many channels are updated concurrently.
	shardCount int
}

// conversationProjector coalesces committed messages into channel_latest rows before durable flush.
type conversationProjector struct {
	writer        channelLatestBatchWriter
	now           func() time.Time
	flushInterval time.Duration
	shards        []conversationProjectorShard

	flushMu sync.Mutex
	runMu   sync.Mutex
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

type conversationProjectorShard struct {
	mu   sync.Mutex
	rows map[conversationProjectorKey]metadb.ChannelLatest
}

type conversationProjectorKey struct {
	channelID   string
	channelType int64
}

func newConversationProjector(opts conversationProjectorOptions) *conversationProjector {
	if opts.flushInterval <= 0 {
		opts.flushInterval = defaultConversationProjectorFlushInterval
	}
	if opts.shardCount <= 0 {
		opts.shardCount = defaultConversationProjectorShardCount
	}
	now := opts.now
	if now == nil {
		now = time.Now
	}
	projector := &conversationProjector{
		writer:        opts.writer,
		now:           now,
		flushInterval: opts.flushInterval,
		shards:        make([]conversationProjectorShard, opts.shardCount),
	}
	for i := range projector.shards {
		projector.shards[i].rows = make(map[conversationProjectorKey]metadb.ChannelLatest)
	}
	return projector
}

func (p *conversationProjector) Start(context.Context) error {
	if p == nil || p.writer == nil {
		return nil
	}
	p.runMu.Lock()
	defer p.runMu.Unlock()
	if p.started {
		return nil
	}
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	p.started = true
	go p.run(p.stopCh, p.doneCh)
	return nil
}

func (p *conversationProjector) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.runMu.Lock()
	if !p.started {
		p.runMu.Unlock()
		return p.Flush(ctx)
	}
	stopCh := p.stopCh
	doneCh := p.doneCh
	p.started = false
	close(stopCh)
	p.runMu.Unlock()

	select {
	case <-doneCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	return p.Flush(ctx)
}

func (p *conversationProjector) run(stopCh <-chan struct{}, doneCh chan<- struct{}) {
	ticker := time.NewTicker(p.flushInterval)
	defer func() {
		ticker.Stop()
		close(doneCh)
	}()
	for {
		select {
		case <-ticker.C:
			_ = p.Flush(context.Background())
		case <-stopCh:
			return
		}
	}
}

func (p *conversationProjector) Submit(_ context.Context, event messageevents.MessageCommitted) error {
	if p == nil || p.writer == nil {
		return nil
	}
	at := p.now().UnixMilli()
	p.merge(metadb.ChannelLatest{
		ChannelID:      event.ChannelID,
		ChannelType:    int64(event.ChannelType),
		LastMessageID:  event.MessageID,
		LastMessageSeq: event.MessageSeq,
		LastAt:         at,
		FromUID:        event.FromUID,
		ClientMsgNo:    event.ClientMsgNo,
		Payload:        append([]byte(nil), event.Payload...),
		UpdatedAt:      at,
	})
	return nil
}

func (p *conversationProjector) Flush(ctx context.Context) error {
	if p == nil || p.writer == nil {
		return nil
	}
	p.flushMu.Lock()
	defer p.flushMu.Unlock()

	rows := p.drain()
	if len(rows) == 0 {
		return nil
	}
	if err := p.writer.UpsertChannelLatestBatch(ctx, rows); err != nil {
		p.mergeRows(rows)
		return err
	}
	return nil
}

func (p *conversationProjector) drain() []metadb.ChannelLatest {
	var rows []metadb.ChannelLatest
	for i := range p.shards {
		shard := &p.shards[i]
		shard.mu.Lock()
		for key, latest := range shard.rows {
			rows = append(rows, latest)
			delete(shard.rows, key)
		}
		shard.mu.Unlock()
	}
	return rows
}

func (p *conversationProjector) mergeRows(rows []metadb.ChannelLatest) {
	for _, latest := range rows {
		p.merge(latest)
	}
}

func (p *conversationProjector) merge(latest metadb.ChannelLatest) {
	if p == nil || len(p.shards) == 0 {
		return
	}
	key := conversationProjectorKey{channelID: latest.ChannelID, channelType: latest.ChannelType}
	shard := &p.shards[p.shardIndex(key)]
	shard.mu.Lock()
	existing, ok := shard.rows[key]
	if !ok || latest.LastMessageSeq >= existing.LastMessageSeq {
		latest.Payload = append([]byte(nil), latest.Payload...)
		shard.rows[key] = latest
	}
	shard.mu.Unlock()
}

func (p *conversationProjector) shardIndex(key conversationProjectorKey) uint32 {
	hash := uint32(2166136261)
	for i := 0; i < len(key.channelID); i++ {
		hash ^= uint32(key.channelID[i])
		hash *= 16777619
	}
	hash ^= uint32(key.channelType)
	hash *= 16777619
	return hash % uint32(len(p.shards))
}

type committedSinkGroup []message.CommittedSink

func combineCommittedSinks(sinks ...message.CommittedSink) message.CommittedSink {
	group := committedSinkGroup{}
	for _, sink := range sinks {
		if sink != nil {
			group = append(group, sink)
		}
	}
	if len(group) == 0 {
		return nil
	}
	return group
}

func (g committedSinkGroup) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	var firstErr error
	for _, sink := range g {
		if sink == nil {
			continue
		}
		if err := sink.Submit(ctx, event.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
