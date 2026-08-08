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
	store.head.LastCommittedSeq = 12
	app := New(Options{Hydrator: store, MembershipMutations: store, Now: func() time.Time { return now }})

	if err := app.ClearUnread(context.Background(), ClearUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); err != nil {
		t.Fatalf("ClearUnread() error = %v", err)
	}

	want := []membershipReadMutation{{uid: "u1", channelID: "g1", channelType: 2, readSeq: 12, updatedAt: now.UnixNano()}}
	if !reflect.DeepEqual(store.readMutations, want) {
		t.Fatalf("read mutations = %#v, want %#v", store.readMutations, want)
	}
}

func TestSetUnreadAdvancesReadSeqToKeepRequestedUnreadTail(t *testing.T) {
	now := time.Unix(0, 456)
	store := newConversationMutationStore()
	store.head.LastCommittedSeq = 12
	app := New(Options{Hydrator: store, MembershipMutations: store, Now: func() time.Time { return now }})

	if err := app.SetUnread(context.Background(), SetUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 3}); err != nil {
		t.Fatalf("SetUnread() error = %v", err)
	}

	if len(store.readMutations) != 1 || store.readMutations[0].readSeq != 9 || store.readMutations[0].updatedAt != now.UnixNano() {
		t.Fatalf("read mutations = %#v, want read seq 9 with fixed updated time", store.readMutations)
	}
}

func TestDeleteConversationHidesThroughLatestMessage(t *testing.T) {
	now := time.Unix(0, 789)
	store := newConversationMutationStore()
	store.head.LastCommittedSeq = 12
	app := New(Options{Hydrator: store, MembershipMutations: store, Now: func() time.Time { return now }})

	if err := app.DeleteConversation(context.Background(), DeleteConversationCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); err != nil {
		t.Fatalf("DeleteConversation() error = %v", err)
	}

	want := []membershipHideMutation{{uid: "u1", channelID: "g1", channelType: 2, deletedToSeq: 12, updatedAt: now.UnixNano()}}
	if !reflect.DeepEqual(store.hideMutations, want) {
		t.Fatalf("hide mutations = %#v, want %#v", store.hideMutations, want)
	}
}

func TestActivateConversationOnlyRaisesMembershipPriorityOnExplicitCommand(t *testing.T) {
	now := time.Unix(0, 999)
	store := newConversationMutationStore()
	app := New(Options{MembershipMutations: store, Now: func() time.Time { return now }})
	if err := app.ActivateConversation(context.Background(), ActivateConversationCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); err != nil {
		t.Fatalf("ActivateConversation() error = %v", err)
	}
	if len(store.activationMutations) != 1 || store.activationMutations[0].activatedAt != now.UnixNano() {
		t.Fatalf("activation mutations = %#v", store.activationMutations)
	}
}

type membershipReadMutation struct {
	uid, channelID string
	channelType    int64
	readSeq        uint64
	updatedAt      int64
}

type membershipHideMutation struct {
	uid, channelID string
	channelType    int64
	deletedToSeq   uint64
	updatedAt      int64
}

type membershipActivationMutation struct {
	uid, channelID string
	channelType    int64
	activatedAt    int64
	updatedAt      int64
}

type conversationMutationStore struct {
	membership          metadb.UserChannelMembership
	head                HydrationResult
	readMutations       []membershipReadMutation
	hideMutations       []membershipHideMutation
	activationMutations []membershipActivationMutation
}

func newConversationMutationStore() *conversationMutationStore {
	return &conversationMutationStore{
		membership: metadb.UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1},
		head:       HydrationResult{Key: ConversationKey{ChannelID: "g1", ChannelType: 2}, Outcome: HydrationNoVisibleMessage},
	}
}

func (s *conversationMutationStore) GetUserChannelMembership(_ context.Context, _, _ string, _ int64) (metadb.UserChannelMembership, bool, error) {
	return s.membership, true, nil
}

func (s *conversationMutationStore) HydrateConversationHeads(_ context.Context, _ string, _ []metadb.UserChannelMembership) ([]HydrationResult, error) {
	return []HydrationResult{s.head}, nil
}

func (s *conversationMutationStore) AdvanceUserChannelMembershipReadSeq(_ context.Context, uid, channelID string, channelType int64, readSeq uint64, updatedAt int64) error {
	s.readMutations = append(s.readMutations, membershipReadMutation{uid: uid, channelID: channelID, channelType: channelType, readSeq: readSeq, updatedAt: updatedAt})
	return nil
}

func (s *conversationMutationStore) HideUserChannelMembership(_ context.Context, uid, channelID string, channelType int64, deletedToSeq uint64, updatedAt int64) error {
	s.hideMutations = append(s.hideMutations, membershipHideMutation{uid: uid, channelID: channelID, channelType: channelType, deletedToSeq: deletedToSeq, updatedAt: updatedAt})
	return nil
}

func (s *conversationMutationStore) ActivateUserChannelMembership(_ context.Context, uid, channelID string, channelType int64, activatedAt, updatedAt int64) error {
	s.activationMutations = append(s.activationMutations, membershipActivationMutation{uid: uid, channelID: channelID, channelType: channelType, activatedAt: activatedAt, updatedAt: updatedAt})
	return nil
}
