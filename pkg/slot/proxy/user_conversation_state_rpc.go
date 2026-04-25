package proxy

import (
	"context"
	"encoding/json"
	"fmt"

	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const userConversationStateRPCServiceID uint8 = 11

const (
	userConversationStateRPCGet      = "get"
	userConversationStateRPCList     = "list_active"
	userConversationStateRPCScanPage = "scan_page"
	userConversationStateRPCTouch    = "touch_active"
	userConversationStateRPCClear    = "clear_active"
)

type userConversationStateRPCRequest struct {
	Op          string                               `json:"op"`
	SlotID      uint64                               `json:"slot_id"`
	HashSlot    uint16                               `json:"hash_slot,omitempty"`
	UID         string                               `json:"uid,omitempty"`
	ChannelID   string                               `json:"channel_id,omitempty"`
	ChannelType int64                                `json:"channel_type,omitempty"`
	After       *metadb.ConversationCursor           `json:"after,omitempty"`
	Limit       int                                  `json:"limit,omitempty"`
	Patches     []metadb.UserConversationActivePatch `json:"patches,omitempty"`
	Keys        []metadb.ConversationKey             `json:"keys,omitempty"`
}

type userConversationStateRPCResponse struct {
	Status   string                         `json:"status"`
	LeaderID uint64                         `json:"leader_id,omitempty"`
	State    *metadb.UserConversationState  `json:"state,omitempty"`
	States   []metadb.UserConversationState `json:"states,omitempty"`
	Cursor   metadb.ConversationCursor      `json:"cursor,omitempty"`
	Done     bool                           `json:"done,omitempty"`
}

func (r userConversationStateRPCResponse) rpcStatus() string {
	return r.Status
}

func (r userConversationStateRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (s *Store) GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	return s.getUserConversationStateAuthoritative(ctx, slotID, hashSlot, uid, channelID, channelType)
}

func (s *Store) ListUserConversationActive(ctx context.Context, uid string, limit int) ([]metadb.UserConversationState, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	return s.listUserConversationActiveAuthoritative(ctx, slotID, hashSlot, uid, limit)
}

func (s *Store) ScanUserConversationStatePage(ctx context.Context, uid string, after metadb.ConversationCursor, limit int) ([]metadb.UserConversationState, metadb.ConversationCursor, bool, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	return s.scanUserConversationStatePageAuthoritative(ctx, slotID, hashSlot, uid, after, limit)
}

func (s *Store) TouchUserConversationActiveAt(ctx context.Context, patches []metadb.UserConversationActivePatch) error {
	if len(patches) == 0 {
		return nil
	}

	grouped, err := s.groupUserConversationActivePatchesBySlotAndHashSlot(patches)
	if err != nil {
		return err
	}
	for slotID, groups := range grouped {
		for hashSlot, groupPatches := range groups {
			if s.shouldServeSlotLocally(slotID) {
				cmd := metafsm.EncodeTouchUserConversationActiveAtCommand(groupPatches)
				if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
				Op:       userConversationStateRPCTouch,
				SlotID:   uint64(slotID),
				HashSlot: hashSlot,
				Patches:  groupPatches,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ClearUserConversationActiveAt(ctx context.Context, uid string, keys []metadb.ConversationKey) error {
	if len(keys) == 0 {
		return nil
	}

	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	if s.shouldServeSlotLocally(slotID) {
		cmd := metafsm.EncodeClearUserConversationActiveAtCommand(uid, keys)
		return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
	}

	_, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
		Op:       userConversationStateRPCClear,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
		Keys:     keys,
	})
	return err
}

func (s *Store) getUserConversationStateAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid, channelID string, channelType int64) (metadb.UserConversationState, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetUserConversationState(ctx, uid, channelID, channelType)
	}

	resp, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
		Op:          userConversationStateRPCGet,
		SlotID:      uint64(slotID),
		HashSlot:    hashSlot,
		UID:         uid,
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	if err != nil {
		return metadb.UserConversationState{}, err
	}
	if resp.State == nil {
		return metadb.UserConversationState{}, metadb.ErrNotFound
	}
	return *resp.State, nil
}

func (s *Store) listUserConversationActiveAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid string, limit int) ([]metadb.UserConversationState, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).ListUserConversationActive(ctx, uid, limit)
	}

	resp, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
		Op:       userConversationStateRPCList,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	return append([]metadb.UserConversationState(nil), resp.States...), nil
}

func (s *Store) scanUserConversationStatePageAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid string, after metadb.ConversationCursor, limit int) ([]metadb.UserConversationState, metadb.ConversationCursor, bool, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).ListUserConversationStatePage(ctx, uid, after, limit)
	}

	resp, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
		Op:       userConversationStateRPCScanPage,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
		After:    &after,
		Limit:    limit,
	})
	if err != nil {
		return nil, metadb.ConversationCursor{}, false, err
	}
	return append([]metadb.UserConversationState(nil), resp.States...), resp.Cursor, resp.Done, nil
}

func (s *Store) callUserConversationStateRPC(ctx context.Context, slotID multiraft.SlotID, req userConversationStateRPCRequest) (userConversationStateRPCResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return userConversationStateRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, userConversationStateRPCServiceID, payload, decodeUserConversationStateRPCResponse)
}

func (s *Store) handleUserConversationStateRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req userConversationStateRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	hashSlot := req.HashSlot
	if hashSlot == 0 {
		hashSlot = hashSlotForKey(s.cluster, req.UID)
	}
	switch req.Op {
	case userConversationStateRPCGet:
		state, err := s.db.ForHashSlot(hashSlot).GetUserConversationState(ctx, req.UID, req.ChannelID, req.ChannelType)
		if err == metadb.ErrNotFound {
			return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{Status: rpcStatusNotFound})
		}
		if err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{
			Status: rpcStatusOK,
			State:  &state,
		})
	case userConversationStateRPCList:
		states, err := s.db.ForHashSlot(hashSlot).ListUserConversationActive(ctx, req.UID, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{
			Status: rpcStatusOK,
			States: states,
		})
	case userConversationStateRPCScanPage:
		var after metadb.ConversationCursor
		if req.After != nil {
			after = *req.After
		}
		states, cursor, done, err := s.db.ForHashSlot(hashSlot).ListUserConversationStatePage(ctx, req.UID, after, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{
			Status: rpcStatusOK,
			States: states,
			Cursor: cursor,
			Done:   done,
		})
	case userConversationStateRPCTouch:
		cmd := metafsm.EncodeTouchUserConversationActiveAtCommand(req.Patches)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{Status: rpcStatusOK})
	case userConversationStateRPCClear:
		cmd := metafsm.EncodeClearUserConversationActiveAtCommand(req.UID, req.Keys)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{Status: rpcStatusOK})
	default:
		return nil, fmt.Errorf("metastore: unknown user conversation state rpc op %q", req.Op)
	}
}

func encodeUserConversationStateRPCResponse(resp userConversationStateRPCResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeUserConversationStateRPCResponse(body []byte) (userConversationStateRPCResponse, error) {
	var resp userConversationStateRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return userConversationStateRPCResponse{}, err
	}
	return resp, nil
}

func (s *Store) groupUserConversationActivePatchesBySlotAndHashSlot(patches []metadb.UserConversationActivePatch) (map[multiraft.SlotID]map[uint16][]metadb.UserConversationActivePatch, error) {
	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.UserConversationActivePatch, len(patches))
	for _, patch := range patches {
		if patch.UID == "" {
			return nil, fmt.Errorf("metastore: empty uid in touch patch")
		}
		slotID := s.cluster.SlotForKey(patch.UID)
		hashSlot := hashSlotForKey(s.cluster, patch.UID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.UserConversationActivePatch)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], patch)
	}
	return grouped, nil
}
