package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterMessageEventFacadeAppendsTerminalThroughChannelHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-channel"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-event-1",
		EventID:     "evt-1",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"snapshot":{"kind":"text","text":"hello"}}`),
		UpdatedAt:   1001,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent() error = %v", err)
	}
	if result.MsgEventSeq != 1 || result.Status != metadb.EventStatusClosed {
		t.Fatalf("append result = %#v, want seq=1 closed", result)
	}

	states, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-event-1", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(channel hash slot) error = %v", err)
	}
	if len(states) != 1 || states[0].LastMsgEventSeq != 1 || states[0].LastEventID != "evt-1" {
		t.Fatalf("stored states = %#v, want one state at seq 1", states)
	}
}

func TestClusterMessageEventFacadeCachesStreamUntilTerminalEvent(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-stream-cache"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-stream-cache",
		EventID:     "evt-delta-1",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","delta":"hello "}`),
		UpdatedAt:   1001,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(delta 1) error = %v", err)
	}
	if result.MsgEventSeq != 0 || result.Status != metadb.EventStatusOpen {
		t.Fatalf("delta result = %#v, want cache-only open seq 0", result)
	}

	result, err = node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-stream-cache",
		EventID:     "evt-delta-2",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1002,
		Payload:     []byte(`{"kind":"text","delta":"world"}`),
		UpdatedAt:   1003,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(delta 2) error = %v", err)
	}
	if !strings.Contains(string(result.State.SnapshotPayload), "hello world") {
		t.Fatalf("cached delta snapshot = %s, want accumulated text", result.State.SnapshotPayload)
	}

	states, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-stream-cache", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(before terminal) error = %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("states before terminal = %#v, want no durable event state", states)
	}
	key := metadb.MessageEventMessageKey{ChannelID: channelID, ChannelType: 2, ClientMsgNo: "cmn-stream-cache"}
	summary, err := node.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{key}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch(before terminal) error = %v", err)
	}
	if len(summary[key]) != 1 || summary[key][0].LastMsgEventSeq != 0 || summary[key][0].Status != metadb.EventStatusOpen {
		t.Fatalf("summary before terminal = %#v, want one cached open state", summary[key])
	}
	if !strings.Contains(string(summary[key][0].SnapshotPayload), "hello world") {
		t.Fatalf("summary snapshot before terminal = %s, want cached text", summary[key][0].SnapshotPayload)
	}

	closed, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-stream-cache",
		EventID:     "evt-close",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1004,
		Payload:     []byte(`{"end_reason":2}`),
		UpdatedAt:   1005,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(close) error = %v", err)
	}
	if closed.MsgEventSeq != 1 || closed.Status != metadb.EventStatusClosed || closed.State.EndReason != 2 {
		t.Fatalf("close result = %#v, want one durable closed event", closed)
	}
	if !strings.Contains(string(closed.State.SnapshotPayload), "hello world") {
		t.Fatalf("closed snapshot = %s, want cached text merged", closed.State.SnapshotPayload)
	}

	states, err = node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-stream-cache", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(after terminal) error = %v", err)
	}
	if len(states) != 1 || states[0].LastMsgEventSeq != 1 || states[0].LastEventID != "evt-close" {
		t.Fatalf("states after terminal = %#v, want one closed durable state", states)
	}
	summary, err = node.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{key}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch(after terminal) error = %v", err)
	}
	if len(summary[key]) != 1 || summary[key][0].LastMsgEventSeq != 1 || summary[key][0].Status != metadb.EventStatusClosed {
		t.Fatalf("summary after terminal = %#v, want one durable closed state", summary[key])
	}
}

func TestClusterMessageEventFacadeFinishFlushesCachedLanes(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-stream-finish"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for _, tc := range []struct {
		eventID string
		key     string
		delta   string
	}{
		{eventID: "evt-main-delta", key: "main", delta: "answer"},
		{eventID: "evt-tool-delta", key: "tool", delta: "lookup"},
	} {
		if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: "cmn-stream-finish",
			EventID:     tc.eventID,
			EventKey:    tc.key,
			EventType:   metadb.EventTypeStreamDelta,
			Visibility:  metadb.VisibilityPublic,
			OccurredAt:  1000,
			Payload:     []byte(`{"kind":"text","delta":"` + tc.delta + `"}`),
			UpdatedAt:   1001,
		}); err != nil {
			t.Fatalf("AppendMessageEvent(%s delta) error = %v", tc.key, err)
		}
	}

	result, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-stream-finish",
		EventID:     "evt-finish",
		EventType:   metadb.EventTypeStreamFinish,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1002,
		Payload:     []byte(`{"end_reason":3}`),
		UpdatedAt:   1003,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(finish) error = %v", err)
	}
	if result.EventKey != metadb.EventKeyFinish || result.Status != metadb.EventStatusClosed {
		t.Fatalf("finish result = %#v, want closed finish marker", result)
	}

	states, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, "cmn-stream-finish", 10)
	if err != nil {
		t.Fatalf("ListMessageEventStates(after finish) error = %v", err)
	}
	byKey := make(map[string]metadb.MessageEventState, len(states))
	for _, state := range states {
		byKey[state.EventKey] = state
	}
	for _, want := range []struct {
		key  string
		text string
	}{
		{key: "main", text: "answer"},
		{key: "tool", text: "lookup"},
	} {
		state, ok := byKey[want.key]
		if !ok {
			t.Fatalf("durable states after finish = %#v, missing flushed %s lane", states, want.key)
		}
		if state.Status != metadb.EventStatusClosed || state.LastMsgEventSeq == 0 {
			t.Fatalf("%s lane after finish = %#v, want durable closed state", want.key, state)
		}
		if !strings.Contains(string(state.SnapshotPayload), want.text) {
			t.Fatalf("%s lane snapshot = %s, want cached text %q", want.key, state.SnapshotPayload, want.text)
		}
	}
	if _, ok := byKey[metadb.EventKeyFinish]; !ok {
		t.Fatalf("durable states after finish = %#v, missing finish marker", states)
	}
}

func TestClusterMessageEventFinishFlushUsesSingleProposal(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-finish-batch"
	waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for _, tc := range []struct {
		eventID string
		key     string
		delta   string
	}{
		{eventID: "evt-main-delta", key: "main", delta: "answer"},
		{eventID: "evt-tool-delta", key: "tool", delta: "lookup"},
	} {
		if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: "cmn-finish-batch",
			EventID:     tc.eventID,
			EventKey:    tc.key,
			EventType:   metadb.EventTypeStreamDelta,
			Visibility:  metadb.VisibilityPublic,
			OccurredAt:  1000,
			Payload:     []byte(`{"kind":"text","delta":"` + tc.delta + `"}`),
			UpdatedAt:   1001,
		}); err != nil {
			t.Fatalf("AppendMessageEvent(%s delta) error = %v", tc.key, err)
		}
	}

	proposer := wrapNodeResultProposer(t, node)
	before := proposer.resultCallCount()
	if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-finish-batch",
		EventID:     "evt-finish",
		EventType:   metadb.EventTypeStreamFinish,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1002,
		Payload:     []byte(`{"end_reason":3}`),
		UpdatedAt:   1003,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(finish) error = %v", err)
	}
	if got := proposer.resultCallCount() - before; got != 1 {
		t.Fatalf("finish result proposals = %d, want 1", got)
	}
}

func TestClusterMessageEventCoalescesConcurrentFinishesForSameChannel(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-finish-coalesce"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, clientMsgNo := range []string{"cmn-coalesce-a", "cmn-coalesce-b"} {
		if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: clientMsgNo,
			EventID:     "evt-delta-" + clientMsgNo,
			EventKey:    "main",
			EventType:   metadb.EventTypeStreamDelta,
			Visibility:  metadb.VisibilityPublic,
			OccurredAt:  1000,
			Payload:     []byte(`{"kind":"text","delta":"` + clientMsgNo + `"}`),
			UpdatedAt:   1001,
		}); err != nil {
			t.Fatalf("AppendMessageEvent(delta %s) error = %v", clientMsgNo, err)
		}
	}

	proposer := wrapNodeResultProposer(t, node)
	before := proposer.resultCallCount()
	var wg sync.WaitGroup
	results := make([]metadb.MessageEventAppendResult, 2)
	errs := make([]error, 2)
	for i, clientMsgNo := range []string{"cmn-coalesce-a", "cmn-coalesce-b"} {
		i, clientMsgNo := i, clientMsgNo
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
				ChannelID:   channelID,
				ChannelType: 2,
				ClientMsgNo: clientMsgNo,
				EventID:     "evt-finish-" + clientMsgNo,
				EventType:   metadb.EventTypeStreamFinish,
				Visibility:  metadb.VisibilityPublic,
				OccurredAt:  1002 + int64(i),
				Payload:     []byte(`{"end_reason":3}`),
				UpdatedAt:   1004 + int64(i),
			})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("AppendMessageEvent(finish %d) error = %v", i, err)
		}
	}
	if got := proposer.resultCallCount() - before; got != 1 {
		t.Fatalf("coalesced finish result proposals = %d, want 1", got)
	}
	for i, clientMsgNo := range []string{"cmn-coalesce-a", "cmn-coalesce-b"} {
		if results[i].ClientMsgNo != clientMsgNo || results[i].EventKey != metadb.EventKeyFinish || results[i].MsgEventSeq == 0 {
			t.Fatalf("finish result %d = %#v, want result for %s finish marker", i, results[i], clientMsgNo)
		}
		states, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListMessageEventStates(ctx, channelID, 2, clientMsgNo, 10)
		if err != nil {
			t.Fatalf("ListMessageEventStates(%s) error = %v", clientMsgNo, err)
		}
		if len(states) != 2 {
			t.Fatalf("states for %s = %#v, want flushed main lane and finish marker", clientMsgNo, states)
		}
	}
}

func TestClusterMessageEventFinishWithoutCachedLanesFailsClosed(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-finish-cache-miss"
	waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	proposer := wrapNodeResultProposer(t, node)
	before := proposer.resultCallCount()
	_, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-finish-cache-miss",
		EventID:     "evt-finish",
		EventType:   metadb.EventTypeStreamFinish,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1002,
		Payload:     []byte(`{"end_reason":3}`),
		UpdatedAt:   1003,
	})
	if !errors.Is(err, ErrMessageEventStreamCacheMiss) {
		t.Fatalf("AppendMessageEvent(finish without cache) error = %v, want ErrMessageEventStreamCacheMiss", err)
	}
	if got := proposer.resultCallCount() - before; got != 0 {
		t.Fatalf("finish cache miss proposals = %d, want 0", got)
	}
}

func TestClusterMessageEventCacheClearsWhenLocalSlotLeadershipIsLost(t *testing.T) {
	observer := &recordingMessageEventObserver{}
	node := &Node{
		cfg:                     Config{NodeID: 1, MessageEvent: MessageEventConfig{Observer: observer}},
		router:                  routing.NewRouter(),
		messageEventStreamCache: newMessageEventStreamCache(0),
	}
	snapshot := routeAuthoritySnapshot(1)
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}})
	node.started.Store(true)

	channelID := "message-event-clear-on-leader-loss"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-clear-on-leader-loss",
		EventID:     "evt-delta",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","delta":"stale"}`),
		UpdatedAt:   1001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(delta) error = %v", err)
	}
	if cache := observer.lastCache(); cache.Sessions != 1 || cache.OpenLanes != 1 {
		t.Fatalf("cache after delta = %#v, want one open session", cache)
	}

	before := node.router.Table()
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 10}})
	node.publishRouteAuthorityChanges(before)

	if cache := observer.lastCache(); cache.Sessions != 0 || cache.OpenLanes != 0 || cache.PayloadBytes != 0 {
		t.Fatalf("cache after local leadership loss = %#v, want empty cache", cache)
	}
}

func TestClusterMessageEventCacheClearsAcrossSerializedLocalRemoteLocalAuthorityUpdates(t *testing.T) {
	node := &Node{
		cfg:                     Config{NodeID: 1},
		router:                  routing.NewRouter(),
		messageEventStreamCache: newMessageEventStreamCache(0),
		routeAuthorityEpochs:    make(map[uint16]uint64),
	}
	if err := node.router.UpdateControlSnapshot(routeAuthoritySnapshot(1)); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}})
	node.publishRouteAuthorityChanges(nil)
	node.started.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   "message-event-serialized-leader-loss",
		ChannelType: 2,
		ClientMsgNo: "cmn-serialized-leader-loss",
		EventID:     "evt-delta",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","delta":"stale"}`),
		UpdatedAt:   1001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(delta) error = %v", err)
	}
	if got := node.messageEventStreamCache.observation(); got.Sessions != 1 {
		t.Fatalf("cache before authority changes = %#v, want one session", got)
	}

	remoteMutated := make(chan struct{})
	releaseRemote := make(chan struct{})
	remoteDone := make(chan struct{})
	go func() {
		defer close(remoteDone)
		_ = node.updateRouteAuthorityTable(func() error {
			node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 10}})
			close(remoteMutated)
			<-releaseRemote
			return nil
		})
	}()
	<-remoteMutated

	localMutated := make(chan struct{})
	localDone := make(chan struct{})
	go func() {
		defer close(localDone)
		_ = node.updateRouteAuthorityTable(func() error {
			node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 11}})
			close(localMutated)
			return nil
		})
	}()
	select {
	case <-localMutated:
		t.Fatal("local reacquire mutated Router before remote authority loss was published")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseRemote)
	<-remoteDone
	<-localDone

	if got := node.messageEventStreamCache.observation(); got.Sessions != 0 || got.OpenLanes != 0 || got.PayloadBytes != 0 {
		t.Fatalf("cache after local-remote-local authority changes = %#v, want empty cache", got)
	}
	if route := node.router.Table(); route == nil || route.SlotLeaders[1] != 1 || route.SlotLeaderTerms[1] != 11 {
		t.Fatalf("final route = %#v, want local leader term 11", route)
	}
}

func TestClusterMessageEventObserverTracksCacheAndFinishBatch(t *testing.T) {
	observer := &recordingMessageEventObserver{}
	cfg := Config{NodeID: 1, ListenAddr: freeTCPAddr(t), DataDir: t.TempDir()}
	cfg.Control.ClusterID = "cluster-message-event-observer"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1
	cfg.Channel.TickInterval = time.Millisecond
	cfg.MessageEvent.Observer = observer
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New(single node) error = %v", err)
	}
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "message-event-observer"
	waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for _, tc := range []struct {
		eventID string
		key     string
		delta   string
	}{
		{eventID: "evt-main-delta", key: "main", delta: "answer"},
		{eventID: "evt-tool-delta", key: "tool", delta: "lookup"},
	} {
		if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: "cmn-observer",
			EventID:     tc.eventID,
			EventKey:    tc.key,
			EventType:   metadb.EventTypeStreamDelta,
			Visibility:  metadb.VisibilityPublic,
			OccurredAt:  1000,
			Payload:     []byte(`{"kind":"text","delta":"` + tc.delta + `"}`),
			UpdatedAt:   1001,
		}); err != nil {
			t.Fatalf("AppendMessageEvent(%s delta) error = %v", tc.key, err)
		}
	}
	if got := observer.proposeCount(); got != 0 {
		t.Fatalf("propose observations after cache-only deltas = %d, want 0", got)
	}
	cache := observer.lastCache()
	if cache.Sessions != 1 || cache.OpenLanes != 2 || cache.PayloadBytes == 0 || cache.MaxSessions != defaultMessageEventStreamCacheMaxSessions {
		t.Fatalf("cache observation after deltas = %#v, want one session/two lanes/non-zero bytes/default max", cache)
	}

	if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-observer",
		EventID:     "evt-finish",
		EventType:   metadb.EventTypeStreamFinish,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1002,
		Payload:     []byte(`{"end_reason":3}`),
		UpdatedAt:   1003,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(finish) error = %v", err)
	}
	observer.requireAppend(t, "cache", metadb.EventTypeStreamDelta, "ok")
	observer.requireAppend(t, "finish_batch", metadb.EventTypeStreamFinish, "ok")
	observer.requirePropose(t, "finish_batch", "ok", 3)
	observer.requireAppendStage(t, "finish_batch", "ok", "finish_batch_build")
	observer.requireAppendStage(t, "finish_batch", "ok", "finish_cache_remove")
	observer.requireProposeStage(t, "finish_batch", "ok", "encode")
	observer.requireProposeStage(t, "finish_batch", "ok", "slot_propose_wait")
	observer.requireProposeStage(t, "finish_batch", "ok", "slot_propose_submit")
	observer.requireProposeStage(t, "finish_batch", "ok", "slot_future_wait")
	observer.requireProposeStage(t, "finish_batch", "ok", "slot_control_wait")
	observer.requireProposeStage(t, "finish_batch", "ok", "slot_raft_commit_wait")
	observer.requireProposeStage(t, "finish_batch", "ok", "slot_fsm_apply")
	observer.requireProposeStage(t, "finish_batch", "ok", "slot_fsm_commit")
	observer.requireProposeStage(t, "finish_batch", "ok", "slot_mark_applied")
	observer.requireProposeStage(t, "finish_batch", "ok", "decode")
	cache = observer.lastCache()
	if cache.Sessions != 0 || cache.OpenLanes != 0 || cache.PayloadBytes != 0 {
		t.Fatalf("cache observation after finish = %#v, want empty cache", cache)
	}
}

func TestClusterGetMessageEventStatesBatchRoutesEachMessage(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelA := channelLatestKeyForHashSlot(t, 1, 4)
	channelB := channelLatestKeyForHashSlot(t, 3, 4)
	waitRouteKeyLeaderReady(t, node, channelA)
	waitRouteKeyLeaderReady(t, node, channelB)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelA,
		ChannelType: 2,
		ClientMsgNo: "cmn-a",
		EventID:     "evt-a",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"snapshot":{"kind":"text","text":"a"}}`),
		UpdatedAt:   1001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(channelA) error = %v", err)
	}
	if _, err := node.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelB,
		ChannelType: 2,
		ClientMsgNo: "cmn-b",
		EventID:     "evt-b",
		EventKey:    "tool",
		EventType:   metadb.EventTypeStreamClose,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  2000,
		Payload:     []byte(`{"snapshot":{"kind":"text","text":"b"}}`),
		UpdatedAt:   2001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(channelB) error = %v", err)
	}

	keyA := metadb.MessageEventMessageKey{ChannelID: channelA, ChannelType: 2, ClientMsgNo: "cmn-a"}
	keyB := metadb.MessageEventMessageKey{ChannelID: channelB, ChannelType: 2, ClientMsgNo: "cmn-b"}
	missing := metadb.MessageEventMessageKey{ChannelID: channelA, ChannelType: 2, ClientMsgNo: "missing"}
	got, err := node.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{keyA, missing, keyB, keyA}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetMessageEventStatesBatch() returned %d keys, want 2: %#v", len(got), got)
	}
	if len(got[keyA]) != 1 || got[keyA][0].EventKey != "main" {
		t.Fatalf("states for keyA = %#v, want main lane", got[keyA])
	}
	if len(got[keyB]) != 1 || got[keyB][0].EventKey != "tool" {
		t.Fatalf("states for keyB = %#v, want tool lane", got[keyB])
	}
	if _, ok := got[missing]; ok {
		t.Fatalf("missing key returned states: %#v", got[missing])
	}
}

func TestClusterGetMessageEventStatesBatchReadsLeaderCacheFromFollower(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)

	channelID := "message-event-remote-cache"
	route := waitRouteKeyLeaderConverged(t, nodes, channelID)
	follower := firstNonLeaderNode(t, nodes, route.Leader)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := follower.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-remote-cache",
		EventID:     "evt-remote-delta",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","delta":"remote"}`),
		UpdatedAt:   1001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(follower delta) error = %v", err)
	}

	key := metadb.MessageEventMessageKey{ChannelID: channelID, ChannelType: 2, ClientMsgNo: "cmn-remote-cache"}
	got, err := follower.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{key}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch(follower) error = %v", err)
	}
	if len(got[key]) != 1 || got[key][0].Status != metadb.EventStatusOpen || got[key][0].LastMsgEventSeq != 0 {
		t.Fatalf("follower state batch = %#v, want leader cached open state", got[key])
	}
	if !strings.Contains(string(got[key][0].SnapshotPayload), "remote") {
		t.Fatalf("follower snapshot = %s, want leader cached text", got[key][0].SnapshotPayload)
	}
}

func TestMergeMessageEventTerminalPayloadKeepsCachedSnapshot(t *testing.T) {
	snapshot := []byte(`{"kind":"text","text":"cached"}`)

	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "missing snapshot", payload: []byte(`{"end_reason":2}`)},
		{name: "null snapshot", payload: []byte(`{"snapshot":null,"end_reason":2}`)},
		{name: "invalid json", payload: []byte(`not-json`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeMessageEventTerminalPayload(tc.payload, snapshot)
			var body map[string]json.RawMessage
			if err := json.Unmarshal(got, &body); err != nil {
				t.Fatalf("merged payload is invalid JSON: %s: %v", got, err)
			}
			if !strings.Contains(string(body["snapshot"]), "cached") {
				t.Fatalf("snapshot = %s, want cached snapshot in payload %s", body["snapshot"], got)
			}
			if tc.name == "invalid json" && !strings.Contains(string(body["raw_payload"]), "not-json") {
				t.Fatalf("raw_payload = %s, want original invalid payload preserved", body["raw_payload"])
			}
		})
	}
}

func TestMessageEventStreamCacheRejectsNewActiveSessionWhenFull(t *testing.T) {
	cache := newMessageEventStreamCache(1)
	first := metadb.MessageEventAppend{
		ChannelID:   "g1",
		ChannelType: 2,
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		Payload:     []byte(`{"kind":"text","delta":"a"}`),
	}
	if _, err := cache.appendCached(first); err != nil {
		t.Fatalf("appendCached(first) error = %v", err)
	}
	second := first
	second.ClientMsgNo = "cmn-2"
	second.EventID = "evt-2"
	if _, err := cache.appendCached(second); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("appendCached(second) error = %v, want ErrBackpressured", err)
	}

	closeResult := metadb.MessageEventAppendResult{
		ChannelID:   first.ChannelID,
		ChannelType: first.ChannelType,
		ClientMsgNo: first.ClientMsgNo,
		EventID:     "evt-close",
		EventKey:    "main",
		MsgEventSeq: 1,
		Status:      metadb.EventStatusClosed,
		State: metadb.MessageEventState{
			ChannelID:   first.ChannelID,
			ChannelType: first.ChannelType,
			ClientMsgNo: first.ClientMsgNo,
			EventKey:    "main",
			Status:      metadb.EventStatusClosed,
		},
	}
	cache.markTerminalPersisted(metadb.MessageEventAppend{
		ChannelID:   first.ChannelID,
		ChannelType: first.ChannelType,
		ClientMsgNo: first.ClientMsgNo,
		EventID:     "evt-close",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
	}, closeResult)
	if _, err := cache.appendCached(second); err != nil {
		t.Fatalf("appendCached(second after terminal eviction) error = %v", err)
	}
}

type countingResultProposer struct {
	delegate interface {
		Propose(context.Context, propose.Request) error
	}
	resultDelegate resultProposer
	mu             sync.Mutex
	resultCalls    int
}

func wrapNodeResultProposer(t *testing.T, node *Node) *countingResultProposer {
	t.Helper()
	node.mu.Lock()
	defer node.mu.Unlock()
	delegate := node.proposer
	resultDelegate, ok := delegate.(resultProposer)
	if !ok {
		t.Fatalf("node proposer %T does not support ProposeResult", delegate)
	}
	proposer := &countingResultProposer{delegate: delegate, resultDelegate: resultDelegate}
	node.proposer = proposer
	return proposer
}

func (p *countingResultProposer) Propose(ctx context.Context, req propose.Request) error {
	return p.delegate.Propose(ctx, req)
}

func (p *countingResultProposer) ProposeResult(ctx context.Context, req propose.Request) ([]byte, error) {
	p.mu.Lock()
	p.resultCalls++
	p.mu.Unlock()
	return p.resultDelegate.ProposeResult(ctx, req)
}

func (p *countingResultProposer) resultCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resultCalls
}

type recordingMessageEventObserver struct {
	mu            sync.Mutex
	appends       []MessageEventAppendObservation
	appendStages  []MessageEventAppendStageObservation
	proposes      []MessageEventProposeObservation
	proposeStages []MessageEventProposeStageObservation
	caches        []MessageEventStreamCacheObservation
}

func (o *recordingMessageEventObserver) ObserveMessageEventAppend(event MessageEventAppendObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.appends = append(o.appends, event)
}

func (o *recordingMessageEventObserver) ObserveMessageEventAppendStage(event MessageEventAppendStageObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.appendStages = append(o.appendStages, event)
}

func (o *recordingMessageEventObserver) ObserveMessageEventPropose(event MessageEventProposeObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.proposes = append(o.proposes, event)
}

func (o *recordingMessageEventObserver) ObserveMessageEventProposeStage(event MessageEventProposeStageObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.proposeStages = append(o.proposeStages, event)
}

func (o *recordingMessageEventObserver) SetMessageEventStreamCache(event MessageEventStreamCacheObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.caches = append(o.caches, event)
}

func (o *recordingMessageEventObserver) proposeCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.proposes)
}

func (o *recordingMessageEventObserver) lastCache() MessageEventStreamCacheObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.caches) == 0 {
		return MessageEventStreamCacheObservation{}
	}
	return o.caches[len(o.caches)-1]
}

func (o *recordingMessageEventObserver) requireAppend(t *testing.T, path, eventType, result string) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, event := range o.appends {
		if event.Path == path && event.EventType == eventType && event.Result == result {
			return
		}
	}
	t.Fatalf("append observations = %#v, missing path=%s event_type=%s result=%s", o.appends, path, eventType, result)
}

func (o *recordingMessageEventObserver) requirePropose(t *testing.T, path, result string, batchSize int) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, event := range o.proposes {
		if event.Path == path && event.Result == result && event.BatchSize == batchSize {
			return
		}
	}
	t.Fatalf("propose observations = %#v, missing path=%s result=%s batch_size=%d", o.proposes, path, result, batchSize)
}

func (o *recordingMessageEventObserver) requireAppendStage(t *testing.T, path, result, stage string) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, event := range o.appendStages {
		if event.Path == path && event.Result == result && event.Stage == stage {
			return
		}
	}
	t.Fatalf("append stage observations = %#v, missing path=%s result=%s stage=%s", o.appendStages, path, result, stage)
}

func (o *recordingMessageEventObserver) requireProposeStage(t *testing.T, path, result, stage string) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, event := range o.proposeStages {
		if event.Path == path && event.Result == result && event.Stage == stage {
			return
		}
	}
	t.Fatalf("propose stage observations = %#v, missing path=%s result=%s stage=%s", o.proposeStages, path, result, stage)
}
