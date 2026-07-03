package cluster

import (
	"container/heap"
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ScanUsersSlotPage reads one user metadata page for a physical Slot.
func (n *Node) ScanUsersSlotPage(ctx context.Context, slotID uint32, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, metadb.UserCursor{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, metadb.UserCursor{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, metadb.UserCursor{}, false, ErrNotStarted
	}
	if limit <= 0 {
		return nil, metadb.UserCursor{}, false, metadb.ErrInvalidArgument
	}
	snapshot, err := n.LocalControlSnapshot(ctx)
	if err != nil {
		return nil, metadb.UserCursor{}, false, err
	}
	hashSlots := hashSlotsOfPhysicalSlot(snapshot.HashSlots, slotID)
	if len(hashSlots) == 0 {
		return nil, metadb.UserCursor{}, false, ErrSlotNotFound
	}

	queue := make(userMetadataMergeHeap, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		item, ok, err := n.loadUserMetadataMergeItem(ctx, hashSlot, after)
		if err != nil {
			return nil, metadb.UserCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, item)
		}
	}

	users := make([]metadb.User, 0, limit)
	cursor := after
	for len(users) < limit && queue.Len() > 0 {
		item := heap.Pop(&queue).(userMetadataMergeItem)
		users = append(users, item.User)
		cursor = metadb.UserCursor{UID: item.User.UID}
		if item.Done {
			continue
		}
		next, ok, err := n.loadUserMetadataMergeItem(ctx, item.HashSlot, item.Cursor)
		if err != nil {
			return nil, metadb.UserCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, next)
		}
	}
	if len(users) == 0 {
		cursor = after
	}
	return users, cursor, queue.Len() == 0, nil
}

func (n *Node) loadUserMetadataMergeItem(ctx context.Context, hashSlot uint16, after metadb.UserCursor) (userMetadataMergeItem, bool, error) {
	users, cursor, done, err := n.defaultSlotMetaDB.ForHashSlot(hashSlot).ListUsersPage(ctx, after, 1)
	if err != nil {
		return userMetadataMergeItem{}, false, err
	}
	if len(users) == 0 {
		return userMetadataMergeItem{}, false, nil
	}
	return userMetadataMergeItem{
		HashSlot: hashSlot,
		User:     users[0],
		Cursor:   cursor,
		Done:     done,
	}, true, nil
}

type userMetadataMergeItem struct {
	HashSlot uint16
	User     metadb.User
	Cursor   metadb.UserCursor
	Done     bool
}

type userMetadataMergeHeap []userMetadataMergeItem

func (h userMetadataMergeHeap) Len() int { return len(h) }

func (h userMetadataMergeHeap) Less(i, j int) bool {
	return h[i].User.UID < h[j].User.UID
}

func (h userMetadataMergeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *userMetadataMergeHeap) Push(x any) {
	*h = append(*h, x.(userMetadataMergeItem))
}

func (h *userMetadataMergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
