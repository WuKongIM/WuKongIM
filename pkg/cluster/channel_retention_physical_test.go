package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestChannelRetentionGCOnceSkipsChannelsWithoutBoundary(t *testing.T) {
	node, runtime := newChannelRetentionGCNode(t)
	id := channelv2.ChannelID{ID: "retention-skip", Type: 1}
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
	id := channelv2.ChannelID{ID: "retention-apply", Type: 1}
	seedChannelRetentionCatalogAndMeta(t, node, id, 4, 2)
	runtime.results = map[channelv2.ChannelID]channelv2.RetentionApplyResult{
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
	first := channelv2.ChannelID{ID: "retention-error-a", Type: 1}
	second := channelv2.ChannelID{ID: "retention-error-b", Type: 1}
	seedChannelRetentionCatalogAndMeta(t, node, first, 2, 1)
	seedChannelRetentionCatalogAndMeta(t, node, second, 2, 1)
	boom := errors.New("apply failed")
	runtime.applyErrs = map[channelv2.ChannelID]error{first: boom}
	runtime.results = map[channelv2.ChannelID]channelv2.RetentionApplyResult{
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

func newChannelRetentionGCNode(t *testing.T) (*Node, *recordingRetentionChannelService) {
	t.Helper()
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	runtime := &recordingRetentionChannelService{}
	node.channels = runtime
	return node, runtime
}

func seedChannelRetentionCatalogAndMeta(t *testing.T, node *Node, id channelv2.ChannelID, messages int, retentionThroughSeq uint64) {
	t.Helper()
	if node.defaultChannelStore == nil || node.defaultSlotMetaDB == nil {
		t.Fatal("node default stores are not initialized")
	}
	cs, err := node.defaultChannelStore.ChannelStore(channelv2.ChannelKeyForID(id), id)
	if err != nil {
		t.Fatalf("ChannelStore() error = %v", err)
	}
	records := make([]channelv2.Record, 0, messages)
	for i := 1; i <= messages; i++ {
		records = append(records, channelv2.Record{ID: uint64(i), Payload: []byte{byte(i)}, SizeBytes: 1})
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
		Status:              uint8(channelv2.StatusActive),
		RetentionThroughSeq: retentionThroughSeq,
	})
	if err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}
}

type recordingRetentionChannelService struct {
	results   map[channelv2.ChannelID]channelv2.RetentionApplyResult
	applyErrs map[channelv2.ChannelID]error

	lastApply  channelv2.RetentionApplyRequest
	applyCalls int
}

func (s *recordingRetentionChannelService) Append(context.Context, channelv2.AppendRequest) (channelv2.AppendResult, error) {
	return channelv2.AppendResult{}, nil
}

func (s *recordingRetentionChannelService) AppendBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	return channelv2.AppendBatchResult{}, nil
}

func (s *recordingRetentionChannelService) ResolveAppendAuthority(context.Context, channelv2.ChannelID) (channelv2.Meta, error) {
	return channelv2.Meta{}, nil
}

func (s *recordingRetentionChannelService) ReadChannelLastVisible(context.Context, channelv2.ChannelID, uint64) (channelv2.Message, bool, error) {
	return channelv2.Message{}, false, nil
}

func (s *recordingRetentionChannelService) RuntimeSnapshot(context.Context) (channelv2.RuntimeSnapshot, error) {
	return channelv2.RuntimeSnapshot{}, nil
}

func (s *recordingRetentionChannelService) RuntimeProbe(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error) {
	return channelv2.RuntimeProbeResult{}, nil
}

func (s *recordingRetentionChannelService) RuntimeEvict(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error) {
	return channelv2.RuntimeEvictResult{}, nil
}

func (s *recordingRetentionChannelService) DrainChannel(context.Context, channelv2.DrainChannelRequest) (channelv2.DrainChannelResult, error) {
	return channelv2.DrainChannelResult{}, nil
}

func (s *recordingRetentionChannelService) RetentionView(context.Context, channelv2.ChannelID) (channelv2.RetentionView, error) {
	return channelv2.RetentionView{}, nil
}

func (s *recordingRetentionChannelService) ApplyRetentionBoundary(_ context.Context, req channelv2.RetentionApplyRequest) (channelv2.RetentionApplyResult, error) {
	s.applyCalls++
	s.lastApply = req
	if err := s.applyErrs[req.ChannelID]; err != nil {
		return channelv2.RetentionApplyResult{}, err
	}
	if result, ok := s.results[req.ChannelID]; ok {
		return result, nil
	}
	return channelv2.RetentionApplyResult{ChannelID: req.ChannelID, ThroughSeq: req.ThroughSeq, LocalRetentionThroughSeq: req.ThroughSeq}, nil
}

func (s *recordingRetentionChannelService) Tick(context.Context) error { return nil }

func (s *recordingRetentionChannelService) Close() error { return nil }
