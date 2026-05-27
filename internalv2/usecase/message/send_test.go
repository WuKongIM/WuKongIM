package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

func TestSendRejectsInvalidCommandsAndDoesNotAppend(t *testing.T) {
	appender := &recordingAppender{}
	ids := &sequenceIDs{next: 100}
	app := New(Options{Appender: appender, MessageID: ids})

	cases := []struct {
		name string
		cmd  SendCommand
		want Reason
	}{
		{name: "missing sender", cmd: SendCommand{ChannelID: "c1", ChannelType: 1, Payload: []byte("x")}, want: ReasonAuthFail},
		{name: "missing channel", cmd: SendCommand{FromUID: "u1", ChannelType: 1, Payload: []byte("x")}, want: ReasonInvalidRequest},
		{name: "missing channel type", cmd: SendCommand{FromUID: "u1", ChannelID: "c1", Payload: []byte("x")}, want: ReasonInvalidRequest},
		{name: "missing payload", cmd: SendCommand{FromUID: "u1", ChannelID: "c1", ChannelType: 1}, want: ReasonInvalidRequest},
		{name: "nopersist unsupported", cmd: SendCommand{FromUID: "u1", ChannelID: "c1", ChannelType: 1, Payload: []byte("x"), NoPersist: true}, want: ReasonUnsupported},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := app.Send(context.Background(), tc.cmd)
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if got.Reason != tc.want {
				t.Fatalf("Send() Reason = %v, want %v", got.Reason, tc.want)
			}
		})
	}
	if appender.calls != 0 {
		t.Fatalf("append calls = %d, want 0", appender.calls)
	}
	if ids.allocated != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocated)
	}
}

func TestSendBatchAllocatesIDsAppendsAndPreservesOrder(t *testing.T) {
	appender := &recordingAppender{}
	ids := &sequenceIDs{next: 100}
	app := New(Options{Appender: appender, MessageID: ids})

	results := app.SendBatch([]SendBatchItem{
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ClientSeq: 1, ClientMsgNo: "m1", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ClientSeq: 2, ClientMsgNo: "m2", ChannelID: "room", ChannelType: 1, Payload: []byte("two")}},
	})

	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for i, result := range results {
		if result.Err != nil {
			t.Fatalf("result[%d] error = %v", i, result.Err)
		}
		if result.Result.Reason != ReasonSuccess {
			t.Fatalf("result[%d] reason = %v, want success", i, result.Result.Reason)
		}
	}
	if results[0].Result.MessageID != 100 || results[0].Result.MessageSeq != 1 {
		t.Fatalf("first result = %#v, want id=100 seq=1", results[0].Result)
	}
	if results[1].Result.MessageID != 101 || results[1].Result.MessageSeq != 2 {
		t.Fatalf("second result = %#v, want id=101 seq=2", results[1].Result)
	}
	if appender.calls != 1 {
		t.Fatalf("append calls = %d, want 1 same-channel segment", appender.calls)
	}
	if got := string(appender.requests[0].Messages[0].Payload); got != "one" {
		t.Fatalf("first appended payload = %q, want one", got)
	}
}

func TestSendBatchSplitsAdjacentChannelSegments(t *testing.T) {
	appender := &recordingAppender{}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 1}})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 1, Payload: []byte("a1")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "b", ChannelType: 1, Payload: []byte("b1")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 1, Payload: []byte("a2")}},
	})

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if appender.calls != 3 {
		t.Fatalf("append calls = %d, want 3 adjacent segments", appender.calls)
	}
}

func TestSendBatchMapsAppendItemErrorsToReasons(t *testing.T) {
	appender := &recordingAppender{itemErrs: []error{nil, ErrRouteNotReady}}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 10}})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("ok")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("retry")}},
	})

	if results[0].Result.Reason != ReasonSuccess {
		t.Fatalf("first reason = %v, want success", results[0].Result.Reason)
	}
	if results[1].Result.Reason != ReasonNodeNotMatch {
		t.Fatalf("second reason = %v, want node-not-match", results[1].Result.Reason)
	}
	if results[1].Err != nil {
		t.Fatalf("second err = %v, want nil business result", results[1].Err)
	}
}

func TestCommittedSinkErrorDoesNotChangeSendResult(t *testing.T) {
	observer := &recordingObserver{}
	app := New(Options{
		Appender:  &recordingAppender{},
		MessageID: &sequenceIDs{next: 50},
		Committed: failingCommitted{err: errors.New(
			"sink down",
		)},
		Observer: observer,
	})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != ReasonSuccess || result.MessageSeq == 0 {
		t.Fatalf("Send() result = %#v, want success with seq", result)
	}
	if observer.committedErrors != 1 {
		t.Fatalf("committed errors = %d, want 1", observer.committedErrors)
	}
}

func TestSendReturnsErrorWhenAppenderOrAllocatorMissing(t *testing.T) {
	noAppender := New(Options{MessageID: &sequenceIDs{next: 1}})
	_, err := noAppender.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("hello")})
	if !errors.Is(err, ErrAppenderRequired) {
		t.Fatalf("no appender error = %v, want %v", err, ErrAppenderRequired)
	}

	noIDs := New(Options{Appender: &recordingAppender{}})
	_, err = noIDs.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("hello")})
	if !errors.Is(err, ErrMessageIDAllocatorRequired) {
		t.Fatalf("no allocator error = %v, want %v", err, ErrMessageIDAllocatorRequired)
	}
}

type recordingAppender struct {
	calls    int
	requests []AppendBatchRequest
	itemErrs []error
}

func (a *recordingAppender) AppendBatch(_ context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.calls++
	a.requests = append(a.requests, cloneAppendRequest(req))
	items := make([]AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		msg.MessageSeq = uint64(i + 1)
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
		if i < len(a.itemErrs) {
			items[i].Err = a.itemErrs[i]
		}
	}
	return AppendBatchResult{Items: items}, nil
}

type sequenceIDs struct {
	next      uint64
	allocated int
}

func (s *sequenceIDs) Next() uint64 {
	id := s.next
	s.next++
	s.allocated++
	return id
}

type failingCommitted struct{ err error }

func (f failingCommitted) Submit(context.Context, messageevents.MessageCommitted) error {
	return f.err
}

type recordingObserver struct{ committedErrors int }

func (o *recordingObserver) CommittedSinkError(SendCommand, error) {
	o.committedErrors++
}
