package proxy

import (
	"context"
	"fmt"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// MembershipReadBatchMaxKeys bounds the UID-owned point-read batch and wire payload cardinality.
const MembershipReadBatchMaxKeys = 200

// GetUserChannelMemberships reads exact keys from the current UID Slot leader.
// Only found rows are returned, once per identity; absence never masks a read error.
// It neither scans the directory nor creates membership or channel runtimes.
func (s *Store) GetUserChannelMemberships(ctx context.Context, uid string, keys []metadb.ChannelKey) ([]metadb.UserChannelMembership, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return []metadb.UserChannelMembership{}, nil
	}
	if err := validateMembershipBatch(uid, keys); err != nil {
		return nil, err
	}
	if s == nil || s.cluster == nil || s.db == nil {
		return nil, fmt.Errorf("metastore: membership batch store not ready")
	}
	slotID := s.cluster.SlotForKey(uid)
	if s.shouldServeSlotLocally(slotID) {
		return s.readMembershipBatchLocal(ctx, uid, keys)
	}
	resp, err := s.callMembershipRPC(ctx, slotID, membershipRPCRequest{Op: membershipRPCGetBatch, SlotID: uint64(slotID), UID: uid, Keys: keys})
	if err != nil {
		return nil, err
	}
	if resp.Status != rpcStatusOK {
		return nil, fmt.Errorf("metastore: unexpected membership batch status %q", resp.Status)
	}
	if err := validateMembershipBatchRows(uid, keys, resp.Memberships); err != nil {
		return nil, err
	}
	return resp.Memberships, nil
}

func validateMembershipBatch(uid string, keys []metadb.ChannelKey) error {
	if uid == "" || len(keys) == 0 || len(keys) > MembershipReadBatchMaxKeys {
		return metadb.ErrInvalidArgument
	}
	for _, key := range keys {
		if key.ChannelID == "" || key.ChannelType <= 0 || key.ChannelType > 255 {
			return metadb.ErrInvalidArgument
		}
	}
	return nil
}

func validateMembershipBatchRows(uid string, keys []metadb.ChannelKey, rows []metadb.UserChannelMembership) error {
	remaining := make(map[metadb.ChannelKey]struct{}, len(keys))
	for _, key := range keys {
		remaining[key] = struct{}{}
	}
	for _, row := range rows {
		key := metadb.ChannelKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
		if _, ok := remaining[key]; !ok || row.UID != uid {
			return fmt.Errorf("metastore: mismatched or duplicate membership batch result")
		}
		delete(remaining, key)
	}
	return nil
}

func (s *Store) readMembershipBatchLocal(ctx context.Context, uid string, keys []metadb.ChannelKey) ([]metadb.UserChannelMembership, error) {
	if err := validateMembershipBatch(uid, keys); err != nil {
		return nil, err
	}
	shard := s.db.MetaDB().HashSlot(metadb.HashSlot(hashSlotForKey(s.cluster, uid)))
	rows := make([]metadb.UserChannelMembership, 0, len(keys))
	seen := make(map[metadb.ChannelKey]struct{}, len(keys))
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		row, found, err := shard.GetUserChannelMembership(ctx, uid, key.ChannelID, key.ChannelType)
		if err != nil {
			return nil, err
		}
		if found {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
