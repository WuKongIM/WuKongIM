package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestMessageEventObserverKeepsBoundedOperationalDimensions(t *testing.T) {
	observer := &messageEventContractObserver{}
	node := &Node{cfg: Config{MessageEvent: MessageEventConfig{Observer: observer}}}
	duration := 7 * time.Millisecond
	event := messageEventCacheContractAppend("observer-channel", "message-1", "event-1", "main", metadb.EventTypeStreamDelta, nil)

	node.observeMessageEventAppend(messageEventPathCache, event, messageEventResultOK, duration)
	node.observeMessageEventPropose(messageEventPathFinishBatch, messageEventResultError, 3, duration)
	node.observeMessageEventAppendStage(messageEventPathFinishBatch, messageEventResultOK, messageEventAppendStageFinishBatchBuild, duration)
	node.observeMessageEventProposeStage(messageEventPathDurable, messageEventResultOK, messageEventProposeStageDecode, duration)
	node.setMessageEventStreamCache(MessageEventStreamCacheObservation{Sessions: 2, OpenLanes: 3, PayloadBytes: 64, MaxSessions: 10})

	if len(observer.appends) != 1 || observer.appends[0].Path != messageEventPathCache || observer.appends[0].EventType != metadb.EventTypeStreamDelta || observer.appends[0].Duration != duration {
		t.Fatalf("append observations = %#v", observer.appends)
	}
	if len(observer.proposes) != 1 || observer.proposes[0].Path != messageEventPathFinishBatch || observer.proposes[0].BatchSize != 3 {
		t.Fatalf("propose observations = %#v", observer.proposes)
	}
	if len(observer.appendStages) != 1 || observer.appendStages[0].Stage != messageEventAppendStageFinishBatchBuild {
		t.Fatalf("append-stage observations = %#v", observer.appendStages)
	}
	if len(observer.proposeStages) != 1 || observer.proposeStages[0].Stage != messageEventProposeStageDecode {
		t.Fatalf("propose-stage observations = %#v", observer.proposeStages)
	}
	if len(observer.caches) != 1 || observer.caches[0].Sessions != 2 || observer.caches[0].OpenLanes != 3 {
		t.Fatalf("cache observations = %#v", observer.caches)
	}

	forwarded := &messageEventForwardedStageObserver{}
	adapter := messageEventProposeStageAdapter{node: node, path: messageEventPathDurable, next: forwarded}
	adapter.ObserveChannelAppendStage(defaultSlotStageMetaCreateSubmit, "err", duration)
	adapter.ObserveChannelAppendStage("unmapped_stage", messageEventResultOK, duration)
	if len(observer.proposeStages) != 2 {
		t.Fatalf("mapped propose stages = %#v, want one additional mapped stage", observer.proposeStages)
	}
	mapped := observer.proposeStages[1]
	if mapped.Stage != messageEventProposeStageSlotSubmit || mapped.Result != messageEventResultError || mapped.Path != messageEventPathDurable {
		t.Fatalf("mapped propose stage = %#v", mapped)
	}
	if !reflect.DeepEqual(forwarded.stages, []messageEventForwardedStage{
		{stage: defaultSlotStageMetaCreateSubmit, result: "err", duration: duration},
		{stage: "unmapped_stage", result: messageEventResultOK, duration: duration},
	}) {
		t.Fatalf("forwarded stages = %#v", forwarded.stages)
	}
}

func TestMessageEventErrorClassificationIsStableAndLowCardinality(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "success", err: nil, want: messageEventResultOK},
		{name: "backpressured", err: fmt.Errorf("wrapped: %w", ErrBackpressured), want: messageEventResultBackpressured},
		{name: "not leader", err: ErrNotLeader, want: messageEventResultNotLeader},
		{name: "not started", err: ErrNotStarted, want: messageEventResultNotReady},
		{name: "stopping", err: ErrStopping, want: messageEventResultNotReady},
		{name: "no slot leader", err: ErrNoSlotLeader, want: messageEventResultNoSlotLeader},
		{name: "cache miss", err: ErrMessageEventStreamCacheMiss, want: messageEventResultCacheMiss},
		{name: "invalid argument", err: metadb.ErrInvalidArgument, want: messageEventResultInvalid},
		{name: "stale metadata", err: metadb.ErrStaleMeta, want: messageEventResultInvalid},
		{name: "corrupt reducer", err: metadb.ErrCorruptValue, want: messageEventResultInvalid},
		{name: "unknown", err: errors.New("storage unavailable"), want: messageEventResultError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := messageEventResultForError(tc.err); got != tc.want {
				t.Fatalf("messageEventResultForError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: defaultSlotStageMetaCreateSubmit, want: messageEventProposeStageSlotSubmit, ok: true},
		{input: defaultSlotStageMetaCreateWait, want: messageEventProposeStageSlotFutureWait, ok: true},
		{input: "meta_create_slot_raft_commit_wait", want: messageEventProposeStageSlotRaftCommit, ok: true},
		{input: "meta_create_slot_fsm_commit", want: messageEventProposeStageSlotFSMCommit, ok: true},
		{input: "unknown", want: "", ok: false},
	} {
		if got, ok := messageEventSlotProposalStage(tc.input); got != tc.want || ok != tc.ok {
			t.Fatalf("messageEventSlotProposalStage(%q) = (%q,%t), want (%q,%t)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

func TestMessageEventFinishCoalescerFlushesLiveRequestsTogether(t *testing.T) {
	proposer := &recordingNodeResultProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	channelID := keyForNodeHashSlot(t, 4, 0)
	firstEvent := messageEventCacheContractAppend(channelID, "message-1", "close-1", "main", metadb.EventTypeStreamClose, []byte(`{"end_reason":1}`))
	secondEvent := messageEventCacheContractAppend(channelID, "message-2", "close-2", "main", metadb.EventTypeStreamClose, []byte(`{"end_reason":2}`))
	proposer.result = metafsm.EncodeAppendMessageEventResults([]metadb.MessageEventAppendResult{
		messageEventClosedResultForExactEvent(firstEvent, 10),
		messageEventClosedResultForExactEvent(secondEvent, 11),
	})

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	canceledRequest := &messageEventFinishCoalesceRequest{
		ctx: canceledContext, event: firstEvent, events: []metadb.MessageEventAppend{firstEvent},
		done: make(chan messageEventFinishCoalesceResult, 1),
	}
	firstRequest := &messageEventFinishCoalesceRequest{
		ctx: context.Background(), event: firstEvent, events: []metadb.MessageEventAppend{firstEvent},
		done: make(chan messageEventFinishCoalesceResult, 1),
	}
	secondRequest := &messageEventFinishCoalesceRequest{
		ctx: context.Background(), event: secondEvent, events: []metadb.MessageEventAppend{secondEvent},
		done: make(chan messageEventFinishCoalesceResult, 1),
	}
	key := messageEventFinishCoalesceKey{channelID: channelID, channelType: 2}
	coalescer := newMessageEventFinishCoalescer(time.Second)
	coalescer.groups[key] = &messageEventFinishCoalesceGroup{requests: []*messageEventFinishCoalesceRequest{
		canceledRequest, firstRequest, secondRequest,
	}}
	coalescer.flush(node, key)

	if got := <-canceledRequest.done; !errors.Is(got.err, context.Canceled) || got.path != messageEventPathFinishBatch {
		t.Fatalf("canceled result = %#v, want context cancellation", got)
	}
	if got := <-firstRequest.done; got.err != nil || got.result.EventID != firstEvent.EventID || got.result.MsgEventSeq != 10 || got.path != messageEventPathFinishBatch {
		t.Fatalf("first live result = %#v", got)
	}
	if got := <-secondRequest.done; got.err != nil || got.result.EventID != secondEvent.EventID || got.result.MsgEventSeq != 11 || got.path != messageEventPathFinishBatch {
		t.Fatalf("second live result = %#v", got)
	}
	if proposer.resultCalls != 1 {
		t.Fatalf("coalesced proposal calls = %d, want 1", proposer.resultCalls)
	}
	if _, exists := coalescer.groups[key]; exists {
		t.Fatal("flushed coalescer group remains published")
	}
}

func TestMessageEventFinishCoalescerFailsClosedOnMissingReducerResult(t *testing.T) {
	proposer := &recordingNodeResultProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	channelID := keyForNodeHashSlot(t, 4, 0)
	event := messageEventCacheContractAppend(channelID, "message-1", "close-1", "main", metadb.EventTypeStreamClose, nil)
	other := event
	other.EventID = "different-event"
	proposer.result = metafsm.EncodeAppendMessageEventResult(messageEventClosedResultForExactEvent(other, 4))
	request := &messageEventFinishCoalesceRequest{
		ctx: context.Background(), event: event, events: []metadb.MessageEventAppend{event},
		done: make(chan messageEventFinishCoalesceResult, 1),
	}
	key := messageEventFinishCoalesceKey{channelID: channelID, channelType: 2}
	coalescer := newMessageEventFinishCoalescer(time.Second)
	coalescer.groups[key] = &messageEventFinishCoalesceGroup{requests: []*messageEventFinishCoalesceRequest{request}}
	coalescer.flush(node, key)
	if got := <-request.done; !errors.Is(got.err, metadb.ErrCorruptValue) || got.path != messageEventPathDurable {
		t.Fatalf("missing reducer result = %#v, want durable ErrCorruptValue", got)
	}
}

func TestMessageEventFinishCoalescerRemovalIsIdentityBound(t *testing.T) {
	coalescer := newMessageEventFinishCoalescer(time.Second)
	key := messageEventFinishCoalesceKey{channelID: "room", channelType: 2}
	first := &messageEventFinishCoalesceRequest{}
	second := &messageEventFinishCoalesceRequest{}
	missing := &messageEventFinishCoalesceRequest{}
	coalescer.groups[key] = &messageEventFinishCoalesceGroup{requests: []*messageEventFinishCoalesceRequest{first, second}}
	if coalescer.remove(key, missing) {
		t.Fatal("remove(missing) = true")
	}
	if !coalescer.remove(key, first) || len(coalescer.groups[key].requests) != 1 || coalescer.groups[key].requests[0] != second {
		t.Fatalf("group after first removal = %#v", coalescer.groups[key])
	}
	if !coalescer.remove(key, second) {
		t.Fatal("remove(second) = false")
	}
	if _, exists := coalescer.groups[key]; exists {
		t.Fatal("empty group was not removed")
	}
}

func TestMessageEventRPCProjectionIsDeterministicAndDetached(t *testing.T) {
	firstKey := metadb.MessageEventMessageKey{ChannelID: "b", ChannelType: 2, ClientMsgNo: "m2"}
	secondKey := metadb.MessageEventMessageKey{ChannelID: "a", ChannelType: 2, ClientMsgNo: "m1"}
	rows := map[metadb.MessageEventMessageKey][]metadb.MessageEventState{
		firstKey:  {{EventKey: "main", SnapshotPayload: []byte("first")}},
		secondKey: {{EventKey: "main", SnapshotPayload: []byte("second")}},
	}
	entries := messageEventStateEntriesFromMap(rows)
	if len(entries) != 2 || entries[0].Key != secondKey || entries[1].Key != firstKey {
		t.Fatalf("RPC entries = %#v, want deterministic key order", entries)
	}
	rows[secondKey][0].SnapshotPayload[0] = 'x'
	if string(entries[0].States[0].SnapshotPayload) != "second" {
		t.Fatal("RPC entries alias input state payload")
	}
	entries = append(entries, messageEventStatesRPCEntry{Key: metadb.MessageEventMessageKey{ChannelID: "empty"}})
	mapped := messageEventStateMapFromEntries(entries)
	if len(mapped) != 2 || string(mapped[secondKey][0].SnapshotPayload) != "second" {
		t.Fatalf("mapped RPC states = %#v", mapped)
	}
	entries[0].States[0].SnapshotPayload[0] = 'y'
	if string(mapped[secondKey][0].SnapshotPayload) != "second" {
		t.Fatal("mapped RPC states alias wire payload")
	}
}

func TestMessageEventRPCHandlerValidatesOperationBeforeMutation(t *testing.T) {
	node := newStartedSlotProxyPortNode(t, &recordingProposer{})
	handler := messageEventAppendRPCHandler{node: node}
	channelID := keyForNodeHashSlot(t, 4, 0)
	event := messageEventCacheContractAppend(channelID, "message-rpc", "event-rpc", "main", metadb.EventTypeStreamDelta, []byte(`{"kind":"text","delta":"rpc"}`))
	body, err := json.Marshal(messageEventAppendRPCRequest{Op: "append", Event: event})
	if err != nil {
		t.Fatal(err)
	}
	responseBody, err := handler.HandleRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleRPC(append) error = %v", err)
	}
	var response messageEventAppendRPCResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		t.Fatalf("decode append response error = %v", err)
	}
	if got := messageEventText(t, response.Result.State.SnapshotPayload); got != "rpc" {
		t.Fatalf("RPC append cached text = %q, want rpc", got)
	}
	if _, err := handler.HandleRPC(context.Background(), []byte("not-json")); err == nil {
		t.Fatal("HandleRPC(invalid JSON) error = nil")
	}
	body, err = json.Marshal(messageEventAppendRPCRequest{Op: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.HandleRPC(context.Background(), body); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("HandleRPC(unknown op) error = %v, want ErrInvalidArgument", err)
	}
}

type messageEventContractObserver struct {
	appends       []MessageEventAppendObservation
	appendStages  []MessageEventAppendStageObservation
	proposes      []MessageEventProposeObservation
	proposeStages []MessageEventProposeStageObservation
	caches        []MessageEventStreamCacheObservation
}

func (o *messageEventContractObserver) ObserveMessageEventAppend(event MessageEventAppendObservation) {
	o.appends = append(o.appends, event)
}

func (o *messageEventContractObserver) ObserveMessageEventAppendStage(event MessageEventAppendStageObservation) {
	o.appendStages = append(o.appendStages, event)
}

func (o *messageEventContractObserver) ObserveMessageEventPropose(event MessageEventProposeObservation) {
	o.proposes = append(o.proposes, event)
}

func (o *messageEventContractObserver) ObserveMessageEventProposeStage(event MessageEventProposeStageObservation) {
	o.proposeStages = append(o.proposeStages, event)
}

func (o *messageEventContractObserver) SetMessageEventStreamCache(event MessageEventStreamCacheObservation) {
	o.caches = append(o.caches, event)
}

type messageEventForwardedStage struct {
	stage    string
	result   string
	duration time.Duration
}

type messageEventForwardedStageObserver struct {
	stages []messageEventForwardedStage
}

func (o *messageEventForwardedStageObserver) ObserveChannelAppendStage(stage string, result string, duration time.Duration) {
	o.stages = append(o.stages, messageEventForwardedStage{stage: stage, result: result, duration: duration})
}

func messageEventClosedResultForExactEvent(event metadb.MessageEventAppend, seq uint64) metadb.MessageEventAppendResult {
	return metadb.MessageEventAppendResult{
		ChannelID: event.ChannelID, ChannelType: event.ChannelType,
		ClientMsgNo: event.ClientMsgNo, EventID: event.EventID, EventKey: event.EventKey,
		MsgEventSeq: seq, Status: metadb.EventStatusClosed,
		State: metadb.MessageEventState{
			ChannelID: event.ChannelID, ChannelType: event.ChannelType,
			ClientMsgNo: event.ClientMsgNo, EventKey: event.EventKey,
			Status: metadb.EventStatusClosed, LastMsgEventSeq: seq,
		},
	}
}
