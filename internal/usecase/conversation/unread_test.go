package conversation

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClearUnreadAdvancesReadSeqToLatestMessage(t *testing.T) {
	now := time.Unix(0, 123)
	store := newConversationMutationStore()
	store.head.ReadThroughSeq = 12
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
	store.head.ReadThroughSeq = 12
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
	store.head.ReadThroughSeq = 12
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
	missing             bool
	membershipErr       error
	hydrationErr        error
	hydrationCalls      int
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
	return s.membership, !s.missing, s.membershipErr
}

func (s *conversationMutationStore) HydrateConversationHeads(_ context.Context, _ string, _ []metadb.UserChannelMembership, keepUnread ...uint64) ([]HydrationResult, error) {
	s.hydrationCalls++
	return []HydrationResult{s.head}, s.hydrationErr
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

func TestUnreadEmptyConversationIsIdempotent(t *testing.T) {
	for _, state := range []string{"missing", "tombstone", "missing channel", "empty channel"} {
		t.Run(state, func(t *testing.T) {
			store := newConversationMutationStore()
			switch state {
			case "missing":
				store.missing = true
			case "tombstone":
				store.membership.Tombstone = true
			case "missing channel":
				store.head.Outcome = HydrationDelete
			}
			app := New(Options{Hydrator: store, MembershipMutations: store})
			for attempt := 0; attempt < 2; attempt++ {
				if err := app.ClearUnread(context.Background(), ClearUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); err != nil {
					t.Fatal(err)
				}
				for _, unread := range []int{0, 3} {
					if err := app.SetUnread(context.Background(), SetUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: unread}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(store.readMutations)+len(store.hideMutations)+len(store.activationMutations) != 0 {
				t.Fatal("empty conversation mutated membership")
			}
			if (state == "missing" || state == "tombstone") && store.hydrationCalls != 0 {
				t.Fatal("absent membership triggered a channel read")
			}
		})
	}
}

func TestUnreadEmptyConversationDoesNotHideFailures(t *testing.T) {
	for _, stage := range []string{"membership", "hydration", "route"} {
		t.Run(stage, func(t *testing.T) {
			store := newConversationMutationStore()
			want := metadb.ErrNotFound
			switch stage {
			case "membership":
				store.membershipErr = want
			case "hydration":
				store.hydrationErr = want
			case "route":
				store.head.Outcome = HydrationRetryable
				want = ErrRouteNotReady
			}
			app := New(Options{Hydrator: store, MembershipMutations: store})
			if err := app.ClearUnread(context.Background(), ClearUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); !errors.Is(err, want) {
				t.Fatalf("clear error = %v, want %v", err, want)
			}
			if err := app.SetUnread(context.Background(), SetUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2}); !errors.Is(err, want) {
				t.Fatalf("set error = %v, want %v", err, want)
			}
			if len(store.readMutations) != 0 {
				t.Fatal("failed read caused mutation")
			}
		})
	}
}

func (h *conversationMutationStore) HydratePersistedConversationHeads(ctx context.Context, uid string, rows []metadb.UserChannelMembership) ([]HydrationResult, error) {
	return h.HydrateConversationHeads(ctx, uid, rows)
}
