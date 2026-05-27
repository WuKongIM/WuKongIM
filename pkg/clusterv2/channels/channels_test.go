package channels

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 2,
		LeaderEpoch:  3,
		Leader:       2,
		Replicas:     []uint64{3, 1, 2},
		ISR:          []uint64{2, 1},
		MinISR:       2,
		LeaseUntilMS: leaseUntil.UnixMilli(),
		Status:       uint8(ch.StatusActive),
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

func TestSlotPlacementResolverUsesSlotRouteLeaderAndPeers(t *testing.T) {
	id := ch.ChannelID{ID: "route-placement", Type: 1}
	resolver := NewSlotPlacementResolver(fakePlacementRouter{route: routing.Route{Leader: 2, Peers: []uint64{2, 1, 3}}}, 2)

	placement, err := resolver.ResolveChannelPlacement(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelPlacement() error = %v", err)
	}
	if placement.Leader != 2 || placement.MinISR != 2 {
		t.Fatalf("placement = %#v, want route leader and min ISR", placement)
	}
	if got, want := placement.Replicas, []ch.NodeID{2, 1, 3}; !equalNodeIDs(got, want) {
		t.Fatalf("Replicas = %v, want %v", got, want)
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
	req := channeltransport.PullRequest{ChannelKey: "1:room", ChannelID: ch.ChannelID{ID: "room", Type: 1}, Epoch: 1, LeaderEpoch: 2, Follower: 3, NextOffset: 4, MaxBytes: 1024}
	data, err := EncodePullRequest(req)
	if err != nil {
		t.Fatalf("EncodePullRequest() error = %v", err)
	}
	got, err := DecodePullRequest(data)
	if err != nil {
		t.Fatalf("DecodePullRequest() error = %v", err)
	}
	if got.ChannelKey != req.ChannelKey || got.NextOffset != req.NextOffset {
		t.Fatalf("decoded = %#v, want %#v", got, req)
	}
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
	appendBatch        ch.AppendBatchResult
	lastApplied        ch.Meta
	pullCalls          int
	applyCalls         int
	appendCalls        int
	appendBatchCalls   int
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
	return f.append, nil
}
func (f *fakeRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	f.appendBatchCalls++
	if f.appendRequireApply && f.applyCalls == 0 {
		return ch.AppendBatchResult{}, ch.ErrChannelNotFound
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
