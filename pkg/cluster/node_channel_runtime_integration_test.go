//go:build integration

package cluster

import (
	"context"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

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
