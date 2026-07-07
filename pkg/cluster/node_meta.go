package cluster

import (
	"context"
	"errors"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

const (
	maxChannelLatestBatchItems = 512
	maxConversationBatchItems  = 512
)

// CreateUserMetadata persists durable UID metadata through Slot ownership.
func (n *Node) CreateUserMetadata(ctx context.Context, user metadb.User) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     user.UID,
		Command: metafsm.EncodeCreateUserCommand(user),
	})
}

// GetUserMetadata reads durable UID metadata from the current Slot route.
func (n *Node) GetUserMetadata(ctx context.Context, uid string) (metadb.User, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.User{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.User{}, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.User{}, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return metadb.User{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetUser(ctx, uid)
}

// UpsertDeviceMetadata persists durable per-device token metadata through Slot ownership.
func (n *Node) UpsertDeviceMetadata(ctx context.Context, device metadb.Device) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     device.UID,
		Command: metafsm.EncodeUpsertDeviceCommand(device),
	})
}

// GetDeviceMetadata reads durable per-device token metadata from the current Slot route.
func (n *Node) GetDeviceMetadata(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.Device{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.Device{}, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.Device{}, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return metadb.Device{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetDevice(ctx, uid, deviceFlag)
}

// BindPluginUser persists one UID-owned plugin binding through Slot ownership.
func (n *Node) BindPluginUser(ctx context.Context, binding metadb.PluginUserBinding) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     binding.UID,
		Command: metafsm.EncodeBindPluginUserCommand(binding),
	})
}

// UnbindPluginUser removes one UID-owned plugin binding through Slot ownership.
func (n *Node) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     uid,
		Command: metafsm.EncodeUnbindPluginUserCommand(uid, pluginNo),
	})
}

// ListPluginBindingsByUID reads durable plugin bindings from the UID-owned Slot metadata.
func (n *Node) ListPluginBindingsByUID(ctx context.Context, uid string) ([]metadb.PluginUserBinding, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return nil, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListPluginBindingsByUID(ctx, uid)
}

// UpsertChannelMetadata persists durable channel metadata through Slot ownership.
func (n *Node) UpsertChannelMetadata(ctx context.Context, channel metadb.Channel) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     channel.ChannelID,
		Command: metafsm.EncodeUpsertChannelCommand(channel),
	})
}

// GetChannelMetadata reads durable channel metadata from the current Slot route.
func (n *Node) GetChannelMetadata(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.Channel{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.Channel{}, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.Channel{}, ErrNotStarted
	}
	route, err := n.RouteKey(channelID)
	if err != nil {
		return metadb.Channel{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannel(ctx, channelID, channelType)
}

// GetChannelRuntimeMeta reads authoritative channel runtime metadata from the current Slot route.
func (n *Node) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.ChannelRuntimeMeta{}, ErrNotStarted
	}
	route, err := n.RouteKey(channelID)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelRuntimeMeta(ctx, channelID, channelType)
}

// AdvanceChannelRetentionThroughSeq persists a fenced channel message compaction boundary through Slot ownership.
func (n *Node) AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     req.ChannelID,
		Command: metafsm.EncodeAdvanceChannelRetentionThroughSeqCommand(req),
	})
}

// DeleteChannelMetadata removes durable channel metadata through Slot ownership.
func (n *Node) DeleteChannelMetadata(ctx context.Context, channelID string, channelType int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     channelID,
		Command: metafsm.EncodeDeleteChannelCommand(channelID, channelType),
	})
}

// AddChannelSubscribers appends durable channel subscribers through Slot ownership.
func (n *Node) AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion uint64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	command, err := metafsm.EncodeAddSubscribersCommandChecked(channelID, channelType, uids, subscriberMutationVersion)
	if err != nil {
		return err
	}
	return n.Propose(ctx, ProposeRequest{Key: channelID, Command: command})
}

// RemoveChannelSubscribers removes durable channel subscribers through Slot ownership.
func (n *Node) RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion uint64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	command, err := metafsm.EncodeRemoveSubscribersCommandChecked(channelID, channelType, uids, subscriberMutationVersion)
	if err != nil {
		return err
	}
	return n.Propose(ctx, ProposeRequest{Key: channelID, Command: command})
}

// ListChannelSubscribersPage reads durable channel subscribers from Slot metadata storage.
func (n *Node) ListChannelSubscribersPage(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, "", false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, "", false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, "", false, ErrNotStarted
	}
	route, err := n.RouteKey(channelID)
	if err != nil {
		return nil, "", false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListSubscribersPage(ctx, channelID, channelType, afterUID, limit)
}

// ContainsChannelSubscriber reads one durable subscriber membership from Slot metadata storage.
func (n *Node) ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	if err := ctxErr(ctx); err != nil {
		return false, err
	}
	if err := n.ensureForeground(); err != nil {
		return false, err
	}
	if n.defaultSlotMetaDB == nil {
		return false, ErrNotStarted
	}
	route, err := n.RouteKey(channelID)
	if err != nil {
		return false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ContainsSubscriber(ctx, channelID, channelType, uid)
}

// HasChannelSubscribers reports whether durable subscriber metadata has any row for the channel.
func (n *Node) HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	if err := ctxErr(ctx); err != nil {
		return false, err
	}
	if err := n.ensureForeground(); err != nil {
		return false, err
	}
	if n.defaultSlotMetaDB == nil {
		return false, ErrNotStarted
	}
	route, err := n.RouteKey(channelID)
	if err != nil {
		return false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).HasSubscribers(ctx, channelID, channelType)
}

// UpsertChannelLatest persists a channel-owned latest message projection.
func (n *Node) UpsertChannelLatest(ctx context.Context, latest metadb.ChannelLatest) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	command, err := metafsm.EncodeUpsertChannelLatestCommandChecked(latest)
	if err != nil {
		return err
	}
	return n.Propose(ctx, ProposeRequest{Key: latest.ChannelID, Command: command})
}

// UpsertChannelLatestBatch persists channel-owned latest message projections grouped by physical Slot.
func (n *Node) UpsertChannelLatestBatch(ctx context.Context, latestRows []metadb.ChannelLatest) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	if len(latestRows) == 0 {
		return nil
	}
	groups, err := n.groupChannelLatestBySlot(latestRows)
	if err != nil {
		return err
	}
	for _, slotID := range sortedChannelLatestSlotIDs(groups) {
		group := groups[slotID]
		for start := 0; start < len(group.items); start += maxChannelLatestBatchItems {
			end := start + maxChannelLatestBatchItems
			if end > len(group.items) {
				end = len(group.items)
			}
			items := group.items[start:end]
			command, err := metafsm.EncodeUpsertChannelLatestBatchCommandChecked(items)
			if err != nil {
				return err
			}
			routeHashSlot := group.routeHashSlot
			if len(items) > 0 {
				routeHashSlot = items[0].HashSlot
			}
			if err := n.Propose(ctx, ProposeRequest{
				Command: command,
				Target: ProposeTarget{
					SlotID:      slotID,
					HasSlotID:   true,
					HashSlot:    routeHashSlot,
					HasHashSlot: true,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetChannelLatest reads the latest message projection from the current channel route.
func (n *Node) GetChannelLatest(ctx context.Context, channelID string, channelType int64) (metadb.ChannelLatest, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.ChannelLatest{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.ChannelLatest{}, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.ChannelLatest{}, ErrNotStarted
	}
	route, err := n.RouteKey(channelID)
	if err != nil {
		return metadb.ChannelLatest{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelLatest(ctx, channelID, channelType)
}

// GetChannelLatestBatch reads existing latest message projections for channel keys.
func (n *Node) GetChannelLatestBatch(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelLatest, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return map[metadb.ConversationKey]metadb.ChannelLatest{}, nil
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	out := make(map[metadb.ConversationKey]metadb.ChannelLatest, len(keys))
	seen := make(map[metadb.ConversationKey]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		route, err := n.RouteKey(key.ChannelID)
		if err != nil {
			return nil, err
		}
		latest, err := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelLatest(ctx, key.ChannelID, key.ChannelType)
		if errors.Is(err, metadb.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[key] = latest
	}
	return out, nil
}

// AppendMessageEvent persists one channel-owned message event projection and returns the reducer result.
func (n *Node) AppendMessageEvent(ctx context.Context, event metadb.MessageEventAppend) (metadb.MessageEventAppendResult, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	if n == nil {
		return metadb.MessageEventAppendResult{}, ErrNotStarted
	}
	event, err := normalizeClusterMessageEventAppend(event)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	route, err := n.RouteKey(event.ChannelID)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	if route.Leader == 0 {
		return metadb.MessageEventAppendResult{}, ErrNoSlotLeader
	}
	if route.Leader != n.cfg.NodeID {
		return n.forwardMessageEventAppend(ctx, route.Leader, event)
	}
	return n.appendMessageEventLocal(ctx, event)
}

// GetMessageEventStatesBatch reads projected event lanes for message keys through each channel route.
func (n *Node) GetMessageEventStatesBatch(ctx context.Context, keys []metadb.MessageEventMessageKey, limit int) (map[metadb.MessageEventMessageKey][]metadb.MessageEventState, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return map[metadb.MessageEventMessageKey][]metadb.MessageEventState{}, nil
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	groups := make(map[uint64][]metadb.MessageEventMessageKey)
	seen := make(map[metadb.MessageEventMessageKey]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		route, err := n.RouteKey(key.ChannelID)
		if err != nil {
			return nil, err
		}
		if route.Leader == 0 {
			return nil, ErrNoSlotLeader
		}
		groups[route.Leader] = append(groups[route.Leader], key)
	}
	out := make(map[metadb.MessageEventMessageKey][]metadb.MessageEventState, len(keys))
	for leader, group := range groups {
		var (
			rows map[metadb.MessageEventMessageKey][]metadb.MessageEventState
			err  error
		)
		if leader == n.cfg.NodeID {
			rows, err = n.getMessageEventStatesBatchLocal(ctx, group, limit)
		} else {
			rows, err = n.forwardMessageEventStatesBatch(ctx, leader, group, limit)
		}
		if err != nil {
			return nil, err
		}
		for key, states := range rows {
			if len(states) > 0 {
				out[key] = states
			}
		}
	}
	return out, nil
}

func (n *Node) getMessageEventStatesBatchLocal(ctx context.Context, keys []metadb.MessageEventMessageKey, limit int) (map[metadb.MessageEventMessageKey][]metadb.MessageEventState, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return map[metadb.MessageEventMessageKey][]metadb.MessageEventState{}, nil
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	out := make(map[metadb.MessageEventMessageKey][]metadb.MessageEventState, len(keys))
	seen := make(map[metadb.MessageEventMessageKey]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		route, err := n.RouteKey(key.ChannelID)
		if err != nil {
			return nil, err
		}
		if route.Leader != n.cfg.NodeID {
			return nil, ErrNotLeader
		}
		states, err := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, key.ChannelID, key.ChannelType, key.ClientMsgNo, limit)
		if err != nil {
			if errors.Is(err, metadb.ErrNotFound) {
				states = nil
			} else {
				return nil, err
			}
		}
		states = mergeMessageEventStateOverlay(states, n.messageEventStreamCache.states(key), limit)
		if len(states) > 0 {
			out[key] = states
		}
	}
	return out, nil
}

func mergeMessageEventStateOverlay(durable []metadb.MessageEventState, cached []metadb.MessageEventState, limit int) []metadb.MessageEventState {
	if len(cached) == 0 {
		if limit > 0 && len(durable) > limit {
			return durable[:limit]
		}
		return durable
	}
	merged := make(map[string]metadb.MessageEventState, len(durable)+len(cached))
	for _, state := range durable {
		merged[state.EventKey] = state
	}
	for _, state := range cached {
		existing, ok := merged[state.EventKey]
		if !ok || state.Status == metadb.EventStatusOpen || state.LastMsgEventSeq >= existing.LastMsgEventSeq {
			merged[state.EventKey] = state
		}
	}
	out := make([]metadb.MessageEventState, 0, len(merged))
	for _, state := range merged {
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EventKey < out[j].EventKey })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// UpsertUserChannelMemberships persists UID-owned channel memberships through hash-slot ownership.
func (n *Node) UpsertUserChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	groups, err := n.groupUserChannelMembershipsByHashSlot(channelID, channelType, uids, joinSeq, updatedAt)
	if err != nil {
		return err
	}
	for _, hashSlot := range sortedMembershipHashSlots(groups) {
		command, err := metafsm.EncodeUpsertUserChannelMembershipsCommandChecked(groups[hashSlot])
		if err != nil {
			return err
		}
		if err := n.Propose(ctx, ProposeRequest{
			Command: command,
			Target:  ProposeTarget{HashSlot: hashSlot, HasHashSlot: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

// DeleteUserChannelMemberships removes UID-owned channel memberships through hash-slot ownership.
func (n *Node) DeleteUserChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, updatedAt int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	groups, err := n.groupUserChannelMembershipsByHashSlot(channelID, channelType, uids, 0, updatedAt)
	if err != nil {
		return err
	}
	for _, hashSlot := range sortedMembershipHashSlots(groups) {
		command, err := metafsm.EncodeDeleteUserChannelMembershipsCommandChecked(groups[hashSlot])
		if err != nil {
			return err
		}
		if err := n.Propose(ctx, ProposeRequest{
			Command: command,
			Target:  ProposeTarget{HashSlot: hashSlot, HasHashSlot: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

// ListUserChannelMembershipPage reads UID-owned memberships from Slot metadata storage.
func (n *Node) ListUserChannelMembershipPage(ctx context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListUserChannelMembershipPage(ctx, uid, after, limit)
}

// UpsertConversationStatesBatch persists UID-owned conversation states through Slot ownership.
func (n *Node) UpsertConversationStatesBatch(ctx context.Context, states []metadb.ConversationState) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	if len(states) == 0 {
		return nil
	}
	groups, err := n.groupConversationStatesBySlot(states)
	if err != nil {
		return err
	}
	for _, slotID := range sortedConversationSlotIDs(groups) {
		group := groups[slotID]
		for start := 0; start < len(group.stateItems); start += maxConversationBatchItems {
			end := start + maxConversationBatchItems
			if end > len(group.stateItems) {
				end = len(group.stateItems)
			}
			items := group.stateItems[start:end]
			command, err := metafsm.EncodeUpsertConversationStateBatchCommandChecked(n.cfg.Slots.HashSlotCount, items)
			if err != nil {
				return err
			}
			routeHashSlot := group.routeHashSlot
			if len(items) > 0 {
				routeHashSlot = items[0].HashSlot
			}
			if err := n.Propose(ctx, ProposeRequest{
				Command: command,
				Target: ProposeTarget{
					SlotID:      slotID,
					HasSlotID:   true,
					HashSlot:    routeHashSlot,
					HasHashSlot: true,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// HideConversationsBatch persists UID-owned conversation delete barriers through Slot ownership.
func (n *Node) HideConversationsBatch(ctx context.Context, deletes []metadb.ConversationDelete) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	if len(deletes) == 0 {
		return nil
	}
	groups, err := n.groupConversationDeletesBySlot(deletes)
	if err != nil {
		return err
	}
	for _, slotID := range sortedConversationSlotIDs(groups) {
		group := groups[slotID]
		for start := 0; start < len(group.deleteItems); start += maxConversationBatchItems {
			end := start + maxConversationBatchItems
			if end > len(group.deleteItems) {
				end = len(group.deleteItems)
			}
			items := group.deleteItems[start:end]
			command, err := metafsm.EncodeHideConversationBatchCommandChecked(n.cfg.Slots.HashSlotCount, items)
			if err != nil {
				return err
			}
			routeHashSlot := group.routeHashSlot
			if len(items) > 0 {
				routeHashSlot = items[0].HashSlot
			}
			if err := n.Propose(ctx, ProposeRequest{
				Command: command,
				Target: ProposeTarget{
					SlotID:      slotID,
					HasSlotID:   true,
					HashSlot:    routeHashSlot,
					HasHashSlot: true,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// TouchConversationActiveAtBatch persists UID-owned active-at patches through Slot ownership.
func (n *Node) TouchConversationActiveAtBatch(ctx context.Context, patches []metadb.ConversationActivePatch) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	ctx = propose.WithProposalClass(ctx, propose.ProposalClassBackground)
	if n == nil {
		return ErrNotStarted
	}
	if len(patches) == 0 {
		return nil
	}
	groups, err := n.groupConversationPatchesBySlot(patches)
	if err != nil {
		return err
	}
	for _, slotID := range sortedConversationSlotIDs(groups) {
		group := groups[slotID]
		for start := 0; start < len(group.patchItems); start += maxConversationBatchItems {
			end := start + maxConversationBatchItems
			if end > len(group.patchItems) {
				end = len(group.patchItems)
			}
			items := group.patchItems[start:end]
			command, err := metafsm.EncodeTouchConversationActiveAtBatchCommandChecked(n.cfg.Slots.HashSlotCount, items)
			if err != nil {
				return err
			}
			routeHashSlot := group.routeHashSlot
			if len(items) > 0 {
				routeHashSlot = items[0].HashSlot
			}
			if err := n.Propose(ctx, ProposeRequest{
				Command: command,
				Target: ProposeTarget{
					SlotID:      slotID,
					HasSlotID:   true,
					HashSlot:    routeHashSlot,
					HasHashSlot: true,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetConversationState reads one UID-owned conversation row from Slot metadata storage.
func (n *Node) GetConversationState(ctx context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.ConversationState{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.ConversationState{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.ConversationState{}, false, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return metadb.ConversationState{}, false, err
	}
	state, err := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetConversationState(ctx, kind, uid, channelID, channelType)
	if err != nil {
		if errors.Is(err, metadb.ErrNotFound) {
			return metadb.ConversationState{}, false, nil
		}
		return metadb.ConversationState{}, false, err
	}
	return state, true, nil
}

// GetConversationStates reads UID-owned conversation rows from Slot metadata storage.
func (n *Node) GetConversationStates(ctx context.Context, keys []metadb.ConversationStateKey) (map[metadb.ConversationStateKey]metadb.ConversationState, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	states := make(map[metadb.ConversationStateKey]metadb.ConversationState, len(keys))
	for _, key := range keys {
		route, err := n.RouteKey(key.UID)
		if err != nil {
			return nil, err
		}
		state, err := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetConversationState(ctx, key.Kind, key.UID, key.ChannelID, key.ChannelType)
		if err != nil {
			if errors.Is(err, metadb.ErrNotFound) {
				continue
			}
			return nil, err
		}
		states[key] = state
	}
	return states, nil
}

// ListConversationActivePage reads UID-owned active conversation rows from Slot metadata storage.
func (n *Node) ListConversationActivePage(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, metadb.ConversationActiveCursor{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, metadb.ConversationActiveCursor{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, metadb.ConversationActiveCursor{}, false, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return nil, metadb.ConversationActiveCursor{}, false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListConversationActivePage(ctx, kind, uid, after, limit)
}

type channelLatestSlotBatch struct {
	routeHashSlot uint16
	items         []metafsm.ChannelLatestBatchItem
}

type conversationSlotBatch struct {
	routeHashSlot uint16
	stateItems    []metafsm.ConversationStateBatchItem
	patchItems    []metafsm.ConversationActivePatchBatchItem
	deleteItems   []metafsm.ConversationDeleteBatchItem
}

func (n *Node) groupChannelLatestBySlot(latestRows []metadb.ChannelLatest) (map[uint32]channelLatestSlotBatch, error) {
	groups := make(map[uint32]channelLatestSlotBatch)
	for _, latest := range latestRows {
		if latest.ChannelID == "" || latest.ChannelType == 0 {
			return nil, metadb.ErrInvalidArgument
		}
		route, err := n.RouteKey(latest.ChannelID)
		if err != nil {
			return nil, err
		}
		group := groups[route.SlotID]
		if len(group.items) == 0 {
			group.routeHashSlot = route.HashSlot
		}
		group.items = append(group.items, metafsm.ChannelLatestBatchItem{
			HashSlot: route.HashSlot,
			Latest:   latest,
		})
		groups[route.SlotID] = group
	}
	return groups, nil
}

func sortedChannelLatestSlotIDs(groups map[uint32]channelLatestSlotBatch) []uint32 {
	slotIDs := make([]uint32, 0, len(groups))
	for slotID := range groups {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	return slotIDs
}

func (n *Node) groupConversationStatesBySlot(states []metadb.ConversationState) (map[uint32]conversationSlotBatch, error) {
	groups := make(map[uint32]conversationSlotBatch)
	for _, state := range states {
		if state.UID == "" || state.ChannelID == "" || state.ChannelType == 0 {
			return nil, metadb.ErrInvalidArgument
		}
		route, err := n.RouteKey(state.UID)
		if err != nil {
			return nil, err
		}
		group := groups[route.SlotID]
		if len(group.stateItems) == 0 && len(group.patchItems) == 0 && len(group.deleteItems) == 0 {
			group.routeHashSlot = route.HashSlot
		}
		group.stateItems = append(group.stateItems, metafsm.ConversationStateBatchItem{
			HashSlot: route.HashSlot,
			State:    state,
		})
		groups[route.SlotID] = group
	}
	return groups, nil
}

func (n *Node) groupConversationPatchesBySlot(patches []metadb.ConversationActivePatch) (map[uint32]conversationSlotBatch, error) {
	groups := make(map[uint32]conversationSlotBatch)
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			return nil, metadb.ErrInvalidArgument
		}
		route, err := n.RouteKey(patch.UID)
		if err != nil {
			return nil, err
		}
		group := groups[route.SlotID]
		if len(group.stateItems) == 0 && len(group.patchItems) == 0 && len(group.deleteItems) == 0 {
			group.routeHashSlot = route.HashSlot
		}
		group.patchItems = append(group.patchItems, metafsm.ConversationActivePatchBatchItem{
			HashSlot: route.HashSlot,
			Patch:    patch,
		})
		groups[route.SlotID] = group
	}
	return groups, nil
}

func (n *Node) groupConversationDeletesBySlot(deletes []metadb.ConversationDelete) (map[uint32]conversationSlotBatch, error) {
	groups := make(map[uint32]conversationSlotBatch)
	for _, req := range deletes {
		if req.UID == "" || req.ChannelID == "" || req.ChannelType == 0 {
			return nil, metadb.ErrInvalidArgument
		}
		route, err := n.RouteKey(req.UID)
		if err != nil {
			return nil, err
		}
		group := groups[route.SlotID]
		if len(group.stateItems) == 0 && len(group.patchItems) == 0 && len(group.deleteItems) == 0 {
			group.routeHashSlot = route.HashSlot
		}
		group.deleteItems = append(group.deleteItems, metafsm.ConversationDeleteBatchItem{
			HashSlot: route.HashSlot,
			Delete:   req,
		})
		groups[route.SlotID] = group
	}
	return groups, nil
}

func sortedConversationSlotIDs(groups map[uint32]conversationSlotBatch) []uint32 {
	slotIDs := make([]uint32, 0, len(groups))
	for slotID := range groups {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	return slotIDs
}

func (n *Node) groupUserChannelMembershipsByHashSlot(channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) (map[uint16][]metadb.UserChannelMembership, error) {
	groups := make(map[uint16][]metadb.UserChannelMembership)
	for _, uid := range uids {
		route, err := n.RouteKey(uid)
		if err != nil {
			return nil, err
		}
		groups[route.HashSlot] = append(groups[route.HashSlot], metadb.UserChannelMembership{
			UID:         uid,
			ChannelID:   channelID,
			ChannelType: channelType,
			JoinSeq:     joinSeq,
			UpdatedAt:   updatedAt,
		})
	}
	return groups, nil
}

func sortedMembershipHashSlots(groups map[uint16][]metadb.UserChannelMembership) []uint16 {
	hashSlots := make([]uint16, 0, len(groups))
	for hashSlot := range groups {
		hashSlots = append(hashSlots, hashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	return hashSlots
}
