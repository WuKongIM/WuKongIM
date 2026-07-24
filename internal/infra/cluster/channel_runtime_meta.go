package cluster

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelRuntimeMetaScanNode exposes cluster channel runtime metadata scans for manager pages.
type ChannelRuntimeMetaScanNode interface {
	ScanChannelRuntimeMetaSlotPage(context.Context, uint32, metadb.ChannelRuntimeMetaCursor, int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
}

// ChannelRuntimeMetaPointNode exposes one routed channel runtime metadata lookup.
type ChannelRuntimeMetaPointNode interface {
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
}

// ChannelRuntimeMetaReader adapts cluster runtime metadata scans to management usecases.
type ChannelRuntimeMetaReader struct {
	node ChannelRuntimeMetaScanNode
}

// NewChannelRuntimeMetaReader creates a cluster-backed channel runtime metadata reader.
func NewChannelRuntimeMetaReader(node ChannelRuntimeMetaScanNode) *ChannelRuntimeMetaReader {
	return &ChannelRuntimeMetaReader{node: node}
}

// ScanChannelRuntimeMetaSlotPage returns one runtime metadata page for a physical Slot.
func (r *ChannelRuntimeMetaReader) ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID uint32, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	if r == nil || r.node == nil {
		return nil, after, true, nil
	}
	return r.node.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, limit)
}

// ChannelRuntimeMetaPointReader adapts one exact cluster metadata lookup.
type ChannelRuntimeMetaPointReader struct {
	node ChannelRuntimeMetaPointNode
}

// NewChannelRuntimeMetaPointReader creates the point-lookup adapter.
func NewChannelRuntimeMetaPointReader(node ChannelRuntimeMetaPointNode) *ChannelRuntimeMetaPointReader {
	return &ChannelRuntimeMetaPointReader{node: node}
}

// GetChannelRuntimeMeta returns one exact channel runtime row.
func (r *ChannelRuntimeMetaPointReader) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if r == nil || r.node == nil {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return r.node.GetChannelRuntimeMeta(ctx, channelID, channelType)
}
