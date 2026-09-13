package cluster

import (
	"context"
	"fmt"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

type messageMembershipBatchNode interface {
	GetUserChannelMemberships(context.Context, string, []metadb.ChannelKey) ([]metadb.UserChannelMembership, error)
}

// GetUserChannelMemberships maps exact UID-owned rows to aligned usecase results.
// Missing rows remain explicit; transport/storage failures never become absence.
func (s *MessageMembershipStore) GetUserChannelMemberships(ctx context.Context, uid string, ids []messageusecase.ChannelID) ([]messageusecase.SyncMembershipReadResult, error) {
	if len(ids) > slotproxy.MembershipReadBatchMaxKeys {
		return nil, metadb.ErrInvalidArgument
	}
	results := make([]messageusecase.SyncMembershipReadResult, len(ids))
	if len(ids) == 0 {
		return results, nil
	}
	if s == nil || s.node == nil {
		return nil, messageusecase.ErrSyncMembershipRequired
	}
	batch, ok := s.node.(messageMembershipBatchNode)
	if !ok {
		for i, id := range ids {
			results[i].Membership, results[i].Found, results[i].Err = s.GetUserChannelMembership(ctx, uid, id.ID, int64(id.Type))
		}
		return results, nil
	}
	keys := make([]metadb.ChannelKey, len(ids))
	positions := make(map[metadb.ChannelKey][]int, len(ids))
	for i, id := range ids {
		keys[i] = metadb.ChannelKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
		positions[keys[i]] = append(positions[keys[i]], i)
	}
	rows, err := batch.GetUserChannelMemberships(ctx, uid, keys)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		key := metadb.ChannelKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
		indexes, ok := positions[key]
		if !ok || row.UID != uid {
			return nil, messageusecase.ErrSyncBatchResultMismatch
		}
		for _, i := range indexes {
			results[i] = messageusecase.SyncMembershipReadResult{Membership: row, Found: true}
		}
		delete(positions, key)
	}
	return results, nil
}

// GetChannelsForMessagePull reuses Slot-grouped raw metadata reads, bypassing
// the SEND permission cache and preserving item-scoped authority failures.
func (s *ChannelMetadataStore) GetChannelsForMessagePull(ctx context.Context, ids []messageusecase.ChannelID) ([]messageusecase.SyncChannelStateReadResult, error) {
	if len(ids) > slotproxy.MembershipReadBatchMaxKeys {
		return nil, metadb.ErrInvalidArgument
	}
	results := make([]messageusecase.SyncChannelStateReadResult, len(ids))
	if len(ids) == 0 {
		return results, nil
	}
	if s == nil || s.node == nil {
		return nil, clusterpkg.ErrRouteNotReady
	}
	batch, ok := s.node.(AuthoritativePermissionBatchNode)
	if !ok {
		for i, id := range ids {
			results[i].Channel, results[i].Err = s.GetChannelForMessagePull(ctx, id.ID, int64(id.Type))
		}
		return results, nil
	}
	reads := make([]slotproxy.PermissionMetadataRead, len(ids))
	for i, id := range ids {
		reads[i] = slotproxy.PermissionMetadataRead{Kind: slotproxy.PermissionMetadataReadChannel, ChannelID: id.ID, ChannelType: int64(id.Type)}
	}
	facts := batch.ReadPermissionMetadataBatchAuthoritative(ctx, reads)
	if len(facts) != len(ids) {
		return nil, messageusecase.ErrSyncBatchResultMismatch
	}
	for i, fact := range facts {
		results[i] = messageusecase.SyncChannelStateReadResult{Channel: fact.Channel, Err: fact.Err}
		if fact.Err == nil {
			if !fact.Found {
				results[i].Err = metadb.ErrNotFound
			} else if fact.Channel.ChannelID != ids[i].ID || fact.Channel.ChannelType != int64(ids[i].Type) {
				return nil, fmt.Errorf("channel metadata: mismatched batch identity")
			}
		}
	}
	return results, nil
}

var _ messageusecase.SyncMembershipBatchStore = (*MessageMembershipStore)(nil)
var _ messageusecase.SyncChannelStateBatchStore = (*ChannelMetadataStore)(nil)
