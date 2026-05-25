package proxy

import (
	"container/heap"
	"context"
	"errors"
	"fmt"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const identityRPCServiceID uint8 = 4

const (
	identityRPCGetUser       = "get_user"
	identityRPCGetDevice     = "get_device"
	identityRPCScanUsersPage = "scan_users_page"
)

type identityRPCRequest struct {
	Op         string            `json:"op"`
	SlotID     uint64            `json:"slot_id"`
	UID        string            `json:"uid,omitempty"`
	DeviceFlag int64             `json:"device_flag,omitempty"`
	After      metadb.UserCursor `json:"after,omitempty"`
	Limit      int               `json:"limit,omitempty"`
}

type identityRPCResponse struct {
	Status   string            `json:"status"`
	LeaderID uint64            `json:"leader_id,omitempty"`
	User     *metadb.User      `json:"user,omitempty"`
	Device   *metadb.Device    `json:"device,omitempty"`
	Users    []metadb.User     `json:"users,omitempty"`
	Cursor   metadb.UserCursor `json:"cursor,omitempty"`
	Done     bool              `json:"done,omitempty"`
}

func (r identityRPCResponse) rpcStatus() string {
	return r.Status
}

func (r identityRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (s *Store) getUserAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid string) (metadb.User, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetUser(ctx, uid)
	}

	resp, err := s.callIdentityRPC(ctx, slotID, identityRPCRequest{
		Op:     identityRPCGetUser,
		SlotID: uint64(slotID),
		UID:    uid,
	})
	if err != nil {
		return metadb.User{}, err
	}
	if resp.User == nil {
		return metadb.User{}, metadb.ErrNotFound
	}
	return *resp.User, nil
}

func (s *Store) getDeviceAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid string, deviceFlag int64) (metadb.Device, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetDevice(ctx, uid, deviceFlag)
	}

	resp, err := s.callIdentityRPC(ctx, slotID, identityRPCRequest{
		Op:         identityRPCGetDevice,
		SlotID:     uint64(slotID),
		UID:        uid,
		DeviceFlag: deviceFlag,
	})
	if err != nil {
		return metadb.Device{}, err
	}
	if resp.Device == nil {
		return metadb.Device{}, metadb.ErrNotFound
	}
	return *resp.Device, nil
}

// ScanUsersSlotPage reads one authoritative user page for a physical Slot.
func (s *Store) ScanUsersSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.scanUsersSlotPageLocal(ctx, slotID, after, limit)
	}

	resp, err := s.callIdentityRPC(ctx, slotID, identityRPCRequest{
		Op:     identityRPCScanUsersPage,
		SlotID: uint64(slotID),
		After:  after,
		Limit:  limit,
	})
	if err != nil {
		return nil, metadb.UserCursor{}, false, err
	}
	return append([]metadb.User(nil), resp.Users...), resp.Cursor, resp.Done, nil
}

func (s *Store) callIdentityRPC(ctx context.Context, slotID multiraft.SlotID, req identityRPCRequest) (identityRPCResponse, error) {
	payload, err := encodeIdentityRPCRequestBinary(req)
	if err != nil {
		return identityRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, identityRPCServiceID, payload, decodeIdentityRPCResponse)
}

func (s *Store) handleIdentityRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeIdentityRPCRequest(body)
	if err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeIdentityRPCResponse(identityRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	switch req.Op {
	case identityRPCGetUser:
		hashSlot := hashSlotForKey(s.cluster, req.UID)
		user, err := s.db.ForHashSlot(hashSlot).GetUser(ctx, req.UID)
		if errors.Is(err, metadb.ErrNotFound) {
			return encodeIdentityRPCResponse(identityRPCResponse{Status: rpcStatusNotFound})
		}
		if err != nil {
			return nil, err
		}
		return encodeIdentityRPCResponse(identityRPCResponse{
			Status: rpcStatusOK,
			User:   &user,
		})
	case identityRPCGetDevice:
		hashSlot := hashSlotForKey(s.cluster, req.UID)
		device, err := s.db.ForHashSlot(hashSlot).GetDevice(ctx, req.UID, req.DeviceFlag)
		if errors.Is(err, metadb.ErrNotFound) {
			return encodeIdentityRPCResponse(identityRPCResponse{Status: rpcStatusNotFound})
		}
		if err != nil {
			return nil, err
		}
		return encodeIdentityRPCResponse(identityRPCResponse{
			Status: rpcStatusOK,
			Device: &device,
		})
	case identityRPCScanUsersPage:
		users, cursor, done, err := s.scanUsersSlotPageLocal(ctx, slotID, req.After, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodeIdentityRPCResponse(identityRPCResponse{
			Status: rpcStatusOK,
			Users:  users,
			Cursor: cursor,
			Done:   done,
		})
	default:
		return nil, fmt.Errorf("metastore: unknown identity rpc op %q", req.Op)
	}
}

func (s *Store) scanUsersSlotPageLocal(ctx context.Context, slotID multiraft.SlotID, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error) {
	if s.cluster == nil {
		return nil, metadb.UserCursor{}, false, fmt.Errorf("metastore: cluster not configured")
	}
	if limit <= 0 {
		return nil, metadb.UserCursor{}, false, metadb.ErrInvalidArgument
	}

	hashSlots := s.cluster.HashSlotsOf(slotID)
	if len(hashSlots) == 0 {
		return nil, metadb.UserCursor{}, false, raftcluster.ErrSlotNotFound
	}

	queue := make(userMergeHeap, 0, len(hashSlots))
	for _, hashSlot := range hashSlots {
		item, ok, err := s.loadUserMergeItem(ctx, hashSlot, after)
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
		item := heap.Pop(&queue).(userMergeItem)
		users = append(users, item.User)
		cursor = metadb.UserCursor{UID: item.User.UID}

		if item.Done {
			continue
		}

		nextItem, ok, err := s.loadUserMergeItem(ctx, item.HashSlot, item.Cursor)
		if err != nil {
			return nil, metadb.UserCursor{}, false, err
		}
		if ok {
			heap.Push(&queue, nextItem)
		}
	}

	if len(users) == 0 {
		cursor = after
	}
	return users, cursor, queue.Len() == 0, nil
}

func (s *Store) loadUserMergeItem(ctx context.Context, hashSlot uint16, after metadb.UserCursor) (userMergeItem, bool, error) {
	users, cursor, done, err := s.db.ForHashSlot(hashSlot).ListUsersPage(ctx, after, 1)
	if err != nil {
		return userMergeItem{}, false, err
	}
	if len(users) == 0 {
		return userMergeItem{}, false, nil
	}
	return userMergeItem{
		HashSlot: hashSlot,
		User:     users[0],
		Cursor:   cursor,
		Done:     done,
	}, true, nil
}

type userMergeItem struct {
	HashSlot uint16
	User     metadb.User
	Cursor   metadb.UserCursor
	Done     bool
}

type userMergeHeap []userMergeItem

func (h userMergeHeap) Len() int { return len(h) }

func (h userMergeHeap) Less(i, j int) bool {
	return h[i].User.UID < h[j].User.UID
}

func (h userMergeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *userMergeHeap) Push(x any) {
	*h = append(*h, x.(userMergeItem))
}

func (h *userMergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func encodeIdentityRPCResponse(resp identityRPCResponse) ([]byte, error) {
	return encodeIdentityRPCResponseBinary(resp)
}

func decodeIdentityRPCResponse(body []byte) (identityRPCResponse, error) {
	return decodeIdentityRPCResponseBinary(body)
}
