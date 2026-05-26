package app

import (
	"context"
	"errors"
	"sync"
	"time"

	appretention "github.com/WuKongIM/WuKongIM/internal/runtime/channelretention"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const appChannelRetentionCursorName = "committed"

type appChannelRetentionKeyLister interface {
	ListChannelKeys() ([]channel.ChannelKey, error)
}

type appChannelRetentionKeyListerFunc func() ([]channel.ChannelKey, error)

func (f appChannelRetentionKeyListerFunc) ListChannelKeys() ([]channel.ChannelKey, error) {
	return f()
}

type appChannelRetentionChannels struct {
	keys      appChannelRetentionKeyLister
	batchSize int
	mu        sync.Mutex
	next      int
}

func (l *appChannelRetentionChannels) ListRetentionChannels(ctx context.Context) ([]appretention.Channel, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if l == nil || l.keys == nil {
		return nil, channel.ErrInvalidConfig
	}
	keys, err := l.keys.ListChannelKeys()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	start, limit := l.nextBatch(len(keys))
	channels := make([]appretention.Channel, 0, limit)
	for i := 0; i < limit; i++ {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		key := keys[(start+i)%len(keys)]
		id, err := channelhandler.ParseChannelKey(key)
		if err != nil {
			return nil, err
		}
		channels = append(channels, appretention.Channel{Key: key, ID: id})
	}
	return channels, nil
}

func (l *appChannelRetentionChannels) nextBatch(total int) (start int, limit int) {
	limit = total
	if l.batchSize > 0 && l.batchSize < limit {
		limit = l.batchSize
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.next >= total {
		l.next = 0
	}
	start = l.next
	l.next = (l.next + limit) % total
	return start, limit
}

type appChannelRetentionStores struct {
	engine *channelstore.Engine
}

func (p appChannelRetentionStores) StoreForChannel(ctx context.Context, ch appretention.Channel) (appretention.Store, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if p.engine == nil {
		return nil, channel.ErrInvalidConfig
	}
	store := p.engine.ForChannel(ch.Key, ch.ID)
	if store == nil {
		return nil, channel.ErrInvalidConfig
	}
	return appChannelRetentionStore{store: store}, nil
}

type appChannelRetentionStore struct {
	store *channelstore.ChannelStore
}

func (s appChannelRetentionStore) ScanExpiredMessagePrefix(fromSeq uint64, cutoff time.Time, limit int) (appretention.ScanResult, error) {
	if s.store == nil {
		return appretention.ScanResult{}, channel.ErrInvalidConfig
	}
	result, err := s.store.ScanExpiredMessagePrefix(fromSeq, cutoff, limit)
	if err != nil {
		return appretention.ScanResult{}, err
	}
	return appretention.ScanResult{
		FromSeq:    result.FromSeq,
		ThroughSeq: result.ThroughSeq,
		Count:      result.Count,
	}, nil
}

func (s appChannelRetentionStore) ConfirmCommittedDispatchCursorDurable(name string, minSeq uint64) (uint64, error) {
	if s.store == nil {
		return 0, channel.ErrInvalidConfig
	}
	return s.store.ConfirmCommittedDispatchCursorDurable(name, minSeq)
}

type appChannelRetentionRuntimePort interface {
	RetentionView(key channel.ChannelKey) (channel.RetentionView, error)
	ApplyRetentionBoundary(ctx context.Context, key channel.ChannelKey, throughSeq uint64) error
}

type appChannelRetentionRuntime struct {
	runtime appChannelRetentionRuntimePort
}

func (r appChannelRetentionRuntime) RetentionView(ctx context.Context, key channel.ChannelKey) (channel.RetentionView, error) {
	if err := contextError(ctx); err != nil {
		return channel.RetentionView{}, err
	}
	if r.runtime == nil {
		return channel.RetentionView{}, channel.ErrInvalidConfig
	}
	view, err := r.runtime.RetentionView(key)
	if err != nil {
		if errors.Is(err, channel.ErrChannelNotFound) {
			return channel.RetentionView{}, appretention.ErrChannelUnavailable
		}
		return channel.RetentionView{}, err
	}
	return view, nil
}

func (r appChannelRetentionRuntime) ApplyRetentionBoundary(ctx context.Context, key channel.ChannelKey, throughSeq uint64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if r.runtime == nil {
		return channel.ErrInvalidConfig
	}
	return r.runtime.ApplyRetentionBoundary(ctx, key, throughSeq)
}

type appChannelRetentionMetadataStore interface {
	AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error
}

type appChannelRetentionMetadata struct {
	store appChannelRetentionMetadataStore
}

func (m appChannelRetentionMetadata) AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if m.store == nil {
		return channel.ErrInvalidConfig
	}
	return m.store.AdvanceChannelRetentionThroughSeq(ctx, req)
}

type appChannelRetentionConfig struct {
	ttl              time.Duration
	scanInterval     time.Duration
	channelBatchSize int
	maxTrimMessages  int
}

func resolveAppChannelRetentionConfig(cfg Config) appChannelRetentionConfig {
	return appChannelRetentionConfig{
		ttl:              cfg.ChannelMessageRetention.TTL,
		scanInterval:     cfg.ChannelMessageRetention.ScanInterval,
		channelBatchSize: cfg.ChannelMessageRetention.ChannelBatchSize,
		maxTrimMessages:  cfg.ChannelMessageRetention.MaxTrimMessages,
	}
}

func newAppChannelRetentionWorker(cfg appChannelRetentionConfig, localNodeID uint64, engine *channelstore.Engine, runtime appChannelRetentionRuntimePort, metadata appChannelRetentionMetadataStore, logger wklog.Logger) *appretention.Worker {
	if cfg.ttl <= 0 {
		return nil
	}
	if logger == nil {
		logger = wklog.NewNop()
	}
	return appretention.NewWorker(appretention.Config{
		Channels: &appChannelRetentionChannels{
			keys:      engine,
			batchSize: appChannelRetentionBatchSize(cfg),
		},
		Stores:          appChannelRetentionStores{engine: engine},
		Runtime:         appChannelRetentionRuntime{runtime: runtime},
		Metadata:        appChannelRetentionMetadata{store: metadata},
		LocalNodeID:     channel.NodeID(localNodeID),
		TTL:             cfg.ttl,
		ScanInterval:    cfg.scanInterval,
		MaxTrimMessages: appChannelRetentionMaxTrimMessages(cfg),
		CursorName:      appChannelRetentionCursorName,
		Now:             time.Now,
		Logger:          logger,
	})
}

func appChannelRetentionBatchSize(cfg appChannelRetentionConfig) int {
	return cfg.channelBatchSize
}

func appChannelRetentionMaxTrimMessages(cfg appChannelRetentionConfig) int {
	if cfg.maxTrimMessages > 0 {
		return cfg.maxTrimMessages
	}
	return 10000
}

func contextError(ctx context.Context) error {
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
