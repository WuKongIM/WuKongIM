package proxy

import (
	"context"
	"encoding/json"
	"fmt"

	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const channelUpdateLogRPCServiceID uint8 = 12

const (
	channelUpdateLogRPCBatchGet = "batch_get"
	channelUpdateLogRPCUpsert   = "upsert"
	channelUpdateLogRPCDelete   = "delete"
)

type channelUpdateLogRPCRequest struct {
	Op       string                    `json:"op"`
	SlotID   uint64                    `json:"slot_id"`
	HashSlot uint16                    `json:"hash_slot,omitempty"`
	Keys     []metadb.ConversationKey  `json:"keys,omitempty"`
	Entries  []metadb.ChannelUpdateLog `json:"entries,omitempty"`
}

type channelUpdateLogRPCResponse struct {
	Status   string                    `json:"status"`
	LeaderID uint64                    `json:"leader_id,omitempty"`
	Entries  []metadb.ChannelUpdateLog `json:"entries,omitempty"`
}

func (r channelUpdateLogRPCResponse) rpcStatus() string {
	return r.Status
}

func (r channelUpdateLogRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (s *Store) BatchGetChannelUpdateLogs(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error) {
	if len(keys) == 0 {
		return map[metadb.ConversationKey]metadb.ChannelUpdateLog{}, nil
	}

	grouped := make(map[multiraft.SlotID][]metadb.ConversationKey, len(keys))
	for _, key := range keys {
		slotID := s.cluster.SlotForKey(key.ChannelID)
		grouped[slotID] = append(grouped[slotID], key)
	}

	entries := make(map[metadb.ConversationKey]metadb.ChannelUpdateLog, len(keys))
	for slotID, groupKeys := range grouped {
		groupEntries, err := s.batchGetChannelUpdateLogsAuthoritative(ctx, slotID, groupKeys)
		if err != nil {
			return nil, err
		}
		for key, entry := range groupEntries {
			entries[key] = entry
		}
	}
	return entries, nil
}

func (s *Store) UpsertChannelUpdateLogs(ctx context.Context, entries []metadb.ChannelUpdateLog) error {
	if len(entries) == 0 {
		return nil
	}

	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.ChannelUpdateLog, len(entries))
	for _, entry := range entries {
		slotID := s.cluster.SlotForKey(entry.ChannelID)
		hashSlot := hashSlotForKey(s.cluster, entry.ChannelID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.ChannelUpdateLog)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], entry)
	}

	for slotID, groupByHashSlot := range grouped {
		for hashSlot, groupEntries := range groupByHashSlot {
			if s.shouldServeSlotLocally(slotID) {
				cmd := metafsm.EncodeUpsertChannelUpdateLogsCommand(groupEntries)
				if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callChannelUpdateLogRPC(ctx, slotID, channelUpdateLogRPCRequest{
				Op:       channelUpdateLogRPCUpsert,
				SlotID:   uint64(slotID),
				HashSlot: hashSlot,
				Entries:  groupEntries,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) DeleteChannelUpdateLogs(ctx context.Context, keys []metadb.ConversationKey) error {
	if len(keys) == 0 {
		return nil
	}

	grouped := make(map[multiraft.SlotID]map[uint16][]metadb.ConversationKey, len(keys))
	for _, key := range keys {
		slotID := s.cluster.SlotForKey(key.ChannelID)
		hashSlot := hashSlotForKey(s.cluster, key.ChannelID)
		if grouped[slotID] == nil {
			grouped[slotID] = make(map[uint16][]metadb.ConversationKey)
		}
		grouped[slotID][hashSlot] = append(grouped[slotID][hashSlot], key)
	}

	for slotID, groupByHashSlot := range grouped {
		for hashSlot, groupKeys := range groupByHashSlot {
			if s.shouldServeSlotLocally(slotID) {
				cmd := metafsm.EncodeDeleteChannelUpdateLogsCommand(groupKeys)
				if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd); err != nil {
					return err
				}
				continue
			}
			if _, err := s.callChannelUpdateLogRPC(ctx, slotID, channelUpdateLogRPCRequest{
				Op:       channelUpdateLogRPCDelete,
				SlotID:   uint64(slotID),
				HashSlot: hashSlot,
				Keys:     groupKeys,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) batchGetChannelUpdateLogsAuthoritative(ctx context.Context, slotID multiraft.SlotID, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error) {
	if s.shouldServeSlotLocally(slotID) {
		return s.batchGetChannelUpdateLogsLocal(ctx, keys)
	}

	resp, err := s.callChannelUpdateLogRPC(ctx, slotID, channelUpdateLogRPCRequest{
		Op:     channelUpdateLogRPCBatchGet,
		SlotID: uint64(slotID),
		Keys:   keys,
	})
	if err != nil {
		return nil, err
	}

	entries := make(map[metadb.ConversationKey]metadb.ChannelUpdateLog, len(resp.Entries))
	for _, entry := range resp.Entries {
		key := metadb.ConversationKey{ChannelID: entry.ChannelID, ChannelType: entry.ChannelType}
		entries[key] = entry
	}
	return entries, nil
}

func (s *Store) callChannelUpdateLogRPC(ctx context.Context, slotID multiraft.SlotID, req channelUpdateLogRPCRequest) (channelUpdateLogRPCResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return channelUpdateLogRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, channelUpdateLogRPCServiceID, payload, decodeChannelUpdateLogRPCResponse)
}

func (s *Store) handleChannelUpdateLogRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req channelUpdateLogRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeChannelUpdateLogRPCResponse(channelUpdateLogRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	switch req.Op {
	case channelUpdateLogRPCBatchGet:
		entriesByKey, err := s.batchGetChannelUpdateLogsLocal(ctx, req.Keys)
		if err != nil {
			return nil, err
		}
		entries := make([]metadb.ChannelUpdateLog, 0, len(entriesByKey))
		for _, entry := range entriesByKey {
			entries = append(entries, entry)
		}
		return encodeChannelUpdateLogRPCResponse(channelUpdateLogRPCResponse{
			Status:  rpcStatusOK,
			Entries: entries,
		})
	case channelUpdateLogRPCUpsert:
		cmd := metafsm.EncodeUpsertChannelUpdateLogsCommand(req.Entries)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, req.HashSlot, cmd); err != nil {
			return nil, err
		}
		return encodeChannelUpdateLogRPCResponse(channelUpdateLogRPCResponse{Status: rpcStatusOK})
	case channelUpdateLogRPCDelete:
		cmd := metafsm.EncodeDeleteChannelUpdateLogsCommand(req.Keys)
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, req.HashSlot, cmd); err != nil {
			return nil, err
		}
		return encodeChannelUpdateLogRPCResponse(channelUpdateLogRPCResponse{Status: rpcStatusOK})
	default:
		return nil, fmt.Errorf("metastore: unknown channel update log rpc op %q", req.Op)
	}
}

func encodeChannelUpdateLogRPCResponse(resp channelUpdateLogRPCResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeChannelUpdateLogRPCResponse(body []byte) (channelUpdateLogRPCResponse, error) {
	var resp channelUpdateLogRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return channelUpdateLogRPCResponse{}, err
	}
	return resp, nil
}

func (s *Store) batchGetChannelUpdateLogsLocal(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error) {
	entries := make(map[metadb.ConversationKey]metadb.ChannelUpdateLog, len(keys))
	missing := make([]metadb.ConversationKey, 0, len(keys))

	if s.channelUpdateOverlay != nil {
		hotEntries, err := s.channelUpdateOverlay.BatchGetHotChannelUpdates(ctx, keys)
		if err == nil {
			for key, entry := range hotEntries {
				entries[key] = entry
			}
		}
	}

	for _, key := range keys {
		if _, ok := entries[key]; ok {
			continue
		}
		missing = append(missing, key)
	}
	if len(missing) == 0 {
		return entries, nil
	}

	for _, key := range missing {
		hashSlot := hashSlotForKey(s.cluster, key.ChannelID)
		coldEntries, err := s.db.ForHashSlot(hashSlot).BatchGetChannelUpdateLogs(ctx, []metadb.ConversationKey{key})
		if err != nil {
			return nil, err
		}
		for key, entry := range coldEntries {
			entries[key] = entry
		}
	}
	return entries, nil
}
