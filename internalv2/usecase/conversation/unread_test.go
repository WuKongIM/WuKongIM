package conversation

import (
	"context"
	"reflect"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClearUnreadAdvancesReadSeqToLatestMessage(t *testing.T) {
	now := time.Unix(0, 123)
	store := newConversationMutationStore()
	store.latest[metadb.ConversationKey{ChannelID: "g1", ChannelType: 2}] = LastMessage{MessageSeq: 12}
	app := New(Options{Store: store, Messages: store, Now: func() time.Time { return now }})

	if err := app.ClearUnread(context.Background(), ClearUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); err != nil {
		t.Fatalf("ClearUnread() error = %v", err)
	}

	want := []metadb.ConversationState{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
		ReadSeq:     12,
		UpdatedAt:   now.UnixNano(),
	}}
	if !reflect.DeepEqual(store.upserts, want) {
		t.Fatalf("upserts = %#v, want %#v", store.upserts, want)
	}
}

func TestSetUnreadAdvancesReadSeqToKeepRequestedUnreadTail(t *testing.T) {
	now := time.Unix(0, 456)
	store := newConversationMutationStore()
	store.latest[metadb.ConversationKey{ChannelID: "g1", ChannelType: 2}] = LastMessage{MessageSeq: 12}
	app := New(Options{Store: store, Messages: store, Now: func() time.Time { return now }})

	if err := app.SetUnread(context.Background(), SetUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 3}); err != nil {
		t.Fatalf("SetUnread() error = %v", err)
	}

	if len(store.upserts) != 1 || store.upserts[0].ReadSeq != 9 || store.upserts[0].UpdatedAt != now.UnixNano() {
		t.Fatalf("upserts = %#v, want read seq 9 with fixed updated time", store.upserts)
	}
}

func TestDeleteConversationHidesThroughLatestMessage(t *testing.T) {
	now := time.Unix(0, 789)
	store := newConversationMutationStore()
	store.latest[metadb.ConversationKey{ChannelID: "g1", ChannelType: 2}] = LastMessage{MessageSeq: 12}
	app := New(Options{Store: store, Messages: store, Now: func() time.Time { return now }})

	if err := app.DeleteConversation(context.Background(), DeleteConversationCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); err != nil {
		t.Fatalf("DeleteConversation() error = %v", err)
	}

	want := []metadb.ConversationDelete{{
		UID:          "u1",
		Kind:         metadb.ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		DeletedToSeq: 12,
		UpdatedAt:    now.UnixNano(),
	}}
	if !reflect.DeepEqual(store.deletes, want) {
		t.Fatalf("deletes = %#v, want %#v", store.deletes, want)
	}
}

type conversationMutationStore struct {
	states  map[ConversationKey]metadb.ConversationState
	latest  map[metadb.ConversationKey]LastMessage
	upserts []metadb.ConversationState
	deletes []metadb.ConversationDelete
}

func newConversationMutationStore() *conversationMutationStore {
	return &conversationMutationStore{
		states: make(map[ConversationKey]metadb.ConversationState),
		latest: make(map[metadb.ConversationKey]LastMessage),
	}
}

func (s *conversationMutationStore) ListConversationActiveView(context.Context, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) (ActiveViewPage, error) {
	return ActiveViewPage{Done: true}, nil
}

func (s *conversationMutationStore) GetConversationState(_ context.Context, kind metadb.ConversationKind, uid, channelID string, channelType int64) (metadb.ConversationState, bool, error) {
	state, ok := s.states[ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if ok && state.Kind != kind {
		return metadb.ConversationState{}, false, nil
	}
	if ok {
		state.UID = uid
		state.Kind = kind
	}
	return state, ok, nil
}

func (s *conversationMutationStore) GetLastVisibleMessages(_ context.Context, requests []LastVisibleMessageRequest) (map[metadb.ConversationKey]LastMessage, error) {
	out := make(map[metadb.ConversationKey]LastMessage, len(requests))
	for _, req := range requests {
		key := metadb.ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}
		msg, ok := s.latest[key]
		if ok && msg.MessageSeq > req.VisibleAfterSeq {
			out[key] = msg
		}
	}
	return out, nil
}

func (s *conversationMutationStore) UpsertConversationStates(_ context.Context, states []metadb.ConversationState) error {
	s.upserts = append(s.upserts, states...)
	return nil
}

func (s *conversationMutationStore) HideConversations(_ context.Context, reqs []metadb.ConversationDelete) error {
	s.deletes = append(s.deletes, reqs...)
	return nil
}
