package proxy

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const subscriberRPCServiceID uint8 = 10

type subscriberRPCRequest struct {
	SlotID      uint64 `json:"slot_id"`
	HashSlot    uint16 `json:"hash_slot,omitempty"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	Snapshot    bool   `json:"snapshot,omitempty"`
	AfterUID    string `json:"after_uid,omitempty"`
	Limit       int    `json:"limit"`
	ContainsUID string `json:"contains_uid,omitempty"`
	HasAny      bool   `json:"has_any,omitempty"`
}

type subscriberRPCResponse struct {
	Status     string   `json:"status"`
	LeaderID   uint64   `json:"leader_id,omitempty"`
	UIDs       []string `json:"uids,omitempty"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Done       bool     `json:"done"`
	Contains   bool     `json:"contains,omitempty"`
	HasAny     bool     `json:"has_any,omitempty"`
}

func (r subscriberRPCResponse) rpcStatus() string {
	return r.Status
}

func (r subscriberRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (s *Store) listChannelSubscribersAuthoritative(ctx context.Context, slotID multiraft.SlotID, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	hashSlot := hashSlotForKey(s.cluster, channelID)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).ListSubscribersPage(ctx, channelID, channelType, afterUID, limit)
	}

	resp, err := s.callSubscriberRPC(ctx, slotID, subscriberRPCRequest{
		SlotID:      uint64(slotID),
		HashSlot:    hashSlot,
		ChannelID:   channelID,
		ChannelType: channelType,
		AfterUID:    afterUID,
		Limit:       limit,
	})
	if err != nil {
		return nil, "", false, err
	}
	return append([]string(nil), resp.UIDs...), resp.NextCursor, resp.Done, nil
}

func (s *Store) SnapshotChannelSubscribers(ctx context.Context, channelID string, channelType int64) ([]string, error) {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).ListSubscribersSnapshot(ctx, channelID, channelType)
	}

	resp, err := s.callSubscriberRPC(ctx, slotID, subscriberRPCRequest{
		SlotID:      uint64(slotID),
		HashSlot:    hashSlot,
		ChannelID:   channelID,
		ChannelType: channelType,
		Snapshot:    true,
	})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), resp.UIDs...), nil
}

// ContainsChannelSubscriber reads a subscriber point lookup from the authoritative slot owner.
func (s *Store) ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	slotID := s.cluster.SlotForKey(channelID)
	return s.containsChannelSubscriberAuthoritative(ctx, slotID, channelID, channelType, uid)
}

// HasChannelSubscribers reads subscriber-set non-emptiness from the authoritative slot owner.
func (s *Store) HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	slotID := s.cluster.SlotForKey(channelID)
	return s.hasChannelSubscribersAuthoritative(ctx, slotID, channelID, channelType)
}

func (s *Store) containsChannelSubscriberAuthoritative(ctx context.Context, slotID multiraft.SlotID, channelID string, channelType int64, uid string) (bool, error) {
	hashSlot := hashSlotForKey(s.cluster, channelID)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).ContainsSubscriber(ctx, channelID, channelType, uid)
	}

	resp, err := s.callSubscriberRPC(ctx, slotID, subscriberRPCRequest{
		SlotID:      uint64(slotID),
		HashSlot:    hashSlot,
		ChannelID:   channelID,
		ChannelType: channelType,
		ContainsUID: uid,
	})
	if err != nil {
		return false, err
	}
	return resp.Contains, nil
}

func (s *Store) hasChannelSubscribersAuthoritative(ctx context.Context, slotID multiraft.SlotID, channelID string, channelType int64) (bool, error) {
	hashSlot := hashSlotForKey(s.cluster, channelID)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).HasSubscribers(ctx, channelID, channelType)
	}

	resp, err := s.callSubscriberRPC(ctx, slotID, subscriberRPCRequest{
		SlotID:      uint64(slotID),
		HashSlot:    hashSlot,
		ChannelID:   channelID,
		ChannelType: channelType,
		HasAny:      true,
	})
	if err != nil {
		return false, err
	}
	return resp.HasAny, nil
}

func (s *Store) callSubscriberRPC(ctx context.Context, slotID multiraft.SlotID, req subscriberRPCRequest) (subscriberRPCResponse, error) {
	payload, err := encodeSubscriberRPCRequestBinary(req)
	if err != nil {
		return subscriberRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, subscriberRPCServiceID, payload, decodeSubscriberRPCResponse)
}

func (s *Store) handleSubscriberRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeSubscriberRPCRequest(body)
	if err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeSubscriberRPCResponse(subscriberRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	hashSlot := req.HashSlot
	if hashSlot == 0 {
		hashSlot = hashSlotForKey(s.cluster, req.ChannelID)
	}
	if req.ContainsUID != "" {
		ok, err := s.db.ForHashSlot(hashSlot).ContainsSubscriber(ctx, req.ChannelID, req.ChannelType, req.ContainsUID)
		if err != nil {
			return nil, err
		}
		return encodeSubscriberRPCResponse(subscriberRPCResponse{
			Status:   rpcStatusOK,
			Contains: ok,
		})
	}
	if req.HasAny {
		ok, err := s.db.ForHashSlot(hashSlot).HasSubscribers(ctx, req.ChannelID, req.ChannelType)
		if err != nil {
			return nil, err
		}
		return encodeSubscriberRPCResponse(subscriberRPCResponse{
			Status: rpcStatusOK,
			HasAny: ok,
		})
	}
	if req.Snapshot {
		uids, err := s.db.ForHashSlot(hashSlot).ListSubscribersSnapshot(ctx, req.ChannelID, req.ChannelType)
		if err != nil {
			return nil, err
		}
		return encodeSubscriberRPCResponse(subscriberRPCResponse{
			Status: rpcStatusOK,
			UIDs:   uids,
			Done:   true,
		})
	}

	uids, nextCursor, done, err := s.db.ForHashSlot(hashSlot).ListSubscribersPage(ctx, req.ChannelID, req.ChannelType, req.AfterUID, req.Limit)
	if err != nil {
		return nil, err
	}
	return encodeSubscriberRPCResponse(subscriberRPCResponse{
		Status:     rpcStatusOK,
		UIDs:       uids,
		NextCursor: nextCursor,
		Done:       done,
	})
}

func encodeSubscriberRPCResponse(resp subscriberRPCResponse) ([]byte, error) {
	return encodeSubscriberRPCResponseBinary(resp)
}

func decodeSubscriberRPCResponse(body []byte) (subscriberRPCResponse, error) {
	return decodeSubscriberRPCResponseBinary(body)
}
