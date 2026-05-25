package channels

import (
	"context"
	"fmt"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// ChannelMetaSource resolves authoritative ChannelV2 metadata.
type ChannelMetaSource interface {
	// ResolveChannelMeta returns metadata for id.
	ResolveChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error)
}

// StaticMetaSource is an immutable in-memory metadata source for tests and smoke runs.
type StaticMetaSource struct {
	items map[ch.ChannelID]ch.Meta
}

// NewStaticMetaSource creates a StaticMetaSource from metas.
func NewStaticMetaSource(metas []ch.Meta) *StaticMetaSource {
	items := make(map[ch.ChannelID]ch.Meta, len(metas))
	for _, meta := range metas {
		if meta.ID == (ch.ChannelID{}) {
			continue
		}
		if meta.Key == "" {
			meta.Key = ch.ChannelKeyForID(meta.ID)
		}
		items[meta.ID] = cloneMeta(meta)
	}
	return &StaticMetaSource{items: items}
}

// ResolveChannelMeta returns metadata for id.
func (s *StaticMetaSource) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.Meta{}, err
	}
	meta, ok := s.items[id]
	if !ok {
		return ch.Meta{}, fmt.Errorf("%w: %v", ch.ErrChannelNotFound, id)
	}
	return cloneMeta(meta), nil
}

func cloneMeta(meta ch.Meta) ch.Meta {
	meta.Replicas = append([]ch.NodeID(nil), meta.Replicas...)
	meta.ISR = append([]ch.NodeID(nil), meta.ISR...)
	return meta
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
