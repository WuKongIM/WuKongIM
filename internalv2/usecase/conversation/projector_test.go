package conversation

import (
	"context"
	"reflect"
	"testing"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	testChannelTypePerson uint8 = 1
	testChannelTypeGroup  uint8 = 2
)

func TestProjectorPersonalMessageTouchesSenderAndPeer(t *testing.T) {
	store := &recordingConversationBatchStore{}
	projector := NewProjector(ProjectorOptions{Store: store})
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")

	err := projector.HandleCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         channelID,
		ChannelType:       testChannelTypePerson,
		FromUID:           "u1",
		MessageSeq:        42,
		ServerTimestampMS: 1000,
	})
	if err != nil {
		t.Fatalf("HandleCommitted() error = %v", err)
	}

	want := []metadb.UserConversationState{
		{UID: "u1", ChannelID: channelID, ChannelType: int64(testChannelTypePerson), ActiveAt: 1000, UpdatedAt: 1000},
		{UID: "u2", ChannelID: channelID, ChannelType: int64(testChannelTypePerson), ActiveAt: 1000, UpdatedAt: 1000},
	}
	if !reflect.DeepEqual(store.states, want) {
		t.Fatalf("states = %#v, want %#v", store.states, want)
	}
}

func TestProjectorSmallGroupFansOutMembers(t *testing.T) {
	members := &recordingMemberSource{classes: map[string]MemberClass{
		"g1": {
			IsSmall: true,
			Members: []Member{
				{UID: "u1"},
				{UID: "u2"},
				{UID: "u3"},
			},
		},
	}}
	store := &recordingConversationBatchStore{}
	projector := NewProjector(ProjectorOptions{Store: store, Members: members, SmallGroupFanoutLimit: 3})

	err := projector.HandleCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "g1",
		ChannelType:       testChannelTypeGroup,
		FromUID:           "u1",
		MessageSeq:        7,
		ServerTimestampMS: 2000,
	})
	if err != nil {
		t.Fatalf("HandleCommitted() error = %v", err)
	}

	if got, want := members.calls, []memberSourceCall{{channelID: "g1", channelType: int64(testChannelTypeGroup), limit: 4}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("member calls = %#v, want %#v", got, want)
	}
	want := []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: int64(testChannelTypeGroup), ActiveAt: 2000, UpdatedAt: 2000},
		{UID: "u2", ChannelID: "g1", ChannelType: int64(testChannelTypeGroup), ActiveAt: 2000, UpdatedAt: 2000},
		{UID: "u3", ChannelID: "g1", ChannelType: int64(testChannelTypeGroup), ActiveAt: 2000, UpdatedAt: 2000},
	}
	if !reflect.DeepEqual(store.states, want) {
		t.Fatalf("states = %#v, want %#v", store.states, want)
	}
}

func TestProjectorLargeGroupTouchesOnlySender(t *testing.T) {
	members := &recordingMemberSource{classes: map[string]MemberClass{
		"g-large": {IsSmall: false},
	}}
	store := &recordingConversationBatchStore{}
	projector := NewProjector(ProjectorOptions{Store: store, Members: members, SmallGroupFanoutLimit: 2})

	err := projector.HandleCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "g-large",
		ChannelType:       testChannelTypeGroup,
		FromUID:           "sender",
		MessageSeq:        9,
		ServerTimestampMS: 3000,
	})
	if err != nil {
		t.Fatalf("HandleCommitted() error = %v", err)
	}

	want := []metadb.UserConversationState{{
		UID:          "sender",
		ChannelID:    "g-large",
		ChannelType:  int64(testChannelTypeGroup),
		ActiveAt:     3000,
		UpdatedAt:    3000,
		SparseActive: true,
	}}
	if !reflect.DeepEqual(store.states, want) {
		t.Fatalf("states = %#v, want %#v", store.states, want)
	}
}

func TestProjectorInitializesJoinFloor(t *testing.T) {
	members := &recordingMemberSource{classes: map[string]MemberClass{
		"g-join": {
			IsSmall: true,
			Members: []Member{
				{UID: "u1", JoinSeq: 11},
				{UID: "u2"},
			},
		},
	}}
	store := &recordingConversationBatchStore{}
	projector := NewProjector(ProjectorOptions{Store: store, Members: members, SmallGroupFanoutLimit: 2})

	err := projector.HandleCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "g-join",
		ChannelType:       testChannelTypeGroup,
		FromUID:           "u1",
		MessageSeq:        12,
		ServerTimestampMS: 4000,
	})
	if err != nil {
		t.Fatalf("HandleCommitted() error = %v", err)
	}

	want := []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g-join", ChannelType: int64(testChannelTypeGroup), ReadSeq: 10, DeletedToSeq: 10, ActiveAt: 4000, UpdatedAt: 4000},
		{UID: "u2", ChannelID: "g-join", ChannelType: int64(testChannelTypeGroup), ActiveAt: 4000, UpdatedAt: 4000},
	}
	if !reflect.DeepEqual(store.states, want) {
		t.Fatalf("states = %#v, want %#v", store.states, want)
	}
}

type memberSourceCall struct {
	channelID   string
	channelType int64
	limit       int
}

type recordingMemberSource struct {
	calls   []memberSourceCall
	classes map[string]MemberClass
}

func (s *recordingMemberSource) ClassifyMembers(_ context.Context, channelID string, channelType int64, limit int) (MemberClass, error) {
	s.calls = append(s.calls, memberSourceCall{channelID: channelID, channelType: channelType, limit: limit})
	return s.classes[channelID], nil
}

type recordingConversationBatchStore struct {
	states []metadb.UserConversationState
}

func (s *recordingConversationBatchStore) UpsertUserConversationStatesBatch(_ context.Context, states []metadb.UserConversationState) error {
	s.states = append(s.states, states...)
	return nil
}
