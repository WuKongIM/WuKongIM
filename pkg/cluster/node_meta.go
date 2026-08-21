package cluster

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

const (
	maxChannelLatestBatchItems            = 512
	maxMembershipBatchItems               = 512
	maxMembershipProposalConcurrency      = 2
	maxPersonDirectoryProposalConcurrency = 10
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
	if n.defaultSlotProxy == nil {
		return metadb.ChannelRuntimeMeta{}, ErrNotStarted
	}
	return n.defaultSlotProxy.GetChannelRuntimeMeta(ctx, channelID, channelType)
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
	proposals := make([]userMembershipProposal, 0, len(groups))
	for _, hashSlot := range sortedMembershipHashSlots(groups) {
		command, err := metafsm.EncodeUpsertUserChannelMembershipsCommandChecked(groups[hashSlot])
		if err != nil {
			return err
		}
		proposals = append(proposals, userMembershipProposal{hashSlot: hashSlot, command: command, rows: len(groups[hashSlot])})
	}
	return n.submitUserMembershipProposals(ctx, proposals, "upsert")
}

// AdmitPersonDirectoryTasks durably records source-owned projection work in
// the same Slot FSM batch as create-only Channel runtime metadata. Results are
// aligned with tasks so one unavailable source Slot cannot fail unrelated,
// already committed admissions.
func (n *Node) AdmitPersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTask) []error {
	results := make([]error, len(tasks))
	n.AdmitPersonDirectoryTaskWaves(ctx, tasks, func(index int, err error) {
		if index >= 0 && index < len(results) {
			results[index] = err
		}
	})
	return results
}

// AdmitPersonDirectoryTaskWaves records source-owned projection tasks and
// emits every aligned result as soon as its independent Slot proposal
// completes. emit is called serially and exactly once for every input before
// the method returns.
func (n *Node) AdmitPersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, error)) {
	if emit == nil {
		return
	}
	completed := make([]bool, len(tasks))
	complete := func(index int, err error) {
		if index < 0 || index >= len(completed) || completed[index] {
			return
		}
		completed[index] = true
		emit(index, err)
	}
	completeAll := func(err error) {
		for i := range tasks {
			complete(i, err)
		}
	}
	if err := ctxErr(ctx); err != nil {
		completeAll(err)
		return
	}
	if n == nil {
		completeAll(ErrNotStarted)
		return
	}
	if err := n.ensureForeground(); err != nil {
		completeAll(err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	if len(tasks) > metafsm.MaxPersonDirectoryBatchItems {
		completeAll(metadb.ErrInvalidArgument)
		return
	}
	keys := make([]string, len(tasks))
	ids := make([]channelruntime.ChannelID, len(tasks))
	for i, task := range tasks {
		if task.ChannelID == "" || task.ChannelType != 1 || task.CreatedAt < 0 {
			complete(i, metadb.ErrInvalidArgument)
			continue
		}
		keys[i] = task.ChannelID
		ids[i] = channelruntime.ChannelID{ID: task.ChannelID, Type: uint8(task.ChannelType)}
	}
	routed, err := n.router.RouteKeysPartial(keys)
	if err != nil {
		completeAll(mapRouteError(err))
		return
	}
	validIndices := make([]int, 0, len(tasks))
	validIDs := make([]channelruntime.ChannelID, 0, len(tasks))
	validRoutes := make([]routing.Route, 0, len(tasks))
	for i, result := range routed {
		if completed[i] {
			continue
		}
		if result.Err != nil {
			complete(i, mapRouteError(result.Err))
			continue
		}
		validIndices = append(validIndices, i)
		validIDs = append(validIDs, ids[i])
		validRoutes = append(validRoutes, result.Route)
	}
	if len(validIndices) == 0 {
		return
	}
	replicaCount := int(n.cfg.Channel.ReplicaCount)
	if replicaCount == 0 {
		replicaCount = int(n.cfg.Slots.ReplicaCount)
	}
	resolver := channels.NewSlotPlacementResolver(n.router, &n.channelDataNodes, replicaCount)
	placements, err := resolver.ResolveChannelPlacementBatch(ctx, validIDs, validRoutes)
	if err != nil {
		for _, index := range validIndices {
			complete(index, err)
		}
		return
	}
	type admissionGroupItem struct {
		item  metafsm.PersonDirectoryAdmissionBatchItem
		index int
	}
	groups := make(map[uint32][]admissionGroupItem)
	for validIndex, index := range validIndices {
		task := tasks[index]
		meta, err := channels.RuntimeMetaFromPlacement(ids[index], placements[validIndex])
		if err != nil {
			complete(index, err)
			continue
		}
		route := validRoutes[validIndex]
		groups[route.SlotID] = append(groups[route.SlotID], admissionGroupItem{
			item: metafsm.PersonDirectoryAdmissionBatchItem{HashSlot: route.HashSlot, Task: task, RuntimeMeta: meta}, index: index,
		})
	}
	slotIDs := make([]uint32, 0, len(groups))
	for slotID := range groups {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	proposals := make([]personDirectoryProposal, 0, len(groups))
	for _, slotID := range slotIDs {
		group := groups[slotID]
		for start := 0; start < len(group); start += metafsm.MaxPersonDirectoryBatchItems {
			end := min(start+metafsm.MaxPersonDirectoryBatchItems, len(group))
			items := make([]metafsm.PersonDirectoryAdmissionBatchItem, end-start)
			indices := make([]int, end-start)
			for i := start; i < end; i++ {
				items[i-start] = group[i].item
				indices[i-start] = group[i].index
			}
			command, err := metafsm.EncodeAdmitPersonDirectoryTaskBatchCommandChecked(items)
			if err != nil {
				for _, index := range indices {
					complete(index, err)
				}
				continue
			}
			proposals = append(proposals, personDirectoryProposal{slotID: slotID, hashSlot: items[0].HashSlot, command: command, indices: indices})
		}
	}
	if observer, ok := n.cfg.Channel.Observer.(channels.AppendStageObserver); ok {
		ctx = propose.WithStageObserver(ctx, observer)
	}
	n.submitPersonDirectoryProposalWaves(ctx, proposals, complete)
	completeAll(metadb.ErrInvalidArgument)
}

// EnsureUserChannelMembershipBatch projects create-if-absent rows and
// preserves one error aligned with every input membership. Slot-group success
// is retained when another independent Slot group fails.
func (n *Node) EnsureUserChannelMembershipBatch(ctx context.Context, memberships []metadb.UserChannelMembership) []error {
	results := make([]error, len(memberships))
	if err := ctxErr(ctx); err != nil {
		for i := range results {
			results[i] = err
		}
		return results
	}
	if n == nil {
		for i := range results {
			results[i] = ErrNotStarted
		}
		return results
	}
	keys := make([]string, len(memberships))
	for i, membership := range memberships {
		if membership.UID == "" || membership.ChannelID == "" || membership.ChannelType != 1 {
			results[i] = metadb.ErrInvalidArgument
			continue
		}
		keys[i] = membership.UID
	}
	routes, err := n.RouteKeysPartial(keys)
	if err != nil {
		for i := range results {
			if results[i] == nil {
				results[i] = err
			}
		}
		return results
	}
	type groupItem struct {
		item  metafsm.UserChannelMembershipBatchItem
		index int
	}
	groups := make(map[uint32][]groupItem)
	for i, result := range routes {
		if results[i] != nil {
			continue
		}
		if result.Err != nil {
			results[i] = result.Err
			continue
		}
		groups[result.Route.SlotID] = append(groups[result.Route.SlotID], groupItem{
			item: metafsm.UserChannelMembershipBatchItem{HashSlot: result.Route.HashSlot, Membership: memberships[i]}, index: i,
		})
	}
	slotIDs := make([]uint32, 0, len(groups))
	for slotID := range groups {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	proposals := make([]personDirectoryProposal, 0, len(groups))
	for _, slotID := range slotIDs {
		group := groups[slotID]
		for start := 0; start < len(group); start += metafsm.MaxPersonDirectoryBatchItems {
			end := min(start+metafsm.MaxPersonDirectoryBatchItems, len(group))
			items := make([]metafsm.UserChannelMembershipBatchItem, end-start)
			indices := make([]int, end-start)
			for i := start; i < end; i++ {
				items[i-start] = group[i].item
				indices[i-start] = group[i].index
			}
			command, err := metafsm.EncodeEnsureUserChannelMembershipBatchCommandChecked(items)
			if err != nil {
				for _, index := range indices {
					results[index] = err
				}
				continue
			}
			proposals = append(proposals, personDirectoryProposal{
				slotID: slotID, hashSlot: items[0].HashSlot, command: command, indices: indices,
			})
		}
	}
	n.submitPersonDirectoryProposalWaves(ctx, proposals, func(index int, err error) {
		results[index] = err
		if err == nil {
			n.observeMembershipMutation("ordinary", "ensure", 1)
		}
	})
	return results
}

type personDirectoryProposal struct {
	slotID   uint32
	hashSlot uint16
	command  []byte
	indices  []int
}

func (n *Node) submitPersonDirectoryProposalWaves(ctx context.Context, proposals []personDirectoryProposal, emit func(int, error)) {
	if len(proposals) == 0 {
		return
	}
	type proposalResult struct {
		proposal personDirectoryProposal
		err      error
	}
	jobs := make(chan int, len(proposals))
	completed := make(chan proposalResult, len(proposals))
	for index := range proposals {
		jobs <- index
	}
	close(jobs)
	workerCount := min(len(proposals), maxPersonDirectoryProposalConcurrency)
	var workers sync.WaitGroup
	worker := func() {
		for index := range jobs {
			proposal := proposals[index]
			err := n.Propose(ctx, ProposeRequest{Command: proposal.command, Target: ProposeTarget{
				HashSlot: proposal.hashSlot, HasHashSlot: true, SlotID: proposal.slotID, HasSlotID: true,
			}})
			completed <- proposalResult{proposal: proposal, err: err}
		}
	}
	if workerCount > 1 {
		workers.Add(workerCount - 1)
		goruntimeregistry.SafeGoN(n.cfg.Goroutines, goruntimeregistry.TaskClusterMembershipBatch, workerCount-1, func(int) {
			defer workers.Done()
			worker()
		})
	}
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterMembershipBatch, func() {
		worker()
		workers.Wait()
		close(completed)
	})
	for result := range completed {
		for _, index := range result.proposal.indices {
			emit(index, result.err)
		}
	}
}

type userMembershipProposal struct {
	hashSlot uint16
	command  []byte
	rows     int
}

func (n *Node) submitUserMembershipProposals(ctx context.Context, proposals []userMembershipProposal, action string) error {
	if len(proposals) == 0 {
		return nil
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, len(proposals))
	for index := range proposals {
		jobs <- index
	}
	close(jobs)
	succeeded := make([]bool, len(proposals))
	workerCount := min(len(proposals), maxMembershipProposalConcurrency)
	var workers sync.WaitGroup
	var firstErr error
	var firstErrOnce sync.Once
	worker := func() {
		for index := range jobs {
			if workerCtx.Err() != nil {
				return
			}
			proposal := proposals[index]
			if err := n.Propose(workerCtx, ProposeRequest{
				Command: proposal.command,
				Target:  ProposeTarget{HashSlot: proposal.hashSlot, HasHashSlot: true},
			}); err != nil {
				firstErrOnce.Do(func() {
					firstErr = err
					cancel()
				})
				continue
			}
			succeeded[index] = true
		}
	}
	if workerCount > 1 {
		workers.Add(workerCount - 1)
		goruntimeregistry.SafeGoN(n.cfg.Goroutines, goruntimeregistry.TaskClusterMembershipBatch, workerCount-1, func(int) {
			defer workers.Done()
			worker()
		})
	}
	worker()
	workers.Wait()
	for index, ok := range succeeded {
		if ok {
			n.observeMembershipMutation("ordinary", action, proposals[index].rows)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	return ctxErr(ctx)
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
	if n.defaultSlotProxy == nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, ErrNotStarted
	}
	return n.defaultSlotProxy.ListUserChannelMembershipPage(ctx, uid, after, limit)
}

// GetUserChannelMembership reads one UID-owned ordinary membership.
func (n *Node) GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.UserChannelMembership{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.UserChannelMembership{}, false, err
	}
	if n.defaultSlotProxy == nil {
		return metadb.UserChannelMembership{}, false, ErrNotStarted
	}
	return n.defaultSlotProxy.GetUserChannelMembership(ctx, uid, channelID, channelType)
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
	if n.defaultSlotProxy == nil {
		return nil, metadb.UserCMDChannelMembershipCursor{}, false, ErrNotStarted
	}
	return n.defaultSlotProxy.ListUserCMDChannelMembershipPage(ctx, uid, after, limit)
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
