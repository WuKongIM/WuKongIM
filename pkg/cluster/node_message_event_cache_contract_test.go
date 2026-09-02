package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestMessageEventStreamCachePreservesOpenLanesAndAccounting(t *testing.T) {
	cache := newMessageEventStreamCache(4)
	main := messageEventCacheContractAppend("cache-accounting", "message-1", "event-1", "main", metadb.EventTypeStreamDelta, []byte(`{"kind":"text","delta":"a"}`))
	if _, observation, err := cache.appendCachedObserved(main); err != nil {
		t.Fatalf("appendCachedObserved(first delta) error = %v", err)
	} else if observation.Sessions != 1 || observation.OpenLanes != 1 || observation.PayloadBytes == 0 {
		t.Fatalf("first observation = %#v, want one non-empty open lane", observation)
	}

	second := main
	second.EventID = "event-2"
	second.Payload = []byte(`{"kind":"text","delta":"b"}`)
	secondResult, secondObservation, err := cache.appendCachedObserved(second)
	if err != nil {
		t.Fatalf("appendCachedObserved(second delta) error = %v", err)
	}
	if got := messageEventText(t, secondResult.State.SnapshotPayload); got != "ab" {
		t.Fatalf("reduced text = %q, want ab", got)
	}
	if secondObservation.Sessions != 1 || secondObservation.OpenLanes != 1 {
		t.Fatalf("second observation = %#v, want one open lane", secondObservation)
	}

	duplicate := second
	duplicate.Payload = []byte(`{"kind":"text","delta":"must-not-apply"}`)
	duplicateResult, duplicateObservation, err := cache.appendCachedObserved(duplicate)
	if err != nil {
		t.Fatalf("appendCachedObserved(duplicate) error = %v", err)
	}
	if got := messageEventText(t, duplicateResult.State.SnapshotPayload); got != "ab" {
		t.Fatalf("duplicate reduced text = %q, want idempotent ab", got)
	}
	if duplicateObservation != secondObservation {
		t.Fatalf("duplicate observation = %#v, want unchanged %#v", duplicateObservation, secondObservation)
	}

	tool := main
	tool.EventID = "event-3"
	tool.EventKey = "tool"
	tool.EventType = metadb.EventTypeStreamSnapshot
	tool.Payload = []byte(`{"kind":"json","value":{"done":true}}`)
	if _, observation, err := cache.appendCachedObserved(tool); err != nil {
		t.Fatalf("appendCachedObserved(tool snapshot) error = %v", err)
	} else if observation.OpenLanes != 2 || observation.PayloadBytes <= secondObservation.PayloadBytes {
		t.Fatalf("tool observation = %#v, want two accounted open lanes", observation)
	}

	finish := main
	finish.EventID = "event-finish"
	finish.EventKey = metadb.EventKeyFinish
	finish.EventType = metadb.EventTypeStreamFinish
	open := cache.openStatesForFinish(finish)
	if got := []string{open[0].EventKey, open[1].EventKey}; !reflect.DeepEqual(got, []string{"main", "tool"}) {
		t.Fatalf("open lane order = %#v, want deterministic [main tool]", got)
	}
	open[0].SnapshotPayload[0] = 'x'
	stored := messageEventStateByKey(t, cache.states(metadb.MessageEventMessageKey{
		ChannelID: main.ChannelID, ChannelType: main.ChannelType, ClientMsgNo: main.ClientMsgNo,
	}), "main")
	if got := messageEventText(t, stored.SnapshotPayload); got != "ab" {
		t.Fatalf("mutating returned state changed cache text to %q", got)
	}

	closeResult := metadb.MessageEventAppendResult{
		ChannelID: main.ChannelID, ChannelType: main.ChannelType,
		ClientMsgNo: main.ClientMsgNo, EventID: "event-close", EventKey: "main",
		MsgEventSeq: 8, Status: metadb.EventStatusClosed,
		State: metadb.MessageEventState{
			ChannelID: main.ChannelID, ChannelType: main.ChannelType,
			ClientMsgNo: main.ClientMsgNo, EventKey: "main",
			Status: metadb.EventStatusClosed, LastMsgEventSeq: 8,
			SnapshotPayload: []byte(`{"kind":"text","text":"ab"}`),
		},
	}
	closeEvent := main
	closeEvent.EventID = "event-close"
	closeEvent.EventType = metadb.EventTypeStreamClose
	cache.markTerminalPersisted(closeEvent, closeResult)
	if observation := cache.observation(); observation.OpenLanes != 1 || observation.Sessions != 1 {
		t.Fatalf("terminal observation = %#v, want only tool lane open", observation)
	}
	remaining := cache.openStatesForFinish(finish)
	if len(remaining) != 1 || remaining[0].EventKey != "tool" {
		t.Fatalf("remaining open lanes = %#v, want tool only", remaining)
	}

	if observation := cache.removeObserved(main); observation.Sessions != 0 || observation.OpenLanes != 0 || observation.PayloadBytes != 0 {
		t.Fatalf("remove observation = %#v, want empty accounting", observation)
	}
}

func TestMessageEventStreamCacheRestoreFenceRequiresExplicitResume(t *testing.T) {
	cache := newMessageEventStreamCache(2)
	event := messageEventCacheContractAppend("restore-fence", "message-1", "event-1", "main", metadb.EventTypeStreamDelta, []byte(`{"kind":"text","delta":"a"}`))
	if _, err := cache.appendCached(event); err != nil {
		t.Fatalf("appendCached() error = %v", err)
	}

	cache.pauseForRestore()
	if observation := cache.observation(); observation.Sessions != 0 || observation.OpenLanes != 0 || observation.PayloadBytes != 0 {
		t.Fatalf("pause observation = %#v, want cleared cache", observation)
	}
	if _, err := cache.appendCached(event); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("appendCached(paused) error = %v, want ErrMaintenance", err)
	}

	cache.resetAfterRestore()
	if _, err := cache.appendCached(event); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("appendCached(after reset, before resume) error = %v, want ErrMaintenance", err)
	}
	cache.resumeAfterRestore()
	if _, err := cache.appendCached(event); err != nil {
		t.Fatalf("appendCached(after resume) error = %v", err)
	}
}

func TestMessageEventStreamCacheRemovesOnlyLostHashSlotSessions(t *testing.T) {
	cache := newMessageEventStreamCache(4)
	left := keyForNodeHashSlot(t, 4, 0)
	right := keyForNodeHashSlot(t, 4, 3)
	leftEvent := messageEventCacheContractAppend(left, "left", "event-left", "main", metadb.EventTypeStreamDelta, []byte(`{"kind":"text","delta":"left"}`))
	rightEvent := messageEventCacheContractAppend(right, "right", "event-right", "main", metadb.EventTypeStreamDelta, []byte(`{"kind":"text","delta":"right"}`))
	if _, err := cache.appendCached(leftEvent); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.appendCached(rightEvent); err != nil {
		t.Fatal(err)
	}

	observation := cache.removeHashSlotsObserved(map[uint16]struct{}{0: {}}, 4)
	if observation.Sessions != 1 || observation.OpenLanes != 1 {
		t.Fatalf("observation = %#v, want one retained session", observation)
	}
	if states := cache.states(metadb.MessageEventMessageKey{ChannelID: left, ChannelType: 2, ClientMsgNo: "left"}); len(states) != 0 {
		t.Fatalf("lost hash-slot states = %#v, want removed", states)
	}
	if states := cache.states(metadb.MessageEventMessageKey{ChannelID: right, ChannelType: 2, ClientMsgNo: "right"}); len(states) != 1 {
		t.Fatalf("unaffected hash-slot states = %#v, want retained", states)
	}
	if got := cache.removeHashSlotsObserved(nil, 4); got != observation {
		t.Fatalf("empty removal observation = %#v, want unchanged %#v", got, observation)
	}
}

func TestMessageEventNormalizationDefinesStableIngressDefaults(t *testing.T) {
	payload := []byte(`{"kind":"text","delta":"hello"}`)
	normalized, err := normalizeClusterMessageEventAppend(metadb.MessageEventAppend{
		ChannelID: " room ", ChannelType: 2, ClientMsgNo: " message ",
		EventID: " event ", EventType: " STREAM.DELTA ", Payload: payload,
	})
	if err != nil {
		t.Fatalf("normalizeClusterMessageEventAppend() error = %v", err)
	}
	if normalized.ChannelID != "room" || normalized.ClientMsgNo != "message" || normalized.EventID != "event" ||
		normalized.EventKey != metadb.EventKeyDefault || normalized.EventType != metadb.EventTypeStreamDelta ||
		normalized.Visibility != metadb.VisibilityPublic {
		t.Fatalf("normalized event = %#v", normalized)
	}
	payload[0] = 'x'
	if !json.Valid(normalized.Payload) {
		t.Fatal("normalized payload aliases caller memory")
	}

	finish, err := normalizeClusterMessageEventAppend(metadb.MessageEventAppend{
		ChannelID: "room", ChannelType: 2, ClientMsgNo: "message",
		EventID: "finish", EventKey: "caller-key", EventType: metadb.EventTypeStreamFinish,
	})
	if err != nil {
		t.Fatalf("normalize finish error = %v", err)
	}
	if finish.EventKey != metadb.EventKeyFinish {
		t.Fatalf("finish EventKey = %q, want %q", finish.EventKey, metadb.EventKeyFinish)
	}

	for _, invalid := range []metadb.MessageEventAppend{
		{ChannelType: 2, ClientMsgNo: "message", EventID: "event", EventType: metadb.EventTypeStreamOpen},
		{ChannelID: "room", ChannelType: 0, ClientMsgNo: "message", EventID: "event", EventType: metadb.EventTypeStreamOpen},
		{ChannelID: "room", ChannelType: 2, ClientMsgNo: "message", EventID: "event", EventType: "unknown"},
	} {
		if _, err := normalizeClusterMessageEventAppend(invalid); !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("normalize invalid %#v error = %v, want ErrInvalidArgument", invalid, err)
		}
	}
}

func TestNodeMessageEventLocalPathsKeepCacheAndDurabilitySeparated(t *testing.T) {
	proposer := &recordingNodeResultProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	node.messageEventFinishCoalescer = nil
	channelID := keyForNodeHashSlot(t, 4, 0)
	delta := messageEventCacheContractAppend(channelID, "message-1", "event-delta", "main", metadb.EventTypeStreamDelta, []byte(`{"kind":"text","delta":"hello"}`))
	if result, err := node.AppendMessageEvent(context.Background(), delta); err != nil {
		t.Fatalf("AppendMessageEvent(delta) error = %v", err)
	} else if got := messageEventText(t, result.State.SnapshotPayload); got != "hello" {
		t.Fatalf("cached delta text = %q, want hello", got)
	}
	if proposer.resultCalls != 0 {
		t.Fatalf("cache-only delta proposals = %d, want 0", proposer.resultCalls)
	}

	closeEvent := delta
	closeEvent.EventID = "event-close"
	closeEvent.EventType = metadb.EventTypeStreamClose
	closeEvent.Payload = []byte(`{"end_reason":2}`)
	closeResult := messageEventClosedResultForExactEvent(closeEvent, 7)
	closeResult.State.SnapshotPayload = []byte(`{"kind":"text","text":"hello"}`)
	proposer.result = metafsm.EncodeAppendMessageEventResult(closeResult)
	got, err := node.AppendMessageEvent(context.Background(), closeEvent)
	if err != nil {
		t.Fatalf("AppendMessageEvent(close) error = %v", err)
	}
	if got.MsgEventSeq != 7 || got.Status != metadb.EventStatusClosed || proposer.resultCalls != 1 {
		t.Fatalf("close result = %#v proposals=%d, want one durable close", got, proposer.resultCalls)
	}
	if proposer.last.Key != channelID {
		t.Fatalf("durable proposal key = %q, want channel %q", proposer.last.Key, channelID)
	}
	if observation := node.messageEventStreamCache.observation(); observation.Sessions != 1 || observation.OpenLanes != 0 {
		t.Fatalf("post-close cache = %#v, want retained terminal idempotency session", observation)
	}

	miss := messageEventCacheContractAppend(keyForNodeHashSlot(t, 4, 1), "message-miss", "finish-miss", metadb.EventKeyFinish, metadb.EventTypeStreamFinish, nil)
	before := proposer.resultCalls
	if _, err := node.AppendMessageEvent(context.Background(), miss); !errors.Is(err, ErrMessageEventStreamCacheMiss) {
		t.Fatalf("AppendMessageEvent(finish cache miss) error = %v, want ErrMessageEventStreamCacheMiss", err)
	}
	if proposer.resultCalls != before {
		t.Fatalf("finish cache miss proposals = %d, want none", proposer.resultCalls-before)
	}

	node.maintenance.Store(true)
	if _, err := node.AppendMessageEvent(context.Background(), delta); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("AppendMessageEvent(maintenance) error = %v, want ErrMaintenance", err)
	}
}

func TestNodeMessageEventFinishFlushesAllCachedLanesInOneProposal(t *testing.T) {
	proposer := &recordingNodeResultProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	node.messageEventFinishCoalescer = nil
	channelID := keyForNodeHashSlot(t, 4, 0)
	for index, lane := range []string{"main", "tool"} {
		event := messageEventCacheContractAppend(channelID, "message-finish", "delta-"+lane, lane, metadb.EventTypeStreamDelta, []byte(`{"kind":"text","delta":"lane"}`))
		event.OccurredAt = int64(index + 1)
		if _, err := node.AppendMessageEvent(context.Background(), event); err != nil {
			t.Fatalf("AppendMessageEvent(%s delta) error = %v", lane, err)
		}
	}
	finish := messageEventCacheContractAppend(channelID, "message-finish", "finish-1", metadb.EventKeyFinish, metadb.EventTypeStreamFinish, []byte(`{"end_reason":1}`))
	proposer.result = metafsm.EncodeAppendMessageEventResults([]metadb.MessageEventAppendResult{
		messageEventClosedResult(finish, "main", 10, []byte(`{"kind":"text","text":"lane"}`)),
		messageEventClosedResult(finish, "tool", 11, []byte(`{"kind":"text","text":"lane"}`)),
		messageEventClosedResult(finish, metadb.EventKeyFinish, 12, nil),
	})

	result, err := node.AppendMessageEvent(context.Background(), finish)
	if err != nil {
		t.Fatalf("AppendMessageEvent(finish) error = %v", err)
	}
	if result.EventKey != metadb.EventKeyFinish || result.MsgEventSeq != 12 {
		t.Fatalf("finish result = %#v, want final batch result", result)
	}
	if proposer.resultCalls != 1 {
		t.Fatalf("finish proposals = %d, want one atomic batch proposal", proposer.resultCalls)
	}
	if observation := node.messageEventStreamCache.observation(); observation.Sessions != 0 || observation.OpenLanes != 0 || observation.PayloadBytes != 0 {
		t.Fatalf("post-finish cache = %#v, want removed session", observation)
	}
}

func TestNodeMessageEventDurableResultFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result []byte
		want   error
	}{
		{name: "stale metadata", result: []byte(metafsm.ApplyResultStaleMeta), want: metadb.ErrStaleMeta},
		{name: "corrupt reducer bytes", result: []byte("not-a-result"), want: metadb.ErrCorruptValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposer := &recordingNodeResultProposer{result: tc.result}
			node := newStartedSlotProxyPortNode(t, proposer)
			channelID := keyForNodeHashSlot(t, 4, 0)
			event := messageEventCacheContractAppend(channelID, "message-error", "event-close", "main", metadb.EventTypeStreamClose, []byte(`{"end_reason":2}`))
			if _, err := node.AppendMessageEvent(context.Background(), event); !errors.Is(err, tc.want) {
				t.Fatalf("AppendMessageEvent() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMergeMessageEventStateOverlayKeepsFreshOpenProjection(t *testing.T) {
	durable := []metadb.MessageEventState{
		{EventKey: "main", Status: metadb.EventStatusClosed, LastMsgEventSeq: 9},
		{EventKey: "persisted", Status: metadb.EventStatusClosed, LastMsgEventSeq: 7},
	}
	cached := []metadb.MessageEventState{
		{EventKey: "main", Status: metadb.EventStatusOpen, LastMsgEventSeq: 0},
		{EventKey: "older-terminal", Status: metadb.EventStatusClosed, LastMsgEventSeq: 2},
	}
	got := mergeMessageEventStateOverlay(durable, cached, 3)
	if keys := []string{got[0].EventKey, got[1].EventKey, got[2].EventKey}; !reflect.DeepEqual(keys, []string{"main", "older-terminal", "persisted"}) {
		t.Fatalf("merged keys = %#v, want deterministic limited order", keys)
	}
	if got[0].Status != metadb.EventStatusOpen {
		t.Fatalf("main status = %q, want live open overlay", got[0].Status)
	}

	newerDurable := []metadb.MessageEventState{{EventKey: "same", Status: metadb.EventStatusClosed, LastMsgEventSeq: 8}}
	olderCached := []metadb.MessageEventState{{EventKey: "same", Status: metadb.EventStatusClosed, LastMsgEventSeq: 7}}
	if merged := mergeMessageEventStateOverlay(newerDurable, olderCached, 0); merged[0].LastMsgEventSeq != 8 {
		t.Fatalf("terminal overlay rolled back durable seq to %d", merged[0].LastMsgEventSeq)
	}
}

func messageEventCacheContractAppend(channelID, clientMsgNo, eventID, eventKey, eventType string, payload []byte) metadb.MessageEventAppend {
	return metadb.MessageEventAppend{
		ChannelID: channelID, ChannelType: 2, ClientMsgNo: clientMsgNo,
		EventID: eventID, EventKey: eventKey, EventType: eventType,
		Visibility: metadb.VisibilityPublic, OccurredAt: 1,
		Payload: payload, UpdatedAt: 2,
	}
}

func messageEventClosedResult(event metadb.MessageEventAppend, eventKey string, seq uint64, snapshot []byte) metadb.MessageEventAppendResult {
	eventID := event.EventID
	if eventKey != metadb.EventKeyFinish {
		eventID = finishFlushMessageEventID(event.EventID, eventKey)
	}
	return metadb.MessageEventAppendResult{
		ChannelID: event.ChannelID, ChannelType: event.ChannelType,
		ClientMsgNo: event.ClientMsgNo, EventID: eventID, EventKey: eventKey,
		MsgEventSeq: seq, Status: metadb.EventStatusClosed,
		State: metadb.MessageEventState{
			ChannelID: event.ChannelID, ChannelType: event.ChannelType,
			ClientMsgNo: event.ClientMsgNo, EventKey: eventKey,
			Status: metadb.EventStatusClosed, LastMsgEventSeq: seq,
			SnapshotPayload: snapshot,
		},
	}
}

func messageEventText(t *testing.T, payload []byte) string {
	t.Helper()
	var snapshot struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode message-event snapshot %q: %v", payload, err)
	}
	return snapshot.Text
}

func messageEventStateByKey(t *testing.T, states []metadb.MessageEventState, eventKey string) metadb.MessageEventState {
	t.Helper()
	for _, state := range states {
		if state.EventKey == eventKey {
			return state
		}
	}
	t.Fatalf("event key %q missing from %#v", eventKey, states)
	return metadb.MessageEventState{}
}

func TestMessageEventLostAuthorityDetectionUsesBeforeAndAfterSnapshots(t *testing.T) {
	before := routeAuthorityTable(1)
	router := routing.NewRouter()
	if err := router.UpdateControlSnapshot(routeAuthoritySnapshot(2)); err != nil {
		t.Fatal(err)
	}
	router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 10}})
	node := &Node{cfg: Config{NodeID: 1}, messageEventStreamCache: newMessageEventStreamCache(2)}
	lost := node.messageEventLostLocalAuthorityHashSlots(before, router.Table())
	if !reflect.DeepEqual(lost, map[uint16]struct{}{0: {}, 1: {}}) {
		t.Fatalf("lost hash slots = %#v, want both former local slots", lost)
	}
}
