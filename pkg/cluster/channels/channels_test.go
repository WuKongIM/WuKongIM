package channels

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
	"github.com/stretchr/testify/require"
)

func TestStaticMetaSourceResolvesAndDerivesKey(t *testing.T) {
	id := ch.ChannelID{ID: "room", Type: 1}
	source := NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}})
	meta, err := source.ResolveChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelMeta() error = %v", err)
	}
	if meta.Key != ch.ChannelKeyForID(id) {
		t.Fatalf("key = %q, want derived", meta.Key)
	}
}

func TestStaticMetaSourceEnsuresExistingMeta(t *testing.T) {
	id := ch.ChannelID{ID: "ensure-static", Type: 1}
	source := NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}})
	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if meta.ID != id || meta.Key != ch.ChannelKeyForID(id) {
		t.Fatalf("meta = %#v, want derived identity", meta)
	}
}

func TestSlotMetaSourceResolvesAuthoritativeRuntimeMeta(t *testing.T) {
	id := ch.ChannelID{ID: "room", Type: 1}
	leaseUntil := time.UnixMilli(1234).UTC()
	source := NewSlotMetaSource(runtimeMetaReaderFake{meta: metadb.ChannelRuntimeMeta{
		ChannelID:           id.ID,
		ChannelType:         int64(id.Type),
		ChannelEpoch:        2,
		LeaderEpoch:         3,
		Leader:              2,
		Replicas:            []uint64{3, 1, 2},
		ISR:                 []uint64{2, 1},
		MinISR:              2,
		LeaseUntilMS:        leaseUntil.UnixMilli(),
		RetentionThroughSeq: 9,
		WriteFenceToken:     "migration-1",
		WriteFenceVersion:   7,
		WriteFenceReason:    uint8(ch.WriteFenceReasonLeaderTransfer),
		WriteFenceUntilMS:   leaseUntil.Add(time.Second).UnixMilli(),
		Status:              uint8(ch.StatusActive),
	}})

	meta, err := source.ResolveChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelMeta() error = %v", err)
	}
	if meta.Key != ch.ChannelKeyForID(id) || meta.ID != id || meta.Epoch != 2 || meta.LeaderEpoch != 3 || meta.Leader != 2 {
		t.Fatalf("meta identity/epochs = %#v", meta)
	}
	if got, want := meta.Replicas, []ch.NodeID{1, 2, 3}; !equalNodeIDs(got, want) {
		t.Fatalf("Replicas = %v, want %v", got, want)
	}
	if got, want := meta.ISR, []ch.NodeID{1, 2}; !equalNodeIDs(got, want) {
		t.Fatalf("ISR = %v, want %v", got, want)
	}
	if meta.MinISR != 2 || !meta.LeaseUntil.Equal(leaseUntil) || meta.Status != ch.StatusActive {
		t.Fatalf("meta quorum/lease/status = %#v", meta)
	}
	if meta.RetentionThroughSeq != 9 {
		t.Fatalf("RetentionThroughSeq = %d, want 9", meta.RetentionThroughSeq)
	}
	require.Equal(t, ch.WriteFence{
		Token:   "migration-1",
		Version: 7,
		Reason:  ch.WriteFenceReasonLeaderTransfer,
		Until:   leaseUntil.Add(time.Second),
	}, meta.WriteFence)
}

func TestSlotMetaSourceProjectsClearedWriteFenceVersion(t *testing.T) {
	id := ch.ChannelID{ID: "cleared-fence", Type: 1}
	source := NewSlotMetaSource(runtimeMetaReaderFake{meta: metadb.ChannelRuntimeMeta{
		ChannelID:         id.ID,
		ChannelType:       int64(id.Type),
		ChannelEpoch:      2,
		LeaderEpoch:       3,
		Leader:            1,
		Replicas:          []uint64{1, 2},
		ISR:               []uint64{1, 2},
		MinISR:            1,
		Status:            uint8(ch.StatusActive),
		WriteFenceVersion: 8,
	}})

	meta, err := source.ResolveChannelMeta(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, ch.WriteFence{Version: 8}, meta.WriteFence)
	require.False(t, meta.WriteFence.Set())
}

func TestServicePassesAppendBatchTuningToRuntime(t *testing.T) {
	id := ch.ChannelID{ID: "batch-tuning", Type: 1}
	meta := ch.Meta{
		Key:         ch.ChannelKeyForID(id),
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1},
		ISR:         []ch.NodeID{1},
		MinISR:      1,
		Status:      ch.StatusActive,
	}
	service, err := NewService(Config{
		LocalNode:               1,
		Store:                   channelstore.NewMemoryFactory(),
		MetaSource:              NewStaticMetaSource([]ch.Meta{meta}),
		ReactorCount:            1,
		StoreAppendBatchMaxWait: 100 * time.Microsecond,
		AppendBatchMaxRecords:   10,
		AppendBatchMaxWait:      time.Hour,
	})
	require.NoError(t, err)
	defer service.Close()
	require.NoError(t, service.ApplyMeta(meta))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = service.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("wait-for-batch")}})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestServicePassesAppendAdaptiveFlushTuningToRuntime(t *testing.T) {
	id := ch.ChannelID{ID: "adaptive-batch-tuning", Type: 1}
	meta := ch.Meta{
		Key:         ch.ChannelKeyForID(id),
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1},
		ISR:         []ch.NodeID{1},
		MinISR:      1,
		Status:      ch.StatusActive,
	}
	service, err := NewService(Config{
		LocalNode:                1,
		Store:                    channelstore.NewMemoryFactory(),
		MetaSource:               NewStaticMetaSource([]ch.Meta{meta}),
		ReactorCount:             1,
		AppendBatchMaxRecords:    10,
		AppendBatchMaxWait:       time.Hour,
		AppendBatchAdaptiveFlush: true,
		AppendBatchColdMaxWait:   time.Millisecond,
	})
	require.NoError(t, err)
	defer service.Close()
	require.NoError(t, service.ApplyMeta(meta))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := service.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("cold-flush")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.MessageSeq)
}

func TestSlotMetaSourceEnsuresExistingRuntimeMeta(t *testing.T) {
	id := ch.ChannelID{ID: "ensure-existing", Type: 1}
	reader := &runtimeMetaReaderFake{meta: metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 2,
		LeaderEpoch:  3,
		Leader:       2,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		MinISR:       1,
		Status:       uint8(ch.StatusActive),
	}}
	source := NewSlotMetaSource(reader, SlotMetaSourceOptions{DefaultReplicas: []ch.NodeID{1, 2}})

	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if meta.Epoch != 2 || meta.LeaderEpoch != 3 || meta.Leader != 2 || reader.upserts != 0 {
		t.Fatalf("meta=%#v upserts=%d, want existing without create", meta, reader.upserts)
	}
}

func TestSlotMetaSourceCreatesMissingRuntimeMeta(t *testing.T) {
	id := ch.ChannelID{ID: "ensure-create", Type: 1}
	reader := &runtimeMetaReaderFake{err: metadb.ErrNotFound}
	source := NewSlotMetaSource(reader, SlotMetaSourceOptions{DefaultReplicas: []ch.NodeID{2, 1}, DefaultMinISR: 1})

	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if reader.upserts != 1 {
		t.Fatalf("upserts = %d, want one create", reader.upserts)
	}
	if meta.ID != id || meta.Epoch != 1 || meta.LeaderEpoch != 1 || meta.Leader != 2 || meta.Status != ch.StatusActive {
		t.Fatalf("created meta = %#v, want initial active meta", meta)
	}
	if got, want := meta.Replicas, []ch.NodeID{1, 2}; !equalNodeIDs(got, want) {
		t.Fatalf("Replicas = %v, want %v", got, want)
	}
}

func TestSlotMetaSourceObservesEnsureMetaStageBreakdown(t *testing.T) {
	id := ch.ChannelID{ID: "ensure-create-observed", Type: 1}
	reader := &runtimeMetaReaderFake{err: metadb.ErrNotFound}
	observer := &appendStageObserver{}
	source := NewSlotMetaSource(reader, SlotMetaSourceOptions{
		DefaultReplicas: []ch.NodeID{2, 1},
		DefaultMinISR:   1,
		Observer:        observer,
	})

	_, err := source.EnsureChannelMeta(context.Background(), id)
	require.NoError(t, err)

	requireAppendStage(t, observer.events, "meta_slot_read", "miss")
	requireAppendStage(t, observer.events, "meta_create_build", "ok")
	requireAppendStage(t, observer.events, "meta_create_propose", "ok")
	requireAppendStage(t, observer.events, "meta_create_write", "ok")
	requireAppendStage(t, observer.events, "meta_final_read", "ok")
}

func TestSlotMetaSourceReturnsCreatedMetaWhenLocalReadLagsAfterWrite(t *testing.T) {
	id := ch.ChannelID{ID: "ensure-create-lagging-read", Type: 1}
	store := &laggingRuntimeMetaStore{}
	observer := &appendStageObserver{}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{DefaultReplicas: []ch.NodeID{2, 1}, DefaultMinISR: 1, Observer: observer})

	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if store.upserts != 1 {
		t.Fatalf("upserts = %d, want one create", store.upserts)
	}
	if meta.ID != id || meta.Leader != 2 || meta.Epoch != 1 || meta.LeaderEpoch != 1 {
		t.Fatalf("created meta = %#v, want deterministic initial meta", meta)
	}
	requireAppendStage(t, observer.events, "meta_final_read", "miss")
}

func TestSlotMetaSourceCreatesMissingRuntimeMetaFromPlacement(t *testing.T) {
	id := ch.ChannelID{ID: "ensure-placement", Type: 1}
	reader := &runtimeMetaReaderFake{err: metadb.ErrNotFound}
	source := NewSlotMetaSource(reader, SlotMetaSourceOptions{
		Placement: fakePlacementResolver{placement: ChannelPlacement{
			Leader:   3,
			Replicas: []ch.NodeID{2, 3, 1},
			MinISR:   2,
		}},
	})

	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if reader.upserts != 1 {
		t.Fatalf("upserts = %d, want one create", reader.upserts)
	}
	if meta.Leader != 3 || meta.MinISR != 2 {
		t.Fatalf("created meta leader/minISR = %#v, want placement", meta)
	}
	if got, want := meta.Replicas, []ch.NodeID{1, 2, 3}; !equalNodeIDs(got, want) {
		t.Fatalf("Replicas = %v, want %v", got, want)
	}
}

func TestSlotPlacementResolverUsesDataNodesInsteadOfSlotPeers(t *testing.T) {
	id := ch.ChannelID{ID: "route-placement", Type: 1}
	resolver := NewSlotPlacementResolver(
		fakePlacementRouter{route: routing.Route{Leader: 2, Peers: []uint64{1, 2, 3}}},
		fakeDataNodeProvider{nodes: []uint64{4, 5, 6}},
		3,
	)

	placement, err := resolver.ResolveChannelPlacement(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelPlacement() error = %v", err)
	}
	if placement.MinISR != 2 {
		t.Fatalf("MinISR = %d, want quorum 2", placement.MinISR)
	}
	if len(placement.Replicas) != 3 {
		t.Fatalf("Replicas = %v, want 3 data replicas", placement.Replicas)
	}
	for _, replica := range placement.Replicas {
		if replica < 4 || replica > 6 {
			t.Fatalf("Replicas = %v, want only data nodes 4,5,6", placement.Replicas)
		}
	}
}

func TestSlotPlacementResolverPrefersRoutePreferredLeaderOnlyWhenSelected(t *testing.T) {
	id := ch.ChannelID{ID: "route-placement-preferred", Type: 1}
	resolver := NewSlotPlacementResolver(
		fakePlacementRouter{route: routing.Route{Leader: 3, PreferredLeader: 4, Peers: []uint64{1, 2, 3}}},
		fakeDataNodeProvider{nodes: []uint64{4, 5, 6}},
		3,
	)

	placement, err := resolver.ResolveChannelPlacement(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelPlacement() error = %v", err)
	}
	if placement.Leader != 4 {
		t.Fatalf("Leader = %d, want selected preferred leader 4", placement.Leader)
	}

	withoutPreferred := NewSlotPlacementResolver(
		fakePlacementRouter{route: routing.Route{Leader: 3, PreferredLeader: 9, Peers: []uint64{1, 2, 3}}},
		fakeDataNodeProvider{nodes: []uint64{4, 5, 6}},
		3,
	)
	next, err := withoutPreferred.ResolveChannelPlacement(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelPlacement(without preferred) error = %v", err)
	}
	if next.Leader == 9 {
		t.Fatalf("Leader = %d, preferred leader outside replicas must not be used", next.Leader)
	}
	if !nodeIDIn(next.Replicas, next.Leader) {
		t.Fatalf("Leader = %d, replicas=%v, want leader in replicas", next.Leader, next.Replicas)
	}
}

func TestSlotPlacementResolverHashesFullChannelIdentity(t *testing.T) {
	resolver := NewSlotPlacementResolver(
		fakePlacementRouter{route: routing.Route{Leader: 1, Peers: []uint64{1, 2, 3}}},
		fakeDataNodeProvider{nodes: []uint64{1, 2, 3, 4, 5, 6, 7, 8}},
		3,
	)

	first, err := resolver.ResolveChannelPlacement(context.Background(), ch.ChannelID{ID: "same-id", Type: 1})
	if err != nil {
		t.Fatalf("ResolveChannelPlacement(type=1) error = %v", err)
	}
	second, err := resolver.ResolveChannelPlacement(context.Background(), ch.ChannelID{ID: "same-id", Type: 2})
	if err != nil {
		t.Fatalf("ResolveChannelPlacement(type=2) error = %v", err)
	}
	if equalNodeIDs(first.Replicas, second.Replicas) {
		t.Fatalf("Replicas for same ID and different Type both = %v, want type-specific placement hash", first.Replicas)
	}
}

func TestSlotPlacementResolverRejectsInvalidDataNodeCandidates(t *testing.T) {
	id := ch.ChannelID{ID: "invalid-placement", Type: 1}
	tests := []struct {
		name         string
		dataNodes    DataNodeProvider
		replicaCount int
	}{
		{name: "nil provider", dataNodes: nil, replicaCount: 1},
		{name: "zero replicas", dataNodes: fakeDataNodeProvider{nodes: []uint64{1, 2}}, replicaCount: 0},
		{name: "insufficient unique nodes", dataNodes: fakeDataNodeProvider{nodes: []uint64{1, 1, 2}}, replicaCount: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewSlotPlacementResolver(
				fakePlacementRouter{route: routing.Route{Leader: 1, Peers: []uint64{1, 2, 3}}},
				tt.dataNodes,
				tt.replicaCount,
			)
			_, err := resolver.ResolveChannelPlacement(context.Background(), id)
			if !errors.Is(err, ch.ErrInvalidConfig) {
				t.Fatalf("ResolveChannelPlacement() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestSlotPlacementResolverDeduplicatesDataNodeCandidates(t *testing.T) {
	id := ch.ChannelID{ID: "dedupe-placement", Type: 1}
	resolver := NewSlotPlacementResolver(
		fakePlacementRouter{route: routing.Route{Leader: 1, Peers: []uint64{1, 2, 3}}},
		fakeDataNodeProvider{nodes: []uint64{7, 5, 7, 6, 5}},
		3,
	)

	placement, err := resolver.ResolveChannelPlacement(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelPlacement() error = %v", err)
	}
	if len(placement.Replicas) != 3 {
		t.Fatalf("Replicas = %v, want three unique nodes", placement.Replicas)
	}
	seen := map[ch.NodeID]bool{}
	for _, replica := range placement.Replicas {
		if replica < 5 || replica > 7 {
			t.Fatalf("Replicas = %v, want only data nodes 5,6,7", placement.Replicas)
		}
		if seen[replica] {
			t.Fatalf("Replicas = %v, want no duplicates", placement.Replicas)
		}
		seen[replica] = true
	}
}

func TestSlotMetaSourceMapsMissingRuntimeMetaToChannelNotFound(t *testing.T) {
	source := NewSlotMetaSource(runtimeMetaReaderFake{err: metadb.ErrNotFound})
	_, err := source.ResolveChannelMeta(context.Background(), ch.ChannelID{ID: "missing", Type: 1})
	if !errors.Is(err, ch.ErrChannelNotFound) {
		t.Fatalf("ResolveChannelMeta() error = %v, want ErrChannelNotFound", err)
	}
}

func TestServiceRequiresCombinedRuntime(t *testing.T) {
	_, err := NewService(Config{Runtime: clusterOnlyRuntime{}})
	if err == nil {
		t.Fatal("NewService() error = nil, want combined runtime error")
	}
	svc, err := NewService(Config{Runtime: &fakeRuntime{}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if svc.Runtime() == nil || svc.Server() == nil {
		t.Fatal("service did not retain cluster and transport server surfaces")
	}
}

func TestCodecRoundTripsPull(t *testing.T) {
	req := channeltransport.PullRequest{ChannelKey: "1:room", ChannelID: ch.ChannelID{ID: "room", Type: 1}, Epoch: 1, LeaderEpoch: 2, Follower: 3, NextOffset: 4, AckOffset: 3, MaxBytes: 1024, NeedMeta: true}
	data, err := EncodePullRequest(req)
	if err != nil {
		t.Fatalf("EncodePullRequest() error = %v", err)
	}
	got, err := DecodePullRequest(data)
	if err != nil {
		t.Fatalf("DecodePullRequest() error = %v", err)
	}
	require.Equal(t, req, got)
}

func TestCodecRoundTripsPullResponseMeta(t *testing.T) {
	resp := channeltransport.PullResponse{
		ChannelKey:      "1:room",
		Epoch:           1,
		LeaderEpoch:     2,
		LeaderHW:        3,
		LeaderLEO:       4,
		ActivityVersion: 5,
		Meta: &ch.Meta{
			Key:         "1:room",
			ID:          ch.ChannelID{ID: "room", Type: 1},
			Epoch:       1,
			LeaderEpoch: 2,
			Leader:      1,
			Replicas:    []ch.NodeID{1, 3},
			ISR:         []ch.NodeID{1, 3},
			MinISR:      2,
			Status:      ch.StatusActive,
		},
	}
	data, err := encodePullResponse(resp)
	if err != nil {
		t.Fatalf("encodePullResponse() error = %v", err)
	}
	got, err := decodePullResponse(data)
	if err != nil {
		t.Fatalf("decodePullResponse() error = %v", err)
	}
	require.Equal(t, resp, got)
}

func TestCodecRoundTripsPullHintSlimFields(t *testing.T) {
	req := channeltransport.PullHintRequest{
		ChannelKey:      "1:room",
		ChannelID:       ch.ChannelID{ID: "room", Type: 1},
		Epoch:           1,
		LeaderEpoch:     2,
		Leader:          1,
		LeaderLEO:       4,
		ActivityVersion: 4,
		Reason:          channeltransport.PullHintReasonAppend,
	}
	data, err := encodePullHintRequest(req)
	if err != nil {
		t.Fatalf("encodePullHintRequest() error = %v", err)
	}
	got, err := decodePullHintRequest(data)
	if err != nil {
		t.Fatalf("decodePullHintRequest() error = %v", err)
	}
	require.Equal(t, req, got)
}

func TestCodecEncodesAllFramesWithBinaryPayload(t *testing.T) {
	sampleMeta := &ch.Meta{
		Key:         "1:room",
		ID:          ch.ChannelID{ID: "room", Type: 1},
		Epoch:       1,
		LeaderEpoch: 2,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2, 3},
		ISR:         []ch.NodeID{1, 2},
		MinISR:      2,
		LeaseUntil:  time.Unix(1700000000, 123),
		Status:      ch.StatusActive,
	}
	sampleRecord := ch.Record{ID: 10, Index: 11, Epoch: 12, FromUID: "u1", ClientMsgNo: "record-client", Payload: []byte("record-payload"), SizeBytes: 14, ServerTimestampMS: 1700000000123}
	sampleMessage := ch.Message{
		MessageID:         21,
		MessageSeq:        22,
		ChannelID:         "room",
		ChannelType:       1,
		FromUID:           "u1",
		ClientMsgNo:       "m1",
		ServerTimestampMS: 1700000000456,
		TraceID:           "trace-message",
		ChannelKey:        "channel/key-message",
		Payload:           []byte("message-payload"),
	}

	tests := []struct {
		name   string
		encode func() ([]byte, error)
		decode func([]byte)
	}{
		{
			name: "pull request",
			encode: func() ([]byte, error) {
				return EncodePullRequest(channeltransport.PullRequest{ChannelKey: "1:room", ChannelID: ch.ChannelID{ID: "room", Type: 1}, Epoch: 1, LeaderEpoch: 2, Follower: 3, NextOffset: 4, AckOffset: 5, MaxBytes: 1024, NeedMeta: true})
			},
			decode: func(data []byte) {
				got, err := DecodePullRequest(data)
				require.NoError(t, err)
				require.Equal(t, uint64(5), got.AckOffset)
				require.True(t, got.NeedMeta)
			},
		},
		{
			name: "pull response",
			encode: func() ([]byte, error) {
				return encodePullResponse(channeltransport.PullResponse{ChannelKey: "1:room", Epoch: 1, LeaderEpoch: 2, LeaderHW: 3, LeaderLEO: 4, ActivityVersion: 5, NextPullAfter: 250 * time.Millisecond, Control: channeltransport.PullControlStop, Meta: sampleMeta, Records: []ch.Record{sampleRecord}})
			},
			decode: func(data []byte) {
				got, err := decodePullResponse(data)
				require.NoError(t, err)
				require.Equal(t, channeltransport.PullControlStop, got.Control)
				require.Equal(t, sampleMeta, got.Meta)
				require.Equal(t, []ch.Record{sampleRecord}, got.Records)
			},
		},
		{
			name: "pull batch request",
			encode: func() ([]byte, error) {
				return encodePullBatchRequest(channeltransport.PullBatchRequest{Items: []channeltransport.PullRequest{
					{ChannelKey: "1:room-a", ChannelID: ch.ChannelID{ID: "room-a", Type: 1}, Epoch: 1, LeaderEpoch: 2, Follower: 3, NextOffset: 4, AckOffset: 5, MaxBytes: 1024},
					{ChannelKey: "1:room-b", ChannelID: ch.ChannelID{ID: "room-b", Type: 1}, Epoch: 6, LeaderEpoch: 7, Follower: 8, NextOffset: 9, AckOffset: 10, MaxBytes: 2048, NeedMeta: true},
				}})
			},
			decode: func(data []byte) {
				got, err := decodePullBatchRequest(data)
				require.NoError(t, err)
				require.Len(t, got.Items, 2)
				require.Equal(t, ch.ChannelKey("1:room-a"), got.Items[0].ChannelKey)
				require.Equal(t, uint64(10), got.Items[1].AckOffset)
				require.True(t, got.Items[1].NeedMeta)
			},
		},
		{
			name: "pull batch response",
			encode: func() ([]byte, error) {
				return encodePullBatchResponse(channeltransport.PullBatchResponse{Items: []channeltransport.PullBatchItemResult{
					{Response: channeltransport.PullResponse{ChannelKey: "1:room-a", Epoch: 1, LeaderEpoch: 2, LeaderHW: 3, LeaderLEO: 4, ActivityVersion: 5, Control: channeltransport.PullControlContinue, Records: []ch.Record{sampleRecord}}},
					{Err: ch.ErrStaleMeta},
				}})
			},
			decode: func(data []byte) {
				got, err := decodePullBatchResponse(data)
				require.NoError(t, err)
				require.Len(t, got.Items, 2)
				require.Equal(t, ch.ChannelKey("1:room-a"), got.Items[0].Response.ChannelKey)
				require.ErrorIs(t, got.Items[1].Err, ch.ErrStaleMeta)
			},
		},
		{
			name: "ack request",
			encode: func() ([]byte, error) {
				return encodeAckRequest(channeltransport.AckRequest{ChannelKey: "1:room", Epoch: 1, LeaderEpoch: 2, Follower: 3, MatchOffset: 4, ActivityVersion: 5, Stopped: true})
			},
			decode: func(data []byte) {
				got, err := decodeAckRequest(data)
				require.NoError(t, err)
				require.True(t, got.Stopped)
				require.Equal(t, uint64(4), got.MatchOffset)
			},
		},
		{
			name: "ack response",
			encode: func() ([]byte, error) {
				return encodeRPCResult(kindAck, nil, ch.ErrNotReady)
			},
			decode: func(data []byte) {
				require.ErrorIs(t, decodeRPCResult(data, kindAck, nil), ch.ErrNotReady)
			},
		},
		{
			name: "pull hint request",
			encode: func() ([]byte, error) {
				return encodePullHintRequest(channeltransport.PullHintRequest{ChannelKey: "1:room", ChannelID: ch.ChannelID{ID: "room", Type: 1}, Epoch: 1, LeaderEpoch: 2, Leader: 3, LeaderLEO: 4, ActivityVersion: 5, Reason: channeltransport.PullHintReasonResume})
			},
			decode: func(data []byte) {
				got, err := decodePullHintRequest(data)
				require.NoError(t, err)
				require.Equal(t, channeltransport.PullHintReasonResume, got.Reason)
				require.Equal(t, ch.NodeID(3), got.Leader)
			},
		},
		{
			name: "pull hint batch request",
			encode: func() ([]byte, error) {
				return encodePullHintBatchRequest(channeltransport.PullHintBatchRequest{Items: []channeltransport.PullHintRequest{
					{ChannelKey: "1:room-a", ChannelID: ch.ChannelID{ID: "room-a", Type: 1}, Epoch: 1, LeaderEpoch: 2, Leader: 3, LeaderLEO: 4, ActivityVersion: 5, Reason: channeltransport.PullHintReasonAppend},
					{ChannelKey: "1:room-b", ChannelID: ch.ChannelID{ID: "room-b", Type: 1}, Epoch: 6, LeaderEpoch: 7, Leader: 8, LeaderLEO: 9, ActivityVersion: 10, Reason: channeltransport.PullHintReasonResume},
				}})
			},
			decode: func(data []byte) {
				got, err := decodePullHintBatchRequest(data)
				require.NoError(t, err)
				require.Len(t, got.Items, 2)
				require.Equal(t, channeltransport.PullHintReasonAppend, got.Items[0].Reason)
				require.Equal(t, channeltransport.PullHintReasonResume, got.Items[1].Reason)
			},
		},
		{
			name: "pull hint batch response",
			encode: func() ([]byte, error) {
				return encodePullHintBatchResponse(channeltransport.PullHintBatchResponse{Items: []channeltransport.PullHintBatchItemResult{{}, {Err: ch.ErrNotReady}}})
			},
			decode: func(data []byte) {
				got, err := decodePullHintBatchResponse(data)
				require.NoError(t, err)
				require.Len(t, got.Items, 2)
				require.NoError(t, got.Items[0].Err)
				require.ErrorIs(t, got.Items[1].Err, ch.ErrNotReady)
			},
		},
		{
			name: "notify request",
			encode: func() ([]byte, error) {
				return encodeNotifyRequest(channeltransport.NotifyRequest{ChannelKey: "1:room", ChannelID: ch.ChannelID{ID: "room", Type: 1}, Epoch: 1, LeaderEpoch: 2, Leader: 3, LeaderLEO: 4})
			},
			decode: func(data []byte) {
				got, err := decodeNotifyRequest(data)
				require.NoError(t, err)
				require.Equal(t, ch.NodeID(3), got.Leader)
				require.Equal(t, uint64(4), got.LeaderLEO)
			},
		},
		{
			name: "append request",
			encode: func() ([]byte, error) {
				return encodeAppendRequest(ch.AppendRequest{ChannelID: ch.ChannelID{ID: "room", Type: 1}, Message: sampleMessage, CommitMode: ch.CommitModeQuorum, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 2})
			},
			decode: func(data []byte) {
				got, err := decodeAppendRequest(data)
				require.NoError(t, err)
				require.Equal(t, sampleMessage, got.Message)
				require.Equal(t, ch.CommitModeQuorum, got.CommitMode)
			},
		},
		{
			name: "append response",
			encode: func() ([]byte, error) {
				return encodeAppendResponse(ch.AppendResult{MessageID: 21, MessageSeq: 22, Message: sampleMessage})
			},
			decode: func(data []byte) {
				got, err := decodeAppendResponse(data)
				require.NoError(t, err)
				require.Equal(t, sampleMessage, got.Message)
			},
		},
		{
			name: "append batch request",
			encode: func() ([]byte, error) {
				return encodeAppendBatchRequest(ch.AppendBatchRequest{ChannelID: ch.ChannelID{ID: "room", Type: 1}, Messages: []ch.Message{sampleMessage}, TraceID: "trace-request", ChannelKey: "channel/key-request", Attempt: 3, CommitMode: ch.CommitModeLocal, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 2, OmitResultPayload: true})
			},
			decode: func(data []byte) {
				got, err := decodeAppendBatchRequest(data)
				require.NoError(t, err)
				require.Equal(t, []ch.Message{sampleMessage}, got.Messages)
				require.Equal(t, "trace-request", got.TraceID)
				require.Equal(t, "channel/key-request", got.ChannelKey)
				require.Equal(t, 3, got.Attempt)
				require.True(t, got.OmitResultPayload)
			},
		},
		{
			name: "append batch response",
			encode: func() ([]byte, error) {
				return encodeAppendBatchResponse(ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageID: 21, MessageSeq: 22, Message: sampleMessage}, {Err: ch.ErrBackpressured}}})
			},
			decode: func(data []byte) {
				got, err := decodeAppendBatchResponse(data)
				require.NoError(t, err)
				require.Len(t, got.Items, 2)
				require.Equal(t, sampleMessage, got.Items[0].Message)
				require.ErrorIs(t, got.Items[1].Err, ch.ErrBackpressured)
			},
		},
		{
			name: "last visible request",
			encode: func() ([]byte, error) {
				return encodeLastVisibleRequest(LastVisibleRequest{
					ChannelID:            ch.ChannelID{ID: "room", Type: 1},
					VisibleAfterSeq:      7,
					ExpectedLeader:       2,
					ExpectedChannelEpoch: 3,
					ExpectedLeaderEpoch:  4,
				})
			},
			decode: func(data []byte) {
				got, err := decodeLastVisibleRequest(data)
				require.NoError(t, err)
				require.Equal(t, ch.ChannelID{ID: "room", Type: 1}, got.ChannelID)
				require.Equal(t, uint64(7), got.VisibleAfterSeq)
				require.Equal(t, ch.NodeID(2), got.ExpectedLeader)
				require.Equal(t, uint64(3), got.ExpectedChannelEpoch)
				require.Equal(t, uint64(4), got.ExpectedLeaderEpoch)
			},
		},
		{
			name: "last visible response",
			encode: func() ([]byte, error) {
				return encodeLastVisibleResponse(LastVisibleResponse{Message: sampleMessage, Found: true})
			},
			decode: func(data []byte) {
				got, err := decodeLastVisibleResponse(data)
				require.NoError(t, err)
				require.True(t, got.Found)
				require.Equal(t, sampleMessage, got.Message)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.encode()
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(data), 2)
			require.False(t, json.Valid(data[2:]), "payload should use binary encoding, got JSON: %s", data[2:])
			tt.decode(data)
		})
	}
}

func TestCodecLastVisibleResponsePreservesApplicationError(t *testing.T) {
	data, err := encodeRPCResult(kindLastVisibleResponse, LastVisibleResponse{}, ch.ErrStaleMeta)
	require.NoError(t, err)

	_, err = decodeLastVisibleResponse(data)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
}

func TestCodecCurrentEncoderEmitsV4Frames(t *testing.T) {
	msg := ch.Message{
		MessageID:         21,
		MessageSeq:        22,
		ChannelID:         "room",
		ChannelType:       1,
		FromUID:           "u1",
		ClientMsgNo:       "m1",
		ServerTimestampMS: 1700000000456,
		TraceID:           "trace-message",
		ChannelKey:        "channel/key-message",
		Payload:           []byte("message-payload"),
	}
	data, err := encodeAppendRequest(ch.AppendRequest{ChannelID: ch.ChannelID{ID: "room", Type: 1}, Message: msg})
	require.NoError(t, err)
	require.Equal(t, codecVersion, data[0])

	got, err := decodeAppendRequest(data)
	require.NoError(t, err)
	require.Equal(t, msg, got.Message)
}

func TestCodecDecodesLegacyV3AppendRequestMessageLayout(t *testing.T) {
	want := ch.Message{
		MessageID:   21,
		MessageSeq:  22,
		ChannelID:   "room",
		ChannelType: 1,
		FromUID:     "u1",
		ClientMsgNo: "m1",
		TraceID:     "",
		ChannelKey:  "channel/key-message",
		Payload:     []byte{0},
	}
	body := appendChannelID(nil, ch.ChannelID{ID: "room", Type: 1})
	body = appendLegacyV3Message(body, want)
	body = append(body, byte(ch.CommitModeQuorum))
	body = appendUvarint(body, 1)
	body = appendUvarint(body, 2)
	data := appendLegacyV3Frame(kindAppend, body)

	got, err := decodeAppendRequest(data)
	require.NoError(t, err)
	require.Equal(t, ch.ChannelID{ID: "room", Type: 1}, got.ChannelID)
	require.Equal(t, want, got.Message)
	require.Zero(t, got.Message.ServerTimestampMS)
	require.Equal(t, ch.CommitModeQuorum, got.CommitMode)
	require.Equal(t, uint64(1), got.ExpectedChannelEpoch)
	require.Equal(t, uint64(2), got.ExpectedLeaderEpoch)
}

func TestCodecDecodesLegacyV3PullResponseRecordLayout(t *testing.T) {
	want := []ch.Record{
		{ID: 10, Index: 11, Epoch: 12, Payload: []byte{0}, SizeBytes: 1},
		{ID: 20, Index: 21, Epoch: 22, Payload: []byte("record-two"), SizeBytes: 10},
	}
	resp := channeltransport.PullResponse{
		ChannelKey:      "1:room",
		Epoch:           1,
		LeaderEpoch:     2,
		LeaderHW:        3,
		LeaderLEO:       4,
		ActivityVersion: 5,
		NextPullAfter:   250 * time.Millisecond,
		Control:         channeltransport.PullControlContinue,
	}
	body := []byte{rpcResultOK}
	body = appendLegacyV3PullResponse(body, resp, want)
	data := appendLegacyV3Frame(kindPullResponse, body)

	got, err := decodePullResponse(data)
	require.NoError(t, err)
	require.Equal(t, resp.ChannelKey, got.ChannelKey)
	require.Equal(t, resp.Epoch, got.Epoch)
	require.Equal(t, resp.LeaderEpoch, got.LeaderEpoch)
	require.Equal(t, resp.LeaderHW, got.LeaderHW)
	require.Equal(t, resp.LeaderLEO, got.LeaderLEO)
	require.Equal(t, resp.ActivityVersion, got.ActivityVersion)
	require.Equal(t, resp.NextPullAfter, got.NextPullAfter)
	require.Equal(t, resp.Control, got.Control)
	require.Equal(t, want, got.Records)
	for _, record := range got.Records {
		require.Empty(t, record.FromUID)
		require.Empty(t, record.ClientMsgNo)
		require.Zero(t, record.ServerTimestampMS)
	}
}

func TestCodecDecodesLegacyV3MessageLayout(t *testing.T) {
	want := ch.Message{
		MessageID:   21,
		MessageSeq:  22,
		ChannelID:   "room",
		ChannelType: 1,
		FromUID:     "u1",
		ClientMsgNo: "m1",
		TraceID:     "trace-message",
		ChannelKey:  "channel/key-message",
		Payload:     []byte("message-payload"),
	}
	body := appendLegacyV3Message(nil, want)

	got, offset, err := readMessage(body, 0, legacyCodecVersionV3)
	require.NoError(t, err)
	require.Equal(t, len(body), offset)
	require.Equal(t, want, got)
	require.Zero(t, got.ServerTimestampMS)
}

func TestCodecDecodesLegacyV3RecordLayoutInSlices(t *testing.T) {
	want := []ch.Record{
		{ID: 10, Index: 11, Epoch: 12, Payload: []byte("record-one"), SizeBytes: 10},
		{ID: 20, Index: 21, Epoch: 22, Payload: []byte("record-two"), SizeBytes: 10},
	}
	body := appendSliceHeader(nil, len(want), false)
	for _, record := range want {
		body = appendLegacyV3Record(body, record)
	}

	got, offset, err := readRecords(body, 0, legacyCodecVersionV3)
	require.NoError(t, err)
	require.Equal(t, len(body), offset)
	require.Equal(t, want, got)
	for _, record := range got {
		require.Empty(t, record.FromUID)
		require.Empty(t, record.ClientMsgNo)
		require.Zero(t, record.ServerTimestampMS)
	}
}

func TestTransportClientPreservesChannelApplicationErrors(t *testing.T) {
	sentinels := []error{
		ch.ErrInvalidConfig,
		ch.ErrBackpressured,
		ch.ErrNotLeader,
		ch.ErrNotReady,
		ch.ErrStaleMeta,
		ch.ErrChannelNotFound,
		ch.ErrNotReplica,
		ch.ErrClosed,
		ch.ErrTooManyChannels,
	}
	methods := []struct {
		name string
		call func(context.Context, *TransportClient, ch.NodeID) error
	}{
		{
			name: "pull",
			call: func(ctx context.Context, client *TransportClient, node ch.NodeID) error {
				_, err := client.Pull(ctx, node, channeltransport.PullRequest{ChannelKey: "1:room"})
				return err
			},
		},
		{
			name: "pull_hint",
			call: func(ctx context.Context, client *TransportClient, node ch.NodeID) error {
				return client.PullHint(ctx, node, channeltransport.PullHintRequest{ChannelKey: "1:room"})
			},
		},
		{
			name: "ack",
			call: func(ctx context.Context, client *TransportClient, node ch.NodeID) error {
				return client.Ack(ctx, node, channeltransport.AckRequest{ChannelKey: "1:room"})
			},
		},
		{
			name: "notify",
			call: func(ctx context.Context, client *TransportClient, node ch.NodeID) error {
				return client.Notify(ctx, node, channeltransport.NotifyRequest{ChannelKey: "1:room"})
			},
		},
	}

	for _, sentinel := range sentinels {
		for _, method := range methods {
			t.Run(method.name+"/"+sentinel.Error(), func(t *testing.T) {
				network := clusternet.NewLocalNetwork()
				server := &rpcErrorServer{err: sentinel}
				RegisterHandlers(network, 2, server)
				client := NewTransportClient(network)

				err := method.call(context.Background(), client, 2)
				require.ErrorIs(t, err, sentinel)
			})
		}
	}
}

func TestTransportClientPreservesForwardApplicationErrors(t *testing.T) {
	sentinels := []error{
		ch.ErrInvalidConfig,
		ch.ErrBackpressured,
		ch.ErrNotLeader,
		ch.ErrNotReady,
		ch.ErrStaleMeta,
		ch.ErrChannelNotFound,
		ch.ErrNotReplica,
		ch.ErrClosed,
		ch.ErrTooManyChannels,
	}

	for _, sentinel := range sentinels {
		t.Run("append/"+sentinel.Error(), func(t *testing.T) {
			network := clusternet.NewLocalNetwork()
			runtime := &fakeRuntime{appendErr: sentinel}
			service, err := NewService(Config{Runtime: runtime})
			require.NoError(t, err)
			RegisterServiceHandlers(network, 2, service)
			client := NewTransportClient(network)

			_, err = client.ForwardAppend(context.Background(), 2, ch.AppendRequest{ChannelID: ch.ChannelID{ID: "room", Type: 1}})
			require.ErrorIs(t, err, sentinel)
		})

		t.Run("append_batch/"+sentinel.Error(), func(t *testing.T) {
			network := clusternet.NewLocalNetwork()
			runtime := &fakeRuntime{appendBatchErr: sentinel}
			service, err := NewService(Config{Runtime: runtime})
			require.NoError(t, err)
			RegisterServiceHandlers(network, 2, service)
			client := NewTransportClient(network)

			_, err = client.ForwardAppendBatch(context.Background(), 2, ch.AppendBatchRequest{ChannelID: ch.ChannelID{ID: "room", Type: 1}})
			require.ErrorIs(t, err, sentinel)
		})
	}
}

func TestTransportClientShardsForwardAppendBatchByChannel(t *testing.T) {
	caller := &recordingShardCaller{response: mustEncodeAppendBatchResponse(t, ch.AppendBatchResult{})}
	client := NewTransportClient(caller)

	_, err := client.ForwardAppendBatch(context.Background(), 2, ch.AppendBatchRequest{ChannelID: ch.ChannelID{ID: "room-a", Type: 2}})
	require.NoError(t, err)
	require.Equal(t, clusternet.RPCChannelAppendBatch, caller.lastServiceID)
	firstShard := caller.lastShardKey

	_, err = client.ForwardAppendBatch(context.Background(), 2, ch.AppendBatchRequest{ChannelID: ch.ChannelID{ID: "room-a", Type: 2}})
	require.NoError(t, err)
	require.Equal(t, firstShard, caller.lastShardKey)

	_, err = client.ForwardAppendBatch(context.Background(), 2, ch.AppendBatchRequest{ChannelID: ch.ChannelID{ID: "room-b", Type: 2}})
	require.NoError(t, err)
	require.NotEqual(t, firstShard, caller.lastShardKey)
	require.Zero(t, caller.unshardedCalls)
}

func TestTransportClientUsesOwnedShardCallerForForwardAppendBatch(t *testing.T) {
	caller := &recordingOwnedShardCaller{
		recordingShardCaller: recordingShardCaller{response: mustEncodeAppendBatchResponse(t, ch.AppendBatchResult{})},
	}
	client := NewTransportClient(caller)

	_, err := client.ForwardAppendBatch(context.Background(), 2, ch.AppendBatchRequest{ChannelID: ch.ChannelID{ID: "room-a", Type: 2}})

	require.NoError(t, err)
	require.Equal(t, 1, caller.ownedShardedCallCount)
	require.Zero(t, caller.shardedCallCount)
	require.Zero(t, caller.unshardedCalls)
	require.Equal(t, clusternet.RPCChannelAppendBatch, caller.lastServiceID)
	require.NotEmpty(t, caller.lastPayload)
}

func TestTransportClientDispatchesPull(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	runtime := &fakeRuntime{pull: channeltransport.PullResponse{ChannelKey: "1:room", LeaderHW: 9}}
	RegisterHandlers(network, 2, runtime)
	client := NewTransportClient(network)
	resp, err := client.Pull(context.Background(), 2, channeltransport.PullRequest{ChannelKey: "1:room"})
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if resp.LeaderHW != 9 || runtime.pullCalls != 1 {
		t.Fatalf("resp=%#v calls=%d, want HW 9 one call", resp, runtime.pullCalls)
	}
}

func TestTransportClientDispatchesPullBatch(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	runtime := &fakeBatchRuntime{pullBatch: channeltransport.PullBatchResponse{Items: []channeltransport.PullBatchItemResult{
		{Response: channeltransport.PullResponse{ChannelKey: "1:room-a", LeaderHW: 9}},
		{Err: ch.ErrStaleMeta},
	}}}
	RegisterHandlers(network, 2, runtime)
	client := NewTransportClient(network)

	resp, err := client.PullBatch(context.Background(), 2, channeltransport.PullBatchRequest{Items: []channeltransport.PullRequest{
		{ChannelKey: "1:room-a"},
		{ChannelKey: "1:room-b"},
	}})

	require.NoError(t, err)
	require.Equal(t, 1, runtime.pullBatchCalls)
	require.Len(t, runtime.lastPullBatch.Items, 2)
	require.Len(t, resp.Items, 2)
	require.Equal(t, uint64(9), resp.Items[0].Response.LeaderHW)
	require.ErrorIs(t, resp.Items[1].Err, ch.ErrStaleMeta)
}

func TestTransportClientDispatchesPullHintBatch(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	runtime := &fakeBatchRuntime{pullHintBatch: channeltransport.PullHintBatchResponse{Items: []channeltransport.PullHintBatchItemResult{
		{},
		{Err: ch.ErrNotReady},
	}}}
	RegisterHandlers(network, 2, runtime)
	client := NewTransportClient(network)

	resp, err := client.PullHintBatch(context.Background(), 2, channeltransport.PullHintBatchRequest{Items: []channeltransport.PullHintRequest{
		{ChannelKey: "1:room-a"},
		{ChannelKey: "1:room-b"},
	}})

	require.NoError(t, err)
	require.Equal(t, 1, runtime.pullHintBatchCalls)
	require.Len(t, runtime.lastPullHintBatch.Items, 2)
	require.Len(t, resp.Items, 2)
	require.NoError(t, resp.Items[0].Err)
	require.ErrorIs(t, resp.Items[1].Err, ch.ErrNotReady)
}

func TestServiceDelegatesAppend(t *testing.T) {
	runtime := &fakeRuntime{}
	svc, err := NewService(Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = svc.Append(context.Background(), ch.AppendRequest{ChannelID: ch.ChannelID{ID: "room", Type: 1}})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if runtime.appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", runtime.appendCalls)
	}
}

func TestServiceAppliesResolvedMetaBeforeLocalAppend(t *testing.T) {
	id := ch.ChannelID{ID: "local-append", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	runtime := &fakeRuntime{appendRequireApply: true, append: ch.AppendResult{MessageSeq: 7}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta})})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	res, err := svc.Append(context.Background(), ch.AppendRequest{ChannelID: id})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if res.MessageSeq != 7 || runtime.applyCalls != 1 || runtime.appendCalls != 1 || runtime.lastApplied.Key != meta.Key {
		t.Fatalf("result=%#v applyCalls=%d appendCalls=%d lastApplied=%#v", res, runtime.applyCalls, runtime.appendCalls, runtime.lastApplied)
	}
}

func TestServiceEnsuresMetaBeforeLocalAppend(t *testing.T) {
	id := ch.ChannelID{ID: "ensure-local-append", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	source := &fakeEnsuringMetaSource{meta: meta}
	runtime := &fakeRuntime{appendRequireApply: true, append: ch.AppendResult{MessageSeq: 12}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	res, err := svc.Append(context.Background(), ch.AppendRequest{ChannelID: id})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if res.MessageSeq != 12 || source.ensureCalls != 1 || source.resolveCalls != 0 || runtime.applyCalls != 1 {
		t.Fatalf("result=%#v ensure=%d resolve=%d apply=%d", res, source.ensureCalls, source.resolveCalls, runtime.applyCalls)
	}
}

func TestServiceAppliesResolvedMetaBeforeLocalAppendBatch(t *testing.T) {
	id := ch.ChannelID{ID: "local-append-batch", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	runtime := &fakeRuntime{appendRequireApply: true, appendBatch: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 7}}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta})})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	res, err := svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("hello")}}})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].MessageSeq != 7 || runtime.applyCalls != 1 || runtime.appendBatchCalls != 1 {
		t.Fatalf("result=%#v applyCalls=%d appendBatchCalls=%d", res, runtime.applyCalls, runtime.appendBatchCalls)
	}
}

func TestServiceObservesAppendBatchStageDurations(t *testing.T) {
	id := ch.ChannelID{ID: "observe-append-batch", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	observer := &appendStageObserver{}
	runtime := &fakeRuntime{appendRequireApply: true, appendBatch: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 8}}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Observer: observer})
	require.NoError(t, err)

	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("hello")}}})
	require.NoError(t, err)

	requireAppendStage(t, observer.events, "meta_resolve", "ok")
	requireAppendStage(t, observer.events, "meta_apply", "ok")
	requireAppendStage(t, observer.events, "runtime_append", "ok")
}

func TestServiceUsesAppendMetaCacheAfterFirstResolve(t *testing.T) {
	id := ch.ChannelID{ID: "cached", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	source := &countingMetaSource{meta: meta}
	runtime := &fakeRuntime{appendRequireApply: true, appendBatch: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 1}}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("hello")}}})
		require.NoError(t, err)
	}
	require.Equal(t, 1, source.ensureCalls)
}

func TestServiceInvalidatesAppendMetaCacheAndRetriesOnce(t *testing.T) {
	id := ch.ChannelID{ID: "stale", Type: 1}
	stale := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	fresh := stale
	fresh.LeaderEpoch = 2
	source := &countingMetaSource{metas: []ch.Meta{stale, fresh}}
	runtime := &staleOnceRuntime{result: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 2}}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("hello")}}})
	require.NoError(t, err)
	require.Equal(t, 2, source.ensureCalls)
	require.Equal(t, 2, runtime.appendCalls)
}

func TestServiceInvalidatesAppendMetaCacheOnRetryableErrors(t *testing.T) {
	for _, retryErr := range []error{ch.ErrNotLeader, ch.ErrNotReady, ch.ErrChannelNotFound, ch.ErrNotReplica} {
		t.Run(retryErr.Error(), func(t *testing.T) {
			id := ch.ChannelID{ID: "retryable", Type: 1}
			stale := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
			fresh := stale
			fresh.LeaderEpoch = 2
			source := &countingMetaSource{metas: []ch.Meta{stale, fresh}}
			runtime := &retryableOnceRuntime{err: retryErr, result: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 3}}}}
			svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
			require.NoError(t, err)

			_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("warm")}}})
			require.NoError(t, err)
			_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("retry")}}})
			require.NoError(t, err)
			require.Equal(t, 2, source.ensureCalls)
			require.Equal(t, 3, runtime.appendCalls)
		})
	}
}

func TestServiceInvalidatesAppendMetaCacheOnTextualRetryableErrors(t *testing.T) {
	id := ch.ChannelID{ID: "textual-retryable", Type: 1}
	stale := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	fresh := stale
	fresh.LeaderEpoch = 2
	source := &countingMetaSource{metas: []ch.Meta{stale, fresh}}
	runtime := &retryableOnceRuntime{err: errors.New(ch.ErrNotReplica.Error()), result: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 3}}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("warm")}}})
	require.NoError(t, err)
	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("retry")}}})
	require.NoError(t, err)
	require.Equal(t, 2, source.ensureCalls)
	require.Equal(t, 3, runtime.appendCalls)
}

func TestServiceInvalidatesAppendMetaCacheAndRetriesWhenWriteFenceClears(t *testing.T) {
	id := ch.ChannelID{ID: "write-fence-clears", Type: 1}
	fenced := ch.Meta{
		Key:         ch.ChannelKeyForID(id),
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1},
		ISR:         []ch.NodeID{1},
		MinISR:      1,
		Status:      ch.StatusActive,
		WriteFence:  ch.WriteFence{Token: "migration-1", Version: 1, Reason: ch.WriteFenceReasonLeaderTransfer},
	}
	clear := fenced
	clear.LeaderEpoch = 2
	clear.WriteFence = ch.WriteFence{}
	source := &countingMetaSource{metas: []ch.Meta{fenced, clear}}
	runtime := &writeFenceRuntime{}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("retry")}}})

	require.NoError(t, err)
	require.Equal(t, 2, source.ensureCalls)
	require.Equal(t, 2, runtime.applyCalls)
	require.Equal(t, 2, runtime.appendCalls)
}

func TestServiceInvalidatesSingleAppendMetaCacheAndRetriesWhenWriteFenceClears(t *testing.T) {
	id := ch.ChannelID{ID: "single-write-fence-clears", Type: 1}
	fenced := ch.Meta{
		Key:         ch.ChannelKeyForID(id),
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1},
		ISR:         []ch.NodeID{1},
		MinISR:      1,
		Status:      ch.StatusActive,
		WriteFence:  ch.WriteFence{Token: "migration-1", Version: 1, Reason: ch.WriteFenceReasonLeaderTransfer},
	}
	clear := fenced
	clear.LeaderEpoch = 2
	clear.WriteFence = ch.WriteFence{}
	source := &countingMetaSource{metas: []ch.Meta{fenced, clear}}
	runtime := &writeFenceRuntime{}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	_, err = svc.Append(context.Background(), ch.AppendRequest{ChannelID: id, Message: ch.Message{Payload: []byte("retry")}})

	require.NoError(t, err)
	require.Equal(t, 2, source.ensureCalls)
	require.Equal(t, 2, runtime.applyCalls)
	require.Equal(t, 2, runtime.singleAppendCalls)
}

func TestServiceReturnsWriteFencedAfterAuthoritativeReloadStaysFenced(t *testing.T) {
	id := ch.ChannelID{ID: "write-fence-stays", Type: 1}
	fenced := ch.Meta{
		Key:         ch.ChannelKeyForID(id),
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1},
		ISR:         []ch.NodeID{1},
		MinISR:      1,
		Status:      ch.StatusActive,
		WriteFence:  ch.WriteFence{Token: "failover-1", Version: 1, Reason: ch.WriteFenceReasonFailover},
	}
	source := &countingMetaSource{metas: []ch.Meta{fenced, fenced}}
	runtime := &writeFenceRuntime{}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("fenced")}}})

	require.ErrorIs(t, err, ch.ErrWriteFenced)
	require.Equal(t, 2, source.ensureCalls)
	require.Equal(t, 2, runtime.applyCalls)
	require.Equal(t, 2, runtime.appendCalls)
}

func TestServiceDoesNotCacheInvalidAppendMeta(t *testing.T) {
	id := ch.ChannelID{ID: "invalid-cache", Type: 1}
	invalid := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, Status: ch.StatusActive}
	valid := invalid
	valid.MinISR = 1
	source := &countingMetaSource{metas: []ch.Meta{invalid, valid}}
	runtime := &validatingRuntime{result: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 4}}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	require.NoError(t, err)

	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("bad")}}})
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	_, err = svc.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("fixed")}}})
	require.NoError(t, err)
	require.Equal(t, 2, source.ensureCalls)
	require.Equal(t, 2, runtime.applyCalls)
	require.Equal(t, 1, runtime.appendCalls)
}

func TestServiceForwardsAppendToResolvedLeader(t *testing.T) {
	id := ch.ChannelID{ID: "forward-append", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
	source := NewStaticMetaSource([]ch.Meta{meta})
	network := clusternet.NewLocalNetwork()
	client := NewTransportClient(network)
	leaderRuntime := &fakeRuntime{appendRequireApply: true, append: ch.AppendResult{MessageSeq: 9}}
	leader, err := NewService(Config{Runtime: leaderRuntime, LocalNode: 2, MetaSource: source, Forward: client})
	if err != nil {
		t.Fatalf("NewService(leader) error = %v", err)
	}
	RegisterServiceHandlers(network, 2, leader)
	followerRuntime := &fakeRuntime{}
	follower, err := NewService(Config{Runtime: followerRuntime, LocalNode: 1, MetaSource: source, Forward: client})
	if err != nil {
		t.Fatalf("NewService(follower) error = %v", err)
	}

	res, err := follower.Append(context.Background(), ch.AppendRequest{ChannelID: id})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if res.MessageSeq != 9 {
		t.Fatalf("Append() MessageSeq = %d, want forwarded result", res.MessageSeq)
	}
	if followerRuntime.applyCalls != 0 || followerRuntime.appendCalls != 0 {
		t.Fatalf("follower runtime apply=%d append=%d, want no local append", followerRuntime.applyCalls, followerRuntime.appendCalls)
	}
	if leaderRuntime.applyCalls != 1 || leaderRuntime.appendCalls != 1 {
		t.Fatalf("leader runtime apply=%d append=%d, want one forwarded append", leaderRuntime.applyCalls, leaderRuntime.appendCalls)
	}
}

func TestServiceEnsuresMetaBeforeForwardedAppend(t *testing.T) {
	id := ch.ChannelID{ID: "ensure-forward-append", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
	source := &fakeEnsuringMetaSource{meta: meta}
	network := clusternet.NewLocalNetwork()
	client := NewTransportClient(network)
	leaderRuntime := &fakeRuntime{appendRequireApply: true, append: ch.AppendResult{MessageSeq: 13}}
	leader, err := NewService(Config{Runtime: leaderRuntime, LocalNode: 2, MetaSource: source, Forward: client})
	if err != nil {
		t.Fatalf("NewService(leader) error = %v", err)
	}
	RegisterServiceHandlers(network, 2, leader)
	followerRuntime := &fakeRuntime{}
	follower, err := NewService(Config{Runtime: followerRuntime, LocalNode: 1, MetaSource: source, Forward: client})
	if err != nil {
		t.Fatalf("NewService(follower) error = %v", err)
	}

	res, err := follower.Append(context.Background(), ch.AppendRequest{ChannelID: id})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if res.MessageSeq != 13 || source.ensureCalls != 2 || source.resolveCalls != 0 {
		t.Fatalf("result=%#v ensure=%d resolve=%d", res, source.ensureCalls, source.resolveCalls)
	}
}

func TestServiceResolveAppendAuthorityUsesAppendEnsurePath(t *testing.T) {
	id := ch.ChannelID{ID: "resolve-authority", Type: 1}
	want := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 7, LeaderEpoch: 9, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
	source := &fakeEnsuringMetaSource{meta: want}
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	got, err := svc.ResolveAppendAuthority(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveAppendAuthority() error = %v", err)
	}
	if source.ensureCalls != 1 || source.resolveCalls != 0 {
		t.Fatalf("ensure=%d resolve=%d, want resolver to use append ensure path", source.ensureCalls, source.resolveCalls)
	}
	if got.ID != id || got.Key != ch.ChannelKeyForID(id) || got.Leader != 2 || got.Epoch != 7 || got.LeaderEpoch != 9 {
		t.Fatalf("authority meta = %#v, want identity/leader/epochs from ensure path", got)
	}
}

func TestServiceResolveAppendAuthorityCreatesMissingRuntimeMeta(t *testing.T) {
	id := ch.ChannelID{ID: "resolve-authority-create", Type: 1}
	reader := &runtimeMetaReaderFake{err: metadb.ErrNotFound}
	source := NewSlotMetaSource(reader, SlotMetaSourceOptions{DefaultReplicas: []ch.NodeID{2, 1}, DefaultMinISR: 1})
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	got, err := svc.ResolveAppendAuthority(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveAppendAuthority() error = %v", err)
	}
	if reader.upserts != 1 {
		t.Fatalf("upserts = %d, want missing metadata to be created", reader.upserts)
	}
	if got.ID != id || got.Key != ch.ChannelKeyForID(id) || got.Leader != 2 || got.Epoch != 1 || got.LeaderEpoch != 1 {
		t.Fatalf("created authority meta = %#v, want deterministic initial authority", got)
	}
}

func TestServiceForwardsAppendBatchToResolvedLeader(t *testing.T) {
	id := ch.ChannelID{ID: "forward-append-batch", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
	source := NewStaticMetaSource([]ch.Meta{meta})
	network := clusternet.NewLocalNetwork()
	client := NewTransportClient(network)
	leaderRuntime := &fakeRuntime{appendRequireApply: true, appendBatch: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 11}}}}
	leader, err := NewService(Config{Runtime: leaderRuntime, LocalNode: 2, MetaSource: source, Forward: client})
	if err != nil {
		t.Fatalf("NewService(leader) error = %v", err)
	}
	RegisterServiceHandlers(network, 2, leader)
	followerRuntime := &fakeRuntime{}
	follower, err := NewService(Config{Runtime: followerRuntime, LocalNode: 1, MetaSource: source, Forward: client})
	if err != nil {
		t.Fatalf("NewService(follower) error = %v", err)
	}

	res, err := follower.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("hello")}}})
	if err != nil {
		t.Fatalf("AppendBatch() error = %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].MessageSeq != 11 {
		t.Fatalf("AppendBatch() result = %#v, want forwarded result", res)
	}
	if followerRuntime.applyCalls != 0 || followerRuntime.appendBatchCalls != 0 {
		t.Fatalf("follower runtime apply=%d appendBatch=%d, want no local append", followerRuntime.applyCalls, followerRuntime.appendBatchCalls)
	}
	if leaderRuntime.applyCalls != 1 || leaderRuntime.appendBatchCalls != 1 {
		t.Fatalf("leader runtime apply=%d appendBatch=%d, want one forwarded append batch", leaderRuntime.applyCalls, leaderRuntime.appendBatchCalls)
	}
}

func TestServiceReadChannelLastVisibleUsesLocalLeaderStore(t *testing.T) {
	id := ch.ChannelID{ID: "read-last-local", Type: 1}
	factory := channelstore.NewMemoryFactory()
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{
		{ID: 10, FromUID: "u1", ClientMsgNo: "client-10", ServerTimestampMS: 700, Payload: []byte("old"), SizeBytes: 3},
		{ID: 11, FromUID: "u2", ClientMsgNo: "client-11", ServerTimestampMS: 900, Payload: []byte("new"), SizeBytes: 3},
	}})
	require.NoError(t, err)
	source := NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}})
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source, Store: factory})
	require.NoError(t, err)

	got, ok, err := svc.ReadChannelLastVisible(context.Background(), id, 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(11), got.MessageID)
	require.Equal(t, uint64(2), got.MessageSeq)
	require.Equal(t, "u2", got.FromUID)
	require.Equal(t, "client-11", got.ClientMsgNo)
	require.Equal(t, int64(900), got.ServerTimestampMS)
	require.Equal(t, []byte("new"), got.Payload)

	_, ok, err = svc.ReadChannelLastVisible(context.Background(), id, 2)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestServiceReadChannelLastVisibleHonorsRetentionThroughSeq(t *testing.T) {
	id := ch.ChannelID{ID: "read-last-retained", Type: 1}
	factory := channelstore.NewMemoryFactory()
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{
		{ID: 10, Payload: []byte("old"), SizeBytes: 3},
		{ID: 11, Payload: []byte("retained"), SizeBytes: 8},
	}})
	require.NoError(t, err)
	source := NewStaticMetaSource([]ch.Meta{{
		ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1,
		Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1,
		RetentionThroughSeq: 2,
		Status:              ch.StatusActive,
	}})
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source, Store: factory})
	require.NoError(t, err)

	_, ok, err := svc.ReadChannelLastVisible(context.Background(), id, 0)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestServiceReadChannelLastVisibleForwardsToResolvedLeader(t *testing.T) {
	id := ch.ChannelID{ID: "read-last-remote", Type: 1}
	source := NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}})
	forward := &recordingLastVisibleForward{
		message: ch.Message{MessageID: 22, MessageSeq: 9, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("remote")},
		ok:      true,
	}
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source, Forward: forward})
	require.NoError(t, err)

	got, ok, err := svc.ReadChannelLastVisible(context.Background(), id, 7)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(22), got.MessageID)
	require.Equal(t, []byte("remote"), got.Payload)
	require.Equal(t, ch.NodeID(2), forward.lastNode)
	require.Equal(t, id, forward.lastID)
	require.Equal(t, uint64(7), forward.lastVisibleAfterSeq)
}

func TestServiceReadChannelLastVisibleForwardsOverRPCToLeader(t *testing.T) {
	id := ch.ChannelID{ID: "read-last-rpc", Type: 1}
	meta := ch.Meta{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	source := NewStaticMetaSource([]ch.Meta{meta})
	network := clusternet.NewLocalNetwork()
	client := NewTransportClient(network)
	leaderFactory := channelstore.NewMemoryFactory()
	leaderStore, err := leaderFactory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = leaderStore.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{
		{ID: 30, FromUID: "leader", ClientMsgNo: "client-30", ServerTimestampMS: 3000, Payload: []byte("leader-tail"), SizeBytes: 11},
	}})
	require.NoError(t, err)
	leader, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 2, MetaSource: source, Store: leaderFactory, Forward: client})
	require.NoError(t, err)
	RegisterServiceHandlers(network, 2, leader)
	followerFactory := channelstore.NewMemoryFactory()
	followerStore, err := followerFactory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = followerStore.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{
		{ID: 99, Payload: []byte("wrong-node"), SizeBytes: 10},
	}})
	require.NoError(t, err)
	follower, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source, Store: followerFactory, Forward: client})
	require.NoError(t, err)

	got, ok, err := follower.ReadChannelLastVisible(context.Background(), id, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(30), got.MessageID)
	require.Equal(t, []byte("leader-tail"), got.Payload)
}

func TestServiceReadChannelLastVisibleForwardToleratesLaggingLeaderMeta(t *testing.T) {
	id := ch.ChannelID{ID: "read-last-lagging-meta", Type: 1}
	originMeta := ch.Meta{ID: id, Epoch: 3, LeaderEpoch: 5, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	network := clusternet.NewLocalNetwork()
	client := NewTransportClient(network)
	leaderFactory := channelstore.NewMemoryFactory()
	leaderStore, err := leaderFactory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = leaderStore.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{
		{ID: 40, FromUID: "leader", ClientMsgNo: "client-40", ServerTimestampMS: 4000, Payload: []byte("lagging-tail"), SizeBytes: 12},
	}})
	require.NoError(t, err)
	leaderSource := &errMetaSource{err: metadb.ErrNotFound}
	leader, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 2, MetaSource: leaderSource, Store: leaderFactory, Forward: client})
	require.NoError(t, err)
	RegisterServiceHandlers(network, 2, leader)
	origin, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{originMeta}), Store: channelstore.NewMemoryFactory(), Forward: client})
	require.NoError(t, err)

	got, ok, err := origin.ReadChannelLastVisible(context.Background(), id, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(40), got.MessageID)
	require.Equal(t, []byte("lagging-tail"), got.Payload)
	require.Equal(t, 1, leaderSource.calls)
}

func TestServiceObservesForwardAppendBatchSubStages(t *testing.T) {
	id := ch.ChannelID{ID: "forward-append-batch-observed", Type: 1}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
	source := NewStaticMetaSource([]ch.Meta{meta})
	network := clusternet.NewLocalNetwork()
	client := NewTransportClient(network)
	leaderObserver := &appendStageObserver{}
	leaderRuntime := &fakeRuntime{appendRequireApply: true, appendBatch: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: 11}}}}
	leader, err := NewService(Config{Runtime: leaderRuntime, LocalNode: 2, MetaSource: source, Forward: client, Observer: leaderObserver})
	require.NoError(t, err)
	RegisterServiceHandlers(network, 2, leader)
	followerObserver := &appendStageObserver{}
	followerRuntime := &fakeRuntime{}
	follower, err := NewService(Config{Runtime: followerRuntime, LocalNode: 1, MetaSource: source, Forward: client, Observer: followerObserver})
	require.NoError(t, err)

	_, err = follower.AppendBatch(context.Background(), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{Payload: []byte("hello")}}})
	require.NoError(t, err)

	requireAppendStage(t, followerObserver.events, "forward_append", "ok")
	requireAppendStage(t, followerObserver.events, "forward_append_rpc", "ok")
	requireAppendStage(t, leaderObserver.events, "forward_append_remote", "ok")
}

func TestServiceRecoversCommittedForwardAppendBatchAfterDeadline(t *testing.T) {
	id := ch.ChannelID{ID: "recover-forward", Type: 2}
	meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	source := NewStaticMetaSource([]ch.Meta{meta})
	runtime := &fakeRuntime{
		committed: map[uint64]ch.Message{
			10: {MessageID: 10, MessageSeq: 7, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("accepted")},
		},
	}
	forward := &deadlineForwardClient{}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source, Forward: forward})
	require.NoError(t, err)

	res, err := svc.AppendBatch(context.Background(), ch.AppendBatchRequest{
		ChannelID: id,
		Messages: []ch.Message{
			{MessageID: 10, Payload: []byte("accepted")},
			{MessageID: 11, Payload: []byte("missing")},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Items, 2)
	require.NoError(t, res.Items[0].Err)
	require.Equal(t, uint64(10), res.Items[0].MessageID)
	require.Equal(t, uint64(7), res.Items[0].MessageSeq)
	require.ErrorIs(t, res.Items[1].Err, context.DeadlineExceeded)
	require.Equal(t, 2, runtime.lookupCalls)
	require.Equal(t, 1, forward.batchCalls)
}

type clusterOnlyRuntime struct{}

func (clusterOnlyRuntime) ApplyMeta(ch.Meta) error { return nil }
func (clusterOnlyRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, nil
}
func (clusterOnlyRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	return ch.AppendBatchResult{}, nil
}
func (clusterOnlyRuntime) Tick(context.Context) error { return nil }
func (clusterOnlyRuntime) Close() error               { return nil }

type fakeRuntime struct {
	pull               channeltransport.PullResponse
	append             ch.AppendResult
	appendErr          error
	appendBatch        ch.AppendBatchResult
	appendBatchErr     error
	committed          map[uint64]ch.Message
	lookupErr          error
	lastApplied        ch.Meta
	pullCalls          int
	applyCalls         int
	appendCalls        int
	appendBatchCalls   int
	lookupCalls        int
	appendRequireApply bool
}

func (f *fakeRuntime) ApplyMeta(meta ch.Meta) error {
	f.applyCalls++
	f.lastApplied = meta
	return nil
}
func (f *fakeRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	f.appendCalls++
	if f.appendRequireApply && f.applyCalls == 0 {
		return ch.AppendResult{}, ch.ErrChannelNotFound
	}
	if f.appendErr != nil {
		return ch.AppendResult{}, f.appendErr
	}
	return f.append, nil
}
func (f *fakeRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	f.appendBatchCalls++
	if f.appendRequireApply && f.applyCalls == 0 {
		return ch.AppendBatchResult{}, ch.ErrChannelNotFound
	}
	if f.appendBatchErr != nil {
		return ch.AppendBatchResult{}, f.appendBatchErr
	}
	return f.appendBatch, nil
}
func (f *fakeRuntime) Tick(context.Context) error { return nil }
func (f *fakeRuntime) Close() error               { return nil }
func (f *fakeRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	f.pullCalls++
	return f.pull, nil
}
func (f *fakeRuntime) HandleAck(context.Context, channeltransport.AckRequest) error { return nil }
func (f *fakeRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return nil
}
func (f *fakeRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error { return nil }
func (f *fakeRuntime) LookupCommittedMessage(_ context.Context, _ ch.ChannelID, messageID uint64) (ch.Message, bool, error) {
	f.lookupCalls++
	if f.lookupErr != nil {
		return ch.Message{}, false, f.lookupErr
	}
	msg, ok := f.committed[messageID]
	return msg, ok, nil
}

type fakeBatchRuntime struct {
	fakeRuntime
	pullBatch          channeltransport.PullBatchResponse
	pullHintBatch      channeltransport.PullHintBatchResponse
	lastPullBatch      channeltransport.PullBatchRequest
	lastPullHintBatch  channeltransport.PullHintBatchRequest
	pullBatchCalls     int
	pullHintBatchCalls int
}

func (f *fakeBatchRuntime) HandlePullBatch(_ context.Context, req channeltransport.PullBatchRequest) (channeltransport.PullBatchResponse, error) {
	f.pullBatchCalls++
	f.lastPullBatch = req
	return f.pullBatch, nil
}

func (f *fakeBatchRuntime) HandlePullHintBatch(_ context.Context, req channeltransport.PullHintBatchRequest) (channeltransport.PullHintBatchResponse, error) {
	f.pullHintBatchCalls++
	f.lastPullHintBatch = req
	return f.pullHintBatch, nil
}

type deadlineForwardClient struct {
	batchCalls int
}

func (c *deadlineForwardClient) ForwardAppend(context.Context, ch.NodeID, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, context.DeadlineExceeded
}

func (c *deadlineForwardClient) ForwardAppendBatch(context.Context, ch.NodeID, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	c.batchCalls++
	return ch.AppendBatchResult{}, context.DeadlineExceeded
}

func (c *deadlineForwardClient) ForwardLastVisible(context.Context, ch.NodeID, LastVisibleRequest) (LastVisibleResponse, error) {
	return LastVisibleResponse{}, context.DeadlineExceeded
}

type recordingLastVisibleForward struct {
	message             ch.Message
	ok                  bool
	err                 error
	lastNode            ch.NodeID
	lastID              ch.ChannelID
	lastVisibleAfterSeq uint64
}

func (f *recordingLastVisibleForward) ForwardAppend(context.Context, ch.NodeID, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, nil
}

func (f *recordingLastVisibleForward) ForwardAppendBatch(context.Context, ch.NodeID, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	return ch.AppendBatchResult{}, nil
}

func (f *recordingLastVisibleForward) ForwardLastVisible(_ context.Context, node ch.NodeID, req LastVisibleRequest) (LastVisibleResponse, error) {
	f.lastNode = node
	f.lastID = req.ChannelID
	f.lastVisibleAfterSeq = req.VisibleAfterSeq
	return LastVisibleResponse{Message: f.message, Found: f.ok}, f.err
}

type rpcErrorServer struct {
	err error
}

func (s *rpcErrorServer) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	return channeltransport.PullResponse{}, s.err
}

func (s *rpcErrorServer) HandleAck(context.Context, channeltransport.AckRequest) error {
	return s.err
}

func (s *rpcErrorServer) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return s.err
}

func (s *rpcErrorServer) HandleNotify(context.Context, channeltransport.NotifyRequest) error {
	return s.err
}

type appendStageEvent struct {
	stage  string
	result string
}

type appendStageObserver struct {
	events []appendStageEvent
}

func (o *appendStageObserver) SetReactorMailboxDepth(int, string, int) {}
func (o *appendStageObserver) SetWorkerQueueDepth(string, int)         {}
func (o *appendStageObserver) ObserveAppendBatch(int, int, time.Duration) {
}
func (o *appendStageObserver) ObserveAppendLatency(ch.CommitMode, time.Duration) {}
func (o *appendStageObserver) ObserveWorkerResult(worker.TaskKind, error, time.Duration) {
}
func (o *appendStageObserver) ObserveChannelAppendStage(stage string, result string, _ time.Duration) {
	o.events = append(o.events, appendStageEvent{stage: stage, result: result})
}

func requireAppendStage(t *testing.T, events []appendStageEvent, stage string, result string) {
	t.Helper()
	for _, event := range events {
		if event.stage == stage && event.result == result {
			return
		}
	}
	t.Fatalf("append stage %s/%s not observed in %#v", stage, result, events)
}

type countingMetaSource struct {
	meta        ch.Meta
	metas       []ch.Meta
	ensureCalls int
}

func (s *countingMetaSource) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	return s.EnsureChannelMeta(ctx, id)
}

func (s *countingMetaSource) EnsureChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error) {
	s.ensureCalls++
	if len(s.metas) > 0 {
		index := s.ensureCalls - 1
		if index >= len(s.metas) {
			index = len(s.metas) - 1
		}
		return cloneMeta(s.metas[index]), nil
	}
	return cloneMeta(s.meta), nil
}

type staleOnceRuntime struct {
	result      ch.AppendBatchResult
	appendCalls int
}

func (r *staleOnceRuntime) ApplyMeta(ch.Meta) error { return nil }
func (r *staleOnceRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, nil
}
func (r *staleOnceRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	r.appendCalls++
	if r.appendCalls == 1 {
		return ch.AppendBatchResult{}, ch.ErrStaleMeta
	}
	return r.result, nil
}
func (r *staleOnceRuntime) Tick(context.Context) error { return nil }
func (r *staleOnceRuntime) Close() error               { return nil }
func (r *staleOnceRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	return channeltransport.PullResponse{}, nil
}
func (r *staleOnceRuntime) HandleAck(context.Context, channeltransport.AckRequest) error { return nil }
func (r *staleOnceRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error {
	return nil
}
func (r *staleOnceRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return nil
}

type retryableOnceRuntime struct {
	err         error
	result      ch.AppendBatchResult
	appendCalls int
}

func (r *retryableOnceRuntime) ApplyMeta(ch.Meta) error { return nil }
func (r *retryableOnceRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, nil
}
func (r *retryableOnceRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	r.appendCalls++
	if r.appendCalls == 2 {
		return ch.AppendBatchResult{}, r.err
	}
	return r.result, nil
}
func (r *retryableOnceRuntime) Tick(context.Context) error { return nil }
func (r *retryableOnceRuntime) Close() error               { return nil }
func (r *retryableOnceRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	return channeltransport.PullResponse{}, nil
}
func (r *retryableOnceRuntime) HandleAck(context.Context, channeltransport.AckRequest) error {
	return nil
}
func (r *retryableOnceRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error {
	return nil
}
func (r *retryableOnceRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return nil
}

type writeFenceRuntime struct {
	fenced            bool
	applyCalls        int
	appendCalls       int
	singleAppendCalls int
}

func (r *writeFenceRuntime) ApplyMeta(meta ch.Meta) error {
	r.applyCalls++
	r.fenced = meta.WriteFence.Set()
	return nil
}
func (r *writeFenceRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	r.singleAppendCalls++
	if r.fenced {
		return ch.AppendResult{}, ch.ErrWriteFenced
	}
	return ch.AppendResult{MessageSeq: uint64(r.singleAppendCalls)}, nil
}
func (r *writeFenceRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	r.appendCalls++
	if r.fenced {
		return ch.AppendBatchResult{}, ch.ErrWriteFenced
	}
	return ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageSeq: uint64(r.appendCalls)}}}, nil
}
func (r *writeFenceRuntime) Tick(context.Context) error { return nil }
func (r *writeFenceRuntime) Close() error               { return nil }
func (r *writeFenceRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	return channeltransport.PullResponse{}, nil
}
func (r *writeFenceRuntime) HandleAck(context.Context, channeltransport.AckRequest) error {
	return nil
}
func (r *writeFenceRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error {
	return nil
}
func (r *writeFenceRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return nil
}

type validatingRuntime struct {
	result      ch.AppendBatchResult
	applyCalls  int
	appendCalls int
}

func (r *validatingRuntime) ApplyMeta(meta ch.Meta) error {
	r.applyCalls++
	if meta.MinISR <= 0 || meta.MinISR > len(meta.ISR) {
		return ch.ErrInvalidConfig
	}
	return nil
}
func (r *validatingRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, nil
}
func (r *validatingRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	r.appendCalls++
	return r.result, nil
}
func (r *validatingRuntime) Tick(context.Context) error { return nil }
func (r *validatingRuntime) Close() error               { return nil }
func (r *validatingRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	return channeltransport.PullResponse{}, nil
}
func (r *validatingRuntime) HandleAck(context.Context, channeltransport.AckRequest) error {
	return nil
}
func (r *validatingRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error {
	return nil
}
func (r *validatingRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return nil
}

type runtimeMetaReaderFake struct {
	meta    metadb.ChannelRuntimeMeta
	err     error
	upserts int
}

func (f runtimeMetaReaderFake) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	if f.err != nil {
		return metadb.ChannelRuntimeMeta{}, f.err
	}
	return f.meta, nil
}

func (f *runtimeMetaReaderFake) UpsertChannelRuntimeMeta(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.upserts++
	f.err = nil
	f.meta = metadb.NormalizeChannelRuntimeMeta(meta)
	return nil
}

type laggingRuntimeMetaStore struct {
	upserts int
}

func (f *laggingRuntimeMetaStore) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
}

func (f *laggingRuntimeMetaStore) UpsertChannelRuntimeMeta(context.Context, metadb.ChannelRuntimeMeta) error {
	f.upserts++
	return nil
}

type fakeEnsuringMetaSource struct {
	meta         ch.Meta
	ensureCalls  int
	resolveCalls int
}

func (s *fakeEnsuringMetaSource) ResolveChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error) {
	s.resolveCalls++
	return s.meta, nil
}

func (s *fakeEnsuringMetaSource) EnsureChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error) {
	s.ensureCalls++
	return s.meta, nil
}

type errMetaSource struct {
	err   error
	calls int
}

func (s *errMetaSource) ResolveChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error) {
	s.calls++
	return ch.Meta{}, s.err
}

type fakePlacementResolver struct {
	placement ChannelPlacement
	err       error
}

func (r fakePlacementResolver) ResolveChannelPlacement(context.Context, ch.ChannelID) (ChannelPlacement, error) {
	if r.err != nil {
		return ChannelPlacement{}, r.err
	}
	return r.placement, nil
}

type fakePlacementRouter struct {
	route routing.Route
	err   error
}

func (r fakePlacementRouter) RouteKey(string) (routing.Route, error) {
	if r.err != nil {
		return routing.Route{}, r.err
	}
	return r.route, nil
}

type fakeDataNodeProvider struct {
	nodes []uint64
}

func (p fakeDataNodeProvider) DataNodes() []uint64 {
	return append([]uint64(nil), p.nodes...)
}

type recordingShardCaller struct {
	response         []byte
	lastServiceID    uint8
	lastShardKey     uint64
	unshardedCalls   int
	shardedCallCount int
}

func (c *recordingShardCaller) Call(context.Context, uint64, uint8, []byte) ([]byte, error) {
	c.unshardedCalls++
	return nil, errors.New("unsharded call")
}

func (c *recordingShardCaller) CallShard(_ context.Context, _ uint64, serviceID uint8, shardKey uint64, _ []byte) ([]byte, error) {
	c.shardedCallCount++
	c.lastServiceID = serviceID
	c.lastShardKey = shardKey
	return append([]byte(nil), c.response...), nil
}

type recordingOwnedShardCaller struct {
	recordingShardCaller
	ownedShardedCallCount int
	lastPayload           []byte
}

func (c *recordingOwnedShardCaller) CallShardOwned(_ context.Context, _ uint64, serviceID uint8, shardKey uint64, payload transportv2.OwnedBuffer) ([]byte, error) {
	c.ownedShardedCallCount++
	c.lastServiceID = serviceID
	c.lastShardKey = shardKey
	c.lastPayload = append([]byte(nil), payload.Bytes()...)
	payload.Release()
	return append([]byte(nil), c.response...), nil
}

func mustEncodeAppendBatchResponse(t *testing.T, res ch.AppendBatchResult) []byte {
	t.Helper()
	data, err := encodeAppendBatchResponse(res)
	require.NoError(t, err)
	return data
}

func appendLegacyV3Frame(kind uint8, body []byte) []byte {
	data := []byte{legacyCodecVersionV3, kind}
	return append(data, body...)
}

func appendLegacyV3Message(dst []byte, msg ch.Message) []byte {
	dst = appendUvarint(dst, msg.MessageID)
	dst = appendUvarint(dst, msg.MessageSeq)
	dst = appendString(dst, msg.ChannelID)
	dst = append(dst, msg.ChannelType)
	dst = appendString(dst, msg.FromUID)
	dst = appendString(dst, msg.ClientMsgNo)
	dst = appendString(dst, msg.TraceID)
	dst = appendChannelKey(dst, ch.ChannelKey(msg.ChannelKey))
	dst = appendOptionalBytes(dst, msg.Payload)
	return dst
}

func appendLegacyV3PullResponse(dst []byte, resp channeltransport.PullResponse, records []ch.Record) []byte {
	dst = appendChannelKey(dst, resp.ChannelKey)
	dst = appendUvarint(dst, resp.Epoch)
	dst = appendUvarint(dst, resp.LeaderEpoch)
	dst = appendUvarint(dst, resp.LeaderHW)
	dst = appendUvarint(dst, resp.LeaderLEO)
	dst = appendUvarint(dst, resp.ActivityVersion)
	dst = appendVarint(dst, int64(resp.NextPullAfter))
	dst = append(dst, byte(resp.Control))
	dst = appendMetaPtr(dst, resp.Meta)
	dst = appendSliceHeader(dst, len(records), records == nil)
	for _, record := range records {
		dst = appendLegacyV3Record(dst, record)
	}
	return dst
}

func appendLegacyV3Record(dst []byte, record ch.Record) []byte {
	dst = appendUvarint(dst, record.ID)
	dst = appendUvarint(dst, record.Index)
	dst = appendUvarint(dst, record.Epoch)
	dst = appendOptionalBytes(dst, record.Payload)
	dst = appendVarint(dst, int64(record.SizeBytes))
	return dst
}

func nodeIDIn(nodes []ch.NodeID, node ch.NodeID) bool {
	for _, item := range nodes {
		if item == node {
			return true
		}
	}
	return false
}

func equalNodeIDs(a, b []ch.NodeID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
