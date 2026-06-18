package conversation

import (
	"context"
	"sort"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type syncCandidate struct {
	key              ConversationKey
	state            metadb.UserConversationState
	hasState         bool
	clientLastSeq    uint64
	overlay          bool
	overlayCandidate bool
}

type syncConversationView struct {
	key              ConversationKey
	state            metadb.UserConversationState
	displayUpdatedAt int64
	conversation     SyncConversation
}

// Sync returns a legacy-compatible conversation working set for uid.
func (a *App) Sync(ctx context.Context, query SyncQuery) (SyncResult, error) {
	if a == nil || a.store == nil || a.stateStore == nil || a.messages == nil {
		return SyncResult{}, ErrStoreRequired
	}
	if query.UID == "" || query.MsgCount < 0 || query.Limit < 0 || query.Limit > maxSyncLimit {
		return SyncResult{}, ErrInvalidRequest
	}
	limit := normalizeSyncLimit(query.Limit)
	candidates := make(map[ConversationKey]*syncCandidate)
	active, err := a.store.ListUserConversationActiveView(ctx, query.UID, metadb.UserConversationActiveCursor{}, a.activeScanLimit)
	if err != nil {
		return SyncResult{}, err
	}
	addSyncActiveCandidates(active.Rows, candidates)
	if err := a.addSyncOverlayCandidates(ctx, query, candidates); err != nil {
		return SyncResult{}, err
	}
	overlayItems := countSyncOverlayCandidates(candidates)

	keys := filterSyncCandidateKeys(candidates, query.ExcludeChannelTypes)
	latestByKey, err := a.loadSyncLatestMessages(ctx, keys)
	if err != nil {
		return SyncResult{}, err
	}
	views := make([]syncConversationView, 0, len(keys))
	for _, key := range keys {
		latest, ok := latestByKey[key]
		if !ok {
			continue
		}
		view, ok := buildSyncConversationView(query, candidates[key], latest)
		if !ok {
			continue
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		return syncConversationViewHigherPriority(views[i], views[j])
	})
	if len(views) > limit {
		views = views[:limit]
	}
	var recentLoadDuration time.Duration
	if query.MsgCount > 0 {
		recentLoadStart := time.Now()
		if err := a.assignSyncRecents(ctx, views, query.MsgCount); err != nil {
			return SyncResult{}, err
		}
		recentLoadDuration = time.Since(recentLoadStart)
		if recentLoadDuration <= 0 {
			recentLoadDuration = time.Nanosecond
		}
	}

	result := SyncResult{
		Conversations:      make([]SyncConversation, 0, len(views)),
		OverlayItems:       overlayItems,
		RecentLoadDuration: recentLoadDuration,
	}
	for _, view := range views {
		result.Conversations = append(result.Conversations, view.conversation)
	}
	return result, nil
}

func normalizeSyncLimit(limit int) int {
	if limit <= 0 {
		return defaultSyncLimit
	}
	return limit
}

func addSyncActiveCandidates(states []metadb.UserConversationState, candidates map[ConversationKey]*syncCandidate) {
	for _, state := range states {
		key := ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}
		candidate := ensureSyncCandidate(candidates, key)
		candidate.state = state
		candidate.hasState = true
	}
}

func (a *App) addSyncOverlayCandidates(ctx context.Context, query SyncQuery, candidates map[ConversationKey]*syncCandidate) error {
	for key, lastSeq := range query.LastMsgSeqs {
		candidate, exists := candidates[key]
		if !exists {
			candidate = ensureSyncCandidate(candidates, key)
			candidate.overlayCandidate = true
		}
		candidate.clientLastSeq = lastSeq
		if candidate.hasState {
			candidate.overlay = false
			continue
		}
		state, ok, err := a.stateStore.GetUserConversationState(ctx, query.UID, key.ChannelID, key.ChannelType)
		if err != nil {
			return err
		}
		if ok {
			candidate.state = state
			candidate.hasState = true
			candidate.overlay = false
			continue
		}
		candidate.state = metadb.UserConversationState{
			UID:         query.UID,
			ChannelID:   key.ChannelID,
			ChannelType: key.ChannelType,
		}
		candidate.overlay = true
	}
	return nil
}

func countSyncOverlayCandidates(candidates map[ConversationKey]*syncCandidate) int {
	count := 0
	for _, candidate := range candidates {
		if candidate != nil && candidate.overlayCandidate {
			count++
		}
	}
	return count
}

func (a *App) loadSyncLatestMessages(ctx context.Context, keys []ConversationKey) (map[ConversationKey]LastMessage, error) {
	requests := make([]LastVisibleMessageRequest, 0, len(keys))
	for _, key := range keys {
		requests = append(requests, LastVisibleMessageRequest{ChannelID: key.ChannelID, ChannelType: key.ChannelType})
	}
	latest, err := a.messages.GetLastVisibleMessages(ctx, requests)
	if err != nil {
		return nil, err
	}
	out := make(map[ConversationKey]LastMessage, len(latest))
	for key, msg := range latest {
		out[ConversationKey{ChannelID: key.ChannelID, ChannelType: key.ChannelType}] = msg
	}
	return out, nil
}

func buildSyncConversationView(query SyncQuery, candidate *syncCandidate, latest LastMessage) (syncConversationView, bool) {
	if candidate == nil || latest.MessageSeq == 0 {
		return syncConversationView{}, false
	}
	if candidate.overlay && candidate.clientLastSeq >= latest.MessageSeq {
		return syncConversationView{}, false
	}
	baseReadedTo := maxUint64(candidate.state.ReadSeq, candidate.state.DeletedToSeq)
	if latest.MessageSeq <= candidate.state.DeletedToSeq {
		return syncConversationView{}, false
	}
	unread := 0
	if latest.MessageSeq > baseReadedTo {
		unread = int(latest.MessageSeq - baseReadedTo)
	}
	readedTo := baseReadedTo
	if latest.FromUID == query.UID {
		unread = 0
		readedTo = latest.MessageSeq
	}
	if query.OnlyUnread && unread == 0 {
		return syncConversationView{}, false
	}

	displayUpdatedAt := latest.ServerTimestampMS * int64(1_000_000)
	syncUpdatedAt := displayUpdatedAt
	if candidate.state.UpdatedAt > syncUpdatedAt {
		syncUpdatedAt = candidate.state.UpdatedAt
	}
	return syncConversationView{
		key:              candidate.key,
		state:            candidate.state,
		displayUpdatedAt: displayUpdatedAt,
		conversation: SyncConversation{
			ChannelID:       candidate.key.ChannelID,
			ChannelType:     uint8(candidate.key.ChannelType),
			Unread:          unread,
			Timestamp:       latest.ServerTimestampMS / 1000,
			LastMsgSeq:      uint32(latest.MessageSeq),
			LastClientMsgNo: latest.ClientMsgNo,
			ReadToMsgSeq:    uint32(readedTo),
			Version:         syncUpdatedAt,
		},
	}, true
}

func (a *App) assignSyncRecents(ctx context.Context, views []syncConversationView, limit int) error {
	recentStore, ok := a.messages.(RecentMessageStore)
	if !ok {
		return ErrStoreRequired
	}
	keys := make([]ConversationKey, 0, len(views))
	for _, view := range views {
		keys = append(keys, view.key)
	}
	recentsByKey, err := recentStore.GetRecentMessages(ctx, keys, limit)
	if err != nil {
		return err
	}
	for i := range views {
		assignConversationRecents(&views[i], recentsByKey[views[i].key])
	}
	return nil
}

func ensureSyncCandidate(candidates map[ConversationKey]*syncCandidate, key ConversationKey) *syncCandidate {
	candidate, ok := candidates[key]
	if ok {
		return candidate
	}
	candidate = &syncCandidate{key: key}
	candidates[key] = candidate
	return candidate
}

func filterSyncCandidateKeys(candidates map[ConversationKey]*syncCandidate, excluded []uint8) []ConversationKey {
	blocked := make(map[int64]struct{}, len(excluded))
	for _, channelType := range excluded {
		blocked[int64(channelType)] = struct{}{}
	}
	keys := make([]ConversationKey, 0, len(candidates))
	for key := range candidates {
		if runtimechannelid.IsCommandChannel(key.ChannelID) {
			continue
		}
		if _, ok := blocked[key.ChannelType]; ok {
			continue
		}
		if key.ChannelType <= 0 || key.ChannelType > 255 {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func assignConversationRecents(view *syncConversationView, recents []SyncMessage) {
	if view == nil {
		return
	}
	recents = filterVisibleRecents(recents, view.state.DeletedToSeq)
	sort.Slice(recents, func(left, right int) bool {
		return recents[left].MessageSeq > recents[right].MessageSeq
	})
	view.conversation.Recents = recents
}

func filterVisibleRecents(recents []SyncMessage, deletedToSeq uint64) []SyncMessage {
	out := make([]SyncMessage, 0, len(recents))
	for _, msg := range recents {
		if msg.MessageSeq <= deletedToSeq {
			continue
		}
		msg.Payload = append([]byte(nil), msg.Payload...)
		out = append(out, msg)
	}
	return out
}

func syncConversationViewHigherPriority(left, right syncConversationView) bool {
	return syncConversationViewLowerPriority(right, left)
}

func syncConversationViewLowerPriority(left, right syncConversationView) bool {
	if left.displayUpdatedAt != right.displayUpdatedAt {
		return left.displayUpdatedAt < right.displayUpdatedAt
	}
	if left.key.ChannelType != right.key.ChannelType {
		return left.key.ChannelType > right.key.ChannelType
	}
	return left.key.ChannelID > right.key.ChannelID
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
