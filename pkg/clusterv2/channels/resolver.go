package channels

import (
	"context"
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelMetaSource resolves authoritative ChannelV2 metadata.
type ChannelMetaSource interface {
	// ResolveChannelMeta returns metadata for id.
	ResolveChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error)
}

// RuntimeMetaReader reads authoritative ChannelRuntimeMeta from unified metadata storage.
type RuntimeMetaReader interface {
	// GetChannelRuntimeMeta reads one authoritative runtime metadata record.
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
}

// SlotMetaSource resolves ChannelV2 metadata from Slot authoritative runtime metadata.
type SlotMetaSource struct {
	reader RuntimeMetaReader
}

// NewSlotMetaSource creates a Slot-backed ChannelMetaSource.
func NewSlotMetaSource(reader RuntimeMetaReader) *SlotMetaSource {
	return &SlotMetaSource{reader: reader}
}

// ResolveChannelMeta returns metadata for id from authoritative Slot storage.
func (s *SlotMetaSource) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.Meta{}, err
	}
	if s == nil || s.reader == nil {
		return ch.Meta{}, fmt.Errorf("%w: slot metadata reader is nil", ch.ErrInvalidConfig)
	}
	meta, err := s.reader.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		if errors.Is(err, metadb.ErrNotFound) {
			return ch.Meta{}, fmt.Errorf("%w: %v", ch.ErrChannelNotFound, id)
		}
		return ch.Meta{}, err
	}
	if meta.ChannelID != id.ID || meta.ChannelType != int64(id.Type) {
		return ch.Meta{}, fmt.Errorf("%w: resolved %s/%d for %v", ch.ErrStaleMeta, meta.ChannelID, meta.ChannelType, id)
	}
	return projectRuntimeMeta(meta), nil
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

func projectRuntimeMeta(meta metadb.ChannelRuntimeMeta) ch.Meta {
	meta = metadb.NormalizeChannelRuntimeMeta(meta)
	id := ch.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}
	var leaseUntil time.Time
	if meta.LeaseUntilMS > 0 {
		leaseUntil = time.UnixMilli(meta.LeaseUntilMS).UTC()
	}
	return ch.Meta{
		Key:         ch.ChannelKeyForID(id),
		ID:          id,
		Epoch:       meta.ChannelEpoch,
		LeaderEpoch: meta.LeaderEpoch,
		Leader:      ch.NodeID(meta.Leader),
		Replicas:    projectNodeIDs(meta.Replicas),
		ISR:         projectNodeIDs(meta.ISR),
		MinISR:      int(meta.MinISR),
		LeaseUntil:  leaseUntil,
		Status:      ch.Status(meta.Status),
	}
}

func projectNodeIDs(ids []uint64) []ch.NodeID {
	out := make([]ch.NodeID, 0, len(ids))
	for _, id := range ids {
		out = append(out, ch.NodeID(id))
	}
	return out
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

var _ ChannelMetaSource = (*SlotMetaSource)(nil)
