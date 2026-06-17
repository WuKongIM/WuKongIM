package cluster

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelBusinessScanNode exposes clusterv2 channel metadata scans for manager pages.
type ChannelBusinessScanNode interface {
	ScanChannelsSlotPage(context.Context, uint32, metadb.ChannelCursor, int) ([]metadb.Channel, metadb.ChannelCursor, bool, error)
}

// ChannelBusinessReader adapts clusterv2 channel metadata scans to management usecases.
type ChannelBusinessReader struct {
	node ChannelBusinessScanNode
}

// NewChannelBusinessReader creates a clusterv2-backed channel business reader.
func NewChannelBusinessReader(node ChannelBusinessScanNode) *ChannelBusinessReader {
	return &ChannelBusinessReader{node: node}
}

// ScanChannelsSlotPage returns one channel metadata page for a physical Slot.
func (r *ChannelBusinessReader) ScanChannelsSlotPage(ctx context.Context, slotID uint32, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	if r == nil || r.node == nil {
		return nil, after, true, nil
	}
	return r.node.ScanChannelsSlotPage(ctx, slotID, after, limit)
}
