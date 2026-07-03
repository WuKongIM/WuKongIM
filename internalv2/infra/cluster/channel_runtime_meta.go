package cluster

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelRuntimeMetaScanNode exposes cluster channel runtime metadata scans for manager pages.
type ChannelRuntimeMetaScanNode interface {
	ScanChannelRuntimeMetaSlotPage(context.Context, uint32, metadb.ChannelRuntimeMetaCursor, int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
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
