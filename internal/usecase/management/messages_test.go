package management

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListMessagesMapsQueryPage(t *testing.T) {
	reader := &fakeMessageReader{
		result: MessageQueryPage{
			Items: []Message{{
				MessageID: 101, MessageSeq: 9, ClientMsgNo: "c-101",
				ChannelID: "room-1", ChannelType: 2, FromUID: "u1",
				Timestamp: 1713859200, Payload: []byte("hello"),
			}},
			HasMore:       true,
			NextBeforeSeq: 9,
		},
	}
	app := New(Options{Messages: reader})

	got, err := app.ListMessages(context.Background(), ListMessagesRequest{
		ChannelID: " room-1 ", ChannelType: 2, Limit: 1,
		Cursor: MessageListCursor{BeforeSeq: 12}, MessageID: 101, ClientMsgNo: "c-101",
	})

	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	wantReq := MessageQueryRequest{ChannelID: "room-1", ChannelType: 2, Limit: 1, BeforeSeq: 12, MessageID: 101, ClientMsgNo: "c-101"}
	if reader.lastReq != wantReq {
		t.Fatalf("query = %#v, want %#v", reader.lastReq, wantReq)
	}
	if !got.HasMore || got.NextCursor.BeforeSeq != 9 || !sameMessages(got.Items, reader.result.Items) {
		t.Fatalf("result = %#v, want mapped page %#v", got, reader.result)
	}
}

func TestListMessagesMapsClusterLatestPage(t *testing.T) {
	reader := &fakeLatestMessageReader{result: LatestMessageQueryPage{
		Items:   []Message{{MessageID: 103, MessageSeq: 7, ChannelID: "room-2", ChannelType: 2}},
		HasMore: true, NextBeforeMessageID: 103,
	}}
	app := New(Options{LatestMessages: reader})

	got, err := app.ListMessages(context.Background(), ListMessagesRequest{
		Limit: 1, Cursor: MessageListCursor{BeforeMessageID: 110},
	})
	if err != nil {
		t.Fatalf("ListMessages(latest) error = %v", err)
	}
	if reader.lastReq != (LatestMessageQueryRequest{BeforeMessageID: 110, Limit: 1}) {
		t.Fatalf("latest request = %#v", reader.lastReq)
	}
	if len(got.Items) != 1 || got.Items[0].MessageID != 103 || !got.HasMore || got.NextCursor.BeforeMessageID != 103 {
		t.Fatalf("latest result = %#v", got)
	}
}

func TestListMessagesRejectsInvalidRequest(t *testing.T) {
	app := New(Options{Messages: &fakeMessageReader{}, LatestMessages: &fakeLatestMessageReader{}})
	for _, req := range []ListMessagesRequest{
		{ChannelID: "", ChannelType: 2, Limit: 10},
		{ChannelID: "", Limit: 10, MessageID: 1},
		{ChannelID: "", Limit: 10, Cursor: MessageListCursor{BeforeSeq: 2}},
		{ChannelID: "room-1", ChannelType: 0, Limit: 10},
		{ChannelID: "room-1", ChannelType: 256, Limit: 10},
		{ChannelID: "room-1", ChannelType: 2, Limit: 0},
		{ChannelID: "room-1", ChannelType: 2, Limit: 10, Cursor: MessageListCursor{BeforeMessageID: 2}},
	} {
		_, err := app.ListMessages(context.Background(), req)
		if !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("ListMessages(%#v) error = %v, want %v", req, err, metadb.ErrInvalidArgument)
		}
	}
}

func TestListMessagesPropagatesReaderErrors(t *testing.T) {
	wantErr := errors.New("boom")
	app := New(Options{Messages: &fakeMessageReader{err: wantErr}})

	_, err := app.ListMessages(context.Background(), ListMessagesRequest{
		ChannelID: "room-1", ChannelType: 2, Limit: 10,
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("ListMessages() error = %v, want %v", err, wantErr)
	}
}

func TestAdvanceMessageRetentionDelegatesToPort(t *testing.T) {
	operator := &fakeMessageRetentionOperator{
		result: AdvanceMessageRetentionResponse{
			ChannelID: "room-1", ChannelType: 2, RequestedThroughSeq: 10,
			AdvancedThroughSeq: 8, MinAvailableSeq: 9, Status: MessageRetentionStatusAdvanced,
		},
	}
	app := New(Options{MessageRetention: operator})

	got, err := app.AdvanceMessageRetention(context.Background(), AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10, DryRun: true,
	})

	if err != nil {
		t.Fatalf("AdvanceMessageRetention() error = %v", err)
	}
	wantReq := AdvanceMessageRetentionRequest{ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10, DryRun: true}
	if operator.lastReq != wantReq {
		t.Fatalf("retention request = %#v, want %#v", operator.lastReq, wantReq)
	}
	if got != operator.result {
		t.Fatalf("retention result = %#v, want %#v", got, operator.result)
	}
}

func TestAdvanceMessageRetentionRejectsInvalidRequest(t *testing.T) {
	app := New(Options{})

	_, err := app.AdvanceMessageRetention(context.Background(), AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2,
	})

	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("AdvanceMessageRetention() error = %v, want %v", err, metadb.ErrInvalidArgument)
	}
}

func TestAdvanceMessageRetentionReturnsUnavailableWhenPortMissing(t *testing.T) {
	app := New(Options{})

	_, err := app.AdvanceMessageRetention(context.Background(), AdvanceMessageRetentionRequest{
		ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10,
	})

	if !errors.Is(err, ErrMessageRetentionUnavailable) {
		t.Fatalf("AdvanceMessageRetention() error = %v, want %v", err, ErrMessageRetentionUnavailable)
	}
}

type fakeMessageReader struct {
	lastReq MessageQueryRequest
	result  MessageQueryPage
	err     error
}

type fakeLatestMessageReader struct {
	lastReq LatestMessageQueryRequest
	result  LatestMessageQueryPage
	err     error
}

func (f *fakeLatestMessageReader) QueryLatestMessages(_ context.Context, req LatestMessageQueryRequest) (LatestMessageQueryPage, error) {
	f.lastReq = req
	return f.result, f.err
}

func (f *fakeMessageReader) QueryMessages(_ context.Context, req MessageQueryRequest) (MessageQueryPage, error) {
	f.lastReq = req
	return f.result, f.err
}

type fakeMessageRetentionOperator struct {
	lastReq AdvanceMessageRetentionRequest
	result  AdvanceMessageRetentionResponse
	err     error
}

func (f *fakeMessageRetentionOperator) AdvanceMessageRetention(_ context.Context, req AdvanceMessageRetentionRequest) (AdvanceMessageRetentionResponse, error) {
	f.lastReq = req
	return f.result, f.err
}
