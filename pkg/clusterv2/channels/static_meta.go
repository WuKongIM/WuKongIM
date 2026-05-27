package channels

import (
	"context"
	"fmt"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

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

// EnsureChannelMeta returns existing static metadata for append admission.
func (s *StaticMetaSource) EnsureChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	return s.ResolveChannelMeta(ctx, id)
}
