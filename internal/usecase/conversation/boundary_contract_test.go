package conversation

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateListRequestRejectsMalformedPaginationState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  ListRequest
	}{
		{name: "missing uid", req: ListRequest{}},
		{name: "negative coverage", req: ListRequest{UID: "u1", CompletedCoverage: -1}},
		{name: "negative limit", req: ListRequest{UID: "u1", Limit: -1}},
		{name: "oversized limit", req: ListRequest{UID: "u1", Limit: maxListLimit + 1}},
		{name: "negative cursor time", req: ListRequest{UID: "u1", Cursor: Cursor{ActiveAt: -1, ChannelID: "c1", ChannelType: 2}}},
		{name: "cursor missing channel", req: ListRequest{UID: "u1", Cursor: Cursor{ActiveAt: 1, ChannelType: 2}}},
		{name: "cursor missing type", req: ListRequest{UID: "u1", Cursor: Cursor{ActiveAt: 1, ChannelID: "c1"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateListRequest(test.req); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validateListRequest(%+v) = %v, want ErrInvalidRequest", test.req, err)
			}
		})
	}
	if err := validateListRequest(ListRequest{UID: "u1", Limit: maxListLimit}); err != nil {
		t.Fatalf("validateListRequest(valid boundary): %v", err)
	}
}

func TestLegacyConversationPagePreservesLegacyPagingBounds(t *testing.T) {
	t.Parallel()

	items := make([]Conversation, 520)
	for index := range items {
		items[index].ChannelID = string(rune(index + 1))
	}

	if got := legacyConversationPage(items, 0, 1); len(got) != len(items) {
		t.Fatalf("disabled paging returned %d items, want %d", len(got), len(items))
	}
	if got := legacyConversationPage(items, 1, 0); len(got) != 100 {
		t.Fatalf("default page size returned %d items, want 100", len(got))
	}
	if got := legacyConversationPage(items, 1, 1000); len(got) != 500 {
		t.Fatalf("capped page size returned %d items, want 500", len(got))
	}
	if got := legacyConversationPage(items, 2, 500); len(got) != 20 || got[0].ChannelID != items[500].ChannelID {
		t.Fatalf("last page = %#v, want remaining 20 items", got)
	}
	if got := legacyConversationPage(items, 3, 500); got != nil {
		t.Fatalf("out-of-range page = %#v, want nil", got)
	}
	if got := legacyConversationPage(items, int(^uint(0)>>1), 500); got != nil {
		t.Fatalf("overflowing page = %#v, want nil", got)
	}
}

func TestLegacyConversationHelpersKeepDeterministicOrderAndCursors(t *testing.T) {
	t.Parallel()

	items := []Conversation{
		{ChannelID: "b", ChannelType: 2, ActiveAt: 10},
		{ChannelID: "a", ChannelType: 3, ActiveAt: 10},
		{ChannelID: "a", ChannelType: 1, ActiveAt: 10},
		{ChannelID: "z", ChannelType: 1, ActiveAt: 20},
	}
	sortLegacyConversations(items)
	gotOrder := make([]ConversationKey, len(items))
	for index, item := range items {
		gotOrder[index] = ConversationKey{ChannelID: item.ChannelID, ChannelType: item.ChannelType}
	}
	wantOrder := []ConversationKey{
		{ChannelID: "z", ChannelType: 1},
		{ChannelID: "a", ChannelType: 1},
		{ChannelID: "a", ChannelType: 3},
		{ChannelID: "b", ChannelType: 2},
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("sorted order = %#v, want %#v", gotOrder, wantOrder)
	}

	cursors := legacyConversationCursorMap([]LegacyConversationCursor{
		{ChannelID: "  ", ChannelType: 2, LastMessageSeq: 1},
		{ChannelID: "c1", LastMessageSeq: 2},
		{ChannelID: " c1 ", ChannelType: 2, LastMessageSeq: 3},
		{ChannelID: "c1", ChannelType: 2, LastMessageSeq: 4},
	})
	wantCursors := map[ConversationKey]uint64{{ChannelID: "c1", ChannelType: 2}: 4}
	if !reflect.DeepEqual(cursors, wantCursors) {
		t.Fatalf("cursor map = %#v, want %#v", cursors, wantCursors)
	}
}

func TestCloneLegacyRecentMessagesOwnsMutableResponseState(t *testing.T) {
	t.Parallel()

	original := []LegacyRecentMessage{{
		Payload:    []byte("payload"),
		StreamData: []byte("stream"),
		EventMeta: &LegacyMessageEventMeta{Events: []LegacyMessageEventKeyMeta{{
			EventKey: "typing",
			Status:   "open",
		}}},
		EventHint: &LegacyMessageEventSyncHint{ClientMsgNo: "m1", FromMsgEventSeq: 8},
	}}
	cloned := cloneLegacyRecentMessages(original)
	cloned[0].Payload[0] = 'P'
	cloned[0].StreamData[0] = 'S'
	cloned[0].EventMeta.Events[0].Status = "closed"
	cloned[0].EventHint.FromMsgEventSeq = 9

	if string(original[0].Payload) != "payload" || string(original[0].StreamData) != "stream" {
		t.Fatalf("clone aliased byte slices: %+v", original[0])
	}
	if original[0].EventMeta.Events[0].Status != "open" {
		t.Fatalf("clone aliased event metadata: %+v", original[0].EventMeta)
	}
	if original[0].EventHint.FromMsgEventSeq != 8 {
		t.Fatalf("clone aliased event hint: %+v", original[0].EventHint)
	}
	if got := cloneLegacyMessageEventMeta(nil); got != nil {
		t.Fatalf("cloneLegacyMessageEventMeta(nil) = %+v, want nil", got)
	}
}

func TestLegacyEffectiveReadSeqDoesNotUnderflowUnreadCount(t *testing.T) {
	t.Parallel()

	if got := legacyEffectiveReadSeq(Conversation{ReadSeq: 7}); got != 7 {
		t.Fatalf("missing message read seq = %d, want 7", got)
	}
	if got := legacyEffectiveReadSeq(Conversation{ReadSeq: 7, Unread: 9, LastMessage: &LastMessage{MessageSeq: 8}}); got != 7 {
		t.Fatalf("oversized unread read seq = %d, want 7", got)
	}
	if got := legacyEffectiveReadSeq(Conversation{ReadSeq: 7, Unread: 2, LastMessage: &LastMessage{MessageSeq: 12}}); got != 10 {
		t.Fatalf("derived read seq = %d, want 10", got)
	}
}
