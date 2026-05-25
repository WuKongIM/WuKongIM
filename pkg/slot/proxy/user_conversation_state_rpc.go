package proxy

import (
	"context"
	"errors"
	"fmt"
	"sort"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const userConversationStateRPCServiceID uint8 = 11

const (
	userConversationStateRPCGet           = "get"
	userConversationStateRPCList          = "list_active"
	userConversationStateRPCScanPage      = "scan_page"
	userConversationStateRPCUpsert        = "upsert"
	userConversationStateRPCTouch         = "touch_active"
	userConversationStateRPCClear         = "clear_active"
	userConversationStateRPCHide          = "hide"
	userConversationStateRPCHotHint       = "hot_hint"
	userConversationStateRPCRemoveHotHint = "remove_hot_hint"
)

type userConversationStateRPCRequest struct {
	Op          string                                 `json:"op"`
	SlotID      uint64                                 `json:"slot_id"`
	HashSlot    uint16                                 `json:"hash_slot,omitempty"`
	UID         string                                 `json:"uid,omitempty"`
	ChannelID   string                                 `json:"channel_id,omitempty"`
	ChannelType int64                                  `json:"channel_type,omitempty"`
	After       *metadb.ConversationCursor             `json:"after,omitempty"`
	Limit       int                                    `json:"limit,omitempty"`
	States      []metadb.UserConversationState         `json:"states,omitempty"`
	Patches     []metadb.UserConversationActivePatch   `json:"patches,omitempty"`
	Keys        []metadb.ConversationKey               `json:"keys,omitempty"`
	Deletes     []metadb.UserConversationDelete        `json:"deletes,omitempty"`
	Hints       []metadb.UserConversationActiveHint    `json:"hints,omitempty"`
	Barriers    []metadb.UserConversationDeleteBarrier `json:"barriers,omitempty"`
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

// UpsertUserConversationStates writes user conversation read/delete state through the authoritative UID slot.
func (s *Store) UpsertUserConversationStates(ctx context.Context, states []metadb.UserConversationState) error {
	if len(states) == 0 {
		return nil
	}

	grouped, err := s.groupUserConversationStatesBySlotAndHashSlot(states)
	if err != nil {
		return err
	}
	for slotID, groups := range grouped {
		for hashSlot, groupStates := range groups {
			if s.shouldServeSlotLocally(slotID) {
				cmd := metafsm.EncodeUpsertUserConversationStatesCommand(groupStates)
				if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
				Op:       userConversationStateRPCUpsert,
				SlotID:   uint64(slotID),
				HashSlot: hashSlot,
				States:   groupStates,
			}); err != nil {
				return err
			}
		}
	}
	return nil
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

// HideUserConversations durably hides user conversations and clears active_at on the UID owner.
func (s *Store) HideUserConversations(ctx context.Context, reqs []metadb.UserConversationDelete) error {
	if len(reqs) == 0 {
		return nil
	}

	grouped, err := s.groupUserConversationDeletesBySlotAndHashSlot(reqs)
	if err != nil {
		return err
	}
	for slotID, groups := range grouped {
		for hashSlot, groupReqs := range groups {
			if s.shouldServeSlotLocally(slotID) {
				cmd := metafsm.EncodeHideUserConversationsCommand(groupReqs)
				if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
				Op:       userConversationStateRPCHide,
				SlotID:   uint64(slotID),
				HashSlot: hashSlot,
				Deletes:  groupReqs,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) SubmitUserConversationActiveHints(ctx context.Context, hints []metadb.UserConversationActiveHint) error {
	if len(hints) == 0 {
		return nil
	}

	grouped, err := s.groupUserConversationActiveHintsBySlotAndHashSlot(hints)
	if err != nil {
		return err
	}
	for slotID, groups := range grouped {
		for hashSlot, groupHints := range groups {
			if s.shouldServeSlotLocally(slotID) {
				if err := s.submitUserConversationActiveHintsLocal(ctx, groupHints); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
				Op:       userConversationStateRPCHotHint,
				SlotID:   uint64(slotID),
				HashSlot: hashSlot,
				Hints:    groupHints,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) RemoveUserConversationActiveHints(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error {
	if len(barriers) == 0 {
		return nil
	}

	grouped, err := s.groupUserConversationDeleteBarriersBySlotAndHashSlot(barriers)
	if err != nil {
		return err
	}
	for slotID, groups := range grouped {
		for hashSlot, groupBarriers := range groups {
			if s.shouldServeSlotLocally(slotID) {
				if err := s.removeUserConversationActiveHintsLocal(ctx, groupBarriers); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callUserConversationStateRPC(ctx, slotID, userConversationStateRPCRequest{
				Op:       userConversationStateRPCRemoveHotHint,
				SlotID:   uint64(slotID),
				HashSlot: hashSlot,
				Barriers: groupBarriers,
			}); err != nil {
				return err
			}
		}
	}
	return nil
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
		return s.listUserConversationActiveLocal(ctx, hashSlot, uid, limit)
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
	payload, err := encodeUserConversationStateRPCRequestBinary(req)
	if err != nil {
		return userConversationStateRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, userConversationStateRPCServiceID, payload, decodeUserConversationStateRPCResponse)
}

func (s *Store) handleUserConversationStateRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeUserConversationStateRPCRequest(body)
	if err != nil {
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
		states, err := s.listUserConversationActiveLocal(ctx, hashSlot, req.UID, req.Limit)
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
	case userConversationStateRPCUpsert:
		cmd := metafsm.EncodeUpsertUserConversationStatesCommand(req.States)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{Status: rpcStatusOK})
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
	case userConversationStateRPCHide:
		cmd := metafsm.EncodeHideUserConversationsCommand(req.Deletes)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{Status: rpcStatusOK})
	case userConversationStateRPCHotHint:
		if err := s.submitUserConversationActiveHintsLocal(ctx, req.Hints); err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{Status: rpcStatusOK})
	case userConversationStateRPCRemoveHotHint:
		if err := s.removeUserConversationActiveHintsLocal(ctx, req.Barriers); err != nil {
			return nil, err
		}
		return encodeUserConversationStateRPCResponse(userConversationStateRPCResponse{Status: rpcStatusOK})
	default:
		return nil, fmt.Errorf("metastore: unknown user conversation state rpc op %q", req.Op)
	}
}

func encodeUserConversationStateRPCResponse(resp userConversationStateRPCResponse) ([]byte, error) {
	return encodeUserConversationStateRPCResponseBinary(resp)
}

func decodeUserConversationStateRPCResponse(body []byte) (userConversationStateRPCResponse, error) {
	return decodeUserConversationStateRPCResponseBinary(body)
}

func (s *Store) listUserConversationActiveLocal(ctx context.Context, hashSlot uint16, uid string, limit int) ([]metadb.UserConversationState, error) {
	persisted, err := s.db.ForHashSlot(hashSlot).ListUserConversationActive(ctx, uid, limit)
	if err != nil {
		return nil, err
	}
	if s.userConversationActiveOverlay == nil {
		return persisted, nil
	}

	hints, err := s.userConversationActiveOverlay.ListHotUserConversationActive(ctx, uid, userConversationActiveOverlayAllHintsLimit)
	if err != nil {
		return persisted, nil
	}
	if len(hints) == 0 {
		return persisted, nil
	}

	merged := make(map[metadb.ConversationKey]metadb.UserConversationState, len(persisted)+len(hints))
	for _, state := range persisted {
		merged[metadb.ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}] = state
	}

	for _, hint := range hints {
		if hint.UID != uid || hint.ActiveAt <= 0 {
			continue
		}
		key := metadb.ConversationKey{ChannelID: hint.ChannelID, ChannelType: hint.ChannelType}
		state, ok := merged[key]
		if !ok {
			state, err = s.db.ForHashSlot(hashSlot).GetUserConversationState(ctx, uid, hint.ChannelID, hint.ChannelType)
			switch {
			case err == nil:
			case errors.Is(err, metadb.ErrNotFound):
				state = metadb.UserConversationState{
					UID:         uid,
					ChannelID:   hint.ChannelID,
					ChannelType: hint.ChannelType,
				}
			default:
				return nil, err
			}
		}
		if state.DeletedToSeq > 0 && hint.MessageSeq <= state.DeletedToSeq {
			continue
		}
		if hint.ActiveAt > state.ActiveAt {
			state.ActiveAt = hint.ActiveAt
		}
		if state.ActiveAt > 0 {
			merged[key] = state
		}
	}

	states := make([]metadb.UserConversationState, 0, len(merged))
	for _, state := range merged {
		if state.ActiveAt > 0 {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].ActiveAt != states[j].ActiveAt {
			return states[i].ActiveAt > states[j].ActiveAt
		}
		if states[i].ChannelType != states[j].ChannelType {
			return states[i].ChannelType < states[j].ChannelType
		}
		return states[i].ChannelID < states[j].ChannelID
	})
	if len(states) > limit {
		states = states[:limit]
	}
	return states, nil
}

// userConversationActiveOverlayAllHintsLimit asks the bounded hot overlay for
// every UID-local hint so stale front entries cannot hide a later valid one.
const userConversationActiveOverlayAllHintsLimit = -1

func (s *Store) submitUserConversationActiveHintsLocal(ctx context.Context, hints []metadb.UserConversationActiveHint) error {
	if s.userConversationActiveOverlay == nil || len(hints) == 0 {
		return nil
	}
	return s.userConversationActiveOverlay.SubmitHints(ctx, hints)
}

func (s *Store) removeUserConversationActiveHintsLocal(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error {
	if s.userConversationActiveOverlay == nil || len(barriers) == 0 {
		return nil
	}
	return s.userConversationActiveOverlay.RemoveHints(ctx, barriers)
}

func (s *Store) groupUserConversationStatesBySlotAndHashSlot(states []metadb.UserConversationState) (map[multiraft.SlotID]map[uint16][]metadb.UserConversationState, error) {
	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.UserConversationState, len(states))
	for _, state := range states {
		if state.UID == "" {
			return nil, fmt.Errorf("metastore: empty uid in user conversation state")
		}
		slotID := s.cluster.SlotForKey(state.UID)
		hashSlot := hashSlotForKey(s.cluster, state.UID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.UserConversationState)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], state)
	}
	return grouped, nil
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

func (s *Store) groupUserConversationDeletesBySlotAndHashSlot(reqs []metadb.UserConversationDelete) (map[multiraft.SlotID]map[uint16][]metadb.UserConversationDelete, error) {
	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.UserConversationDelete, len(reqs))
	for _, req := range reqs {
		if req.UID == "" {
			return nil, fmt.Errorf("metastore: empty uid in user conversation delete")
		}
		slotID := s.cluster.SlotForKey(req.UID)
		hashSlot := hashSlotForKey(s.cluster, req.UID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.UserConversationDelete)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], req)
	}
	return grouped, nil
}

func (s *Store) groupUserConversationActiveHintsBySlotAndHashSlot(hints []metadb.UserConversationActiveHint) (map[multiraft.SlotID]map[uint16][]metadb.UserConversationActiveHint, error) {
	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.UserConversationActiveHint, len(hints))
	for _, hint := range hints {
		if hint.UID == "" {
			return nil, fmt.Errorf("metastore: empty uid in active hint")
		}
		slotID := s.cluster.SlotForKey(hint.UID)
		hashSlot := hashSlotForKey(s.cluster, hint.UID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.UserConversationActiveHint)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], hint)
	}
	return grouped, nil
}

func (s *Store) groupUserConversationDeleteBarriersBySlotAndHashSlot(barriers []metadb.UserConversationDeleteBarrier) (map[multiraft.SlotID]map[uint16][]metadb.UserConversationDeleteBarrier, error) {
	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.UserConversationDeleteBarrier, len(barriers))
	for _, barrier := range barriers {
		if barrier.UID == "" {
			return nil, fmt.Errorf("metastore: empty uid in delete barrier")
		}
		slotID := s.cluster.SlotForKey(barrier.UID)
		hashSlot := hashSlotForKey(s.cluster, barrier.UID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.UserConversationDeleteBarrier)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], barrier)
	}
	return grouped, nil
}
