package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestChannelAppenderMapsAppendBatchRequestAndResult(t *testing.T) {
	node := &recordingNode{
		result: channelv2.AppendBatchResult{Items: []channelv2.AppendBatchItemResult{
			{
				MessageID:  10,
				MessageSeq: 101,
				Message: channelv2.Message{
					MessageID:         10,
					MessageSeq:        101,
					ChannelID:         "room",
					ChannelType:       1,
					FromUID:           "u1",
					ClientMsgNo:       "m1",
					TraceID:           "trace-result-1",
					ChannelKey:        "channel/key-result-1",
					Payload:           []byte("accepted-1"),
					ServerTimestampMS: 1001,
				},
			},
			{
				MessageID:  11,
				MessageSeq: 102,
				Message: channelv2.Message{
					MessageID:         11,
					MessageSeq:        102,
					ChannelID:         "room",
					ChannelType:       1,
					FromUID:           "u2",
					ClientMsgNo:       "m2",
					TraceID:           "trace-result-2",
					ChannelKey:        "channel/key-result-2",
					Payload:           []byte("accepted-2"),
					ServerTimestampMS: 1002,
				},
			},
		}},
	}
	appender := NewChannelAppender(node)

	res, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
		ChannelID:           channelappend.ChannelID{ID: "room", Type: 1},
		ExpectedEpoch:       12,
		ExpectedLeaderEpoch: 34,
		TraceID:             "trace-request",
		ChannelKey:          "channel/key-request",
		Attempt:             4,
		CommitMode:          channelappend.CommitModeQuorum,
		OmitResultPayload:   true,
		Messages: []channelappend.Message{
			{
				MessageID:         10,
				MessageSeq:        1,
				ChannelID:         "room",
				ChannelType:       1,
				FromUID:           "u1",
				ClientMsgNo:       "m1",
				TraceID:           "trace-message-1",
				ChannelKey:        "channel/key-message-1",
				Payload:           []byte("hello"),
				ServerTimestampMS: 2001,
			},
			{
				MessageID:         11,
				MessageSeq:        2,
				ChannelID:         "room",
				ChannelType:       1,
				FromUID:           "u2",
				ClientMsgNo:       "m2",
				TraceID:           "trace-message-2",
				ChannelKey:        "channel/key-message-2",
				Payload:           []byte("world"),
				ServerTimestampMS: 2002,
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
	if req.ExpectedChannelEpoch != 12 {
		t.Fatalf("ExpectedChannelEpoch = %d, want 12", req.ExpectedChannelEpoch)
	}
	if req.ExpectedLeaderEpoch != 34 {
		t.Fatalf("ExpectedLeaderEpoch = %d, want 34", req.ExpectedLeaderEpoch)
	}
	if req.CommitMode != channelv2.CommitModeQuorum {
		t.Fatalf("CommitMode = %v, want %v", req.CommitMode, channelv2.CommitModeQuorum)
	}
	if req.TraceID != "trace-request" || req.ChannelKey != "channel/key-request" || req.Attempt != 4 {
		t.Fatalf("trace fields = %q/%q attempt %d, want trace-request/channel/key-request attempt 4", req.TraceID, req.ChannelKey, req.Attempt)
	}
	if !req.OmitResultPayload {
		t.Fatalf("OmitResultPayload = false, want true")
	}
	if len(req.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(req.Messages))
	}
	assertChannelMessage(t, req.Messages[0], channelv2.Message{
		MessageID:         10,
		MessageSeq:        1,
		ChannelID:         "room",
		ChannelType:       1,
		FromUID:           "u1",
		ClientMsgNo:       "m1",
		TraceID:           "trace-message-1",
		ChannelKey:        "channel/key-message-1",
		Payload:           []byte("hello"),
		ServerTimestampMS: 2001,
	})
	assertChannelMessage(t, req.Messages[1], channelv2.Message{
		MessageID:         11,
		MessageSeq:        2,
		ChannelID:         "room",
		ChannelType:       1,
		FromUID:           "u2",
		ClientMsgNo:       "m2",
		TraceID:           "trace-message-2",
		ChannelKey:        "channel/key-message-2",
		Payload:           []byte("world"),
		ServerTimestampMS: 2002,
	})
	if len(res.Items) != 2 {
		t.Fatalf("len(result.Items) = %d, want 2", len(res.Items))
	}
	assertMessageResult(t, res.Items[0], channelappend.AppendBatchItemResult{
		MessageID:  10,
		MessageSeq: 101,
		Message: channelappend.Message{
			MessageID:         10,
			MessageSeq:        101,
			ChannelID:         "room",
			ChannelType:       1,
			FromUID:           "u1",
			ClientMsgNo:       "m1",
			TraceID:           "trace-result-1",
			ChannelKey:        "channel/key-result-1",
			Payload:           []byte("accepted-1"),
			ServerTimestampMS: 1001,
		},
	})
	assertMessageResult(t, res.Items[1], channelappend.AppendBatchItemResult{
		MessageID:  11,
		MessageSeq: 102,
		Message: channelappend.Message{
			MessageID:         11,
			MessageSeq:        102,
			ChannelID:         "room",
			ChannelType:       1,
			FromUID:           "u2",
			ClientMsgNo:       "m2",
			TraceID:           "trace-result-2",
			ChannelKey:        "channel/key-result-2",
			Payload:           []byte("accepted-2"),
			ServerTimestampMS: 1002,
		},
	})
}

func TestChannelAppenderRecordsChannelAppendTraceOnSuccess(t *testing.T) {
	sink := &recordingSendtraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	node := &recordingNode{
		result: channelv2.AppendBatchResult{Items: []channelv2.AppendBatchItemResult{
			{MessageID: 10, MessageSeq: 101, Message: channelv2.Message{MessageID: 10, MessageSeq: 101}},
		}},
	}
	appender := NewChannelAppender(node)

	_, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
		ChannelID:  channelappend.ChannelID{ID: "room", Type: 1},
		TraceID:    "trace-1",
		ChannelKey: "channel/key-1",
		Attempt:    2,
		Messages: []channelappend.Message{
			{
				MessageID:   10,
				ChannelID:   "room",
				ChannelType: 1,
				FromUID:     "u1",
				ClientMsgNo: "client-1",
				TraceID:     "trace-1",
				ChannelKey:  "channel/key-1",
				Payload:     []byte("hello"),
			},
		},
	})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}

	events := sink.snapshot()
	if got := len(events); got != 1 {
		t.Fatalf("sendtrace events = %#v, want 1", events)
	}
	requireChannelAppendTraceEvent(t, events[0], "trace-1", "channel/key-1", "client-1", "u1", 101, 2, 1, sendtrace.ResultOK, "")
}

func TestChannelAppenderRecordsChannelAppendTraceOnBatchError(t *testing.T) {
	sink := &recordingSendtraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	appender := NewChannelAppender(&recordingNode{err: channelv2.ErrBackpressured})

	_, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
		ChannelID:  channelappend.ChannelID{ID: "room", Type: 1},
		TraceID:    "trace-error",
		ChannelKey: "channel/key-error",
		Attempt:    3,
		Messages: []channelappend.Message{
			{MessageID: 10, FromUID: "u1", ClientMsgNo: "client-error", TraceID: "trace-error", ChannelKey: "channel/key-error"},
		},
	})
	if !errors.Is(err, channelappend.ErrBackpressured) {
		t.Fatalf("AppendBatch() error = %v, want backpressured", err)
	}

	events := sink.snapshot()
	if got := len(events); got != 1 {
		t.Fatalf("sendtrace events = %#v, want 1", events)
	}
	requireChannelAppendTraceEvent(t, events[0], "trace-error", "channel/key-error", "client-error", "u1", 0, 3, 1, sendtrace.ResultError, "backpressured")
}

func TestChannelAppenderLogsAppendChannelBatchError(t *testing.T) {
	logger := &recordingClusterLogger{}
	appender := NewChannelAppender(&recordingNode{err: channelv2.ErrNotReady}, logger)

	_, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
		ChannelID:           channelappend.ChannelID{ID: "room", Type: 2},
		ExpectedEpoch:       12,
		ExpectedLeaderEpoch: 34,
		TraceID:             "trace-error",
		ChannelKey:          "channel/key-error",
		Attempt:             3,
		Messages: []channelappend.Message{
			{MessageID: 10, FromUID: "u1", ClientMsgNo: "client-error", TraceID: "trace-error", ChannelKey: "channel/key-error"},
			{MessageID: 11, FromUID: "u2", ClientMsgNo: "client-error-2"},
		},
	})
	if !errors.Is(err, channelappend.ErrRouteNotReady) {
		t.Fatalf("AppendBatch() error = %v, want route not ready", err)
	}

	entry, ok := logger.find("ERROR", "internalv2.infra.cluster.channel_append_batch_failed")
	if !ok {
		t.Fatalf("missing append failure log entries=%#v", logger.entries)
	}
	requireLogField(t, entry.fields, "channelID", "room")
	requireLogField(t, entry.fields, "channelType", int64(2))
	requireLogField(t, entry.fields, "channelKey", "channel/key-error")
	requireLogField(t, entry.fields, "traceID", "trace-error")
	requireLogField(t, entry.fields, "attempt", 3)
	requireLogField(t, entry.fields, "records", 2)
	requireLogField(t, entry.fields, "expectedEpoch", uint64(12))
	requireLogField(t, entry.fields, "expectedLeaderEpoch", uint64(34))
	requireLogField(t, entry.fields, "result", "route_not_ready")
	requireLogField(t, entry.fields, "error", channelv2.ErrNotReady)
}

func TestChannelAppenderDoesNotRecordChannelAppendTraceWithoutTraceIDOrSink(t *testing.T) {
	sink := &recordingSendtraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	appender := NewChannelAppender(&recordingNode{
		result: channelv2.AppendBatchResult{Items: []channelv2.AppendBatchItemResult{
			{MessageID: 10, MessageSeq: 101},
		}},
	})

	_, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
		ChannelID:  channelappend.ChannelID{ID: "room", Type: 1},
		ChannelKey: "channel/key-1",
		Messages:   []channelappend.Message{{MessageID: 10, ClientMsgNo: "client-1"}},
	})
	if err != nil {
		t.Fatalf("AppendBatch() without trace id error = %v", err)
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("sendtrace events = %#v, want none without trace id", events)
	}

	restore()
	_, err = appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
		ChannelID:  channelappend.ChannelID{ID: "room", Type: 1},
		TraceID:    "trace-disabled",
		ChannelKey: "channel/key-disabled",
		Messages:   []channelappend.Message{{MessageID: 10, TraceID: "trace-disabled", ClientMsgNo: "client-disabled"}},
	})
	if err != nil {
		t.Fatalf("AppendBatch() without active sink error = %v", err)
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("sendtrace events = %#v, want none without active sink", events)
	}
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

	res, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
		ChannelID:  channelappend.ChannelID{ID: "room", Type: 1},
		Messages:   []channelappend.Message{{MessageID: 10, Payload: payload}},
		CommitMode: channelappend.CommitModeQuorum,
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
		in   channelappend.CommitMode
		want channelv2.CommitMode
	}{
		{name: "quorum", in: channelappend.CommitModeQuorum, want: channelv2.CommitModeQuorum},
		{name: "local", in: channelappend.CommitModeLocal, want: channelv2.CommitModeLocal},
		{name: "default", in: channelappend.CommitMode(0), want: channelv2.CommitModeQuorum},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &recordingNode{}
			appender := NewChannelAppender(node)
			_, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
				ChannelID:  channelappend.ChannelID{ID: "room", Type: 1},
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
	if _, err := nilAppender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{}); !errors.Is(err, channelappend.ErrAppenderRequired) {
		t.Fatalf("nil appender error = %v, want %v", err, channelappend.ErrAppenderRequired)
	}

	appender := NewChannelAppender(nil)
	if _, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{}); !errors.Is(err, channelappend.ErrAppenderRequired) {
		t.Fatalf("nil node error = %v, want %v", err, channelappend.ErrAppenderRequired)
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
		{name: "clusterv2 not leader", err: clusterv2.ErrNotLeader, want: channelappend.ErrNotLeader},
		{name: "slot propose not leader", err: propose.ErrNotLeader, want: channelappend.ErrNotLeader},
		{name: "channelv2 not leader", err: channelv2.ErrNotLeader, want: channelappend.ErrNotLeader},
		{name: "stale meta", err: channelv2.ErrStaleMeta, want: channelappend.ErrStaleRoute},
		{name: "not replica", err: channelv2.ErrNotReplica, want: channelappend.ErrStaleRoute},
		{name: "textual not replica", err: errors.New(channelv2.ErrNotReplica.Error()), want: channelappend.ErrStaleRoute},
		{name: "channel missing", err: channelv2.ErrChannelNotFound, want: channelappend.ErrChannelNotFound},
		{name: "backpressured", err: channelv2.ErrBackpressured, want: channelappend.ErrBackpressured},
		{name: "clusterv2 route not ready", err: clusterv2.ErrRouteNotReady, want: channelappend.ErrRouteNotReady},
		{name: "clusterv2 no slot leader", err: clusterv2.ErrNoSlotLeader, want: channelappend.ErrRouteNotReady},
		{name: "channelv2 not ready", err: channelv2.ErrNotReady, want: channelappend.ErrRouteNotReady},
		{name: "channelv2 write fenced", err: channelv2.ErrWriteFenced, want: channelappend.ErrRouteNotReady},
		{name: "context canceled", err: context.Canceled, want: context.Canceled, unchanged: true},
		{name: "context deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded, unchanged: true},
		{name: "unknown", err: unknown, want: channelappend.ErrAppendFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			appender := NewChannelAppender(&recordingNode{err: tc.err})
			_, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
				ChannelID: channelappend.ChannelID{ID: "room", Type: 1},
				Messages:  []channelappend.Message{{MessageID: 1, Payload: []byte("x")}},
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

	res, err := appender.AppendBatch(context.Background(), channelappend.AppendBatchRequest{
		ChannelID: channelappend.ChannelID{ID: "room", Type: 1},
		Messages:  []channelappend.Message{{MessageID: 10}},
	})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if len(res.Items) != 1 || !errors.Is(res.Items[0].Err, channelappend.ErrBackpressured) {
		t.Fatalf("item error = %#v, want %v", res.Items, channelappend.ErrBackpressured)
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
		got.TraceID != want.TraceID ||
		got.ChannelKey != want.ChannelKey ||
		got.ServerTimestampMS != want.ServerTimestampMS ||
		string(got.Payload) != string(want.Payload) {
		t.Fatalf("message = %#v, want %#v", got, want)
	}
}

func assertMessageResult(t *testing.T, got, want channelappend.AppendBatchItemResult) {
	t.Helper()
	if got.MessageID != want.MessageID ||
		got.MessageSeq != want.MessageSeq ||
		got.Message.MessageID != want.Message.MessageID ||
		got.Message.MessageSeq != want.Message.MessageSeq ||
		got.Message.ChannelID != want.Message.ChannelID ||
		got.Message.ChannelType != want.Message.ChannelType ||
		got.Message.FromUID != want.Message.FromUID ||
		got.Message.ClientMsgNo != want.Message.ClientMsgNo ||
		got.Message.TraceID != want.Message.TraceID ||
		got.Message.ChannelKey != want.Message.ChannelKey ||
		got.Message.ServerTimestampMS != want.Message.ServerTimestampMS ||
		string(got.Message.Payload) != string(want.Message.Payload) ||
		!errors.Is(got.Err, want.Err) {
		t.Fatalf("result = %#v, want %#v", got, want)
	}
}

type recordingSendtraceSink struct {
	events []sendtrace.Event
}

type clusterLogEntry struct {
	level  string
	fields []wklog.Field
}

type recordingClusterLogger struct {
	entries []clusterLogEntry
}

func (r *recordingClusterLogger) Debug(_ string, fields ...wklog.Field) { r.log("DEBUG", fields...) }
func (r *recordingClusterLogger) Info(_ string, fields ...wklog.Field)  { r.log("INFO", fields...) }
func (r *recordingClusterLogger) Warn(_ string, fields ...wklog.Field)  { r.log("WARN", fields...) }
func (r *recordingClusterLogger) Error(_ string, fields ...wklog.Field) { r.log("ERROR", fields...) }
func (r *recordingClusterLogger) Fatal(_ string, fields ...wklog.Field) { r.log("FATAL", fields...) }
func (r *recordingClusterLogger) Named(string) wklog.Logger             { return r }
func (r *recordingClusterLogger) With(...wklog.Field) wklog.Logger      { return r }
func (r *recordingClusterLogger) Sync() error                           { return nil }

func (r *recordingClusterLogger) log(level string, fields ...wklog.Field) {
	r.entries = append(r.entries, clusterLogEntry{level: level, fields: append([]wklog.Field(nil), fields...)})
}

func (r *recordingClusterLogger) find(level string, event string) (clusterLogEntry, bool) {
	for _, entry := range r.entries {
		if entry.level != level {
			continue
		}
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return entry, true
			}
		}
	}
	return clusterLogEntry{}, false
}

func requireLogField(t *testing.T, fields []wklog.Field, key string, want any) {
	t.Helper()
	for _, field := range fields {
		if field.Key != key {
			continue
		}
		if field.Value != want {
			t.Fatalf("log field %s = %#v, want %#v", key, field.Value, want)
		}
		return
	}
	t.Fatalf("missing log field %s in %#v", key, fields)
}

func (s *recordingSendtraceSink) RecordSendTrace(event sendtrace.Event) {
	s.events = append(s.events, event)
}

func (s *recordingSendtraceSink) snapshot() []sendtrace.Event {
	return append([]sendtrace.Event(nil), s.events...)
}

func requireChannelAppendTraceEvent(t *testing.T, event sendtrace.Event, traceID, channelKey, clientMsgNo, fromUID string, messageSeq uint64, attempt, recordCount int, result sendtrace.Result, errorCode string) {
	t.Helper()
	if event.Stage != sendtrace.StageChannelAppendLocal {
		t.Fatalf("stage = %q, want %q", event.Stage, sendtrace.StageChannelAppendLocal)
	}
	if event.TraceID != traceID || event.ChannelKey != channelKey || event.ClientMsgNo != clientMsgNo || event.FromUID != fromUID {
		t.Fatalf("trace fields = %q/%q/%q/%q, want %q/%q/%q/%q",
			event.TraceID, event.ChannelKey, event.ClientMsgNo, event.FromUID, traceID, channelKey, clientMsgNo, fromUID)
	}
	if event.MessageSeq != messageSeq {
		t.Fatalf("MessageSeq = %d, want %d", event.MessageSeq, messageSeq)
	}
	if event.Attempt != attempt || event.RecordCount != recordCount {
		t.Fatalf("attempt/record count = %d/%d, want %d/%d", event.Attempt, event.RecordCount, attempt, recordCount)
	}
	if event.Result != result || event.ErrorCode != errorCode {
		t.Fatalf("outcome = %q/%q, want %q/%q", event.Result, event.ErrorCode, result, errorCode)
	}
}
