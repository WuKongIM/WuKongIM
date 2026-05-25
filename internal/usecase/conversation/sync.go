package conversation

import (
	"context"
	"sort"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type syncCandidate struct {
	key           ConversationKey
	state         metadb.UserConversationState
	hasState      bool
	clientLastSeq uint64
	overlay       bool
}

type syncConversationView struct {
	key              ConversationKey
	state            metadb.UserConversationState
	displayUpdatedAt int64
	conversation     SyncConversation
}

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
	a.addActiveCandidates(active, candidates)
	if err := a.addOverlayCandidates(ctx, query, candidates); err != nil {
		return SyncResult{}, err
	}

	keys := filterCandidateKeys(candidates, query.ExcludeChannelTypes)
	latestByKey, err := a.facts.LoadLatestMessages(ctx, keys)
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

func (a *App) addActiveCandidates(states []metadb.UserConversationState, candidates map[ConversationKey]*syncCandidate) {
	for _, state := range states {
		key := conversationKey(state.ChannelID, uint8(state.ChannelType))
		candidate := ensureCandidate(candidates, key)
		candidate.state = state
		candidate.hasState = true
	}
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
			candidate.state = metadb.UserConversationState{UID: query.UID, ChannelID: key.ChannelID, ChannelType: int64(key.ChannelType)}
			candidate.overlay = true
		default:
			return err
		}
	}
	return nil
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
		if runtimechannelid.IsCommandChannel(key.ChannelID) {
			continue
		}
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
	return ConversationKey{ChannelID: channelID, ChannelType: channelType}
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
