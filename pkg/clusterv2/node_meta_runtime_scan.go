package clusterv2

import (
	"container/heap"
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ScanChannelRuntimeMetaSlotPage reads one channel runtime metadata page for a physical Slot.
func (n *Node) ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID uint32, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, ErrNotStarted
	}
	if limit <= 0 {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, metadb.ErrInvalidArgument
	}
	snapshot, err := n.LocalControlSnapshot(ctx)
	if err != nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, err
	}
	hashSlots := hashSlotsOfPhysicalSlot(snapshot.HashSlots, slotID)
	if len(hashSlots) == 0 {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, ErrSlotNotFound
	}

	queue := make(channelRuntimeMetaMergeHeap, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		item, ok, err := n.loadChannelRuntimeMetaMergeItem(ctx, hashSlot, after)
		if err != nil {
			return nil, metadb.ChannelRuntimeMetaCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, item)
		}
	}

	metas := make([]metadb.ChannelRuntimeMeta, 0, limit)
	cursor := after
	for len(metas) < limit && queue.Len() > 0 {
		item := heap.Pop(&queue).(channelRuntimeMetaMergeItem)
		metas = append(metas, item.Meta)
		cursor = metadb.ChannelRuntimeMetaCursor{ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType}
		if item.Done {
			continue
		}
		next, ok, err := n.loadChannelRuntimeMetaMergeItem(ctx, item.HashSlot, item.Cursor)
		if err != nil {
			return nil, metadb.ChannelRuntimeMetaCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, next)
		}
	}
	if len(metas) == 0 {
		cursor = after
	}
	return metas, cursor, queue.Len() == 0, nil
}

func (n *Node) loadChannelRuntimeMetaMergeItem(ctx context.Context, hashSlot uint16, after metadb.ChannelRuntimeMetaCursor) (channelRuntimeMetaMergeItem, bool, error) {
	metas, cursor, done, err := n.defaultSlotMetaDB.ForHashSlot(hashSlot).ListChannelRuntimeMetaPage(ctx, after, 1)
	if err != nil {
		return channelRuntimeMetaMergeItem{}, false, err
	}
	if len(metas) == 0 {
		return channelRuntimeMetaMergeItem{}, false, nil
	}
	return channelRuntimeMetaMergeItem{
		HashSlot: hashSlot,
		Meta:     metas[0],
		Cursor:   cursor,
		Done:     done,
	}, true, nil
}

type channelRuntimeMetaMergeItem struct {
	HashSlot uint16
	Meta     metadb.ChannelRuntimeMeta
	Cursor   metadb.ChannelRuntimeMetaCursor
	Done     bool
}

type channelRuntimeMetaMergeHeap []channelRuntimeMetaMergeItem

func (h channelRuntimeMetaMergeHeap) Len() int { return len(h) }

func (h channelRuntimeMetaMergeHeap) Less(i, j int) bool {
	return channelRuntimeMetaLess(h[i].Meta, h[j].Meta)
}

func (h channelRuntimeMetaMergeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *channelRuntimeMetaMergeHeap) Push(x any) {
	*h = append(*h, x.(channelRuntimeMetaMergeItem))
}

func (h *channelRuntimeMetaMergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func channelRuntimeMetaLess(left, right metadb.ChannelRuntimeMeta) bool {
	if len(left.ChannelID) != len(right.ChannelID) {
		return len(left.ChannelID) < len(right.ChannelID)
	}
	if left.ChannelID != right.ChannelID {
		return left.ChannelID < right.ChannelID
	}
	return left.ChannelType < right.ChannelType
}
