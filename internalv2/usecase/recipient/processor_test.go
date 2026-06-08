package recipient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

func TestProcessorUpdatesConversationBeforeDelivery(t *testing.T) {
	var order []string
	conversation := &recordingConversationUpdater{
		onAdmit: func([]conversationusecase.ActivePatch) {
			order = append(order, "conversation")
		},
	}
	delivery := &recordingDeliverySubmitter{
		onSubmit: func(messageevents.MessageCommitted) {
			if len(order) == 0 || order[len(order)-1] != "conversation" {
				t.Fatal("delivery happened before conversation update")
			}
			order = append(order, "delivery")
		},
	}
	processor := NewProcessor(ProcessorOptions{
		LocalNodeID:  1,
		Conversation: conversation,
		Delivery:     delivery,
	})

	req := ProcessRequest{
		Target: localTarget(1),
		Event: messageevents.MessageCommitted{
			MessageID:         100,
			MessageSeq:        9,
			ChannelID:         "group-1",
			ChannelType:       2,
			ServerTimestampMS: 1234,
			Payload:           []byte("payload"),
			MessageScopedUIDs: []string{"previous"},
		},
		Recipients: []Recipient{
			{UID: "u1", JoinSeq: 2},
			{UID: "u2"},
		},
	}

	if err := processor.Process(context.Background(), req); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	wantOrder := []string{"conversation", "delivery"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order = %#v, want %#v", order, wantOrder)
	}
	wantPatches := []conversationusecase.ActivePatch{
		{
			UID:          "u1",
			ChannelID:    "group-1",
			ChannelType:  2,
			ReadSeq:      1,
			DeletedToSeq: 1,
			ActiveAt:     1234,
			UpdatedAt:    1234,
			MessageSeq:   9,
		},
		{
			UID:         "u2",
			ChannelID:   "group-1",
			ChannelType: 2,
			ActiveAt:    1234,
			UpdatedAt:   1234,
			MessageSeq:  9,
		},
	}
	if !reflect.DeepEqual(conversation.patches, wantPatches) {
		t.Fatalf("conversation patches = %#v, want %#v", conversation.patches, wantPatches)
	}
	wantUIDs := []string{"u1", "u2"}
	if !reflect.DeepEqual(delivery.event.MessageScopedUIDs, wantUIDs) {
		t.Fatalf("delivery MessageScopedUIDs = %#v, want %#v", delivery.event.MessageScopedUIDs, wantUIDs)
	}
	if !reflect.DeepEqual(req.Event.MessageScopedUIDs, []string{"previous"}) {
		t.Fatalf("request event MessageScopedUIDs mutated to %#v", req.Event.MessageScopedUIDs)
	}
}

func TestProcessorSkipsDeliveryWhenConversationFails(t *testing.T) {
	wantErr := errors.New("conversation failed")
	conversation := &recordingConversationUpdater{err: wantErr}
	delivery := &recordingDeliverySubmitter{}
	processor := NewProcessor(ProcessorOptions{
		LocalNodeID:  1,
		Conversation: conversation,
		Delivery:     delivery,
	})

	err := processor.Process(context.Background(), ProcessRequest{
		Target:     localTarget(1),
		Event:      messageevents.MessageCommitted{ChannelID: "group-1", ChannelType: 2},
		Recipients: []Recipient{{UID: "u1"}},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Process() error = %v, want %v", err, wantErr)
	}
	if delivery.called {
		t.Fatal("delivery called after conversation failure")
	}
}

func TestProcessorRejectsStaleTarget(t *testing.T) {
	processor := NewProcessor(ProcessorOptions{
		LocalNodeID:  1,
		Conversation: &recordingConversationUpdater{},
		Delivery:     &recordingDeliverySubmitter{},
	})

	err := processor.Process(context.Background(), ProcessRequest{
		Target:     localTarget(2),
		Event:      messageevents.MessageCommitted{ChannelID: "group-1", ChannelType: 2},
		Recipients: []Recipient{{UID: "u1"}},
	})
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Process() error = %v, want %v", err, ErrNotLeader)
	}
}

func TestProcessorAllowsMissingDependenciesAfterTargetValidation(t *testing.T) {
	processor := NewProcessor(ProcessorOptions{LocalNodeID: 1})

	err := processor.Process(context.Background(), ProcessRequest{
		Target:     localTarget(1),
		Event:      messageevents.MessageCommitted{ChannelID: "group-1", ChannelType: 2},
		Recipients: []Recipient{{UID: "u1"}},
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
}

func TestProcessorFiltersEmptyRecipients(t *testing.T) {
	conversation := &recordingConversationUpdater{}
	delivery := &recordingDeliverySubmitter{}
	processor := NewProcessor(ProcessorOptions{
		LocalNodeID:  1,
		Conversation: conversation,
		Delivery:     delivery,
	})

	err := processor.Process(context.Background(), ProcessRequest{
		Target: localTarget(1),
		Event: messageevents.MessageCommitted{
			ChannelID:         "group-1",
			ChannelType:       2,
			MessageScopedUIDs: []string{"previous"},
		},
		Recipients: []Recipient{
			{UID: ""},
			{UID: "u1"},
			{UID: ""},
			{UID: "u2"},
		},
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	wantPatchUIDs := []string{"u1", "u2"}
	var gotPatchUIDs []string
	for _, patch := range conversation.patches {
		gotPatchUIDs = append(gotPatchUIDs, patch.UID)
	}
	if !reflect.DeepEqual(gotPatchUIDs, wantPatchUIDs) {
		t.Fatalf("patch UIDs = %#v, want %#v", gotPatchUIDs, wantPatchUIDs)
	}
	if !reflect.DeepEqual(delivery.event.MessageScopedUIDs, wantPatchUIDs) {
		t.Fatalf("delivery MessageScopedUIDs = %#v, want %#v", delivery.event.MessageScopedUIDs, wantPatchUIDs)
	}
}

func localTarget(nodeID uint64) authority.Target {
	return authority.Target{
		HashSlot:       7,
		SlotID:         3,
		LeaderNodeID:   nodeID,
		RouteRevision:  11,
		AuthorityEpoch: 13,
	}
}

type recordingConversationUpdater struct {
	called  bool
	patches []conversationusecase.ActivePatch
	err     error
	onAdmit func([]conversationusecase.ActivePatch)
}

func (u *recordingConversationUpdater) AdmitPatches(_ context.Context, patches []conversationusecase.ActivePatch) error {
	u.called = true
	u.patches = append([]conversationusecase.ActivePatch(nil), patches...)
	if u.onAdmit != nil {
		u.onAdmit(patches)
	}
	return u.err
}

type recordingDeliverySubmitter struct {
	called   bool
	event    messageevents.MessageCommitted
	onSubmit func(messageevents.MessageCommitted)
}

func (s *recordingDeliverySubmitter) SubmitDelivery(_ context.Context, event messageevents.MessageCommitted) error {
	s.called = true
	s.event = event.Clone()
	if s.onSubmit != nil {
		s.onSubmit(event)
	}
	return nil
}
