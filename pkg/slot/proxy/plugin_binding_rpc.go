package proxy

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// pluginBindingRPCServiceID is globally unique on the shared transport RPC mux.
const pluginBindingRPCServiceID uint8 = 53

const (
	pluginBindingRPCBind           = "bind"
	pluginBindingRPCUnbind         = "unbind"
	pluginBindingRPCListByUID      = "list_by_uid"
	pluginBindingRPCScanByPluginNo = "scan_by_plugin_no"
	pluginBindingRPCExistsByUID    = "exists_by_uid"
	pluginBindingRPCGetInHashSlot  = "get_in_hash_slot"

	pluginBindingScanMaxLimit = 1024

	pluginBindingProxyMaxKeyStringLen    = 1<<16 - 1
	pluginBindingPageCursorMaxEncodedLen = ((pluginBindingProxyMaxKeyStringLen*2 + 64) * 4 / 3) + 8
)

var pluginBindingErrGetInHashSlotUnsupported = fmt.Errorf("metastore: plugin binding get_in_hash_slot unsupported")

type pluginBindingRPCRequest struct {
	Op       string                  `json:"op"`
	SlotID   uint64                  `json:"slot_id"`
	HashSlot uint16                  `json:"hash_slot,omitempty"`
	UID      string                  `json:"uid,omitempty"`
	PluginNo string                  `json:"plugin_no,omitempty"`
	After    *pluginBindingRPCCursor `json:"after,omitempty"`
	Limit    int                     `json:"limit,omitempty"`
}

type pluginBindingRPCResponse struct {
	Status   string                     `json:"status"`
	LeaderID uint64                     `json:"leader_id,omitempty"`
	Bindings []metadb.PluginUserBinding `json:"bindings,omitempty"`
	Cursor   pluginBindingRPCCursor     `json:"cursor,omitempty"`
	Done     bool                       `json:"done,omitempty"`
	Exists   bool                       `json:"exists,omitempty"`
}

func (r pluginBindingRPCResponse) rpcStatus() string {
	return r.Status
}

func (r pluginBindingRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

type pluginBindingRPCCursor struct {
	PluginNo string
	UID      string
}

type pluginBindingPageCursor struct {
	SlotID   multiraft.SlotID
	HashSlot uint16
	Binding  metadb.PluginUserBindingCursor
}

// BindPluginUser binds a UID to a plugin through the UID-owned authoritative slot.
func (s *Store) BindPluginUser(ctx context.Context, uid, pluginNo string) error {
	nowMS := time.Now().UTC().UnixMilli()
	binding := metadb.PluginUserBinding{
		UID:         uid,
		PluginNo:    pluginNo,
		CreatedAtMS: nowMS,
		UpdatedAtMS: nowMS,
	}
	if err := validatePluginBindingInput(uid, pluginNo); err != nil {
		return err
	}
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	if s.shouldServeSlotLocally(slotID) {
		cmd := metafsm.EncodeBindPluginUserCommand(binding)
		return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
	}
	_, err := s.callPluginBindingRPC(ctx, slotID, pluginBindingRPCRequest{
		Op:       pluginBindingRPCBind,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
		PluginNo: pluginNo,
	})
	return err
}

// UnbindPluginUser removes a UID to plugin binding through the UID-owned slot.
func (s *Store) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	if err := validatePluginBindingInput(uid, pluginNo); err != nil {
		return err
	}
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	if s.shouldServeSlotLocally(slotID) {
		cmd := metafsm.EncodeUnbindPluginUserCommand(uid, pluginNo)
		return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
	}
	_, err := s.callPluginBindingRPC(ctx, slotID, pluginBindingRPCRequest{
		Op:       pluginBindingRPCUnbind,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
		PluginNo: pluginNo,
	})
	return err
}

// ListPluginBindingsByUID lists all plugin bindings for one UID from its authoritative slot.
func (s *Store) ListPluginBindingsByUID(ctx context.Context, uid string) ([]metadb.PluginUserBinding, error) {
	if err := validatePluginBindingUID(uid); err != nil {
		return nil, err
	}
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).ListPluginBindingsByUID(ctx, uid)
	}
	resp, err := s.callPluginBindingRPC(ctx, slotID, pluginBindingRPCRequest{
		Op:       pluginBindingRPCListByUID,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
	})
	if err != nil {
		return nil, err
	}
	return append([]metadb.PluginUserBinding(nil), resp.Bindings...), nil
}

// ExistPluginBindingByUID reports whether one UID has any plugin binding.
func (s *Store) ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error) {
	if err := validatePluginBindingUID(uid); err != nil {
		return false, err
	}
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).ExistPluginBindingByUID(ctx, uid)
	}
	resp, err := s.callPluginBindingRPC(ctx, slotID, pluginBindingRPCRequest{
		Op:       pluginBindingRPCExistsByUID,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
	})
	if err != nil {
		return false, err
	}
	return resp.Exists, nil
}

// ListPluginBindingsByPluginNo returns a deterministic plugin-centric page.
func (s *Store) ListPluginBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) ([]metadb.PluginUserBinding, string, bool, error) {
	if err := validatePluginBindingPluginNo(pluginNo); err != nil {
		return nil, "", false, err
	}
	if limit <= 0 {
		return nil, "", false, metadb.ErrInvalidArgument
	}
	if limit > pluginBindingScanMaxLimit {
		limit = pluginBindingScanMaxLimit
	}
	after, err := decodePluginBindingPageCursor(cursor)
	if err != nil {
		return nil, "", false, err
	}
	if err := s.validatePluginBindingPageCursor(pluginNo, after); err != nil {
		return nil, "", false, err
	}

	slotIDs := append([]multiraft.SlotID(nil), s.cluster.SlotIDs()...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	queue := make(pluginBindingMergeHeap, 0, len(slotIDs))
	for _, slotID := range slotIDs {
		hashSlots := append([]uint16(nil), s.cluster.HashSlotsOf(slotID)...)
		if len(hashSlots) == 0 {
			continue
		}
		sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
		for _, hashSlot := range hashSlots {
			item, ok, err := s.loadPluginBindingMergeItem(ctx, slotID, hashSlot, pluginNo, after)
			if err != nil {
				return nil, "", false, err
			}
			if ok {
				heap.Push(&queue, item)
			}
		}
	}

	out := make([]metadb.PluginUserBinding, 0, limit)
	var last pluginBindingPageCursor
	for len(out) < limit && queue.Len() > 0 {
		item := heap.Pop(&queue).(pluginBindingMergeItem)
		out = append(out, item.Binding)
		last = pluginBindingPageCursor{
			SlotID:   item.SlotID,
			HashSlot: item.HashSlot,
			Binding:  metadb.PluginUserBindingCursor{PluginNo: pluginNo, UID: item.Binding.UID},
		}
		if item.Done {
			continue
		}
		nextItem, ok, err := s.loadPluginBindingMergeItem(ctx, item.SlotID, item.HashSlot, pluginNo, pluginBindingPageCursor{SlotID: item.SlotID, HashSlot: item.HashSlot, Binding: item.Cursor})
		if err != nil {
			return nil, "", false, err
		}
		if ok {
			heap.Push(&queue, nextItem)
		}
	}
	if queue.Len() == 0 {
		return out, "", false, nil
	}
	next, err := encodePluginBindingPageCursor(last)
	if err != nil {
		return nil, "", false, err
	}
	return out, next, true, nil
}

func (s *Store) callPluginBindingRPC(ctx context.Context, slotID multiraft.SlotID, req pluginBindingRPCRequest) (pluginBindingRPCResponse, error) {
	payload, err := encodePluginBindingRPCRequestBinary(req)
	if err != nil {
		return pluginBindingRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, pluginBindingRPCServiceID, payload, decodePluginBindingRPCResponse)
}

func (s *Store) handlePluginBindingRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodePluginBindingRPCRequest(body)
	if err != nil {
		return nil, err
	}
	if err := validatePluginBindingRPCRequest(req); err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if redirected, err := s.resolvePluginBindingRPCRoute(req, slotID); redirected != nil || err != nil {
		return redirected, err
	}
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	hashSlot := req.HashSlot
	if pluginBindingRPCUsesUIDRoute(req.Op) && req.UID != "" {
		hashSlot = hashSlotForKey(s.cluster, req.UID)
	}
	switch req.Op {
	case pluginBindingRPCBind:
		if err := validatePluginBindingInput(req.UID, req.PluginNo); err != nil {
			return nil, err
		}
		nowMS := time.Now().UTC().UnixMilli()
		cmd := metafsm.EncodeBindPluginUserCommand(metadb.PluginUserBinding{
			UID:         req.UID,
			PluginNo:    req.PluginNo,
			CreatedAtMS: nowMS,
			UpdatedAtMS: nowMS,
		})
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
			return nil, err
		}
		return encodePluginBindingRPCResponse(pluginBindingRPCResponse{Status: rpcStatusOK})
	case pluginBindingRPCUnbind:
		if err := validatePluginBindingInput(req.UID, req.PluginNo); err != nil {
			return nil, err
		}
		cmd := metafsm.EncodeUnbindPluginUserCommand(req.UID, req.PluginNo)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
			return nil, err
		}
		return encodePluginBindingRPCResponse(pluginBindingRPCResponse{Status: rpcStatusOK})
	case pluginBindingRPCListByUID:
		bindings, err := s.db.ForHashSlot(hashSlot).ListPluginBindingsByUID(ctx, req.UID)
		if err != nil {
			return nil, err
		}
		return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
			Status:   rpcStatusOK,
			Bindings: bindings,
		})
	case pluginBindingRPCExistsByUID:
		exists, err := s.db.ForHashSlot(hashSlot).ExistPluginBindingByUID(ctx, req.UID)
		if err != nil {
			return nil, err
		}
		return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
			Status: rpcStatusOK,
			Exists: exists,
		})
	case pluginBindingRPCScanByPluginNo:
		if !pluginBindingHashSlotOwnedBySlot(s.cluster, slotID, hashSlot) {
			return nil, metadb.ErrInvalidArgument
		}
		after := metadb.PluginUserBindingCursor{}
		if req.After != nil {
			after = metadb.PluginUserBindingCursor{PluginNo: req.After.PluginNo, UID: req.After.UID}
		}
		bindings, cursor, hasMore, err := s.db.ForHashSlot(hashSlot).ScanPluginBindingsByPluginNo(ctx, req.PluginNo, after, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
			Status:   rpcStatusOK,
			Bindings: bindings,
			Cursor:   pluginBindingRPCCursor{PluginNo: cursor.PluginNo, UID: cursor.UID},
			Done:     !hasMore,
		})
	case pluginBindingRPCGetInHashSlot:
		if !pluginBindingHashSlotOwnedBySlot(s.cluster, slotID, hashSlot) {
			return nil, metadb.ErrInvalidArgument
		}
		binding, exists, err := s.getPluginBindingLocalHashSlot(ctx, hashSlot, req.UID, req.PluginNo)
		if err != nil {
			return nil, err
		}
		resp := pluginBindingRPCResponse{Status: rpcStatusOK}
		if exists {
			resp.Bindings = []metadb.PluginUserBinding{binding}
		}
		return encodePluginBindingRPCResponse(resp)
	default:
		return nil, fmt.Errorf("metastore: unknown plugin binding rpc op %q", req.Op)
	}
}

func (s *Store) resolvePluginBindingRPCRoute(req pluginBindingRPCRequest, slotID multiraft.SlotID) ([]byte, error) {
	if req.UID == "" || !pluginBindingRPCUsesUIDRoute(req.Op) {
		return nil, nil
	}
	actualSlotID := s.cluster.SlotForKey(req.UID)
	if actualSlotID == slotID {
		return nil, nil
	}
	leaderID, err := s.cluster.LeaderOf(actualSlotID)
	if err != nil {
		body, encodeErr := encodePluginBindingRPCResponse(pluginBindingRPCResponse{Status: rpcStatusNoLeader})
		return body, encodeErr
	}
	body, err := encodePluginBindingRPCResponse(pluginBindingRPCResponse{
		Status:   rpcStatusNotLeader,
		LeaderID: uint64(leaderID),
	})
	return body, err
}

func (s *Store) scanPluginBindingsSlotHashSlot(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, pluginNo string, after metadb.PluginUserBindingCursor, limit int) ([]metadb.PluginUserBinding, metadb.PluginUserBindingCursor, bool, error) {
	if limit <= 0 {
		return nil, after, true, nil
	}
	if s.shouldServeSlotLocally(slotID) {
		bindings, cursor, hasMore, err := s.db.ForHashSlot(hashSlot).ScanPluginBindingsByPluginNo(ctx, pluginNo, after, limit)
		return bindings, cursor, !hasMore, err
	}
	resp, err := s.callPluginBindingRPC(ctx, slotID, pluginBindingRPCRequest{
		Op:       pluginBindingRPCScanByPluginNo,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		PluginNo: pluginNo,
		After:    pluginBindingRPCCursorPtr(after),
		Limit:    limit,
	})
	if err != nil {
		return nil, metadb.PluginUserBindingCursor{}, false, err
	}
	return append([]metadb.PluginUserBinding(nil), resp.Bindings...), metadb.PluginUserBindingCursor{PluginNo: resp.Cursor.PluginNo, UID: resp.Cursor.UID}, resp.Done, nil
}

func (s *Store) loadPluginBindingMergeItem(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, pluginNo string, after pluginBindingPageCursor) (pluginBindingMergeItem, bool, error) {
	shardAfter := metadb.PluginUserBindingCursor{}
	if after != pluginBindingEmptyPageCursor {
		if pluginBindingShardAfterCursor(slotID, hashSlot, after) {
			binding, exists, err := s.getPluginBindingSlotHashSlot(ctx, slotID, hashSlot, after.Binding.UID, pluginNo)
			if err != nil {
				if err == pluginBindingErrGetInHashSlotUnsupported {
					return s.loadPluginBindingMergeItemCompat(ctx, slotID, hashSlot, pluginNo, after)
				}
				return pluginBindingMergeItem{}, false, err
			}
			if exists {
				return pluginBindingMergeItem{
					SlotID:   slotID,
					HashSlot: hashSlot,
					Binding:  binding,
					Cursor:   after.Binding,
				}, true, nil
			}
		}
		shardAfter = after.Binding
	}
	bindings, cursor, done, err := s.scanPluginBindingsSlotHashSlot(ctx, slotID, hashSlot, pluginNo, shardAfter, 1)
	if err != nil {
		return pluginBindingMergeItem{}, false, err
	}
	if len(bindings) == 0 {
		return pluginBindingMergeItem{}, false, nil
	}
	return pluginBindingMergeItem{
		SlotID:   slotID,
		HashSlot: hashSlot,
		Binding:  bindings[0],
		Cursor:   cursor,
		Done:     done,
	}, true, nil
}

func (s *Store) loadPluginBindingMergeItemCompat(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, pluginNo string, after pluginBindingPageCursor) (pluginBindingMergeItem, bool, error) {
	shardAfter := metadb.PluginUserBindingCursor{}
	for {
		bindings, cursor, done, err := s.scanPluginBindingsSlotHashSlot(ctx, slotID, hashSlot, pluginNo, shardAfter, 1)
		if err != nil {
			return pluginBindingMergeItem{}, false, err
		}
		if len(bindings) == 0 {
			return pluginBindingMergeItem{}, false, nil
		}
		item := pluginBindingMergeItem{
			SlotID:   slotID,
			HashSlot: hashSlot,
			Binding:  bindings[0],
			Cursor:   cursor,
			Done:     done,
		}
		if pluginBindingItemAfterCursor(item, after) {
			return item, true, nil
		}
		if done {
			return pluginBindingMergeItem{}, false, nil
		}
		if cursor == shardAfter {
			return pluginBindingMergeItem{}, false, fmt.Errorf("metastore: plugin binding scan cursor did not advance")
		}
		shardAfter = cursor
	}
}

func (s *Store) getPluginBindingSlotHashSlot(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid, pluginNo string) (metadb.PluginUserBinding, bool, error) {
	if s.shouldServeSlotLocally(slotID) {
		if !pluginBindingHashSlotOwnedBySlot(s.cluster, slotID, hashSlot) {
			return metadb.PluginUserBinding{}, false, metadb.ErrInvalidArgument
		}
		return s.getPluginBindingLocalHashSlot(ctx, hashSlot, uid, pluginNo)
	}
	resp, err := s.callPluginBindingGetInHashSlotRPC(ctx, slotID, pluginBindingRPCRequest{
		Op:       pluginBindingRPCGetInHashSlot,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
		PluginNo: pluginNo,
	})
	if err != nil {
		if pluginBindingGetInHashSlotUnsupported(err) {
			return metadb.PluginUserBinding{}, false, pluginBindingErrGetInHashSlotUnsupported
		}
		return metadb.PluginUserBinding{}, false, err
	}
	if len(resp.Bindings) == 0 {
		return metadb.PluginUserBinding{}, false, nil
	}
	return resp.Bindings[0], true, nil
}

func (s *Store) callPluginBindingGetInHashSlotRPC(ctx context.Context, slotID multiraft.SlotID, req pluginBindingRPCRequest) (pluginBindingRPCResponse, error) {
	payload, err := encodePluginBindingRPCRequestBinary(req)
	if err != nil {
		return pluginBindingRPCResponse{}, err
	}
	return callPluginBindingRPCPreserveUnsupported(ctx, s, slotID, payload)
}

func callPluginBindingRPCPreserveUnsupported(ctx context.Context, s *Store, slotID multiraft.SlotID, payload []byte) (pluginBindingRPCResponse, error) {
	if s.cluster == nil {
		return pluginBindingRPCResponse{}, fmt.Errorf("metastore: cluster not configured")
	}

	peers := s.cluster.PeersForSlot(slotID)
	if len(peers) == 0 {
		return pluginBindingRPCResponse{}, raftcluster.ErrSlotNotFound
	}

	tried := make(map[multiraft.NodeID]struct{}, len(peers))
	candidates := append([]multiraft.NodeID(nil), peers...)
	var lastErr error
	unsupported := false
	for len(candidates) > 0 {
		peer := candidates[0]
		candidates = candidates[1:]
		if _, ok := tried[peer]; ok {
			continue
		}
		tried[peer] = struct{}{}

		body, err := s.cluster.RPCService(ctx, peer, slotID, pluginBindingRPCServiceID, payload)
		if err != nil {
			if pluginBindingGetInHashSlotUnsupported(err) {
				unsupported = true
			}
			lastErr = err
			continue
		}

		resp, err := decodePluginBindingRPCResponse(body)
		if err != nil {
			if pluginBindingGetInHashSlotUnsupported(err) {
				unsupported = true
			}
			lastErr = err
			continue
		}

		switch resp.rpcStatus() {
		case rpcStatusOK, rpcStatusNotFound:
			return resp, nil
		case rpcStatusNotLeader:
			if leaderID := multiraft.NodeID(resp.rpcLeaderID()); leaderID != 0 {
				if _, ok := tried[leaderID]; !ok {
					candidates = append([]multiraft.NodeID{leaderID}, candidates...)
				}
				continue
			}
		case rpcStatusNoLeader:
			lastErr = raftcluster.ErrNoLeader
			continue
		case rpcStatusNoSlot:
			lastErr = raftcluster.ErrSlotNotFound
			continue
		default:
			return pluginBindingRPCResponse{}, fmt.Errorf("metastore: unexpected rpc status %q", resp.rpcStatus())
		}
	}
	if unsupported {
		return pluginBindingRPCResponse{}, pluginBindingErrGetInHashSlotUnsupported
	}
	if lastErr != nil {
		return pluginBindingRPCResponse{}, lastErr
	}
	return pluginBindingRPCResponse{}, raftcluster.ErrNoLeader
}

func (s *Store) getPluginBindingLocalHashSlot(ctx context.Context, hashSlot uint16, uid, pluginNo string) (metadb.PluginUserBinding, bool, error) {
	bindings, err := s.db.ForHashSlot(hashSlot).ListPluginBindingsByUID(ctx, uid)
	if err != nil {
		return metadb.PluginUserBinding{}, false, err
	}
	for _, binding := range bindings {
		if binding.PluginNo == pluginNo {
			return binding, true, nil
		}
	}
	return metadb.PluginUserBinding{}, false, nil
}

func pluginBindingRPCCursorPtr(cursor metadb.PluginUserBindingCursor) *pluginBindingRPCCursor {
	if cursor == (metadb.PluginUserBindingCursor{}) {
		return pluginBindingEmptyRPCCursorPtr
	}
	return &pluginBindingRPCCursor{PluginNo: cursor.PluginNo, UID: cursor.UID}
}

func validatePluginBindingInput(uid, pluginNo string) error {
	if err := validatePluginBindingUID(uid); err != nil {
		return err
	}
	return validatePluginBindingPluginNo(pluginNo)
}

func validatePluginBindingUID(uid string) error {
	if uid == "" || len(uid) > pluginBindingProxyMaxKeyStringLen {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func validatePluginBindingPluginNo(pluginNo string) error {
	if pluginNo == "" || len(pluginNo) > pluginBindingProxyMaxKeyStringLen {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func validatePluginBindingRPCCursor(pluginNo string, cursor *pluginBindingRPCCursor) error {
	if cursor == nil {
		return nil
	}
	if cursor.PluginNo != pluginNo {
		return metadb.ErrInvalidArgument
	}
	return validatePluginBindingUID(cursor.UID)
}

func validatePluginBindingRPCRequest(req pluginBindingRPCRequest) error {
	switch req.Op {
	case pluginBindingRPCBind, pluginBindingRPCUnbind:
		return validatePluginBindingInput(req.UID, req.PluginNo)
	case pluginBindingRPCListByUID, pluginBindingRPCExistsByUID:
		return validatePluginBindingUID(req.UID)
	case pluginBindingRPCScanByPluginNo:
		if err := validatePluginBindingPluginNo(req.PluginNo); err != nil {
			return err
		}
		if req.Limit <= 0 || req.Limit > pluginBindingScanMaxLimit {
			return metadb.ErrInvalidArgument
		}
		return validatePluginBindingRPCCursor(req.PluginNo, req.After)
	case pluginBindingRPCGetInHashSlot:
		return validatePluginBindingInput(req.UID, req.PluginNo)
	default:
		return nil
	}
}

func (s *Store) validatePluginBindingPageCursor(pluginNo string, cursor pluginBindingPageCursor) error {
	if cursor == pluginBindingEmptyPageCursor {
		return nil
	}
	if cursor.Binding.PluginNo != pluginNo || cursor.Binding.UID == "" {
		return metadb.ErrInvalidArgument
	}
	if err := validatePluginBindingUID(cursor.Binding.UID); err != nil {
		return err
	}
	if !pluginBindingHashSlotOwnedBySlot(s.cluster, cursor.SlotID, cursor.HashSlot) {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func pluginBindingHashSlotOwnedBySlot(cluster interface {
	HashSlotsOf(multiraft.SlotID) []uint16
}, slotID multiraft.SlotID, hashSlot uint16) bool {
	for _, owned := range cluster.HashSlotsOf(slotID) {
		if owned == hashSlot {
			return true
		}
	}
	return false
}

func pluginBindingShardAfterCursor(slotID multiraft.SlotID, hashSlot uint16, cursor pluginBindingPageCursor) bool {
	if slotID != cursor.SlotID {
		return slotID > cursor.SlotID
	}
	return hashSlot > cursor.HashSlot
}

func pluginBindingGetInHashSlotUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unknown plugin binding rpc op id") || strings.Contains(msg, "unknown plugin binding rpc op ")
}

func pluginBindingItemAfterCursor(item pluginBindingMergeItem, cursor pluginBindingPageCursor) bool {
	if cursor == pluginBindingEmptyPageCursor {
		return true
	}
	if item.Binding.PluginNo != cursor.Binding.PluginNo {
		return item.Binding.PluginNo > cursor.Binding.PluginNo
	}
	if item.Binding.UID != cursor.Binding.UID {
		return item.Binding.UID > cursor.Binding.UID
	}
	if item.SlotID != cursor.SlotID {
		return item.SlotID > cursor.SlotID
	}
	return item.HashSlot > cursor.HashSlot
}

func pluginBindingRPCUsesUIDRoute(op string) bool {
	switch op {
	case pluginBindingRPCBind, pluginBindingRPCUnbind, pluginBindingRPCListByUID, pluginBindingRPCExistsByUID:
		return true
	default:
		return false
	}
}

type pluginBindingMergeItem struct {
	SlotID   multiraft.SlotID
	HashSlot uint16
	Binding  metadb.PluginUserBinding
	Cursor   metadb.PluginUserBindingCursor
	Done     bool
}

type pluginBindingMergeHeap []pluginBindingMergeItem

func (h pluginBindingMergeHeap) Len() int { return len(h) }

func (h pluginBindingMergeHeap) Less(i, j int) bool {
	if h[i].Binding.PluginNo != h[j].Binding.PluginNo {
		return h[i].Binding.PluginNo < h[j].Binding.PluginNo
	}
	if h[i].Binding.UID != h[j].Binding.UID {
		return h[i].Binding.UID < h[j].Binding.UID
	}
	if h[i].SlotID != h[j].SlotID {
		return h[i].SlotID < h[j].SlotID
	}
	return h[i].HashSlot < h[j].HashSlot
}

func (h pluginBindingMergeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *pluginBindingMergeHeap) Push(x any) {
	*h = append(*h, x.(pluginBindingMergeItem))
}

func (h *pluginBindingMergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
