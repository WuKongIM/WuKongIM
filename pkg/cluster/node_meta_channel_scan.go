package cluster

import (
	"container/heap"
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ScanChannelsSlotPage reads one channel metadata page for a physical Slot.
func (n *Node) ScanChannelsSlotPage(ctx context.Context, slotID uint32, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, metadb.ChannelCursor{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, metadb.ChannelCursor{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, metadb.ChannelCursor{}, false, ErrNotStarted
	}
	if limit <= 0 {
		return nil, metadb.ChannelCursor{}, false, metadb.ErrInvalidArgument
	}
	snapshot, err := n.LocalControlSnapshot(ctx)
	if err != nil {
		return nil, metadb.ChannelCursor{}, false, err
	}
	hashSlots := hashSlotsOfPhysicalSlot(snapshot.HashSlots, slotID)
	if len(hashSlots) == 0 {
		return nil, metadb.ChannelCursor{}, false, ErrSlotNotFound
	}

	queue := make(channelMetadataMergeHeap, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		item, ok, err := n.loadChannelMetadataMergeItem(ctx, hashSlot, after)
		if err != nil {
			return nil, metadb.ChannelCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, item)
		}
	}

	channels := make([]metadb.Channel, 0, limit)
	cursor := after
	for len(channels) < limit && queue.Len() > 0 {
		item := heap.Pop(&queue).(channelMetadataMergeItem)
		channels = append(channels, item.Channel)
		cursor = metadb.ChannelCursor{ChannelID: item.Channel.ChannelID, ChannelType: item.Channel.ChannelType}
		if item.Done {
			continue
		}
		next, ok, err := n.loadChannelMetadataMergeItem(ctx, item.HashSlot, item.Cursor)
		if err != nil {
			return nil, metadb.ChannelCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, next)
		}
	}
	if len(channels) == 0 {
		cursor = after
	}
	return channels, cursor, queue.Len() == 0, nil
}

func (n *Node) loadChannelMetadataMergeItem(ctx context.Context, hashSlot uint16, after metadb.ChannelCursor) (channelMetadataMergeItem, bool, error) {
	channels, cursor, done, err := n.defaultSlotMetaDB.ForHashSlot(hashSlot).ListChannelsPage(ctx, after, 1)
	if err != nil {
		return channelMetadataMergeItem{}, false, err
	}
	if len(channels) == 0 {
		return channelMetadataMergeItem{}, false, nil
	}
	return channelMetadataMergeItem{
		HashSlot: hashSlot,
		Channel:  channels[0],
		Cursor:   cursor,
		Done:     done,
	}, true, nil
}

func hashSlotsOfPhysicalSlot(table control.HashSlotTable, slotID uint32) []uint16 {
	hashSlots := make([]uint16, 0)
	for _, item := range table.Ranges {
		if item.SlotID != slotID {
			continue
		}
		for hashSlot := int(item.From); hashSlot <= int(item.To); hashSlot++ {
			hashSlots = append(hashSlots, uint16(hashSlot))
		}
	}
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	return hashSlots
}

type channelMetadataMergeItem struct {
	HashSlot uint16
	Channel  metadb.Channel
	Cursor   metadb.ChannelCursor
	Done     bool
}

type channelMetadataMergeHeap []channelMetadataMergeItem

func (h channelMetadataMergeHeap) Len() int { return len(h) }

func (h channelMetadataMergeHeap) Less(i, j int) bool {
	return channelMetadataLess(h[i].Channel, h[j].Channel)
}

func (h channelMetadataMergeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *channelMetadataMergeHeap) Push(x any) {
	*h = append(*h, x.(channelMetadataMergeItem))
}

func (h *channelMetadataMergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func channelMetadataLess(left, right metadb.Channel) bool {
	if len(left.ChannelID) != len(right.ChannelID) {
		return len(left.ChannelID) < len(right.ChannelID)
	}
	if left.ChannelID != right.ChannelID {
		return left.ChannelID < right.ChannelID
	}
	return left.ChannelType < right.ChannelType
}
