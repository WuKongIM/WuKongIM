package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestDeleteConversationHidesStateClearsActiveAtAndRemovesHotHint(t *testing.T) {
	now := time.Unix(123, 0)
	repo := newConversationDeleteRepoStub()
	app := New(Options{
		States:  repo,
		Deletes: repo,
		Facts:   repo,
		Now:     func() time.Time { return now },
	})

	err := app.DeleteConversation(context.Background(), DeleteConversationCommand{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  10,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"hide", "remove_hints"}, repo.calls)
	require.Equal(t, []metadb.UserConversationDelete{{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		DeletedToSeq: 10,
		UpdatedAt:    now.UnixNano(),
	}}, repo.hides)
	require.Equal(t, []metadb.UserConversationDeleteBarrier{{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		DeletedToSeq: 10,
	}}, repo.removedBarriers)
}

func TestDeleteConversationUsesLatestMessageSeqWhenCommandSeqIsZero(t *testing.T) {
	now := time.Unix(123, 0)
	repo := newConversationDeleteRepoStub()
	repo.latest[key("g1", 2)] = channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 15}
	app := New(Options{
		States:  repo,
		Deletes: repo,
		Facts:   repo,
		Now:     func() time.Time { return now },
	})

	err := app.DeleteConversation(context.Background(), DeleteConversationCommand{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
	})

	require.NoError(t, err)
	require.Equal(t, uint64(15), repo.hides[0].DeletedToSeq)
	require.Equal(t, []ConversationKey{key("g1", 2)}, repo.latestLoads)
}

func TestDeleteConversationReturnsErrorWhenLatestMessageSeqMissing(t *testing.T) {
	repo := newConversationDeleteRepoStub()
	app := New(Options{
		States:  repo,
		Deletes: repo,
		Facts:   repo,
	})

	err := app.DeleteConversation(context.Background(), DeleteConversationCommand{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
	})

	require.ErrorContains(t, err, "latest message not found")
	require.Empty(t, repo.hides)
	require.Empty(t, repo.removedBarriers)
}

func TestDeleteConversationAllowsNewerMessageReactivation(t *testing.T) {
	now := time.Unix(123, 0)
	repo := newConversationDeleteRepoStub()
	cache := NewActiveHintCache(ActiveHintCacheOptions{
		HintTTL:    time.Hour,
		BarrierTTL: time.Hour,
		Now:        func() time.Time { return now },
	})
	repo.cache = cache
	app := New(Options{
		States:  repo,
		Deletes: repo,
		Facts:   repo,
		Now:     func() time.Time { return now },
	})
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, MessageSeq: 10},
	}))

	require.NoError(t, app.DeleteConversation(context.Background(), DeleteConversationCommand{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  10,
	}))
	require.NoError(t, cache.SubmitHints(context.Background(), []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, MessageSeq: 11},
	}))

	hints, err := cache.ListHotUserConversationActive(context.Background(), "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, MessageSeq: 11},
	}, hints)
}

type conversationDeleteRepoStub struct {
	calls           []string
	hides           []metadb.UserConversationDelete
	removedBarriers []metadb.UserConversationDeleteBarrier
	latest          map[ConversationKey]channel.Message
	latestLoads     []ConversationKey
	cache           *ActiveHintCache
}

func newConversationDeleteRepoStub() *conversationDeleteRepoStub {
	return &conversationDeleteRepoStub{latest: make(map[ConversationKey]channel.Message)}
}

func (r *conversationDeleteRepoStub) HideUserConversations(_ context.Context, reqs []metadb.UserConversationDelete) error {
	r.calls = append(r.calls, "hide")
	r.hides = append(r.hides, reqs...)
	return nil
}

func (r *conversationDeleteRepoStub) RemoveUserConversationActiveHints(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error {
	r.calls = append(r.calls, "remove_hints")
	r.removedBarriers = append(r.removedBarriers, barriers...)
	if r.cache != nil {
		return r.cache.RemoveHints(ctx, barriers)
	}
	return nil
}

func (r *conversationDeleteRepoStub) GetUserConversationState(context.Context, string, string, int64) (metadb.UserConversationState, error) {
	return metadb.UserConversationState{}, metadb.ErrNotFound
}

func (r *conversationDeleteRepoStub) UpsertUserConversationStates(context.Context, []metadb.UserConversationState) error {
	return nil
}

func (r *conversationDeleteRepoStub) ListUserConversationActive(context.Context, string, int) ([]metadb.UserConversationState, error) {
	return nil, nil
}

func (r *conversationDeleteRepoStub) LoadLatestMessages(_ context.Context, keys []ConversationKey) (map[ConversationKey]channel.Message, error) {
	r.latestLoads = append(r.latestLoads, keys...)
	out := make(map[ConversationKey]channel.Message, len(keys))
	for _, key := range keys {
		if msg, ok := r.latest[key]; ok {
			out[key] = msg
		}
	}
	return out, nil
}

func (r *conversationDeleteRepoStub) LoadRecentMessages(context.Context, ConversationKey, int) ([]channel.Message, error) {
	return nil, nil
}
