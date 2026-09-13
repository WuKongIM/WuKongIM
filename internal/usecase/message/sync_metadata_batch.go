package message

import (
	"context"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// prepareSyncBatch uses request-scoped authority batches when both fact ports
// support them. It preserves input-order failures and never starts message reads
// until every fact read joins. Optional point-only adapters retain bounded reads.
func (a *App) prepareSyncBatch(ctx context.Context, uid string, items []SyncChannelMessagesQuery, prepared []preparedSyncChannelMessages) []error {
	errs := make([]error, len(items))
	memberships, batchMemberships := a.memberships.(SyncMembershipBatchStore)
	channels, batchChannels := a.channelState.(SyncChannelStateBatchStore)
	if !batchMemberships || (a.channelState != nil && !batchChannels) {
		runMessageBatchWorkers(goruntimeregistry.TaskMessagePermissionBatch, len(items), maxSyncPermissionWorkers, func(i int) {
			if err := ctx.Err(); err != nil {
				errs[i] = err
				return
			}
			item := items[i]
			item.LoginUID = uid
			prepared[i], errs[i] = a.prepareSyncChannelMessages(ctx, item)
		})
		return errs
	}
	normalized := make([]SyncChannelMessagesQuery, len(items))
	keys := make([]ChannelID, 0, len(items))
	indexes := make([]int, 0, len(items))
	for i, item := range items {
		item.LoginUID = uid
		normalized[i], errs[i] = a.normalizeSyncChannelMessages(item)
		if errs[i] == nil {
			keys = append(keys, ChannelID{ID: normalized[i].ChannelID, Type: item.ChannelType})
			indexes = append(indexes, i)
		}
	}
	if len(keys) == 0 {
		return errs
	}
	facts, err := memberships.GetUserChannelMemberships(ctx, uid, keys)
	if err == nil && len(facts) != len(keys) {
		err = ErrSyncBatchResultMismatch
	}
	if err != nil {
		for _, i := range indexes {
			errs[i] = err
		}
		return errs
	}
	channelKeys := make([]ChannelID, 0, len(keys))
	channelIndexes := make([]int, 0, len(keys))
	for j, i := range indexes {
		if facts[j].Err != nil {
			errs[i] = facts[j].Err
			continue
		}
		prepared[i], errs[i] = prepareSyncMembership(normalized[i], facts[j].Membership, facts[j].Found)
		if errs[i] == nil && !prepared[i].empty {
			channelKeys = append(channelKeys, keys[j])
			channelIndexes = append(channelIndexes, i)
		}
	}
	if a.channelState == nil || len(channelKeys) == 0 {
		return errs
	}
	states, err := channels.GetChannelsForMessagePull(ctx, channelKeys)
	if err == nil && len(states) != len(channelKeys) {
		err = ErrSyncBatchResultMismatch
	}
	for j, i := range channelIndexes {
		if err != nil {
			errs[i] = err
		} else {
			errs[i] = validateSyncChannelState(states[j].Channel, states[j].Err)
		}
	}
	return errs
}
