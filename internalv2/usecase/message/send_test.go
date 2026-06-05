package message

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
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

func TestAppendRequestCarriesSendTraceMetadata(t *testing.T) {
	appender := &recordingAppender{}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 100}})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ClientMsgNo: "m1", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
		{Command: SendCommand{FromUID: "u1", ClientMsgNo: "m2", TraceID: "trace-two", ChannelKey: "channel/key-two", ChannelID: "room", ChannelType: 1, Payload: []byte("two")}},
		{Command: SendCommand{FromUID: "u1", ClientMsgNo: "m3", TraceID: "trace-three", ChannelKey: "channel/key-three", ChannelID: "room", ChannelType: 1, Payload: []byte("three")}},
	})

	for i, result := range results {
		if result.Err != nil || result.Result.Reason != ReasonSuccess {
			t.Fatalf("result[%d] = %#v, want success", i, result)
		}
	}
	if got := len(appender.requests); got != 1 {
		t.Fatalf("append requests = %d, want 1", got)
	}
	req := appender.requests[0]
	if req.TraceID != "trace-two" || req.ChannelKey != "channel/key-two" {
		t.Fatalf("request trace fields = %q/%q, want trace-two/channel/key-two", req.TraceID, req.ChannelKey)
	}
	if req.Attempt != 1 {
		t.Fatalf("request attempt = %d, want 1", req.Attempt)
	}
	if req.Messages[0].TraceID != "" || req.Messages[0].ChannelKey != "" {
		t.Fatalf("first message trace fields = %q/%q, want empty", req.Messages[0].TraceID, req.Messages[0].ChannelKey)
	}
	if req.Messages[1].TraceID != "trace-two" || req.Messages[1].ChannelKey != "channel/key-two" {
		t.Fatalf("second message trace fields = %q/%q, want trace-two/channel/key-two", req.Messages[1].TraceID, req.Messages[1].ChannelKey)
	}
	if req.Messages[2].TraceID != "trace-three" || req.Messages[2].ChannelKey != "channel/key-three" {
		t.Fatalf("third message trace fields = %q/%q, want trace-three/channel/key-three", req.Messages[2].TraceID, req.Messages[2].ChannelKey)
	}
}

func TestSendBatchGroupsSameChannelAcrossBatch(t *testing.T) {
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
	if appender.calls != 2 {
		t.Fatalf("append calls = %d, want 2 channel groups", appender.calls)
	}
	requests := appender.requestsByChannel()
	aReq := requests[ChannelID{ID: "a", Type: 1}]
	if got := len(aReq.Messages); got != 2 {
		t.Fatalf("channel a messages = %d, want 2", got)
	}
	if got := string(aReq.Messages[0].Payload); got != "a1" {
		t.Fatalf("first channel a payload = %q, want a1", got)
	}
	if got := string(aReq.Messages[1].Payload); got != "a2" {
		t.Fatalf("second channel a payload = %q, want a2", got)
	}
}

func TestSendBatchAppendsIndependentChannelsConcurrently(t *testing.T) {
	release := make(chan struct{})
	appender := &blockingAppender{
		started: make(chan ChannelID, 2),
		release: release,
	}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 1}})
	done := make(chan []SendBatchItemResult, 1)

	go func() {
		done <- app.SendBatch([]SendBatchItem{
			{Command: SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 1, Payload: []byte("a1")}},
			{Command: SendCommand{FromUID: "u1", ChannelID: "b", ChannelType: 1, Payload: []byte("b1")}},
		})
	}()

	waitStarted := func(label string) ChannelID {
		t.Helper()
		select {
		case ch := <-appender.started:
			return ch
		case <-time.After(100 * time.Millisecond):
			close(release)
			<-done
			t.Fatalf("%s append did not start before the first append completed", label)
			return ChannelID{}
		}
	}
	first := waitStarted("first")
	second := waitStarted("second")
	if first == second {
		close(release)
		<-done
		t.Fatalf("started channel twice: %#v", first)
	}
	close(release)
	results := <-done
	for i, result := range results {
		if result.Err != nil || result.Result.Reason != ReasonSuccess {
			t.Fatalf("result[%d] = %#v, want success", i, result)
		}
	}
}

func TestSendBatchUsesWideIndependentChannelFanout(t *testing.T) {
	const channelCount = 64
	release := make(chan struct{})
	appender := &blockingAppender{
		started: make(chan ChannelID, channelCount),
		release: release,
	}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 1}})
	items := make([]SendBatchItem, 0, channelCount)
	for i := 0; i < channelCount; i++ {
		items = append(items, SendBatchItem{Command: SendCommand{
			FromUID:     "u1",
			ChannelID:   fmt.Sprintf("c-%02d", i),
			ChannelType: 1,
			Payload:     []byte("payload"),
		}})
	}
	done := make(chan []SendBatchItemResult, 1)
	go func() {
		done <- app.SendBatch(items)
	}()

	started := make(map[ChannelID]struct{}, channelCount)
	timeout := time.After(100 * time.Millisecond)
	released := false
	releaseAll := func() {
		if !released {
			close(release)
			released = true
		}
	}
	for len(started) < channelCount {
		select {
		case ch := <-appender.started:
			started[ch] = struct{}{}
		case <-timeout:
			releaseAll()
			<-done
			t.Fatalf("started independent channel appends = %d, want %d", len(started), channelCount)
		}
	}
	releaseAll()
	results := <-done
	for i, result := range results {
		if result.Err != nil || result.Result.Reason != ReasonSuccess {
			t.Fatalf("result[%d] = %#v, want success", i, result)
		}
	}
}

func TestActiveSegmentItemsReusesSegmentWhenNoneCanceled(t *testing.T) {
	results := make([]SendBatchItemResult, 2)
	items := []preparedSend{
		{index: 0, ctx: context.Background()},
		{index: 1, ctx: context.Background()},
	}

	active := activeSegmentItems(segment{items: items}, results)
	if len(active) != len(items) {
		t.Fatalf("active items = %d, want %d", len(active), len(items))
	}
	if &active[0] != &items[0] || &active[1] != &items[1] {
		t.Fatalf("active items did not reuse segment backing array")
	}
}

func TestPrepareReusesCommandPayloadUntilAppendBoundary(t *testing.T) {
	app := New(Options{Appender: &recordingAppender{}, MessageID: &sequenceIDs{next: 1}})
	payload := []byte("hello")

	prepared, done := app.prepare(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "room",
		ChannelType: 1,
		Payload:     payload,
	})
	if done {
		t.Fatalf("prepare returned done=true, want prepared command")
	}
	if len(prepared.cmd.Payload) == 0 || &prepared.cmd.Payload[0] != &payload[0] {
		t.Fatalf("prepared payload was cloned before append boundary")
	}
}

func TestAppendSegmentClonesPayloadForAppenderBoundary(t *testing.T) {
	payload := []byte("hello")
	appender := &mutatingAppender{}
	app := New(Options{Appender: appender})

	results := make([]SendBatchItemResult, 1)
	app.appendSegment(segment{
		channel: ChannelID{ID: "room", Type: 1},
		items: []preparedSend{{
			index: 0,
			ctx:   context.Background(),
			cmd: SendCommand{
				MessageID:   1,
				ChannelID:   "room",
				ChannelType: 1,
				FromUID:     "u1",
				Payload:     payload,
			},
		}},
	}, results)

	if got := string(payload); got != "hello" {
		t.Fatalf("source payload = %q, want append boundary clone to protect command payload", got)
	}
	if results[0].Result.Reason != ReasonSuccess {
		t.Fatalf("result reason = %v, want success", results[0].Result.Reason)
	}
}

func TestNewLeavesCommittedSinkNilWhenNotConfigured(t *testing.T) {
	app := New(Options{Appender: &recordingAppender{}})
	if app.committed != nil {
		t.Fatalf("committed sink = %T, want nil when not configured", app.committed)
	}
}

func TestAppendSegmentOmitsResultPayloadWhenNoCommittedSink(t *testing.T) {
	appender := &recordingAppender{}
	app := New(Options{Appender: appender})
	results := make([]SendBatchItemResult, 1)

	app.appendSegment(segment{
		channel: ChannelID{ID: "room", Type: 1},
		items: []preparedSend{{
			index: 0,
			ctx:   context.Background(),
			cmd: SendCommand{
				MessageID:   1,
				ChannelID:   "room",
				ChannelType: 1,
				FromUID:     "u1",
				Payload:     []byte("payload"),
			},
		}},
	}, results)

	if got := len(appender.requests); got != 1 {
		t.Fatalf("append requests = %d, want 1", got)
	}
	if !appender.requests[0].OmitResultPayload {
		t.Fatalf("OmitResultPayload = false, want true without committed sink")
	}
}

func TestAppendSegmentKeepsResultPayloadWhenCommittedSinkConfigured(t *testing.T) {
	appender := &recordingAppender{}
	app := New(Options{Appender: appender, Committed: recordingCommitted{}})
	results := make([]SendBatchItemResult, 1)

	app.appendSegment(segment{
		channel: ChannelID{ID: "room", Type: 1},
		items: []preparedSend{{
			index: 0,
			ctx:   context.Background(),
			cmd: SendCommand{
				MessageID:   1,
				ChannelID:   "room",
				ChannelType: 1,
				FromUID:     "u1",
				Payload:     []byte("payload"),
			},
		}},
	}, results)

	if got := len(appender.requests); got != 1 {
		t.Fatalf("append requests = %d, want 1", got)
	}
	if appender.requests[0].OmitResultPayload {
		t.Fatalf("OmitResultPayload = true, want false with committed sink")
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

func TestSendBatchObservesAppendItemResults(t *testing.T) {
	observer := &recordingObserver{}
	appender := &recordingAppender{itemErrs: []error{nil, ErrBackpressured}}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 10}, Observer: observer})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("ok")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("retry")}},
	})

	if results[0].Result.Reason != ReasonSuccess || results[1].Result.Reason != ReasonSystemError {
		t.Fatalf("results = %#v, want success then system error", results)
	}
	if got := len(observer.appendEvents); got != 2 {
		t.Fatalf("append events = %d, want 2", got)
	}
	if observer.appendEvents[0].path != appendMetricPathChannelPlane || observer.appendEvents[0].err != nil {
		t.Fatalf("first append event = %#v, want channelplane success", observer.appendEvents[0])
	}
	if !errors.Is(observer.appendEvents[1].err, ErrBackpressured) {
		t.Fatalf("second append event err = %v, want backpressured", observer.appendEvents[1].err)
	}
}

func TestSendBatchRecordsDurableTraceOnSuccess(t *testing.T) {
	sink := &recordingSendtraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	app := New(Options{Appender: &recordingAppender{}, MessageID: &sequenceIDs{next: 10}})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:      "u1",
		SenderNodeID: 7,
		ClientMsgNo:  "client-1",
		TraceID:      "trace-1",
		ChannelKey:   "channel/key-1",
		ChannelID:    "room",
		ChannelType:  1,
		Payload:      []byte("payload"),
	})

	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != ReasonSuccess || result.MessageSeq != 1 {
		t.Fatalf("Send() result = %#v, want success seq=1", result)
	}
	events := sink.snapshot()
	if got := len(events); got != 1 {
		t.Fatalf("sendtrace events = %#v, want 1", events)
	}
	requireDurableTraceEvent(t, events[0], "trace-1", "channel/key-1", "client-1", "u1", 7, 1, sendtrace.ResultOK, "")
}

func TestSendBatchRecordsDurableTraceOnBatchAppendError(t *testing.T) {
	sink := &recordingSendtraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	app := New(Options{Appender: &recordingAppender{err: ErrRouteNotReady}, MessageID: &sequenceIDs{next: 10}})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", SenderNodeID: 7, ClientMsgNo: "a", TraceID: "trace-a", ChannelKey: "channel/key-a", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
		{Command: SendCommand{FromUID: "u1", SenderNodeID: 7, ClientMsgNo: "b", TraceID: "trace-b", ChannelKey: "channel/key-b", ChannelID: "room", ChannelType: 1, Payload: []byte("two")}},
	})

	for i, result := range results {
		if !errors.Is(result.Err, ErrRouteNotReady) {
			t.Fatalf("result[%d] err = %v, want route-not-ready", i, result.Err)
		}
	}
	events := sink.snapshot()
	if got := len(events); got != 2 {
		t.Fatalf("sendtrace events = %#v, want 2", events)
	}
	requireDurableTraceEvent(t, events[0], "trace-a", "channel/key-a", "a", "u1", 7, 0, sendtrace.ResultError, "route_not_ready")
	requireDurableTraceEvent(t, events[1], "trace-b", "channel/key-b", "b", "u1", 7, 0, sendtrace.ResultError, "route_not_ready")
}

func TestSendBatchRecordsDurableTraceOnItemErrorAndShortResult(t *testing.T) {
	t.Run("item error", func(t *testing.T) {
		sink := &recordingSendtraceSink{}
		restore := sendtrace.SetSink(sink)
		t.Cleanup(restore)
		app := New(Options{Appender: &recordingAppender{itemErrs: []error{ErrBackpressured}}, MessageID: &sequenceIDs{next: 10}})

		results := app.SendBatch([]SendBatchItem{
			{Command: SendCommand{FromUID: "u1", SenderNodeID: 7, ClientMsgNo: "item", TraceID: "trace-item", ChannelKey: "channel/key-item", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
		})

		if results[0].Result.Reason != ReasonSystemError {
			t.Fatalf("result = %#v, want system error", results[0])
		}
		events := sink.snapshot()
		if got := len(events); got != 1 {
			t.Fatalf("sendtrace events = %#v, want 1", events)
		}
		requireDurableTraceEvent(t, events[0], "trace-item", "channel/key-item", "item", "u1", 7, 1, sendtrace.ResultError, "backpressured")
	})

	t.Run("short result", func(t *testing.T) {
		sink := &recordingSendtraceSink{}
		restore := sendtrace.SetSink(sink)
		t.Cleanup(restore)
		app := New(Options{Appender: &recordingAppender{resultLimit: 1}, MessageID: &sequenceIDs{next: 10}})

		results := app.SendBatch([]SendBatchItem{
			{Command: SendCommand{FromUID: "u1", SenderNodeID: 7, ClientMsgNo: "ok", TraceID: "trace-ok", ChannelKey: "channel/key-ok", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
			{Command: SendCommand{FromUID: "u1", SenderNodeID: 7, ClientMsgNo: "missing", TraceID: "trace-missing", ChannelKey: "channel/key-missing", ChannelID: "room", ChannelType: 1, Payload: []byte("two")}},
		})

		if results[0].Result.Reason != ReasonSuccess {
			t.Fatalf("first result = %#v, want success", results[0])
		}
		if !errors.Is(results[1].Err, ErrAppendResultMissing) {
			t.Fatalf("second err = %v, want append result missing", results[1].Err)
		}
		events := sink.snapshot()
		if got := len(events); got != 2 {
			t.Fatalf("sendtrace events = %#v, want 2", events)
		}
		requireDurableTraceEvent(t, events[0], "trace-ok", "channel/key-ok", "ok", "u1", 7, 1, sendtrace.ResultOK, "")
		requireDurableTraceEvent(t, events[1], "trace-missing", "channel/key-missing", "missing", "u1", 7, 0, sendtrace.ResultError, "append_result_missing")
	})
}

func TestSendBatchDoesNotRecordDurableTraceWithoutTraceIDOrSink(t *testing.T) {
	sink := &recordingSendtraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	app := New(Options{Appender: &recordingAppender{}, MessageID: &sequenceIDs{next: 10}})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:      "u1",
		SenderNodeID: 7,
		ClientMsgNo:  "client-1",
		ChannelKey:   "channel/key-1",
		ChannelID:    "room",
		ChannelType:  1,
		Payload:      []byte("payload"),
	})

	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != ReasonSuccess {
		t.Fatalf("Send() reason = %v, want success", result.Reason)
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("sendtrace events = %#v, want none without trace id", events)
	}

	restore()
	_, err = app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ClientMsgNo: "client-2",
		TraceID:     "trace-disabled",
		ChannelKey:  "channel/key-disabled",
		ChannelID:   "room",
		ChannelType: 1,
		Payload:     []byte("payload"),
	})
	if err != nil {
		t.Fatalf("Send() without active sink error = %v", err)
	}
}

func TestSendBatchObservesShortAppendResult(t *testing.T) {
	observer := &recordingObserver{}
	appender := &recordingAppender{resultLimit: 1}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 10}, Observer: observer})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("two")}},
	})

	if results[0].Result.Reason != ReasonSuccess {
		t.Fatalf("first result = %#v, want success", results[0])
	}
	if !errors.Is(results[1].Err, ErrAppendResultMissing) {
		t.Fatalf("second err = %v, want append result missing", results[1].Err)
	}
	if got := len(observer.appendEvents); got != 2 {
		t.Fatalf("append events = %d, want 2", got)
	}
	if !errors.Is(observer.appendEvents[1].err, ErrAppendResultMissing) {
		t.Fatalf("second append event err = %v, want append result missing", observer.appendEvents[1].err)
	}
}

func TestSendBatchRetriesTransientBatchAppendErrorsBeforeDeadline(t *testing.T) {
	appender := &recordingAppender{errs: []error{ErrRouteNotReady}}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 10}})
	deadline := time.Now().Add(time.Second)

	results := app.SendBatch([]SendBatchItem{
		{Deadline: deadline, Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
		{Deadline: deadline, Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("two")}},
	})

	for i, result := range results {
		if result.Err != nil {
			t.Fatalf("result[%d] error = %v, want retry success", i, result.Err)
		}
		if result.Result.Reason != ReasonSuccess {
			t.Fatalf("result[%d] reason = %v, want success", i, result.Result.Reason)
		}
	}
	if appender.calls != 2 {
		t.Fatalf("append calls = %d, want initial attempt plus retry", appender.calls)
	}
	if got := []int{appender.requests[0].Attempt, appender.requests[1].Attempt}; got[0] != 1 || got[1] != 2 {
		t.Fatalf("append attempts = %v, want [1 2]", got)
	}
}

func TestSendBatchObservesBatchAppendErrorsPerActiveItem(t *testing.T) {
	observer := &recordingObserver{}
	appender := &recordingAppender{err: ErrRouteNotReady}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 10}, Observer: observer})

	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("one")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("two")}},
	})

	if !errors.Is(results[0].Err, ErrRouteNotReady) || !errors.Is(results[1].Err, ErrRouteNotReady) {
		t.Fatalf("results = %#v, want route-not-ready errors", results)
	}
	if got := len(observer.appendEvents); got != 2 {
		t.Fatalf("append events = %d, want 2", got)
	}
	for i, event := range observer.appendEvents {
		if event.path != appendMetricPathChannelPlane || !errors.Is(event.err, ErrRouteNotReady) {
			t.Fatalf("append event[%d] = %#v, want route-not-ready channelplane", i, event)
		}
	}
}

func TestSendBatchFiltersCanceledItemsBeforeAppend(t *testing.T) {
	appender := &recordingAppender{}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 10}})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	results := app.SendBatch([]SendBatchItem{
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("ok")}},
		{Context: canceled, Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("canceled")}},
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("also-ok")}},
	})

	if !errors.Is(results[1].Err, context.Canceled) {
		t.Fatalf("canceled result error = %v, want context canceled", results[1].Err)
	}
	if results[0].Result.Reason != ReasonSuccess || results[2].Result.Reason != ReasonSuccess {
		t.Fatalf("active results = %#v %#v, want success", results[0], results[2])
	}
	if appender.calls != 1 {
		t.Fatalf("append calls = %d, want 1", appender.calls)
	}
	if got := len(appender.requests[0].Messages); got != 2 {
		t.Fatalf("appended messages = %d, want 2", got)
	}
	if got := string(appender.requests[0].Messages[0].Payload); got != "ok" {
		t.Fatalf("first appended payload = %q, want ok", got)
	}
	if got := string(appender.requests[0].Messages[1].Payload); got != "also-ok" {
		t.Fatalf("second appended payload = %q, want also-ok", got)
	}
}

func TestSendBatchAppendContextIsNotDerivedFromFirstItemCancellation(t *testing.T) {
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	appender := &contextCapturingAppender{
		onAppend: cancelFirst,
	}
	app := New(Options{Appender: appender, MessageID: &sequenceIDs{next: 10}})

	results := app.SendBatch([]SendBatchItem{
		{Context: firstCtx, Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("first")}},
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 1, Payload: []byte("second")}},
	})

	for i, result := range results {
		if result.Err != nil {
			t.Fatalf("result[%d] error = %v", i, result.Err)
		}
		if result.Result.Reason != ReasonSuccess {
			t.Fatalf("result[%d] reason = %v, want success", i, result.Result.Reason)
		}
	}
	if appender.ctxCanceledAfterFirstCancel {
		t.Fatalf("append context was canceled by first item cancellation")
	}
}

func TestSegmentContextUsesEarliestItemDeadline(t *testing.T) {
	late := time.Now().Add(time.Hour)
	early := time.Now().Add(time.Minute)
	lateCtx, lateCancel := context.WithDeadline(context.Background(), late)
	defer lateCancel()
	earlyCtx, earlyCancel := context.WithDeadline(context.Background(), early)
	defer earlyCancel()

	ctx, cancel := segmentContext([]preparedSend{
		{ctx: lateCtx},
		{ctx: earlyCtx},
	})
	defer cancel()

	got, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("segment context missing deadline")
	}
	if !got.Equal(early) {
		t.Fatalf("segment deadline = %v, want %v", got, early)
	}
}

func TestSegmentContextUsesExplicitBatchItemDeadline(t *testing.T) {
	late := time.Now().Add(time.Hour)
	early := time.Now().Add(time.Minute)
	lateCtx, lateCancel := context.WithDeadline(context.Background(), late)
	defer lateCancel()

	ctx, cancel := segmentContext([]preparedSend{
		{ctx: lateCtx, deadline: early},
	})
	defer cancel()

	got, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("segment context missing deadline")
	}
	if !got.Equal(early) {
		t.Fatalf("segment deadline = %v, want explicit item deadline %v", got, early)
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

func TestSubmitCommittedIncludesDeliveryFields(t *testing.T) {
	committed := &capturingCommitted{}
	app := New(Options{
		Appender:  &recordingAppender{},
		MessageID: &sequenceIDs{next: 50},
		Committed: committed,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:           "u1",
		SenderNodeID:      7,
		SenderSessionID:   42,
		ClientMsgNo:       "client-1",
		ChannelID:         "g1",
		ChannelType:       2,
		Payload:           []byte("hello"),
		RedDot:            true,
		MessageScopedUIDs: []string{"u2", "u3"},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != ReasonSuccess {
		t.Fatalf("Send() reason = %v, want success", result.Reason)
	}
	if len(committed.events) != 1 {
		t.Fatalf("committed events = %d, want 1", len(committed.events))
	}
	event := committed.events[0]
	if event.SenderSessionID != 42 {
		t.Fatalf("SenderSessionID = %d, want 42", event.SenderSessionID)
	}
	if event.SenderNodeID != 7 {
		t.Fatalf("SenderNodeID = %d, want 7", event.SenderNodeID)
	}
	if !event.RedDot {
		t.Fatalf("RedDot = false, want true")
	}
	if event.ClientMsgNo != "client-1" {
		t.Fatalf("ClientMsgNo = %q, want client-1", event.ClientMsgNo)
	}
	if event.FromUID != "u1" {
		t.Fatalf("FromUID = %q, want u1", event.FromUID)
	}
	if len(event.MessageScopedUIDs) != 2 || event.MessageScopedUIDs[0] != "u2" || event.MessageScopedUIDs[1] != "u3" {
		t.Fatalf("MessageScopedUIDs = %#v, want u2,u3", event.MessageScopedUIDs)
	}
}

func TestSendNormalizesPersonChannelBeforeAppendAndCommit(t *testing.T) {
	appender := &recordingAppender{}
	committed := &capturingCommitted{}
	app := New(Options{
		Appender:  appender,
		MessageID: &sequenceIDs{next: 100},
		Committed: committed,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:                "u1",
		ChannelID:              "u2",
		ChannelType:            channelTypePerson,
		NormalizePersonChannel: true,
		Payload:                []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != ReasonSuccess {
		t.Fatalf("Send() reason = %v, want success", result.Reason)
	}
	want := runtimechannelid.EncodePersonChannel("u1", "u2")
	if len(appender.requests) != 1 {
		t.Fatalf("append requests = %d, want 1", len(appender.requests))
	}
	if got := appender.requests[0].ChannelID.ID; got != want {
		t.Fatalf("append channel id = %q, want %q", got, want)
	}
	if got := appender.requests[0].Messages[0].ChannelID; got != want {
		t.Fatalf("append message channel id = %q, want %q", got, want)
	}
	if len(committed.events) != 1 || committed.events[0].ChannelID != want {
		t.Fatalf("committed events = %#v, want normalized channel %q", committed.events, want)
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
	mu          sync.Mutex
	calls       int
	requests    []AppendBatchRequest
	err         error
	errs        []error
	itemErrs    []error
	resultLimit int
}

func (a *recordingAppender) AppendBatch(_ context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.mu.Lock()
	a.calls++
	a.requests = append(a.requests, cloneAppendRequest(req))
	err := a.err
	if a.calls <= len(a.errs) {
		err = a.errs[a.calls-1]
	}
	itemErrs := append([]error(nil), a.itemErrs...)
	resultLimit := a.resultLimit
	a.mu.Unlock()
	if err != nil {
		return AppendBatchResult{}, err
	}
	itemCount := len(req.Messages)
	if resultLimit > 0 && resultLimit < itemCount {
		itemCount = resultLimit
	}
	items := make([]AppendBatchItemResult, itemCount)
	for i, msg := range req.Messages[:itemCount] {
		msg.MessageSeq = uint64(i + 1)
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
		if i < len(itemErrs) {
			items[i].Err = itemErrs[i]
		}
	}
	return AppendBatchResult{Items: items}, nil
}

func (a *recordingAppender) requestsByChannel() map[ChannelID]AppendBatchRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[ChannelID]AppendBatchRequest, len(a.requests))
	for _, req := range a.requests {
		out[req.ChannelID] = cloneAppendRequest(req)
	}
	return out
}

type blockingAppender struct {
	started chan ChannelID
	release <-chan struct{}
}

func (a *blockingAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.started <- req.ChannelID
	select {
	case <-a.release:
	case <-ctx.Done():
		return AppendBatchResult{}, ctx.Err()
	}
	items := make([]AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		msg.MessageSeq = uint64(i + 1)
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
	}
	return AppendBatchResult{Items: items}, nil
}

type mutatingAppender struct{}

func (a *mutatingAppender) AppendBatch(_ context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	items := make([]AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		if len(req.Messages[i].Payload) > 0 {
			req.Messages[i].Payload[0] = 'H'
		}
		msg.MessageSeq = uint64(i + 1)
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
	}
	return AppendBatchResult{Items: items}, nil
}

type contextCapturingAppender struct {
	onAppend                    func()
	ctxCanceledAfterFirstCancel bool
}

func (a *contextCapturingAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	if a.onAppend != nil {
		a.onAppend()
	}
	select {
	case <-ctx.Done():
		a.ctxCanceledAfterFirstCancel = true
	default:
	}
	items := make([]AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		msg.MessageSeq = uint64(i + 1)
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
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

type recordingCommitted struct{}

func (recordingCommitted) Submit(context.Context, messageevents.MessageCommitted) error {
	return nil
}

type capturingCommitted struct {
	events []messageevents.MessageCommitted
}

func (c *capturingCommitted) Submit(_ context.Context, event messageevents.MessageCommitted) error {
	c.events = append(c.events, event.Clone())
	return nil
}

type appendObservation struct {
	path string
	err  error
	dur  time.Duration
}

type recordingObserver struct {
	committedErrors int
	appendEvents    []appendObservation
}

func (o *recordingObserver) CommittedSinkError(SendCommand, error) {
	o.committedErrors++
}

func (o *recordingObserver) AppendFinished(path string, err error, dur time.Duration) {
	o.appendEvents = append(o.appendEvents, appendObservation{path: path, err: err, dur: dur})
}

type recordingSendtraceSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *recordingSendtraceSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSendtraceSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}

func requireDurableTraceEvent(t *testing.T, event sendtrace.Event, traceID, channelKey, clientMsgNo, fromUID string, nodeID uint64, messageSeq uint64, result sendtrace.Result, errorCode string) {
	t.Helper()
	if event.Stage != sendtrace.StageMessageSendDurable {
		t.Fatalf("stage = %q, want %q", event.Stage, sendtrace.StageMessageSendDurable)
	}
	if event.TraceID != traceID || event.ChannelKey != channelKey || event.ClientMsgNo != clientMsgNo || event.FromUID != fromUID {
		t.Fatalf("trace fields = %q/%q/%q/%q, want %q/%q/%q/%q",
			event.TraceID, event.ChannelKey, event.ClientMsgNo, event.FromUID, traceID, channelKey, clientMsgNo, fromUID)
	}
	if event.NodeID != nodeID {
		t.Fatalf("NodeID = %d, want %d", event.NodeID, nodeID)
	}
	if event.MessageSeq != messageSeq {
		t.Fatalf("MessageSeq = %d, want %d", event.MessageSeq, messageSeq)
	}
	if event.Result != result || event.ErrorCode != errorCode {
		t.Fatalf("outcome = %q/%q, want %q/%q", event.Result, event.ErrorCode, result, errorCode)
	}
}
