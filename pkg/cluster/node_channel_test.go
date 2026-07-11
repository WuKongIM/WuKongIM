package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNodeDefaultChannelsUseDurableMessageDBStore(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.HealthReport.Interval = 500 * time.Millisecond
	cfg.HealthReport.TTL = 2 * time.Second
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := WaitNodeReady(readyCtx, node); err != nil {
		readyCancel()
		t.Fatalf("WaitNodeReady() error = %v", err)
	}
	readyCancel()
	waitChannelDataNode(t, node, 1)
	channelID := channelruntime.ChannelID{ID: "durable", Type: 1}
	applyDefaultChannelMeta(t, node, channelID)
	if _, err := node.AppendChannel(context.Background(), channelruntime.AppendRequest{
		ChannelID: channelID,
		Message:   channelruntime.Message{MessageID: 100, Payload: []byte("persisted")},
	}); err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("restart Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	restartReadyCtx, restartReadyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := WaitNodeReady(restartReadyCtx, node); err != nil {
		restartReadyCancel()
		t.Fatalf("restart WaitNodeReady() error = %v", err)
	}
	restartReadyCancel()
	waitChannelDataNode(t, node, 1)
	applyDefaultChannelMeta(t, node, channelID)
	second, err := node.AppendChannel(context.Background(), channelruntime.AppendRequest{
		ChannelID: channelID,
		Message:   channelruntime.Message{MessageID: 101, Payload: []byte("after-restart")},
	})
	if err != nil {
		t.Fatalf("restart AppendChannel() error = %v", err)
	}
	if second.MessageSeq != 2 {
		t.Fatalf("restart AppendChannel() MessageSeq = %d, want 2 from durable message DB LEO", second.MessageSeq)
	}
}

func waitChannelDataNode(t *testing.T, node *Node, nodeID uint64) {
	t.Helper()
	waitUntil(t, func() bool {
		for _, candidate := range node.channelDataNodes.DataNodes() {
			if candidate == nodeID {
				return true
			}
		}
		return false
	})
}

func TestNodeReadChannelCommittedHonorsRetentionThroughSeq(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	waitChannelDataNode(t, node, 1)

	id := channelruntime.ChannelID{ID: "retained-read", Type: 1}
	route := waitRouteKeyLeaderReady(t, node, id.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 4; i++ {
		_, err := node.AppendChannel(ctx, channelruntime.AppendRequest{
			ChannelID: id,
			Message:   channelruntime.Message{MessageID: uint64(100 + i), Payload: []byte{byte('a' + i)}},
		})
		if err != nil {
			t.Fatalf("AppendChannel(%d) error = %v", i, err)
		}
	}
	meta, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if err := node.AdvanceChannelRetentionThroughSeq(ctx, metadb.ChannelRetentionAdvance{
		ChannelID:            id.ID,
		ChannelType:          int64(id.Type),
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedLeaseUntilMS: meta.LeaseUntilMS,
		RetentionThroughSeq:  2,
		RetentionUpdatedAtMS: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("AdvanceChannelRetentionThroughSeq() error = %v", err)
	}
	tracking := newNodeTrackingStoreFactory(node.defaultChannelStore)
	node.channelStoreFactory = tracking

	forward, err := node.ReadChannelCommitted(ctx, id, channelstore.ReadCommittedRequest{FromSeq: 1, MaxSeq: 4, Limit: 10, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("ReadChannelCommitted(forward) error = %v", err)
	}
	if got := nodeMessageSeqs(forward.Messages); !equalNodeMessageSeqs(got, []uint64{3, 4}) {
		t.Fatalf("forward seqs = %v, want [3 4]", got)
	}
	reverse, err := node.ReadChannelCommitted(ctx, id, channelstore.ReadCommittedRequest{FromSeq: 4, MaxSeq: 4, Limit: 10, MaxBytes: 1024, Reverse: true})
	if err != nil {
		t.Fatalf("ReadChannelCommitted(reverse) error = %v", err)
	}
	if got := nodeMessageSeqs(reverse.Messages); !equalNodeMessageSeqs(got, []uint64{4, 3}) {
		t.Fatalf("reverse seqs = %v, want [4 3]", got)
	}
	if got := tracking.Acquired(); got != 2 {
		t.Fatalf("ChannelStore acquisitions = %d, want 2", got)
	}
	if got := tracking.Closed(); got != 2 {
		t.Fatalf("ChannelStore closes = %d, want 2", got)
	}
}

func TestNodeReadChannelCommittedClosesStoreOnPostAcquireEarlyError(t *testing.T) {
	tracking := newNodeTrackingStoreFactory(channelstore.NewMemoryFactory())
	node := &Node{channelStoreFactory: tracking}
	node.started.Store(true)

	_, err := node.ReadChannelCommitted(context.Background(), channelruntime.ChannelID{ID: "missing-meta-db", Type: 1}, channelstore.ReadCommittedRequest{})
	if !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ReadChannelCommitted() error = %v, want %v", err, ErrNotStarted)
	}
	if got := tracking.Acquired(); got != 1 {
		t.Fatalf("ChannelStore acquisitions = %d, want 1", got)
	}
	if got := tracking.Closed(); got != 1 {
		t.Fatalf("ChannelStore closes = %d, want 1", got)
	}
}

func TestNodeWithChannelsOptionOverridesDefault(t *testing.T) {
	channelID := channelruntime.ChannelID{ID: "room", Type: 1}
	svc, err := channels.NewService(channels.Config{
		LocalNode: 1,
		Store:     channelstore.NewMemoryFactory(),
		MetaSource: channels.NewStaticMetaSource([]channelruntime.Meta{{
			ID:          channelID,
			Epoch:       1,
			LeaderEpoch: 1,
			Leader:      1,
			Replicas:    []channelruntime.NodeID{1},
			ISR:         []channelruntime.NodeID{1},
			MinISR:      1,
			Status:      channelruntime.StatusActive,
		}}),
	})
	if err != nil {
		t.Fatalf("channels.NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(svc))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	res, err := node.AppendChannel(context.Background(), channelruntime.AppendRequest{
		ChannelID:            channelID,
		CommitMode:           channelruntime.CommitModeLocal,
		ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch:  1,
		Message:              channelruntime.Message{MessageID: 100, Payload: []byte("hello")},
	})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if res.MessageSeq == 0 {
		t.Fatal("AppendChannel() MessageSeq = 0, want committed sequence")
	}
}

func TestNodeLookupChannelIdempotencyUsesDefaultStore(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	waitChannelDataNode(t, node, 1)

	id := channelruntime.ChannelID{ID: "idempotency-read", Type: 1}
	applyDefaultChannelMeta(t, node, id)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := node.AppendChannel(ctx, channelruntime.AppendRequest{
		ChannelID: id,
		Message: channelruntime.Message{
			MessageID:   501,
			FromUID:     "u1",
			ClientMsgNo: "client-1",
			Payload:     []byte("payload"),
		},
	}); err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	tracking := newNodeTrackingStoreFactory(node.defaultChannelStore)
	node.channelStoreFactory = tracking

	hit, ok, err := node.LookupChannelIdempotency(ctx, id, "u1", "client-1")
	if err != nil {
		t.Fatalf("LookupChannelIdempotency() error = %v", err)
	}
	if !ok {
		t.Fatal("LookupChannelIdempotency() ok = false, want true")
	}
	if hit.Message.MessageID != 501 || hit.Message.MessageSeq != 1 || hit.Message.FromUID != "u1" || hit.Message.ClientMsgNo != "client-1" {
		t.Fatalf("LookupChannelIdempotency() hit = %#v, want committed message", hit)
	}
	if hit.PayloadHash == 0 {
		t.Fatalf("LookupChannelIdempotency() payload hash = 0, want persisted hash")
	}
	if got := tracking.Acquired(); got != 1 {
		t.Fatalf("ChannelStore acquisitions = %d, want 1", got)
	}
	if got := tracking.Closed(); got != 1 {
		t.Fatalf("ChannelStore closes = %d, want 1", got)
	}
}

type nodeTrackingStoreFactory struct {
	base     channelstore.Factory
	acquired atomic.Int64
	closed   atomic.Int64
}

func newNodeTrackingStoreFactory(base channelstore.Factory) *nodeTrackingStoreFactory {
	return &nodeTrackingStoreFactory{base: base}
}

func (f *nodeTrackingStoreFactory) ChannelStore(key channelruntime.ChannelKey, id channelruntime.ChannelID) (channelstore.ChannelStore, error) {
	store, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	f.acquired.Add(1)
	return &nodeTrackingChannelStore{ChannelStore: store, parent: f}, nil
}

func (f *nodeTrackingStoreFactory) Acquired() int64 { return f.acquired.Load() }

func (f *nodeTrackingStoreFactory) Closed() int64 { return f.closed.Load() }

type nodeTrackingChannelStore struct {
	channelstore.ChannelStore
	parent    *nodeTrackingStoreFactory
	closeOnce sync.Once
}

func (s *nodeTrackingChannelStore) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.parent.closed.Add(1)
		err = s.ChannelStore.Close()
	})
	return err
}

func (s *nodeTrackingChannelStore) LookupIdempotency(ctx context.Context, fromUID string, clientMsgNo string) (channelstore.IdempotencyHit, bool, error) {
	lookup, ok := s.ChannelStore.(channelstore.IdempotencyLookup)
	if !ok {
		return channelstore.IdempotencyHit{}, false, channelruntime.ErrInvalidConfig
	}
	return lookup.LookupIdempotency(ctx, fromUID, clientMsgNo)
}

func nodeMessageSeqs(messages []channelruntime.Message) []uint64 {
	out := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.MessageSeq)
	}
	return out
}

func equalNodeMessageSeqs(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestNodeAppendChannelDelegatesToService(t *testing.T) {
	runtime := &nodeChannelRuntime{}
	svc, err := channels.NewService(channels.Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(svc))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	_, err = node.AppendChannel(context.Background(), channelruntime.AppendRequest{ChannelID: channelruntime.ChannelID{ID: "room", Type: 1}})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if runtime.appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", runtime.appendCalls)
	}
}

func TestNodeResolveChannelAppendAuthorityDelegatesToService(t *testing.T) {
	channelID := channelruntime.ChannelID{ID: "resolve-node-authority", Type: 2}
	source := &nodeEnsuringMetaSource{meta: channelruntime.Meta{
		Key:         channelruntime.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       12,
		LeaderEpoch: 4,
		Leader:      2,
		Replicas:    []channelruntime.NodeID{1, 2},
		ISR:         []channelruntime.NodeID{1, 2},
		MinISR:      1,
		Status:      channelruntime.StatusActive,
	}}
	svc, err := channels.NewService(channels.Config{Runtime: &nodeChannelRuntime{}, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(svc))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	got, err := node.ResolveChannelAppendAuthority(context.Background(), channelID)
	if err != nil {
		t.Fatalf("ResolveChannelAppendAuthority() error = %v", err)
	}
	if source.ensureCalls != 1 || source.resolveCalls != 0 {
		t.Fatalf("ensure=%d resolve=%d, want Node facade to delegate to service resolver", source.ensureCalls, source.resolveCalls)
	}
	if got.ID != channelID || got.Key != channelruntime.ChannelKeyForID(channelID) || got.Leader != 2 || got.Epoch != 12 || got.LeaderEpoch != 4 {
		t.Fatalf("resolved meta = %#v, want hosted service authority meta", got)
	}
}

func TestNodeStopClosesChannelService(t *testing.T) {
	runtime := &nodeChannelRuntime{}
	svc, err := channels.NewService(channels.Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(svc))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if runtime.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", runtime.closeCalls)
	}
}

func applyDefaultChannelMeta(t *testing.T, node *Node, channelID channelruntime.ChannelID) {
	t.Helper()
	waitUntil(t, func() bool {
		route, err := node.RouteKey(channelID.ID)
		return err == nil && route.Leader == node.NodeID()
	})
	svc, ok := node.channels.(*channels.Service)
	if !ok {
		t.Fatalf("default channels type = %T, want *channels.Service", node.channels)
	}
	if err := svc.ApplyMeta(channelruntime.Meta{
		Key:         channelruntime.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channelruntime.NodeID{1},
		ISR:         []channelruntime.NodeID{1},
		MinISR:      1,
		Status:      channelruntime.StatusActive,
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
}

type nodeChannelRuntime struct {
	snapshot channelruntime.RuntimeSnapshot
	probe    channelruntime.RuntimeProbeResult
	evict    channelruntime.RuntimeEvictResult

	lastProbe channelruntime.RuntimeSelector
	lastEvict channelruntime.RuntimeSelector

	appendCalls   int
	closeCalls    int
	snapshotCalls int
	probeCalls    int
	evictCalls    int
}

func (r *nodeChannelRuntime) ApplyMeta(channelruntime.Meta) error { return nil }
func (r *nodeChannelRuntime) Append(context.Context, channelruntime.AppendRequest) (channelruntime.AppendResult, error) {
	r.appendCalls++
	return channelruntime.AppendResult{}, nil
}
func (r *nodeChannelRuntime) AppendBatch(context.Context, channelruntime.AppendBatchRequest) (channelruntime.AppendBatchResult, error) {
	return channelruntime.AppendBatchResult{}, nil
}
func (r *nodeChannelRuntime) Tick(context.Context) error { return nil }
func (r *nodeChannelRuntime) Close() error {
	r.closeCalls++
	return nil
}
func (r *nodeChannelRuntime) RuntimeSnapshot(context.Context) (channelruntime.RuntimeSnapshot, error) {
	r.snapshotCalls++
	return r.snapshot, nil
}
func (r *nodeChannelRuntime) RuntimeProbe(_ context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error) {
	r.probeCalls++
	r.lastProbe = selector
	return r.probe, nil
}
func (r *nodeChannelRuntime) RuntimeEvict(_ context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error) {
	r.evictCalls++
	r.lastEvict = selector
	return r.evict, nil
}
func (r *nodeChannelRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	return channeltransport.PullResponse{}, nil
}
func (r *nodeChannelRuntime) HandleAck(context.Context, channeltransport.AckRequest) error {
	return nil
}
func (r *nodeChannelRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return nil
}
func (r *nodeChannelRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error {
	return nil
}

type nodeEnsuringMetaSource struct {
	meta         channelruntime.Meta
	ensureCalls  int
	resolveCalls int
}

func (s *nodeEnsuringMetaSource) ResolveChannelMeta(context.Context, channelruntime.ChannelID) (channelruntime.Meta, error) {
	s.resolveCalls++
	return s.meta, nil
}

func (s *nodeEnsuringMetaSource) EnsureChannelMeta(context.Context, channelruntime.ChannelID) (channelruntime.Meta, error) {
	s.ensureCalls++
	return s.meta, nil
}
