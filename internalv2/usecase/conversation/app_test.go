package conversation

import (
	"context"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListUsesActivePageAndLoadsLastMessages(t *testing.T) {
	store := &recordingActiveStore{
		rows: []metadb.UserConversationState{
			{UID: "u1", ChannelID: "g-a", ChannelType: 2, ActiveAt: 300, UpdatedAt: 301, ReadSeq: 8},
			{UID: "u1", ChannelID: "g-b", ChannelType: 2, ActiveAt: 200, UpdatedAt: 201, ReadSeq: 19},
			{UID: "u1", ChannelID: "g-c", ChannelType: 2, ActiveAt: 100, UpdatedAt: 101},
		},
	}
	messages := &recordingMessageStore{rows: map[metadb.ConversationKey]LastMessage{
		{ChannelID: "g-a", ChannelType: 2}: {MessageID: 30, MessageSeq: 10, FromUID: "u-a", ClientMsgNo: "c-a", ServerTimestampMS: 900, Payload: []byte("a")},
		{ChannelID: "g-b", ChannelType: 2}: {MessageID: 20, MessageSeq: 20, FromUID: "u-b", ClientMsgNo: "c-b", ServerTimestampMS: 800, Payload: []byte("b")},
		{ChannelID: "g-c", ChannelType: 2}: {MessageID: 10, MessageSeq: 1, FromUID: "u-c", ClientMsgNo: "c-c", ServerTimestampMS: 700, Payload: []byte("c")},
	}}
	app := New(Options{Store: store, Messages: messages})

	first, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 2})
	if err != nil {
		t.Fatalf("List(first) error = %v", err)
	}
	if got, want := conversationIDs(first.Items), []string{"g-a", "g-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first conversation IDs = %#v, want %#v", got, want)
	}
	if !first.HasMore {
		t.Fatalf("first HasMore = false, want active page has more")
	}
	if got, want := first.NextCursor, (Cursor{ActiveAt: 200, ChannelID: "g-b", ChannelType: 2}); got != want {
		t.Fatalf("first cursor = %#v, want %#v", got, want)
	}
	if got, want := store.calls, []activePageCall{{uid: "u1", after: metadb.UserConversationActiveCursor{}, limit: 3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active page calls = %#v, want %#v", got, want)
	}
	if got, want := messages.calls, [][]LastVisibleMessageRequest{{{
		ChannelID: "g-a", ChannelType: 2, VisibleAfterSeq: 0,
	}, {
		ChannelID: "g-b", ChannelType: 2, VisibleAfterSeq: 0,
	}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("last message calls = %#v, want %#v", got, want)
	}
	if got := first.Items[0].LastMessage; got == nil || got.MessageSeq != 10 || string(got.Payload) != "a" {
		t.Fatalf("first last message = %#v, want g-a message", got)
	}

	second, err := app.List(context.Background(), ListRequest{UID: "u1", Cursor: first.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("List(second) error = %v", err)
	}
	if got, want := conversationIDs(second.Items), []string{"g-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second conversation IDs = %#v, want %#v", got, want)
	}
	if second.HasMore {
		t.Fatalf("second HasMore = true, want false")
	}
	if got, want := store.calls[1], (activePageCall{uid: "u1", after: metadb.UserConversationActiveCursor{ActiveAt: 200, ChannelID: "g-b", ChannelType: 2}, limit: 3}); got != want {
		t.Fatalf("second active page call = %#v, want %#v", got, want)
	}
}

func TestListKeepsSparseOrderFromActiveAt(t *testing.T) {
	store := &recordingActiveStore{
		rows: []metadb.UserConversationState{
			{UID: "u1", ChannelID: "dense", ChannelType: 2, ActiveAt: 500},
			{UID: "u1", ChannelID: "sparse", ChannelType: 2, ActiveAt: 100, SparseActive: true},
		},
	}
	messages := &recordingMessageStore{rows: map[metadb.ConversationKey]LastMessage{
		{ChannelID: "dense", ChannelType: 2}:  {MessageID: 1, MessageSeq: 1, ServerTimestampMS: 600},
		{ChannelID: "sparse", ChannelType: 2}: {MessageID: 2, MessageSeq: 2, ServerTimestampMS: 900},
	}}
	app := New(Options{Store: store, Messages: messages})

	got, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if ids := conversationIDs(got.Items); !reflect.DeepEqual(ids, []string{"dense", "sparse"}) {
		t.Fatalf("conversation IDs = %#v, want active_at order", ids)
	}
	if !got.Items[1].SparseActive {
		t.Fatalf("SparseActive = false, want true on sparse row")
	}
}

func TestListCalculatesUnreadFromReadAndDeletedFloor(t *testing.T) {
	store := &recordingActiveStore{
		rows: []metadb.UserConversationState{
			{UID: "u1", ChannelID: "g-a", ChannelType: 2, ActiveAt: 300, ReadSeq: 7, DeletedToSeq: 10},
			{UID: "u1", ChannelID: "g-b", ChannelType: 2, ActiveAt: 200, ReadSeq: 30, DeletedToSeq: 5},
		},
	}
	messages := &recordingMessageStore{rows: map[metadb.ConversationKey]LastMessage{
		{ChannelID: "g-a", ChannelType: 2}: {MessageID: 1, MessageSeq: 15},
		{ChannelID: "g-b", ChannelType: 2}: {MessageID: 2, MessageSeq: 20},
	}}
	app := New(Options{Store: store, Messages: messages})

	got, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got.Items[0].Unread != 5 {
		t.Fatalf("g-a Unread = %d, want 5 from last seq 15 minus deleted floor 10", got.Items[0].Unread)
	}
	if got.Items[1].Unread != 0 {
		t.Fatalf("g-b Unread = %d, want 0 when read seq is beyond last seq", got.Items[1].Unread)
	}
	if got, want := messages.calls[0][0].VisibleAfterSeq, uint64(10); got != want {
		t.Fatalf("VisibleAfterSeq = %d, want deleted_to_seq", got)
	}
}

func TestListReturnsConversationWhenLastMessageMissing(t *testing.T) {
	store := &recordingActiveStore{
		rows: []metadb.UserConversationState{
			{UID: "u1", ChannelID: "g-a", ChannelType: 2, ActiveAt: 300, ReadSeq: 3},
		},
	}
	app := New(Options{Store: store, Messages: &recordingMessageStore{rows: map[metadb.ConversationKey]LastMessage{}}})

	got, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(got.Items))
	}
	if got.Items[0].LastMessage != nil || got.Items[0].Unread != 0 {
		t.Fatalf("LastMessage=%#v Unread=%d, want missing message kept with zero unread", got.Items[0].LastMessage, got.Items[0].Unread)
	}
}

func TestListClonesPayload(t *testing.T) {
	payload := []byte("stable")
	store := &recordingActiveStore{
		rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g-a", ChannelType: 2, ActiveAt: 300}},
	}
	messages := &recordingMessageStore{rows: map[metadb.ConversationKey]LastMessage{
		{ChannelID: "g-a", ChannelType: 2}: {MessageID: 1, MessageSeq: 1, Payload: payload},
	}}
	app := New(Options{Store: store, Messages: messages})

	got, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	payload[0] = 'X'
	messages.rows[metadb.ConversationKey{ChannelID: "g-a", ChannelType: 2}] = LastMessage{MessageID: 1, MessageSeq: 1, Payload: []byte("mutated")}
	got.Items[0].LastMessage.Payload[1] = 'Y'

	again, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("List(again) error = %v", err)
	}
	if string(again.Items[0].LastMessage.Payload) != "mutated" {
		t.Fatalf("stored payload was aliased by previous result: %q", again.Items[0].LastMessage.Payload)
	}
}

func conversationIDs(items []Conversation) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ChannelID)
	}
	return out
}

type activePageCall struct {
	uid   string
	after metadb.UserConversationActiveCursor
	limit int
}

type recordingActiveStore struct {
	rows  []metadb.UserConversationState
	calls []activePageCall
}

func (s *recordingActiveStore) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	s.calls = append(s.calls, activePageCall{uid: uid, after: after, limit: limit})
	start := 0
	if after != (metadb.UserConversationActiveCursor{}) {
		for start < len(s.rows) {
			row := s.rows[start]
			start++
			if row.ActiveAt == after.ActiveAt && row.ChannelID == after.ChannelID && row.ChannelType == after.ChannelType {
				break
			}
		}
	}
	page := make([]metadb.UserConversationState, 0, limit)
	var cursor metadb.UserConversationActiveCursor
	for start < len(s.rows) && len(page) < limit {
		row := s.rows[start]
		start++
		if row.UID != uid {
			continue
		}
		page = append(page, row)
		cursor = metadb.UserConversationActiveCursor{ActiveAt: row.ActiveAt, ChannelID: row.ChannelID, ChannelType: row.ChannelType}
	}
	return page, cursor, start >= len(s.rows), nil
}

type recordingMessageStore struct {
	rows  map[metadb.ConversationKey]LastMessage
	calls [][]LastVisibleMessageRequest
}

func (s *recordingMessageStore) GetLastVisibleMessages(_ context.Context, requests []LastVisibleMessageRequest) (map[metadb.ConversationKey]LastMessage, error) {
	s.calls = append(s.calls, append([]LastVisibleMessageRequest(nil), requests...))
	out := make(map[metadb.ConversationKey]LastMessage)
	for _, req := range requests {
		key := metadb.ConversationKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}
		msg, ok := s.rows[key]
		if !ok || msg.MessageSeq <= req.VisibleAfterSeq {
			continue
		}
		msg.Payload = append([]byte(nil), msg.Payload...)
		out[key] = msg
	}
	return out, nil
}
