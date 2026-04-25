package app

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

func (s *channelMetaSync) ActivateByID(ctx context.Context, id channel.ChannelID, source channelruntime.ActivationSource) (channel.Meta, error) {
	if s == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	return s.activate(ctx, channelhandler.KeyFromChannelID(id), source, func(ctx context.Context) (metadb.ChannelRuntimeMeta, error) {
		meta, err := s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		if err != nil {
			if errors.Is(err, metadb.ErrNotFound) && source == channelruntime.ActivationSourceBusiness {
				meta, err = s.ensureChannelRuntimeMeta(ctx, id)
			}
			if err != nil {
				return metadb.ChannelRuntimeMeta{}, err
			}
		}
		return s.reconcileChannelRuntimeMeta(ctx, meta)
	})
}

func (s *channelMetaSync) ActivateByKey(ctx context.Context, key channel.ChannelKey, source channelruntime.ActivationSource) (channel.Meta, error) {
	if s == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	if source != channelruntime.ActivationSourceBusiness {
		if meta, ok := s.cache.loadPositive(key, s.now()); ok {
			return meta, nil
		}
		if err := s.cache.loadNegative(key, s.now()); err != nil {
			return channel.Meta{}, err
		}
	}
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return channel.Meta{}, err
	}
	return s.activate(ctx, key, source, func(ctx context.Context) (metadb.ChannelRuntimeMeta, error) {
		meta, err := s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		if err != nil {
			return metadb.ChannelRuntimeMeta{}, err
		}
		return s.reconcileChannelRuntimeMeta(ctx, meta)
	})
}

func (s *channelMetaSync) activate(ctx context.Context, key channel.ChannelKey, source channelruntime.ActivationSource, load func(context.Context) (metadb.ChannelRuntimeMeta, error)) (channel.Meta, error) {
	s.observeHashSlotTableVersion()
	if source != channelruntime.ActivationSourceBusiness {
		if meta, ok := s.cache.loadPositive(key, s.now()); ok {
			return meta, nil
		}
		if err := s.cache.loadNegative(key, s.now()); err != nil {
			return channel.Meta{}, err
		}
	}
	meta, err := s.cache.runSingleflight(key, func() (channel.Meta, error) {
		loaded, err := load(ctx)
		if err != nil {
			s.cache.storeNegative(key, err, s.now())
			return channel.Meta{}, err
		}
		applied, err := s.applyAuthoritativeMeta(loaded)
		if err != nil {
			return channel.Meta{}, err
		}
		s.cache.storePositive(key, applied, s.now())
		return applied, nil
	})
	if err != nil {
		return channel.Meta{}, err
	}
	return meta, nil
}

func (s *channelMetaSync) applyAuthoritativeMeta(meta metadb.ChannelRuntimeMeta) (channel.Meta, error) {
	if s == nil || s.cluster == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	rootMeta := projectChannelMeta(meta)
	if err := s.cluster.ApplyRoutingMeta(rootMeta); err != nil {
		return channel.Meta{}, err
	}
	if containsUint64(meta.Replicas, s.localNode) {
		if err := s.cluster.EnsureLocalRuntime(rootMeta); err != nil {
			return channel.Meta{}, err
		}
		s.mu.Lock()
		s.trackAppliedLocalKeyLocked(rootMeta.Key)
		s.mu.Unlock()
		s.scheduleLeaderRepairForMeta(rootMeta)
		return rootMeta, nil
	}
	if err := s.removeLocalRuntime(rootMeta.Key); err != nil {
		return channel.Meta{}, err
	}
	return rootMeta, nil
}

func (s *channelMetaSync) now() time.Time {
	return time.Now()
}
