package message

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestCommitCoordinatorConfigDoesNotExposeNoSync(t *testing.T) {
	if _, ok := reflect.TypeOf(CommitCoordinatorConfig{}).FieldByName("NoSync"); ok {
		t.Fatal("CommitCoordinatorConfig exposes NoSync, want durable sync fixed on")
	}
}

func TestCommitCoordinatorConfigKeepsShardCount(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{Shards: 4})

	if got := engine.CommitCoordinatorConfig().Shards; got != 4 {
		t.Fatalf("CommitCoordinatorConfig().Shards = %d, want 4", got)
	}
}

func TestEngineMetricsSnapshotReportsPhysicalStore(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	snapshot := engine.MetricsSnapshot()
	if snapshot.DiskSpaceUsageBytes == 0 {
		t.Fatalf("DiskSpaceUsageBytes = 0, want physical usage")
	}
	if snapshot.ReadAmplification < 0 {
		t.Fatalf("ReadAmplification = %d, want non-negative value", snapshot.ReadAmplification)
	}
}

func TestCompatListMessagesBySeqPreservesCanceledContext(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	store := mustForChannel(t, engine, channel.ChannelKey("compat-canceled-read:1"), channel.ChannelID{ID: "compat-canceled-read", Type: 1})
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.ListMessagesBySeq(ctx, 1, 10, 1024, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ListMessagesBySeq() error = %v, want context canceled", err)
	}
}

func TestCompatEngineAppendReadAndIdempotency(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	id := channel.ChannelID{ID: "compat", Type: 1}
	store := mustForChannel(t, engine, channel.ChannelKey("compat:1"), id)
	msg := channel.Message{
		MessageID:   42,
		Framer:      frame.Framer{RedDot: true},
		Setting:     frame.Setting(3),
		StreamFlag:  frame.StreamFlag(2),
		MsgKey:      "msg-key",
		Expire:      60,
		ClientSeq:   7,
		ClientMsgNo: "client-1",
		StreamNo:    "stream-1",
		StreamID:    9,
		Timestamp:   100,
		ChannelID:   id.ID,
		ChannelType: id.Type,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     []byte("payload"),
	}
	payload := encodeCompatTestMessage(t, msg)

	base, err := store.Append([]channel.Record{{ID: msg.MessageID, Payload: payload, SizeBytes: len(payload)}})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if base != 0 {
		t.Fatalf("Append() base = %d, want 0", base)
	}

	got, ok, err := store.GetMessageBySeq(1)
	if err != nil || !ok {
		t.Fatalf("GetMessageBySeq() = ok %v err %v", ok, err)
	}
	if got.MessageID != msg.MessageID || got.ClientMsgNo != msg.ClientMsgNo || got.FromUID != msg.FromUID || string(got.Payload) != string(msg.Payload) {
		t.Fatalf("GetMessageBySeq() = %+v, want message fields from compat payload", got)
	}

	entry, payloadHash, ok, err := store.LookupIdempotency(channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     msg.FromUID,
		ClientMsgNo: msg.ClientMsgNo,
	})
	if err != nil || !ok {
		t.Fatalf("LookupIdempotency() = ok %v err %v", ok, err)
	}
	if entry.MessageID != msg.MessageID || entry.MessageSeq != 1 || entry.Offset != 0 {
		t.Fatalf("LookupIdempotency() entry = %+v", entry)
	}
	if payloadHash != compatTestFNV64a(msg.Payload) {
		t.Fatalf("LookupIdempotency() payloadHash = %d, want FNV %d", payloadHash, compatTestFNV64a(msg.Payload))
	}

	records, err := store.Read(0, 1<<20)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(records) != 1 || records[0].Index != 1 || records[0].ID != msg.MessageID {
		t.Fatalf("Read() records = %+v", records)
	}
}

func TestCompatCommittedCursorAndRetentionState(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	store := mustForChannel(t, engine, channel.ChannelKey("retention:1"), channel.ChannelID{ID: "retention", Type: 1})
	if err := store.StoreCommittedDispatchCursor("committed", 3); err != nil {
		t.Fatalf("StoreCommittedDispatchCursor() error = %v", err)
	}
	if err := store.AdoptRetentionBoundary(context.Background(), 5, "committed"); err != nil {
		t.Fatalf("AdoptRetentionBoundary() error = %v", err)
	}
	seq, ok, err := store.LoadCommittedDispatchCursor("committed")
	if err != nil || !ok || seq != 5 {
		t.Fatalf("LoadCommittedDispatchCursor() = seq %d ok %v err %v, want 5 true nil", seq, ok, err)
	}
	state, err := store.LoadRetentionState()
	if err != nil {
		t.Fatalf("LoadRetentionState() error = %v", err)
	}
	if state.LocalRetentionThroughSeq != 5 || state.RetainedMaxSeq != 5 {
		t.Fatalf("LoadRetentionState() = %+v, want adopted boundary", state)
	}
	keys, err := engine.ListChannelKeys()
	if err != nil {
		t.Fatalf("ListChannelKeys() error = %v", err)
	}
	if len(keys) != 1 || keys[0] != channel.ChannelKey("retention:1") {
		t.Fatalf("ListChannelKeys() = %v, want retention channel", keys)
	}
}

func TestCompatChannelStoreAppendUsesCommitCoordinatorAcrossChannels(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{FlushWindow: 2 * time.Second, MaxRequests: 2})

	storeA := mustForChannel(t, engine, channel.ChannelKey("coordinator-a:1"), channel.ChannelID{ID: "coordinator-a", Type: 1})
	storeB := mustForChannel(t, engine, channel.ChannelKey("coordinator-b:1"), channel.ChannelID{ID: "coordinator-b", Type: 1})

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	recordA := compatTestRecord(t, 1001, "coordinator-a", "client-a")
	recordB := compatTestRecord(t, 1002, "coordinator-b", "client-b")
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := storeA.Append([]channel.Record{recordA})
		errs <- err
	}()

	select {
	case err := <-errs:
		t.Fatalf("first append completed before a second channel could join the commit batch: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := storeB.Append([]channel.Record{recordB})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
}

func TestCommitCoordinatorRequestObserverSplitsAppendAndApplyLanes(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	observer := &commitRequestCapture{}
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{Observer: observer})

	store := mustForChannel(t, engine, channel.ChannelKey("lane:1"), channel.ChannelID{ID: "lane", Type: 1})
	if _, err := store.Append([]channel.Record{compatTestRecord(t, 2001, "lane", "client-append")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if _, err := store.StoreApplyFetchTrusted(channel.ApplyFetchStoreRequest{
		Records: []channel.Record{compatTestRecord(t, 2002, "lane", "client-apply")},
	}); err != nil {
		t.Fatalf("StoreApplyFetchTrusted() error = %v", err)
	}

	lanes := observer.Lanes()
	if !containsString(lanes, "leader_append") || !containsString(lanes, "follower_apply") {
		t.Fatalf("request lanes = %v, want leader_append and follower_apply", lanes)
	}
}

func TestCommitCoordinatorQueueObserverReceivesEffectiveCapacity(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	observer := &commitQueueCapture{}
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{QueueSize: 3, Observer: observer})

	store := mustForChannel(t, engine, channel.ChannelKey("queue-capacity:1"), channel.ChannelID{ID: "queue-capacity", Type: 1})
	if _, err := store.Append([]channel.Record{compatTestRecord(t, 2501, "queue-capacity", "client-append")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	if !observer.SawCapacity(3) {
		t.Fatalf("queue capacities = %v, want 3", observer.Capacities())
	}
}

func TestCommitCoordinatorQueueObserverReceivesShardedCapacity(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	observer := &commitQueueCapture{}
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{QueueSize: 3, Shards: 4, Observer: observer})

	store := mustForChannel(t, engine, channel.ChannelKey("queue-sharded-capacity:1"), channel.ChannelID{ID: "queue-sharded-capacity", Type: 1})
	if _, err := store.Append([]channel.Record{compatTestRecord(t, 2601, "queue-sharded-capacity", "client-append")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	if !observer.SawCapacity(12) {
		t.Fatalf("queue capacities = %v, want 12", observer.Capacities())
	}
}

func TestPreparedRowsPartitionUsesFirstChannelKey(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	storeA := mustForChannel(t, engine, channel.ChannelKey("partition-a:1"), channel.ChannelID{ID: "partition-a", Type: 1})
	storeB := mustForChannel(t, engine, channel.ChannelKey("partition-b:1"), channel.ChannelID{ID: "partition-b", Type: 1})
	prepared := []preparedCommitRows{{store: storeA}, {store: storeB}}

	if got := preparedRowsPartition(prepared, commitLaneLeaderAppend); got != "partition-a:1" {
		t.Fatalf("preparedRowsPartition() = %q, want first channel key", got)
	}
}

func TestStoreApplyFetchTrustedBatchUsesSingleFollowerApplyRequest(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	observer := &commitRequestCapture{}
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{Observer: observer})

	storeA := mustForChannel(t, engine, channel.ChannelKey("batch-apply-a:1"), channel.ChannelID{ID: "batch-apply-a", Type: 1})
	storeB := mustForChannel(t, engine, channel.ChannelKey("batch-apply-b:1"), channel.ChannelID{ID: "batch-apply-b", Type: 1})
	results := StoreApplyFetchTrustedBatch(context.Background(), []ApplyFetchBatchItem{
		{Store: storeA, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 3001, "batch-apply-a", "client-a")}}},
		{Store: storeB, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 3002, "batch-apply-b", "client-b")}}},
	})
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for i, result := range results {
		if result.Err != nil || result.LEO != 1 {
			t.Fatalf("result[%d] = %+v, want LEO 1 nil error", i, result)
		}
	}
	if got := countString(observer.Lanes(), "follower_apply"); got != 1 {
		t.Fatalf("follower_apply request count = %d, want 1", got)
	}
}

func TestStoreAppendBatchUsesSingleLeaderAppendRequest(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	observer := &commitRequestCapture{}
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{Observer: observer})

	storeA := mustForChannel(t, engine, channel.ChannelKey("batch-append-a:1"), channel.ChannelID{ID: "batch-append-a", Type: 1})
	storeB := mustForChannel(t, engine, channel.ChannelKey("batch-append-b:1"), channel.ChannelID{ID: "batch-append-b", Type: 1})
	results := StoreAppendBatch(context.Background(), []AppendBatchItem{
		{Store: storeA, Records: []channel.Record{compatTestRecord(t, 4001, "batch-append-a", "client-a")}},
		{Store: storeB, Records: []channel.Record{compatTestRecord(t, 4002, "batch-append-b", "client-b")}},
	})
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for i, result := range results {
		if result.Err != nil || result.BaseOffset != 0 || result.LastOffset != 1 {
			t.Fatalf("result[%d] = %+v, want base 0 last 1 nil error", i, result)
		}
	}
	if got := countString(observer.Lanes(), "leader_append"); got != 1 {
		t.Fatalf("leader_append request count = %d, want 1", got)
	}
}

func encodeCompatTestMessage(t *testing.T, msg channel.Message) []byte {
	t.Helper()
	payload := make([]byte, 0, channel.DurableMessageHeaderSize+64)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, msg.MessageID)
	payload = append(payload, 0, byte(msg.Setting), byte(msg.StreamFlag), msg.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, msg.Expire)
	payload = binary.BigEndian.AppendUint64(payload, msg.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, msg.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(msg.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, compatTestFNV64a(msg.Payload))
	payload = appendCompatTestString(payload, msg.MsgKey)
	payload = appendCompatTestString(payload, msg.ClientMsgNo)
	payload = appendCompatTestString(payload, msg.StreamNo)
	payload = appendCompatTestString(payload, msg.ChannelID)
	payload = appendCompatTestString(payload, msg.Topic)
	payload = appendCompatTestString(payload, msg.FromUID)
	payload = appendCompatTestBytes(payload, msg.Payload)
	return payload
}

type commitRequestCapture struct {
	mu     sync.Mutex
	events []CommitCoordinatorRequestEvent
}

func (c *commitRequestCapture) SetCommitCoordinatorQueueDepth(int) {}

func (c *commitRequestCapture) ObserveCommitCoordinatorBatch(CommitCoordinatorBatchEvent) {}

func (c *commitRequestCapture) ObserveCommitCoordinatorRequest(event CommitCoordinatorRequestEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *commitRequestCapture) Lanes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lanes := make([]string, 0, len(c.events))
	for _, event := range c.events {
		lanes = append(lanes, event.Lane)
	}
	return lanes
}

type commitQueueCapture struct {
	mu         sync.Mutex
	capacities []int
}

func (c *commitQueueCapture) SetCommitCoordinatorQueueDepth(int) {}

func (c *commitQueueCapture) SetCommitCoordinatorQueue(_ int, capacity int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.capacities = append(c.capacities, capacity)
}

func (c *commitQueueCapture) ObserveCommitCoordinatorBatch(CommitCoordinatorBatchEvent) {}

func (c *commitQueueCapture) SawCapacity(want int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, capacity := range c.capacities {
		if capacity == want {
			return true
		}
	}
	return false
}

func (c *commitQueueCapture) Capacities() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.capacities...)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func countString(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func compatTestRecord(t *testing.T, messageID uint64, channelID string, clientMsgNo string) channel.Record {
	t.Helper()
	msg := channel.Message{
		MessageID:   messageID,
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelID,
		ChannelType: 1,
		FromUID:     "u1",
		Payload:     []byte("payload"),
	}
	payload := encodeCompatTestMessage(t, msg)
	return channel.Record{ID: messageID, Payload: payload, SizeBytes: len(payload)}
}

func appendCompatTestString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendCompatTestBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func compatTestFNV64a(payload []byte) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	hash := uint64(offset)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= prime
	}
	return hash
}
