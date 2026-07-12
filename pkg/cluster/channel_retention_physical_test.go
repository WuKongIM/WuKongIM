package cluster

import (
	"context"
	"errors"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestChannelRetentionGCOnceSkipsChannelsWithoutBoundary(t *testing.T) {
	node, runtime := newChannelRetentionGCNode(t)
	id := channelruntime.ChannelID{ID: "retention-skip", Type: 1}
	seedChannelRetentionCatalogAndMeta(t, node, id, 3, 0)

	result, err := node.RunChannelRetentionGCOnce(context.Background())
	if err != nil {
		t.Fatalf("RunChannelRetentionGCOnce() error = %v", err)
	}
	if result.ScannedChannels != 1 || result.AppliedChannels != 0 || result.TrimmedChannels != 0 || result.Errors != 0 {
		t.Fatalf("result = %#v, want one scanned channel and no apply", result)
	}
	if runtime.applyCalls != 0 {
		t.Fatalf("apply calls = %d, want 0", runtime.applyCalls)
	}
}

func TestChannelRetentionGCOnceAppliesCommittedBoundary(t *testing.T) {
	node, runtime := newChannelRetentionGCNode(t)
	id := channelruntime.ChannelID{ID: "retention-apply", Type: 1}
	seedChannelRetentionCatalogAndMeta(t, node, id, 4, 2)
	runtime.results = map[channelruntime.ChannelID]channelruntime.RetentionApplyResult{
		id: {ChannelID: id, ThroughSeq: 2, LocalRetentionThroughSeq: 2, PhysicalRetentionThroughSeq: 2, DeletedThroughSeq: 2, Deleted: 2},
	}

	result, err := node.RunChannelRetentionGCOnce(context.Background())
	if err != nil {
		t.Fatalf("RunChannelRetentionGCOnce() error = %v", err)
	}
	if result.ScannedChannels != 1 || result.AppliedChannels != 1 || result.TrimmedChannels != 1 || result.DeletedMessages != 2 || result.Errors != 0 {
		t.Fatalf("result = %#v, want one applied trim deleting two messages", result)
	}
	if runtime.applyCalls != 1 {
		t.Fatalf("apply calls = %d, want 1", runtime.applyCalls)
	}
	got := runtime.lastApply
	if got.ChannelID != id || got.ThroughSeq != 2 || got.Options.MaxTrimMessages != 1000 || got.Options.MaxTrimBytes != 0 {
		t.Fatalf("last apply = %#v, want default bounded trim request", got)
	}
}

func TestChannelRetentionGCOnceContinuesAfterChannelError(t *testing.T) {
	node, runtime := newChannelRetentionGCNode(t)
	first := channelruntime.ChannelID{ID: "retention-error-a", Type: 1}
	second := channelruntime.ChannelID{ID: "retention-error-b", Type: 1}
	seedChannelRetentionCatalogAndMeta(t, node, first, 2, 1)
	seedChannelRetentionCatalogAndMeta(t, node, second, 2, 1)
	boom := errors.New("apply failed")
	runtime.applyErrs = map[channelruntime.ChannelID]error{first: boom}
	runtime.results = map[channelruntime.ChannelID]channelruntime.RetentionApplyResult{
		second: {ChannelID: second, ThroughSeq: 1, LocalRetentionThroughSeq: 1, PhysicalRetentionThroughSeq: 1, DeletedThroughSeq: 1, Deleted: 1},
	}

	result, err := node.RunChannelRetentionGCOnce(context.Background())
	if err != nil {
		t.Fatalf("RunChannelRetentionGCOnce() error = %v", err)
	}
	if result.ScannedChannels != 2 || result.AppliedChannels != 1 || result.TrimmedChannels != 1 || result.Errors != 1 {
		t.Fatalf("result = %#v, want one error and one successful trim", result)
	}
	if runtime.applyCalls != 2 {
		t.Fatalf("apply calls = %d, want both channels attempted", runtime.applyCalls)
	}
}

func TestReadLocalLatestMessagesExcludesRowsAboveCommittedHW(t *testing.T) {
	node, runtime := newChannelRetentionGCNode(t)
	id := channelruntime.ChannelID{ID: "latest-committed", Type: 1}
	seedChannelRetentionCatalogAndMeta(t, node, id, 2, 0)
	runtime.probe = channelruntime.RuntimeProbeResult{Channels: []channelruntime.RuntimeProbeChannel{{ChannelID: id, HW: 1}}}

	items, hasMore, _, err := node.ReadLocalLatestMessages(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadLocalLatestMessages() error = %v", err)
	}
	if hasMore || len(items) != 1 || items[0].MessageID != 1 || items[0].MessageSeq != 1 {
		t.Fatalf("items = %#v hasMore=%v, want only committed message 1", items, hasMore)
	}
}

func TestReadLocalLatestMessagesRecoversSingleReplicaCommittedLEO(t *testing.T) {
	node, _ := newChannelRetentionGCNode(t)
	id := channelruntime.ChannelID{ID: "latest-single-replica", Type: 1}
	seedChannelRetentionCatalogAndMeta(t, node, id, 2, 0)

	items, hasMore, _, err := node.ReadLocalLatestMessages(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadLocalLatestMessages() error = %v", err)
	}
	if hasMore || len(items) != 2 || items[0].MessageID != 2 || items[1].MessageID != 1 {
		t.Fatalf("items = %#v hasMore=%v, want recovered committed 2,1", items, hasMore)
	}
}

func TestReadLocalLatestMessagesRecoversMinISROneLeaderLEO(t *testing.T) {
	node, _ := newChannelRetentionGCNode(t)
	id := channelruntime.ChannelID{ID: "latest-min-isr-one", Type: 1}
	seedChannelRetentionCatalogAndMeta(t, node, id, 2, 0)
	route := waitRouteKeyLeaderReady(t, node, id.ID)
	shard := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot)
	if err := shard.DeleteChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type)); err != nil {
		t.Fatalf("DeleteChannelRuntimeMeta(): %v", err)
	}
	err := shard.UpsertChannelRuntimeMeta(context.Background(), metadb.ChannelRuntimeMeta{
		ChannelID: id.ID, ChannelType: int64(id.Type), ChannelEpoch: 1, LeaderEpoch: 1,
		RouteGeneration: 1, Replicas: []uint64{node.NodeID(), 2}, ISR: []uint64{node.NodeID(), 2},
		Leader: node.NodeID(), MinISR: 1, Status: uint8(channelruntime.StatusActive),
	})
	if err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}

	items, hasMore, _, err := node.ReadLocalLatestMessages(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadLocalLatestMessages() error = %v", err)
	}
	if hasMore || len(items) != 2 || items[0].MessageID != 2 || items[1].MessageID != 1 {
		t.Fatalf("items = %#v hasMore=%v, want recovered committed 2,1", items, hasMore)
	}
}

func TestReadLocalLatestMessagesBoundsHiddenRetentionScan(t *testing.T) {
	node, runtime := newChannelRetentionGCNode(t)
	id := channelruntime.ChannelID{ID: "latest-retained", Type: 1}
	const messages = latestMessageScanMinBudget + 1
	seedChannelRetentionCatalogAndMeta(t, node, id, messages, messages)
	runtime.probe = channelruntime.RuntimeProbeResult{Channels: []channelruntime.RuntimeProbeChannel{{ChannelID: id, HW: messages}}}

	_, _, _, err := node.ReadLocalLatestMessages(context.Background(), 0, 1)
	if !errors.Is(err, channelruntime.ErrBackpressured) {
		t.Fatalf("ReadLocalLatestMessages() error = %v, want bounded scan backpressure", err)
	}
	items, hasMore, _, err := node.ReadLocalLatestMessages(context.Background(), 0, 1)
	if err != nil || hasMore || len(items) != 0 {
		t.Fatalf("ReadLocalLatestMessages(after cleanup) items=%#v hasMore=%v err=%v, want completed empty page", items, hasMore, err)
	}
}

func newChannelRetentionGCNode(t *testing.T) (*Node, *recordingRetentionChannelService) {
	t.Helper()
	node := newDefaultSingleNode(t)
	t.Cleanup(func() { stopNodes(t, node) })
	createdDefaultChannels, err := node.ensureDefaultRuntime()
	if err != nil {
		t.Fatalf("ensureDefaultRuntime() error = %v", err)
	}
	if !createdDefaultChannels || node.channels == nil {
		t.Fatal("ensureDefaultRuntime() did not create the default Channel service")
	}
	defaultChannels := node.channels
	runtime := &recordingRetentionChannelService{}
	node.channels = runtime
	if err := defaultChannels.Close(); err != nil {
		t.Fatalf("close default Channel service before test override: %v", err)
	}
	startNode(t, node)
	return node, runtime
}

func seedChannelRetentionCatalogAndMeta(t *testing.T, node *Node, id channelruntime.ChannelID, messages int, retentionThroughSeq uint64) {
	t.Helper()
	if node.defaultChannelStore == nil || node.defaultSlotMetaDB == nil {
		t.Fatal("node default stores are not initialized")
	}
	cs, err := node.defaultChannelStore.ChannelStore(channelruntime.ChannelKeyForID(id), id)
	if err != nil {
		t.Fatalf("ChannelStore() error = %v", err)
	}
	records := make([]channelruntime.Record, 0, messages)
	for i := 1; i <= messages; i++ {
		records = append(records, channelruntime.Record{ID: uint64(i), Payload: []byte{byte(i)}, SizeBytes: 1})
	}
	if _, err := cs.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: records}); err != nil {
		t.Fatalf("AppendLeader() error = %v", err)
	}
	route := waitRouteKeyLeaderReady(t, node, id.ID)
	err = node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).UpsertChannelRuntimeMeta(context.Background(), metadb.ChannelRuntimeMeta{
		ChannelID:           id.ID,
		ChannelType:         int64(id.Type),
		ChannelEpoch:        1,
		LeaderEpoch:         1,
		RouteGeneration:     1,
		Replicas:            []uint64{node.NodeID()},
		ISR:                 []uint64{node.NodeID()},
		Leader:              node.NodeID(),
		MinISR:              1,
		Status:              uint8(channelruntime.StatusActive),
		RetentionThroughSeq: retentionThroughSeq,
	})
	if err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}
}

type recordingRetentionChannelService struct {
	results   map[channelruntime.ChannelID]channelruntime.RetentionApplyResult
	applyErrs map[channelruntime.ChannelID]error
	probe     channelruntime.RuntimeProbeResult

	lastApply  channelruntime.RetentionApplyRequest
	applyCalls int
}

func (s *recordingRetentionChannelService) Append(context.Context, channelruntime.AppendRequest) (channelruntime.AppendResult, error) {
	return channelruntime.AppendResult{}, nil
}

func (s *recordingRetentionChannelService) AppendBatch(context.Context, channelruntime.AppendBatchRequest) (channelruntime.AppendBatchResult, error) {
	return channelruntime.AppendBatchResult{}, nil
}

func (s *recordingRetentionChannelService) ResolveAppendAuthority(context.Context, channelruntime.ChannelID) (channelruntime.Meta, error) {
	return channelruntime.Meta{}, nil
}

func (s *recordingRetentionChannelService) ReadChannelLastVisible(context.Context, channelruntime.ChannelID, uint64) (channelruntime.Message, bool, error) {
	return channelruntime.Message{}, false, nil
}

func (s *recordingRetentionChannelService) RuntimeSnapshot(context.Context) (channelruntime.RuntimeSnapshot, error) {
	return channelruntime.RuntimeSnapshot{}, nil
}

func (s *recordingRetentionChannelService) RuntimeProbe(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error) {
	return s.probe, nil
}

func (s *recordingRetentionChannelService) RuntimeEvict(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error) {
	return channelruntime.RuntimeEvictResult{}, nil
}

func (s *recordingRetentionChannelService) DrainChannel(context.Context, channelruntime.DrainChannelRequest) (channelruntime.DrainChannelResult, error) {
	return channelruntime.DrainChannelResult{}, nil
}

func (s *recordingRetentionChannelService) RetentionView(context.Context, channelruntime.ChannelID) (channelruntime.RetentionView, error) {
	return channelruntime.RetentionView{}, nil
}

func (s *recordingRetentionChannelService) ApplyRetentionBoundary(_ context.Context, req channelruntime.RetentionApplyRequest) (channelruntime.RetentionApplyResult, error) {
	s.applyCalls++
	s.lastApply = req
	if err := s.applyErrs[req.ChannelID]; err != nil {
		return channelruntime.RetentionApplyResult{}, err
	}
	if result, ok := s.results[req.ChannelID]; ok {
		return result, nil
	}
	return channelruntime.RetentionApplyResult{ChannelID: req.ChannelID, ThroughSeq: req.ThroughSeq, LocalRetentionThroughSeq: req.ThroughSeq}, nil
}

func (s *recordingRetentionChannelService) Tick(context.Context) error { return nil }

func (s *recordingRetentionChannelService) Close() error { return nil }
