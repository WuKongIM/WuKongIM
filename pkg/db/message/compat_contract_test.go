package message

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

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

func TestDiscardForRestoreRemovesCompleteChannelState(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	key := channel.ChannelKey("restore-discard:1")
	id := channel.ChannelID{ID: "restore-discard", Type: 1}
	store := mustForChannel(t, engine, key, id)
	if _, err := store.Append([]channel.Record{
		compatTestRecord(t, 701, id.ID, "discard-1"),
		compatTestRecord(t, 702, id.ID, "discard-2"),
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.StoreCheckpoint(channel.Checkpoint{
		Epoch: 1, HW: 2,
	}); err != nil {
		t.Fatalf("StoreCheckpoint() error = %v", err)
	}
	if err := store.DiscardForRestore(context.Background()); err != nil {
		t.Fatalf("DiscardForRestore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	entries, _, _, err := engine.ListChannelsPage(
		context.Background(), "", 10,
	)
	if err != nil {
		t.Fatalf("ListChannelsPage() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("catalog entries = %v, want empty", entries)
	}
	reopened := mustForChannel(t, engine, key, id)
	defer reopened.Close()
	if leo, err := reopened.LEOWithError(); err != nil || leo != 0 {
		t.Fatalf("LEOWithError() = (%d, %v), want (0, nil)", leo, err)
	}
	if _, err := reopened.LoadCheckpoint(); err == nil {
		t.Fatal("LoadCheckpoint() error = nil, want empty state")
	}
}

func TestDiscardForRestoreDeletesMultipleBoundedPages(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	key := channel.ChannelKey("restore-discard-pages:1")
	id := channel.ChannelID{ID: "restore-discard-pages", Type: 1}
	store := mustForChannel(t, engine, key, id)
	records := make([]channel.Record, 1025)
	for index := range records {
		records[index] = compatTestRecord(
			t,
			uint64(10_000+index),
			id.ID,
			fmt.Sprintf("discard-page-%04d", index),
		)
	}
	if _, err := store.Append(records); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.StoreCheckpoint(channel.Checkpoint{
		Epoch: 1, HW: uint64(len(records)),
	}); err != nil {
		t.Fatalf("StoreCheckpoint() error = %v", err)
	}
	if err := store.DiscardForRestore(context.Background()); err != nil {
		t.Fatalf("DiscardForRestore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	entries, _, _, err := engine.ListChannelsPage(
		context.Background(), "", 10,
	)
	if err != nil {
		t.Fatalf("ListChannelsPage() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("catalog entries = %v, want empty", entries)
	}
	reopened := mustForChannel(t, engine, key, id)
	defer reopened.Close()
	messages, err := reopened.Read(1, len(records)+1)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("remaining messages = %d, want 0", len(messages))
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

func TestCompatListMessagesBySeqReverseLimitDoesNotReadOlderHistory(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	key := channel.ChannelKey("compat-reverse-limit:1")
	id := channel.ChannelID{ID: "compat-reverse-limit", Type: 1}
	store := mustForChannel(t, engine, key, id)
	defer store.Close()
	if _, err := store.Append([]channel.Record{
		compatTestRecord(t, 801, id.ID, "old"),
		compatTestRecord(t, 802, id.ID, "middle"),
		compatTestRecord(t, 803, id.ID, "latest"),
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	batch := engine.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeMessageRowKey(ChannelKey(key), 1, messageHeaderFamilyID), []byte{0x01}); err != nil {
		t.Fatalf("Set(corrupt old header): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(corrupt old header): %v", err)
	}

	messages, err := store.ListMessagesBySeq(context.Background(), 3, 1, 1<<20, true)
	if err != nil {
		t.Fatalf("ListMessagesBySeq(latest only): %v", err)
	}
	if len(messages) != 1 || messages[0].MessageSeq != 3 || messages[0].ClientMsgNo != "latest" {
		t.Fatalf("messages = %#v, want only latest sequence 3", messages)
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

func TestStoreAppendBatchExactProposalReplaysAfterRetentionAndReopen(t *testing.T) {
	path := t.TempDir()
	engine, err := Open(path)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	store, err := engine.ForChannel(channel.ChannelKey("exact-replay"), channel.ChannelID{ID: "exact-replay", Type: 2})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	record := compatExactTestRecord(t, 5, 7101, "exact-replay", "client-1")
	manifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version:      DurableProposalManifestVersion,
		ChannelEpoch: 5,
		LeaderTerm:   7,
		FenceVersion: 9,
		CommandID:    [32]byte{1, 2, 3},
		BaseOffset:   0,
		LastOffset:   1,
	}, []channel.Record{record})

	first := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store:              store,
		Records:            []channel.Record{record},
		ExactBaseOffset:    true,
		ExpectedBaseOffset: 0,
		Proposal:           manifest,
	}})
	if len(first) != 1 || first[0].Err != nil || first[0].Outcome != quorumlog.AppendOutcomeDurable {
		t.Fatalf("first StoreAppendBatch() = %+v, want new exact durable append", first)
	}
	entryValue, ok, err := engine.engine.Get(encodeEntryIdentityKey(ChannelKey("exact-replay"), 1))
	if err != nil || !ok {
		t.Fatalf("entry identity read = ok %v err %v", ok, err)
	}
	entryIdentity, err := decodeDurableEntryIdentity(entryValue)
	if err != nil || entryIdentity.Index != 1 || entryIdentity.CommandID != manifest.CommandID || entryIdentity.Digest != manifest.Digest {
		t.Fatalf("entry identity = %+v err %v, want persisted manifest tail", entryIdentity, err)
	}
	if err := store.AdoptRetentionBoundary(context.Background(), 1, "committed"); err != nil {
		t.Fatalf("AdoptRetentionBoundary(): %v", err)
	}
	if err := store.TrimMessagesThrough(context.Background(), 1); err != nil {
		t.Fatalf("TrimMessagesThrough(): %v", err)
	}
	if _, ok, err := store.GetMessageBySeq(1); err != nil || ok {
		t.Fatalf("GetMessageBySeq(1) after retention = ok %v err %v, want missing", ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close(): %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	engine, err = Open(path)
	if err != nil {
		t.Fatalf("Open() after retention: %v", err)
	}
	defer engine.Close()
	store, err = engine.ForChannel(channel.ChannelKey("exact-replay"), channel.ChannelID{ID: "exact-replay", Type: 2})
	if err != nil {
		t.Fatalf("ForChannel() after retention: %v", err)
	}
	defer store.Close()

	replay := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store:              store,
		Records:            []channel.Record{record},
		ExactBaseOffset:    true,
		ExpectedBaseOffset: 0,
		Proposal:           manifest,
	}})
	if len(replay) != 1 || replay[0].Err != nil || replay[0].Outcome != quorumlog.AppendOutcomeAlreadyDurable {
		t.Fatalf("replay StoreAppendBatch() = %+v, want already durable", replay)
	}
	if got := store.LEO(); got != 1 {
		t.Fatalf("LEO after exact replay = %d, want 1", got)
	}

	conflict := record
	conflict.ID = 7102
	conflict.Payload = append([]byte(nil), record.Payload...)
	conflict.Payload[len(conflict.Payload)-1] ^= 0xff
	conflicted := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store:              store,
		Records:            []channel.Record{conflict},
		ExactBaseOffset:    true,
		ExpectedBaseOffset: 0,
		Proposal:           manifest,
	}})
	if len(conflicted) != 1 || !errors.Is(conflicted[0].Err, channel.ErrCorruptState) || conflicted[0].Outcome != quorumlog.AppendOutcomeConflict {
		t.Fatalf("conflicting replay StoreAppendBatch() = %+v, want corrupt state", conflicted)
	}

	gappedRecord := compatExactTestRecord(t, 5, 7103, "exact-replay", "client-3")
	gappedManifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{7, 8, 9}, BaseOffset: 2, LastOffset: 3,
		PreviousTerm: manifest.LeaderTerm, PreviousIndex: 2, PreviousDigest: manifest.Digest,
	}, []channel.Record{gappedRecord})
	gapped := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store:              store,
		Records:            []channel.Record{gappedRecord},
		ExactBaseOffset:    true,
		ExpectedBaseOffset: 2,
		Proposal:           gappedManifest,
	}})
	if len(gapped) != 1 || !errors.Is(gapped[0].Err, channel.ErrCorruptState) || gapped[0].Outcome != quorumlog.AppendOutcomeConflict {
		t.Fatalf("gapped StoreAppendBatch() = %+v, want corrupt state", gapped)
	}
}

func TestTruncateLogAndHistoryRemovesExactProposalIdentityForReplacementSuffix(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer engine.Close()
	store, err := engine.ForChannel(channel.ChannelKey("exact-truncate"), channel.ChannelID{ID: "exact-truncate", Type: 2})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	defer store.Close()

	firstRecord := compatExactTestRecord(t, 5, 7201, "exact-truncate", "client-1")
	firstManifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 1,
	}, []channel.Record{firstRecord})
	secondRecord := compatExactTestRecord(t, 5, 7202, "exact-truncate", "client-2")
	secondManifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{3}, BaseOffset: 1, LastOffset: 2,
		PreviousTerm: 7, PreviousIndex: 1, PreviousDigest: firstManifest.Digest,
	}, []channel.Record{secondRecord})
	for index, item := range []AppendBatchItem{
		{Store: store, Records: []channel.Record{firstRecord}, ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: firstManifest},
		{Store: store, Records: []channel.Record{secondRecord}, ExactBaseOffset: true, ExpectedBaseOffset: 1, Proposal: secondManifest},
	} {
		result := StoreAppendBatch(context.Background(), []AppendBatchItem{item})
		if len(result) != 1 || result[0].Err != nil {
			t.Fatalf("StoreAppendBatch()[%d] = %+v, want success", index, result)
		}
	}

	if err := store.TruncateLogAndHistory(context.Background(), 1); err != nil {
		t.Fatalf("TruncateLogAndHistory(1): %v", err)
	}
	replacementRecord := compatExactTestRecord(t, 6, 7203, "exact-truncate", "client-3")
	replacement := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 6, LeaderTerm: 8, FenceVersion: 10,
		CommandID: [32]byte{5}, BaseOffset: 1, LastOffset: 2,
		PreviousTerm: 7, PreviousIndex: 1, PreviousDigest: firstManifest.Digest,
	}, []channel.Record{replacementRecord})
	result := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{replacementRecord},
		ExactBaseOffset: true, ExpectedBaseOffset: 1, Proposal: replacement,
	}})
	if len(result) != 1 || result[0].Err != nil || result[0].Outcome != quorumlog.AppendOutcomeDurable {
		t.Fatalf("replacement StoreAppendBatch() = %+v, want new durable suffix", result)
	}
	entryValue, ok, err := engine.engine.Get(encodeEntryIdentityKey(ChannelKey("exact-truncate"), 2))
	if err != nil || !ok {
		t.Fatalf("replacement entry identity read = ok %v err %v", ok, err)
	}
	entryIdentity, err := decodeDurableEntryIdentity(entryValue)
	if err != nil || entryIdentity.CommandID != replacement.CommandID || entryIdentity.Digest != replacement.Digest {
		t.Fatalf("replacement entry identity = %+v err %v", entryIdentity, err)
	}
}

func TestTruncateLogAndHistoryRejectsSplittingExactProposal(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer engine.Close()
	store, err := engine.ForChannel(channel.ChannelKey("exact-truncate-split"), channel.ChannelID{ID: "exact-truncate-split", Type: 2})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	defer store.Close()
	records := []channel.Record{
		compatExactTestRecord(t, 5, 7301, "exact-truncate-split", "client-1"),
		compatExactTestRecord(t, 5, 7302, "exact-truncate-split", "client-2"),
	}
	manifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 2,
	}, records)
	result := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store:           store,
		Records:         records,
		ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: manifest,
	}})
	if len(result) != 1 || result[0].Err != nil {
		t.Fatalf("StoreAppendBatch() = %+v, want success", result)
	}
	if err := store.TruncateLogAndHistory(context.Background(), 1); !errors.Is(err, channel.ErrCorruptState) {
		t.Fatalf("TruncateLogAndHistory(1) error = %v, want corrupt state", err)
	}
	if got := store.LEO(); got != 2 {
		t.Fatalf("LEO after rejected split = %d, want 2", got)
	}
}

func TestStoreAppendBatchRejectsBrokenProposalHashChain(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer engine.Close()
	store, err := engine.ForChannel(channel.ChannelKey("exact-chain"), channel.ChannelID{ID: "exact-chain", Type: 1})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	defer store.Close()
	firstRecord := compatExactTestRecord(t, 5, 7401, "exact-chain", "client-1")
	firstManifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 1,
	}, []channel.Record{firstRecord})
	first := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{firstRecord},
		ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: firstManifest,
	}})
	if len(first) != 1 || first[0].Err != nil {
		t.Fatalf("first StoreAppendBatch() = %+v, want success", first)
	}
	secondRecord := compatExactTestRecord(t, 5, 7402, "exact-chain", "client-2")
	broken := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{3}, BaseOffset: 1, LastOffset: 2,
		PreviousTerm: 7, PreviousIndex: 1, PreviousDigest: [32]byte{99},
	}, []channel.Record{secondRecord})
	second := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{secondRecord},
		ExactBaseOffset: true, ExpectedBaseOffset: 1, Proposal: broken,
	}})
	if len(second) != 1 || !errors.Is(second[0].Err, channel.ErrCorruptState) {
		t.Fatalf("broken-chain StoreAppendBatch() = %+v, want corrupt state", second)
	}
	if got := store.LEO(); got != 1 {
		t.Fatalf("LEO after rejected chain = %d, want 1", got)
	}
}

func TestExactProposalPathsRejectMissingPairedCommandIndex(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*testing.T, *Engine, *ChannelStore, DurableProposalManifest)
	}{
		{name: "predecessor", run: func(t *testing.T, engine *Engine, store *ChannelStore, first DurableProposalManifest) {
			record := compatExactTestRecord(t, 5, 7502, "exact-pair-predecessor", "client-2")
			manifest := sealCompatProposalManifest(t, DurableProposalManifest{
				Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
				CommandID: [32]byte{2}, BaseOffset: 1, LastOffset: 2,
				PreviousTerm: 7, PreviousIndex: 1, PreviousDigest: first.Digest,
			}, []channel.Record{record})
			result := StoreAppendBatch(context.Background(), []AppendBatchItem{{
				Store: store, Records: []channel.Record{record}, ExactBaseOffset: true, ExpectedBaseOffset: 1, Proposal: manifest,
			}})
			if len(result) != 1 || !errors.Is(result[0].Err, channel.ErrCorruptState) {
				t.Fatalf("predecessor append = %+v, want corrupt state", result)
			}
		}},
		{name: "truncate", run: func(t *testing.T, _ *Engine, store *ChannelStore, _ DurableProposalManifest) {
			if err := store.TruncateLogAndHistory(context.Background(), 0); !errors.Is(err, channel.ErrCorruptState) {
				t.Fatalf("TruncateLogAndHistory() error = %v, want corrupt state", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open(): %v", err)
			}
			defer engine.Close()
			key := channel.ChannelKey("exact-pair-" + test.name)
			store, err := engine.ForChannel(key, channel.ChannelID{ID: string(key), Type: 1})
			if err != nil {
				t.Fatalf("ForChannel(): %v", err)
			}
			defer store.Close()
			record := compatExactTestRecord(t, 5, 7501, string(key), "client-1")
			manifest := sealCompatProposalManifest(t, DurableProposalManifest{
				Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
				CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 1,
			}, []channel.Record{record})
			result := StoreAppendBatch(context.Background(), []AppendBatchItem{{
				Store: store, Records: []channel.Record{record}, ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: manifest,
			}})
			if len(result) != 1 || result[0].Err != nil {
				t.Fatalf("first append = %+v, want success", result)
			}
			deletePhysicalTestKey(t, engine, encodeProposalByCommandKey(ChannelKey(key), manifest.CommandID))
			test.run(t, engine, store, manifest)
		})
	}
}

func TestExactProposalReplayRejectsMissingEntryIdentity(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	key := channel.ChannelKey("exact-entry-corruption")
	store, err := db.ForChannel(key, channel.ChannelID{ID: string(key), Type: 1})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	defer store.Close()
	record := compatExactTestRecord(t, 5, 7601, string(key), "client-1")
	manifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 1,
	}, []channel.Record{record})
	item := AppendBatchItem{
		Store: store, Records: []channel.Record{record}, ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: manifest,
	}
	if result := StoreAppendBatch(context.Background(), []AppendBatchItem{item}); len(result) != 1 || result[0].Err != nil {
		t.Fatalf("first append = %+v, want success", result)
	}
	deletePhysicalTestKey(t, db, encodeEntryIdentityKey(ChannelKey(key), 1))
	result := StoreAppendBatch(context.Background(), []AppendBatchItem{item})
	if len(result) != 1 || !errors.Is(result[0].Err, channel.ErrCorruptState) {
		t.Fatalf("replay after entry corruption = %+v, want corrupt state", result)
	}
}

func TestExactProposalTruncateRejectsOrphanedCommandIndex(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	key := channel.ChannelKey("exact-orphan-command")
	store, err := db.ForChannel(key, channel.ChannelID{ID: string(key), Type: 1})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	defer store.Close()
	record := compatExactTestRecord(t, 5, 7701, string(key), "client-1")
	manifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 1,
	}, []channel.Record{record})
	result := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record}, ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: manifest,
	}})
	if len(result) != 1 || result[0].Err != nil {
		t.Fatalf("first append = %+v, want success", result)
	}
	deletePhysicalTestKey(t, db, encodeProposalByLastKey(ChannelKey(key), 1))
	if err := store.TruncateLogAndHistory(context.Background(), 0); !errors.Is(err, channel.ErrCorruptState) {
		t.Fatalf("TruncateLogAndHistory() error = %v, want orphaned command-index corruption", err)
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

func TestStoreAppendBatchSeparatesLeaderFollowerAndTrailingReplicaCommitLanes(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	observer := &commitRequestCapture{}
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{Observer: observer})

	foreground := mustForChannel(t, engine, channel.ChannelKey("foreground-append:1"), channel.ChannelID{ID: "foreground-append", Type: 1})
	follower := mustForChannel(t, engine, channel.ChannelKey("foreground-replica:1"), channel.ChannelID{ID: "foreground-replica", Type: 1})
	trailing := mustForChannel(t, engine, channel.ChannelKey("trailing-replica:1"), channel.ChannelID{ID: "trailing-replica", Type: 1})
	results := StoreAppendBatch(context.Background(), []AppendBatchItem{
		{Store: foreground, Records: []channel.Record{compatTestRecord(t, 4101, "foreground-append", "client-foreground")}},
		{Store: follower, Records: []channel.Record{compatTestRecord(t, 4103, "foreground-replica", "client-follower")}, Class: AppendBatchClassFollowerQuorum},
		{Store: trailing, Records: []channel.Record{compatTestRecord(t, 4102, "trailing-replica", "client-trailing")}, Class: AppendBatchClassTrailing},
	})
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	for i, result := range results {
		if result.Err != nil || result.BaseOffset != 0 || result.LastOffset != 1 {
			t.Fatalf("result[%d] = %+v, want base 0 last 1 nil error", i, result)
		}
	}
	lanes := observer.Lanes()
	if got := countString(lanes, "leader_append"); got != 1 {
		t.Fatalf("leader_append request count = %d, want 1 (lanes %v)", got, lanes)
	}
	if got := countString(lanes, "replica_foreground"); got != 1 {
		t.Fatalf("replica_foreground request count = %d, want 1 (lanes %v)", got, lanes)
	}
	if got := countString(lanes, "replica_trailing"); got != 1 {
		t.Fatalf("replica_trailing request count = %d, want 1 (lanes %v)", got, lanes)
	}
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
