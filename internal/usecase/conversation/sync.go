package conversation

import (
	"container/heap"
	"context"
	"sort"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

type syncCandidate struct {
	key           ConversationKey
	state         metadb.UserConversationState
	hasState      bool
	update        *metadb.ChannelUpdateLog
	clientLastSeq uint64
	overlay       bool
}

type syncConversationView struct {
	key              ConversationKey
	state            metadb.UserConversationState
	displayUpdatedAt int64
	conversation     SyncConversation
}

type incrementalViewRetainer struct {
	limit int
	items map[ConversationKey]*retainedIncrementalView
	heap  retainedIncrementalViewHeap
}

type retainedIncrementalView struct {
	view  syncConversationView
	index int
}

type retainedIncrementalViewHeap []*retainedIncrementalView

type recentMessageBatchLoader interface {
	LoadRecentMessagesBatch(ctx context.Context, keys []ConversationKey, limit int) (map[ConversationKey][]channel.Message, error)
}

func (a *App) Sync(ctx context.Context, query SyncQuery) (SyncResult, error) {
	if a == nil {
		return SyncResult{}, nil
	}
	if query.Limit <= 0 {
		return SyncResult{}, nil
	}

	candidates := make(map[ConversationKey]*syncCandidate)

	active, err := a.states.ListUserConversationActive(ctx, query.UID, a.activeScanLimit)
	if err != nil {
		return SyncResult{}, err
	}
	if err := a.addActiveCandidates(ctx, query.UID, active, candidates); err != nil {
		return SyncResult{}, err
	}

	if err := a.addOverlayCandidates(ctx, query, candidates); err != nil {
		return SyncResult{}, err
	}
	var incrementalViews []syncConversationView
	if query.Version > 0 {
		incrementalViews, err = a.collectIncrementalViews(ctx, query, candidates)
		if err != nil {
			return SyncResult{}, err
		}
	}

	keys := filterCandidateKeys(candidates, query.ExcludeChannelTypes)
	latestByKey, err := a.facts.LoadLatestMessages(ctx, keys)
	if err != nil {
		return SyncResult{}, err
	}

	views := make([]syncConversationView, 0, len(keys)+len(incrementalViews))
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
	views = append(views, incrementalViews...)

	sort.Slice(views, func(i, j int) bool {
		return syncConversationViewHigherPriority(views[i], views[j])
	})

	if len(views) > query.Limit {
		views = views[:query.Limit]
	}

	if query.MsgCount > 0 {
		if batchFacts, ok := a.facts.(recentMessageBatchLoader); ok {
			keys := make([]ConversationKey, 0, len(views))
			for _, view := range views {
				keys = append(keys, view.key)
			}
			recentsByKey, err := batchFacts.LoadRecentMessagesBatch(ctx, keys, query.MsgCount)
			if err != nil {
				return SyncResult{}, err
			}
			for i := range views {
				assignConversationRecents(&views[i], recentsByKey[views[i].key])
			}
		} else {
			for i := range views {
				recents, err := a.facts.LoadRecentMessages(ctx, views[i].key, query.MsgCount)
				if err != nil {
					return SyncResult{}, err
				}
				assignConversationRecents(&views[i], recents)
			}
		}
	}

	result := SyncResult{Conversations: make([]SyncConversation, 0, len(views))}
	for _, view := range views {
		result.Conversations = append(result.Conversations, view.conversation)
	}
	return result, nil
}

func (a *App) addActiveCandidates(ctx context.Context, uid string, states []metadb.UserConversationState, candidates map[ConversationKey]*syncCandidate) error {
	if len(states) == 0 {
		return nil
	}

	keys := make([]metadb.ConversationKey, 0, len(states))
	for _, state := range states {
		keys = append(keys, metadbConversationKey(state.ChannelID, uint8(state.ChannelType)))
	}
	updates, err := a.channelUpdate.BatchGetChannelUpdateLogs(ctx, keys)
	if err != nil {
		return err
	}

	coldKeys := make([]metadb.ConversationKey, 0, len(states))
	for _, state := range states {
		key := conversationKey(state.ChannelID, uint8(state.ChannelType))
		update, ok := updates[metadbConversationKey(state.ChannelID, uint8(state.ChannelType))]
		if ok && a.isCold(update.LastMsgAt) {
			coldKeys = append(coldKeys, metadbConversationKey(state.ChannelID, uint8(state.ChannelType)))
			continue
		}

		candidate := ensureCandidate(candidates, key)
		candidate.state = state
		candidate.hasState = true
		if ok {
			updateCopy := update
			candidate.update = &updateCopy
		}
	}

	if len(coldKeys) > 0 {
		if a.enqueuePendingDemotions(uid, coldKeys) {
			a.async(func() {
				for {
					keys := a.dequeuePendingDemotions(uid)
					if len(keys) == 0 {
						if a.stopPendingDemotions(uid) {
							return
						}
						continue
					}
					if err := a.states.ClearUserConversationActiveAt(context.Background(), uid, keys); err != nil {
						a.failPendingDemotions(uid, keys)
						return
					}
				}
			})
		}
	}
	return nil
}

func (a *App) addOverlayCandidates(ctx context.Context, query SyncQuery, candidates map[ConversationKey]*syncCandidate) error {
	for key, lastSeq := range query.LastMsgSeqs {
		candidate := ensureCandidate(candidates, key)
		candidate.clientLastSeq = lastSeq
		if candidate.hasState {
			candidate.overlay = false
			continue
		}

		state, err := a.states.GetUserConversationState(ctx, query.UID, key.ChannelID, int64(key.ChannelType))
		switch err {
		case nil:
			candidate.state = state
			candidate.hasState = true
			candidate.overlay = false
		case metadb.ErrNotFound:
			candidate.state = metadb.UserConversationState{
				UID:         query.UID,
				ChannelID:   key.ChannelID,
				ChannelType: int64(key.ChannelType),
			}
			candidate.overlay = true
		default:
			return err
		}
	}
	return nil
}

func (a *App) collectIncrementalViews(ctx context.Context, query SyncQuery, candidates map[ConversationKey]*syncCandidate) ([]syncConversationView, error) {
	retainer := newIncrementalViewRetainer(a.incrementalCandidateLimit(query))
	excluded := make(map[uint8]struct{}, len(query.ExcludeChannelTypes))
	for _, channelType := range query.ExcludeChannelTypes {
		excluded[channelType] = struct{}{}
	}
	var after metadb.ConversationCursor
	for {
		page, next, done, err := a.states.ScanUserConversationStatePage(ctx, query.UID, after, a.channelProbeBatchSize)
		if err != nil {
			return nil, err
		}
		if len(page) > 0 {
			keys := make([]metadb.ConversationKey, 0, len(page))
			for _, state := range page {
				keys = append(keys, metadbConversationKey(state.ChannelID, uint8(state.ChannelType)))
			}
			updates, err := a.channelUpdate.BatchGetChannelUpdateLogs(ctx, keys)
			if err != nil {
				return nil, err
			}

			pageCandidates := make([]*syncCandidate, 0, len(page))
			pageFactsKeys := make([]ConversationKey, 0, len(page))
			for _, state := range page {
				key := conversationKey(state.ChannelID, uint8(state.ChannelType))
				if _, blocked := excluded[key.ChannelType]; blocked {
					continue
				}
				update, ok := updates[metadbConversationKey(state.ChannelID, uint8(state.ChannelType))]
				incremental, matched := a.buildIncrementalCandidate(query.Version, key, state, update, ok)
				if !matched {
					continue
				}
				if candidate, exists := candidates[key]; exists {
					mergeIncrementalCandidate(candidate, incremental)
					continue
				}
				pageCandidates = append(pageCandidates, incremental)
				pageFactsKeys = append(pageFactsKeys, key)
			}

			if len(pageFactsKeys) > 0 {
				latestByKey, err := a.facts.LoadLatestMessages(ctx, pageFactsKeys)
				if err != nil {
					return nil, err
				}
				for _, candidate := range pageCandidates {
					latest, ok := latestByKey[candidate.key]
					if !ok {
						continue
					}
					view, ok := buildSyncConversationView(query, candidate, latest)
					if !ok {
						continue
					}
					retainer.Upsert(view)
				}
			}
		}

		if done {
			return retainer.Views(), nil
		}
		after = next
	}
}

func (a *App) buildIncrementalCandidate(version int64, key ConversationKey, state metadb.UserConversationState, update metadb.ChannelUpdateLog, hasUpdate bool) (*syncCandidate, bool) {
	incremental := &syncCandidate{
		key:   key,
		state: state,
	}
	matched := false
	if state.UpdatedAt > version {
		incremental.hasState = true
		matched = true
	}
	if hasUpdate {
		updateCopy := update
		incremental.update = &updateCopy
		if update.UpdatedAt > version && !a.isCold(update.LastMsgAt) {
			incremental.hasState = true
			matched = true
		}
	}
	return incremental, matched
}

func (a *App) incrementalCandidateLimit(query SyncQuery) int {
	limit := query.Limit
	if limit < a.channelProbeBatchSize {
		limit = a.channelProbeBatchSize
	}
	if limit <= 0 {
		limit = 1
	}
	return limit
}

func buildSyncConversationView(query SyncQuery, candidate *syncCandidate, latest channel.Message) (syncConversationView, bool) {
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

	displayUpdatedAt := time.Unix(int64(latest.Timestamp), 0).UnixNano()
	if candidate.update != nil && candidate.update.UpdatedAt > 0 {
		displayUpdatedAt = candidate.update.UpdatedAt
	}
	syncUpdatedAt := displayUpdatedAt
	if candidate.state.UpdatedAt > syncUpdatedAt {
		syncUpdatedAt = candidate.state.UpdatedAt
	}

	return syncConversationView{
		key:              candidate.key,
		state:            candidate.state,
		displayUpdatedAt: displayUpdatedAt,
		conversation: SyncConversation{
			ChannelID:       displayChannelID(query.UID, candidate.key),
			ChannelType:     candidate.key.ChannelType,
			Unread:          unread,
			Timestamp:       int64(latest.Timestamp),
			LastMsgSeq:      uint32(latest.MessageSeq),
			LastClientMsgNo: latest.ClientMsgNo,
			ReadToMsgSeq:    uint32(readedTo),
			Version:         syncUpdatedAt,
		},
	}, true
}

func mergeIncrementalCandidate(dst, src *syncCandidate) {
	if dst == nil || src == nil {
		return
	}
	if src.hasState {
		dst.state = src.state
		dst.hasState = true
	}
	if src.update != nil {
		updateCopy := *src.update
		dst.update = &updateCopy
	}
}

func (a *App) isCold(lastMsgAt int64) bool {
	if lastMsgAt <= 0 {
		return false
	}
	return lastMsgAt <= a.now().Add(-a.coldThreshold).UnixNano()
}

func ensureCandidate(candidates map[ConversationKey]*syncCandidate, key ConversationKey) *syncCandidate {
	candidate, ok := candidates[key]
	if ok {
		return candidate
	}
	candidate = &syncCandidate{key: key}
	candidates[key] = candidate
	return candidate
}

func filterCandidateKeys(candidates map[ConversationKey]*syncCandidate, excluded []uint8) []ConversationKey {
	if len(candidates) == 0 {
		return nil
	}
	blocked := make(map[uint8]struct{}, len(excluded))
	for _, channelType := range excluded {
		blocked[channelType] = struct{}{}
	}
	keys := make([]ConversationKey, 0, len(candidates))
	for key := range candidates {
		if _, ok := blocked[key.ChannelType]; ok {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func filterVisibleRecents(recents []channel.Message, deletedToSeq uint64) []channel.Message {
	if deletedToSeq == 0 {
		return append([]channel.Message(nil), recents...)
	}
	out := make([]channel.Message, 0, len(recents))
	for _, msg := range recents {
		if msg.MessageSeq <= deletedToSeq {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func assignConversationRecents(view *syncConversationView, recents []channel.Message) {
	if view == nil {
		return
	}
	recents = filterVisibleRecents(recents, view.state.DeletedToSeq)
	sort.Slice(recents, func(left, right int) bool {
		return recents[left].MessageSeq > recents[right].MessageSeq
	})
	view.conversation.Recents = recents
}

func conversationKey(channelID string, channelType uint8) ConversationKey {
	return ConversationKey{
		ChannelID:   channelID,
		ChannelType: channelType,
	}
}

func metadbConversationKey(channelID string, channelType uint8) metadb.ConversationKey {
	return metadb.ConversationKey{
		ChannelID:   channelID,
		ChannelType: int64(channelType),
	}
}

func newIncrementalViewRetainer(limit int) *incrementalViewRetainer {
	if limit <= 0 {
		limit = 1
	}
	return &incrementalViewRetainer{
		limit: limit,
		items: make(map[ConversationKey]*retainedIncrementalView, limit),
		heap:  make(retainedIncrementalViewHeap, 0, limit),
	}
}

func (r *incrementalViewRetainer) Upsert(view syncConversationView) {
	if r == nil {
		return
	}
	if retained, ok := r.items[view.key]; ok {
		retained.view = view
		heap.Fix(&r.heap, retained.index)
		return
	}

	retained := &retainedIncrementalView{view: view}
	if len(r.heap) < r.limit {
		heap.Push(&r.heap, retained)
		r.items[view.key] = retained
		return
	}
	if len(r.heap) == 0 || !syncConversationViewHigherPriority(view, r.heap[0].view) {
		return
	}
	evicted := heap.Pop(&r.heap).(*retainedIncrementalView)
	delete(r.items, evicted.view.key)
	heap.Push(&r.heap, retained)
	r.items[view.key] = retained
}

func (r *incrementalViewRetainer) Views() []syncConversationView {
	if r == nil {
		return nil
	}
	views := make([]syncConversationView, 0, len(r.items))
	for _, retained := range r.items {
		views = append(views, retained.view)
	}
	return views
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

func (h retainedIncrementalViewHeap) Len() int {
	return len(h)
}

func (h retainedIncrementalViewHeap) Less(i, j int) bool {
	return syncConversationViewLowerPriority(h[i].view, h[j].view)
}

func (h retainedIncrementalViewHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *retainedIncrementalViewHeap) Push(x any) {
	item := x.(*retainedIncrementalView)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *retainedIncrementalViewHeap) Pop() any {
	old := *h
	last := len(old) - 1
	item := old[last]
	item.index = -1
	*h = old[:last]
	return item
}

func displayChannelID(uid string, key ConversationKey) string {
	if key.ChannelType != frame.ChannelTypePerson {
		return key.ChannelID
	}
	left, right, err := runtimechannelid.DecodePersonChannel(key.ChannelID)
	if err != nil {
		return key.ChannelID
	}
	if left == uid {
		return right
	}
	if right == uid {
		return left
	}
	return key.ChannelID
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
