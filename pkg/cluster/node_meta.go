package cluster

import (
	"context"
	"errors"
	"sort"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

const (
	maxChannelLatestBatchItems = 512
	maxMembershipBatchItems    = 512
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

// CommittedChannelTail returns the durable latest committed sequence currently
// projected for one channel. Membership adds capture it once for the logical
// operation so every UID receives the same visibility boundary.
func (n *Node) CommittedChannelTail(ctx context.Context, channelID string, channelType int64) (uint64, error) {
	if channelID == "" || channelType <= 0 || channelType > 255 {
		return 0, metadb.ErrInvalidArgument
	}
	head, err := n.ReadChannelConversationHead(ctx, channelruntime.ChannelID{ID: channelID, Type: uint8(channelType)}, "__membership_tail__")
	if errors.Is(err, channelruntime.ErrChannelNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return head.LastCommittedSeq, nil
}

// GetChannelLatestBatch reads existing latest message projections for channel keys.
func (n *Node) GetChannelLatestBatch(ctx context.Context, keys []metadb.ChannelKey) (map[metadb.ChannelKey]metadb.ChannelLatest, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return map[metadb.ChannelKey]metadb.ChannelLatest{}, nil
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	out := make(map[metadb.ChannelKey]metadb.ChannelLatest, len(keys))
	seen := make(map[metadb.ChannelKey]struct{}, len(keys))
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
	if err := n.ensureForeground(); err != nil {
		return metadb.MessageEventAppendResult{}, err
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
		start := time.Now()
		result, err := n.forwardMessageEventAppend(ctx, route.Leader, event)
		n.observeMessageEventAppend(messageEventPathForward, event, messageEventResultForError(err), time.Since(start))
		return result, err
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

// UpsertUserChannelMemberships persists live UID-owned memberships initialized
// from one committed channel tail through hash-slot ownership.
func (n *Node) UpsertUserChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, committedTail, sourceVersion uint64, updatedAt int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	groups, err := n.groupUserChannelMembershipsByHashSlot(channelID, channelType, uids, committedTail, sourceVersion, updatedAt, false)
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
		n.observeMembershipMutation("ordinary", "upsert", len(groups[hashSlot]))
	}
	return nil
}

// TombstoneUserChannelMemberships records UID-owned removals through hash-slot ownership.
func (n *Node) TombstoneUserChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, sourceVersion uint64, updatedAt int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	groups, err := n.groupUserChannelMembershipsByHashSlot(channelID, channelType, uids, 0, sourceVersion, updatedAt, true)
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
		n.observeMembershipMutation("ordinary", "tombstone", len(groups[hashSlot]))
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

// GetUserChannelMembership reads one UID-owned ordinary membership.
func (n *Node) GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.UserChannelMembership{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.UserChannelMembership{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.UserChannelMembership{}, false, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return metadb.UserChannelMembership{}, false, err
	}
	row, err := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetUserChannelMembership(ctx, uid, channelID, channelType)
	if errors.Is(err, metadb.ErrNotFound) {
		return metadb.UserChannelMembership{}, false, nil
	}
	return row, err == nil, err
}

// AdvanceUserChannelMembershipReadSeq monotonically advances one badge floor.
func (n *Node) AdvanceUserChannelMembershipReadSeq(ctx context.Context, uid, channelID string, channelType int64, readSeq uint64, updatedAt int64) error {
	return n.proposeUserChannelMembershipMutation(ctx, uid, "read_seq", metafsm.EncodeAdvanceUserChannelMembershipReadSeqCommand([]metadb.UserChannelMembership{{
		UID: uid, ChannelID: channelID, ChannelType: channelType, ReadSeq: readSeq, UpdatedAt: updatedAt,
	}}))
}

// HideUserChannelMembership advances one visibility floor and clears activation.
func (n *Node) HideUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64, deletedToSeq uint64, updatedAt int64) error {
	return n.proposeUserChannelMembershipMutation(ctx, uid, "hide", metafsm.EncodeHideUserChannelMembershipCommand([]metadb.UserChannelMembership{{
		UID: uid, ChannelID: channelID, ChannelType: channelType, DeletedToSeq: deletedToSeq, UpdatedAt: updatedAt,
	}}))
}

// ActivateUserChannelMembership raises one directory-priority timestamp.
func (n *Node) ActivateUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64, activatedAt, updatedAt int64) error {
	return n.proposeUserChannelMembershipMutation(ctx, uid, "activate", metafsm.EncodeActivateUserChannelMembershipCommand([]metadb.UserChannelMembership{{
		UID: uid, ChannelID: channelID, ChannelType: channelType, ActivatedAt: activatedAt, UpdatedAt: updatedAt,
	}}))
}

func (n *Node) proposeUserChannelMembershipMutation(ctx context.Context, uid, operation string, command []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if uid == "" || len(command) == 0 {
		return metadb.ErrInvalidArgument
	}
	if err := n.Propose(ctx, ProposeRequest{Key: uid, Command: command}); err != nil {
		return err
	}
	n.observeMembershipMutation("ordinary", operation, 1)
	return nil
}

// EnsureChannelDirectoryReady monotonically marks canonical person-channel
// membership initialization complete in channel-owned metadata.
func (n *Node) EnsureChannelDirectoryReady(ctx context.Context, channelID string, channelType int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	if channelID == "" || channelType <= 0 {
		return metadb.ErrInvalidArgument
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     channelID,
		Command: metafsm.EncodeEnsureChannelDirectoryReadyCommand(channelID, channelType),
	})
}

// UpsertUserCMDChannelMemberships persists CMD discovery bindings through UID hash-slot ownership.
func (n *Node) UpsertUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error {
	return n.proposeUserCMDChannelMemberships(ctx, memberships, "upsert", metafsm.EncodeUpsertUserCMDChannelMembershipsCommand)
}

// AdvanceUserCMDChannelMembershipAcks monotonically advances CMD acknowledgement cursors.
func (n *Node) AdvanceUserCMDChannelMembershipAcks(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error {
	return n.proposeUserCMDChannelMemberships(ctx, memberships, "ack", metafsm.EncodeAdvanceUserCMDChannelMembershipAcksCommand)
}

// TombstoneUserCMDChannelMemberships removes CMD discovery bindings.
func (n *Node) TombstoneUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error {
	return n.proposeUserCMDChannelMemberships(ctx, memberships, "tombstone", metafsm.EncodeTombstoneUserCMDChannelMembershipsCommand)
}

func (n *Node) proposeUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership, operation string, encode func([]metadb.UserCMDChannelMembership) []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	if len(memberships) == 0 {
		return nil
	}
	groups := make(map[uint16][]metadb.UserCMDChannelMembership)
	for _, membership := range memberships {
		if membership.UID == "" || membership.CommandChannelID == "" || membership.ChannelType <= 0 {
			return metadb.ErrInvalidArgument
		}
		route, err := n.RouteKey(membership.UID)
		if err != nil {
			return err
		}
		groups[route.HashSlot] = append(groups[route.HashSlot], membership)
	}
	for _, hashSlot := range sortedCMDMembershipHashSlots(groups) {
		group := groups[hashSlot]
		for start := 0; start < len(group); start += maxMembershipBatchItems {
			end := start + maxMembershipBatchItems
			if end > len(group) {
				end = len(group)
			}
			if err := n.Propose(ctx, ProposeRequest{
				Command: encode(group[start:end]),
				Target:  ProposeTarget{HashSlot: hashSlot, HasHashSlot: true},
			}); err != nil {
				return err
			}
			n.observeMembershipMutation("cmd", operation, end-start)
		}
	}
	return nil
}

func (n *Node) observeMembershipMutation(directory, operation string, rows int) {
	if n == nil || n.cfg.MembershipObserver == nil || rows <= 0 {
		return
	}
	n.cfg.MembershipObserver.ObserveMembershipMutation(MembershipMutationObservation{
		Directory: directory,
		Operation: operation,
		Rows:      rows,
	})
}

// ListUserCMDChannelMembershipPage reads CMD directory rows from the UID-owned hash slot.
func (n *Node) ListUserCMDChannelMembershipPage(ctx context.Context, uid string, after metadb.UserCMDChannelMembershipCursor, limit int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, metadb.UserCMDChannelMembershipCursor{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, metadb.UserCMDChannelMembershipCursor{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, metadb.UserCMDChannelMembershipCursor{}, false, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return nil, metadb.UserCMDChannelMembershipCursor{}, false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListUserCMDChannelMembershipPage(ctx, uid, after, limit)
}

type channelLatestSlotBatch struct {
	routeHashSlot uint16
	items         []metafsm.ChannelLatestBatchItem
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

func (n *Node) groupUserChannelMembershipsByHashSlot(channelID string, channelType int64, uids []string, committedTail, sourceVersion uint64, updatedAt int64, tombstone bool) (map[uint16][]metadb.UserChannelMembership, error) {
	groups := make(map[uint16][]metadb.UserChannelMembership)
	joinSeq := committedTail + 1
	if joinSeq == 0 {
		joinSeq = committedTail
	}
	for _, uid := range uids {
		route, err := n.RouteKey(uid)
		if err != nil {
			return nil, err
		}
		tombstoneAt := int64(0)
		if tombstone {
			tombstoneAt = updatedAt
		}
		groups[route.HashSlot] = append(groups[route.HashSlot], metadb.UserChannelMembership{
			UID:           uid,
			ChannelID:     channelID,
			ChannelType:   channelType,
			JoinSeq:       joinSeq,
			ReadSeq:       committedTail,
			DeletedToSeq:  committedTail,
			Tombstone:     tombstone,
			TombstoneAt:   tombstoneAt,
			SourceVersion: sourceVersion,
			UpdatedAt:     updatedAt,
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

func sortedCMDMembershipHashSlots(groups map[uint16][]metadb.UserCMDChannelMembership) []uint16 {
	hashSlots := make([]uint16, 0, len(groups))
	for hashSlot := range groups {
		hashSlots = append(hashSlots, hashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	return hashSlots
}
