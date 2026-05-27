package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestChannelAppenderMapsAppendBatchRequestAndResult(t *testing.T) {
	node := &recordingNode{
		result: channelv2.AppendBatchResult{Items: []channelv2.AppendBatchItemResult{
			{
				MessageID:  10,
				MessageSeq: 101,
				Message: channelv2.Message{
					MessageID:   10,
					MessageSeq:  101,
					ChannelID:   "room",
					ChannelType: 1,
					FromUID:     "u1",
					ClientMsgNo: "m1",
					Payload:     []byte("accepted-1"),
				},
			},
			{
				MessageID:  11,
				MessageSeq: 102,
				Message: channelv2.Message{
					MessageID:   11,
					MessageSeq:  102,
					ChannelID:   "room",
					ChannelType: 1,
					FromUID:     "u2",
					ClientMsgNo: "m2",
					Payload:     []byte("accepted-2"),
				},
			},
		}},
	}
	appender := NewChannelAppender(node)

	res, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{
		ChannelID:  message.ChannelID{ID: "room", Type: 1},
		CommitMode: message.CommitModeQuorum,
		Messages: []message.Message{
			{
				MessageID:   10,
				MessageSeq:  1,
				ChannelID:   "room",
				ChannelType: 1,
				FromUID:     "u1",
				ClientMsgNo: "m1",
				Payload:     []byte("hello"),
			},
			{
				MessageID:   11,
				MessageSeq:  2,
				ChannelID:   "room",
				ChannelType: 1,
				FromUID:     "u2",
				ClientMsgNo: "m2",
				Payload:     []byte("world"),
			},
		},
	})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if node.calls != 1 {
		t.Fatalf("calls = %d, want 1", node.calls)
	}
	req := node.last
	if req.ChannelID.ID != "room" || req.ChannelID.Type != 1 {
		t.Fatalf("ChannelID = %#v, want room/1", req.ChannelID)
	}
	if req.CommitMode != channelv2.CommitModeQuorum {
		t.Fatalf("CommitMode = %v, want %v", req.CommitMode, channelv2.CommitModeQuorum)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(req.Messages))
	}
	assertChannelMessage(t, req.Messages[0], channelv2.Message{
		MessageID:   10,
		MessageSeq:  1,
		ChannelID:   "room",
		ChannelType: 1,
		FromUID:     "u1",
		ClientMsgNo: "m1",
		Payload:     []byte("hello"),
	})
	assertChannelMessage(t, req.Messages[1], channelv2.Message{
		MessageID:   11,
		MessageSeq:  2,
		ChannelID:   "room",
		ChannelType: 1,
		FromUID:     "u2",
		ClientMsgNo: "m2",
		Payload:     []byte("world"),
	})
	if len(res.Items) != 2 {
		t.Fatalf("len(result.Items) = %d, want 2", len(res.Items))
	}
	assertMessageResult(t, res.Items[0], message.AppendBatchItemResult{
		MessageID:  10,
		MessageSeq: 101,
		Message: message.Message{
			MessageID:   10,
			MessageSeq:  101,
			ChannelID:   "room",
			ChannelType: 1,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("accepted-1"),
		},
	})
	assertMessageResult(t, res.Items[1], message.AppendBatchItemResult{
		MessageID:  11,
		MessageSeq: 102,
		Message: message.Message{
			MessageID:   11,
			MessageSeq:  102,
			ChannelID:   "room",
			ChannelType: 1,
			FromUID:     "u2",
			ClientMsgNo: "m2",
			Payload:     []byte("accepted-2"),
		},
	})
}

func TestChannelAppenderClonesPayloadsBothDirections(t *testing.T) {
	node := &recordingNode{
		result: channelv2.AppendBatchResult{Items: []channelv2.AppendBatchItemResult{
			{
				MessageID:  10,
				MessageSeq: 1,
				Message:    channelv2.Message{MessageID: 10, MessageSeq: 1, Payload: []byte("accepted")},
			},
		}},
	}
	appender := NewChannelAppender(node)
	payload := []byte("source")

	res, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{
		ChannelID:  message.ChannelID{ID: "room", Type: 1},
		Messages:   []message.Message{{MessageID: 10, Payload: payload}},
		CommitMode: message.CommitModeQuorum,
	})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}

	payload[0] = 'S'
	node.result.Items[0].Message.Payload[0] = 'A'

	if got := string(node.last.Messages[0].Payload); got != "source" {
		t.Fatalf("sent payload = %q, want cloned source", got)
	}
	if got := string(res.Items[0].Message.Payload); got != "accepted" {
		t.Fatalf("result payload = %q, want cloned accepted", got)
	}
}

func TestChannelAppenderMapsCommitModes(t *testing.T) {
	cases := []struct {
		name string
		in   message.CommitMode
		want channelv2.CommitMode
	}{
		{name: "quorum", in: message.CommitModeQuorum, want: channelv2.CommitModeQuorum},
		{name: "local", in: message.CommitModeLocal, want: channelv2.CommitModeLocal},
		{name: "default", in: message.CommitMode(0), want: channelv2.CommitModeQuorum},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &recordingNode{}
			appender := NewChannelAppender(node)
			_, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{
				ChannelID:  message.ChannelID{ID: "room", Type: 1},
				CommitMode: tc.in,
			})
			if err != nil {
				t.Fatalf("AppendBatch() error = %v", err)
			}
			if node.last.CommitMode != tc.want {
				t.Fatalf("CommitMode = %v, want %v", node.last.CommitMode, tc.want)
			}
		})
	}
}

func TestChannelAppenderRequiresNode(t *testing.T) {
	var nilAppender *ChannelAppender
	if _, err := nilAppender.AppendBatch(context.Background(), message.AppendBatchRequest{}); !errors.Is(err, message.ErrAppenderRequired) {
		t.Fatalf("nil appender error = %v, want %v", err, message.ErrAppenderRequired)
	}

	appender := NewChannelAppender(nil)
	if _, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{}); !errors.Is(err, message.ErrAppenderRequired) {
		t.Fatalf("nil node error = %v, want %v", err, message.ErrAppenderRequired)
	}
}

func TestChannelAppenderMapsTypedErrors(t *testing.T) {
	unknown := errors.New("boom")
	cases := []struct {
		name      string
		err       error
		want      error
		unchanged bool
	}{
		{name: "clusterv2 not leader", err: clusterv2.ErrNotLeader, want: message.ErrNotLeader},
		{name: "channelv2 not leader", err: channelv2.ErrNotLeader, want: message.ErrNotLeader},
		{name: "stale meta", err: channelv2.ErrStaleMeta, want: message.ErrStaleRoute},
		{name: "channel missing", err: channelv2.ErrChannelNotFound, want: message.ErrChannelNotFound},
		{name: "backpressured", err: channelv2.ErrBackpressured, want: message.ErrBackpressured},
		{name: "clusterv2 route not ready", err: clusterv2.ErrRouteNotReady, want: message.ErrRouteNotReady},
		{name: "channelv2 not ready", err: channelv2.ErrNotReady, want: message.ErrRouteNotReady},
		{name: "context canceled", err: context.Canceled, want: context.Canceled, unchanged: true},
		{name: "context deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded, unchanged: true},
		{name: "unknown", err: unknown, want: message.ErrAppendFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			appender := NewChannelAppender(&recordingNode{err: tc.err})
			_, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{
				ChannelID: message.ChannelID{ID: "room", Type: 1},
				Messages:  []message.Message{{MessageID: 1, Payload: []byte("x")}},
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("AppendBatch() error = %v, want %v", err, tc.want)
			}
			if tc.unchanged && err != tc.err {
				t.Fatalf("AppendBatch() error = %v, want unchanged %v", err, tc.err)
			}
			if tc.err == unknown && !errors.Is(err, unknown) {
				t.Fatalf("AppendBatch() error = %v, want source wrapped", err)
			}
		})
	}
}

func TestChannelAppenderMapsItemErrors(t *testing.T) {
	node := &recordingNode{
		result: channelv2.AppendBatchResult{Items: []channelv2.AppendBatchItemResult{
			{MessageID: 10, Err: channelv2.ErrBackpressured},
		}},
	}
	appender := NewChannelAppender(node)

	res, err := appender.AppendBatch(context.Background(), message.AppendBatchRequest{
		ChannelID: message.ChannelID{ID: "room", Type: 1},
		Messages:  []message.Message{{MessageID: 10}},
	})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if len(res.Items) != 1 || !errors.Is(res.Items[0].Err, message.ErrBackpressured) {
		t.Fatalf("item error = %#v, want %v", res.Items, message.ErrBackpressured)
	}
}

type recordingNode struct {
	calls  int
	last   channelv2.AppendBatchRequest
	result channelv2.AppendBatchResult
	err    error
}

func (n *recordingNode) AppendChannelBatch(_ context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	n.calls++
	n.last = req
	if n.err != nil {
		return channelv2.AppendBatchResult{}, n.err
	}
	return n.result, nil
}

func assertChannelMessage(t *testing.T, got, want channelv2.Message) {
	t.Helper()
	if got.MessageID != want.MessageID ||
		got.MessageSeq != want.MessageSeq ||
		got.ChannelID != want.ChannelID ||
		got.ChannelType != want.ChannelType ||
		got.FromUID != want.FromUID ||
		got.ClientMsgNo != want.ClientMsgNo ||
		string(got.Payload) != string(want.Payload) {
		t.Fatalf("message = %#v, want %#v", got, want)
	}
}

func assertMessageResult(t *testing.T, got, want message.AppendBatchItemResult) {
	t.Helper()
	if got.MessageID != want.MessageID ||
		got.MessageSeq != want.MessageSeq ||
		got.Message.MessageID != want.Message.MessageID ||
		got.Message.MessageSeq != want.Message.MessageSeq ||
		got.Message.ChannelID != want.Message.ChannelID ||
		got.Message.ChannelType != want.Message.ChannelType ||
		got.Message.FromUID != want.Message.FromUID ||
		got.Message.ClientMsgNo != want.Message.ClientMsgNo ||
		string(got.Message.Payload) != string(want.Message.Payload) ||
		!errors.Is(got.Err, want.Err) {
		t.Fatalf("result = %#v, want %#v", got, want)
	}
}
