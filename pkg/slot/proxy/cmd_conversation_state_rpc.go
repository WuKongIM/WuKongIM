package proxy

import (
	"context"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// cmdConversationStateRPCServiceID is globally unique on the shared transport
// RPC mux; 14 is reserved by the controller service.
const cmdConversationStateRPCServiceID uint8 = 49

const (
	cmdConversationStateRPCGet         = "get"
	cmdConversationStateRPCList        = "list_active"
	cmdConversationStateRPCUpsert      = "upsert"
	cmdConversationStateRPCAdvanceRead = "advance_read"
)

type cmdConversationStateRPCRequest struct {
	Op          string                            `json:"op"`
	SlotID      uint64                            `json:"slot_id"`
	HashSlot    uint16                            `json:"hash_slot,omitempty"`
	UID         string                            `json:"uid,omitempty"`
	ChannelID   string                            `json:"channel_id,omitempty"`
	ChannelType int64                             `json:"channel_type,omitempty"`
	Limit       int                               `json:"limit,omitempty"`
	States      []metadb.CMDConversationState     `json:"states,omitempty"`
	Patches     []metadb.CMDConversationReadPatch `json:"patches,omitempty"`
}

type cmdConversationStateRPCResponse struct {
	Status   string                        `json:"status"`
	LeaderID uint64                        `json:"leader_id,omitempty"`
	State    *metadb.CMDConversationState  `json:"state,omitempty"`
	States   []metadb.CMDConversationState `json:"states,omitempty"`
}

func (r cmdConversationStateRPCResponse) rpcStatus() string {
	return r.Status
}

func (r cmdConversationStateRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

// GetCMDConversationState reads one UID-owned durable CMD cursor.
func (s *Store) GetCMDConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.CMDConversationState, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	return s.getCMDConversationStateAuthoritative(ctx, slotID, hashSlot, uid, channelID, channelType)
}

// ListCMDConversationActive lists active durable CMD cursors from the UID owner.
func (s *Store) ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]metadb.CMDConversationState, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	return s.listCMDConversationActiveAuthoritative(ctx, slotID, hashSlot, uid, limit)
}

// UpsertCMDConversationStates writes durable CMD cursors through UID-owned slots.
func (s *Store) UpsertCMDConversationStates(ctx context.Context, states []metadb.CMDConversationState) error {
	if len(states) == 0 {
		return nil
	}

	grouped, err := s.groupCMDConversationStatesBySlotAndHashSlot(states)
	if err != nil {
		return err
	}
	for slotID, groups := range grouped {
		for hashSlot, groupStates := range groups {
			if s.shouldServeSlotLocally(slotID) {
				cmd := metafsm.EncodeUpsertCMDConversationStatesCommand(groupStates)
				if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callCMDConversationStateRPC(ctx, slotID, cmdConversationStateRPCRequest{
				Op:       cmdConversationStateRPCUpsert,
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

// AdvanceCMDConversationReadSeq advances durable CMD read cursors on UID owners.
func (s *Store) AdvanceCMDConversationReadSeq(ctx context.Context, patches []metadb.CMDConversationReadPatch) error {
	if len(patches) == 0 {
		return nil
	}

	grouped, err := s.groupCMDConversationReadPatchesBySlotAndHashSlot(patches)
	if err != nil {
		return err
	}
	for slotID, groups := range grouped {
		for hashSlot, groupPatches := range groups {
			if s.shouldServeSlotLocally(slotID) {
				cmd := metafsm.EncodeAdvanceCMDConversationReadSeqCommand(groupPatches)
				if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callCMDConversationStateRPC(ctx, slotID, cmdConversationStateRPCRequest{
				Op:       cmdConversationStateRPCAdvanceRead,
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

func (s *Store) getCMDConversationStateAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid, channelID string, channelType int64) (metadb.CMDConversationState, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetCMDConversationState(ctx, uid, channelID, channelType)
	}

	resp, err := s.callCMDConversationStateRPC(ctx, slotID, cmdConversationStateRPCRequest{
		Op:          cmdConversationStateRPCGet,
		SlotID:      uint64(slotID),
		HashSlot:    hashSlot,
		UID:         uid,
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	if err != nil {
		return metadb.CMDConversationState{}, err
	}
	if resp.State == nil {
		return metadb.CMDConversationState{}, metadb.ErrNotFound
	}
	return *resp.State, nil
}

func (s *Store) listCMDConversationActiveAuthoritative(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, uid string, limit int) ([]metadb.CMDConversationState, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).ListCMDConversationActive(ctx, uid, limit)
	}

	resp, err := s.callCMDConversationStateRPC(ctx, slotID, cmdConversationStateRPCRequest{
		Op:       cmdConversationStateRPCList,
		SlotID:   uint64(slotID),
		HashSlot: hashSlot,
		UID:      uid,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	return append([]metadb.CMDConversationState(nil), resp.States...), nil
}

func (s *Store) callCMDConversationStateRPC(ctx context.Context, slotID multiraft.SlotID, req cmdConversationStateRPCRequest) (cmdConversationStateRPCResponse, error) {
	payload, err := encodeCMDConversationStateRPCRequestBinary(req)
	if err != nil {
		return cmdConversationStateRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, cmdConversationStateRPCServiceID, payload, decodeCMDConversationStateRPCResponse)
}

func (s *Store) handleCMDConversationStateRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeCMDConversationStateRPCRequest(body)
	if err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeCMDConversationStateRPCResponse(cmdConversationStateRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	hashSlot := req.HashSlot
	if hashSlot == 0 {
		hashSlot = hashSlotForKey(s.cluster, req.uidForHashSlotFallback())
	}
	switch req.Op {
	case cmdConversationStateRPCGet:
		state, err := s.db.ForHashSlot(hashSlot).GetCMDConversationState(ctx, req.UID, req.ChannelID, req.ChannelType)
		if err == metadb.ErrNotFound {
			return encodeCMDConversationStateRPCResponse(cmdConversationStateRPCResponse{Status: rpcStatusNotFound})
		}
		if err != nil {
			return nil, err
		}
		return encodeCMDConversationStateRPCResponse(cmdConversationStateRPCResponse{
			Status: rpcStatusOK,
			State:  &state,
		})
	case cmdConversationStateRPCList:
		states, err := s.db.ForHashSlot(hashSlot).ListCMDConversationActive(ctx, req.UID, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodeCMDConversationStateRPCResponse(cmdConversationStateRPCResponse{
			Status: rpcStatusOK,
			States: states,
		})
	case cmdConversationStateRPCUpsert:
		cmd := metafsm.EncodeUpsertCMDConversationStatesCommand(req.States)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
			return nil, err
		}
		return encodeCMDConversationStateRPCResponse(cmdConversationStateRPCResponse{Status: rpcStatusOK})
	case cmdConversationStateRPCAdvanceRead:
		cmd := metafsm.EncodeAdvanceCMDConversationReadSeqCommand(req.Patches)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
			return nil, err
		}
		return encodeCMDConversationStateRPCResponse(cmdConversationStateRPCResponse{Status: rpcStatusOK})
	default:
		return nil, fmt.Errorf("metastore: unknown cmd conversation state rpc op %q", req.Op)
	}
}

func encodeCMDConversationStateRPCResponse(resp cmdConversationStateRPCResponse) ([]byte, error) {
	return encodeCMDConversationStateRPCResponseBinary(resp)
}

func decodeCMDConversationStateRPCResponse(body []byte) (cmdConversationStateRPCResponse, error) {
	return decodeCMDConversationStateRPCResponseBinary(body)
}

func (r cmdConversationStateRPCRequest) uidForHashSlotFallback() string {
	if r.UID != "" {
		return r.UID
	}
	if len(r.States) > 0 {
		return r.States[0].UID
	}
	if len(r.Patches) > 0 {
		return r.Patches[0].UID
	}
	return ""
}

func (s *Store) groupCMDConversationStatesBySlotAndHashSlot(states []metadb.CMDConversationState) (map[multiraft.SlotID]map[uint16][]metadb.CMDConversationState, error) {
	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.CMDConversationState, len(states))
	for _, state := range states {
		if state.UID == "" {
			return nil, fmt.Errorf("metastore: empty uid in cmd conversation state")
		}
		slotID := s.cluster.SlotForKey(state.UID)
		hashSlot := hashSlotForKey(s.cluster, state.UID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.CMDConversationState)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], state)
	}
	return grouped, nil
}

func (s *Store) groupCMDConversationReadPatchesBySlotAndHashSlot(patches []metadb.CMDConversationReadPatch) (map[multiraft.SlotID]map[uint16][]metadb.CMDConversationReadPatch, error) {
	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.CMDConversationReadPatch, len(patches))
	for _, patch := range patches {
		if patch.UID == "" {
			return nil, fmt.Errorf("metastore: empty uid in cmd conversation read patch")
		}
		slotID := s.cluster.SlotForKey(patch.UID)
		hashSlot := hashSlotForKey(s.cluster, patch.UID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.CMDConversationReadPatch)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], patch)
	}
	return grouped, nil
}
