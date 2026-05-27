package clusterv2

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
)

func TestNodeDefaultChannelsUseDurableMessageDBStore(t *testing.T) {
	cfg := validNodeConfig(t)
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	channelID := channelv2.ChannelID{ID: "durable", Type: 1}
	applyDefaultChannelMeta(t, node, channelID)
	if _, err := node.AppendChannel(context.Background(), channelv2.AppendRequest{
		ChannelID: channelID,
		Message:   channelv2.Message{MessageID: 100, Payload: []byte("persisted")},
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
	applyDefaultChannelMeta(t, node, channelID)
	second, err := node.AppendChannel(context.Background(), channelv2.AppendRequest{
		ChannelID: channelID,
		Message:   channelv2.Message{MessageID: 101, Payload: []byte("after-restart")},
	})
	if err != nil {
		t.Fatalf("restart AppendChannel() error = %v", err)
	}
	if second.MessageSeq != 2 {
		t.Fatalf("restart AppendChannel() MessageSeq = %d, want 2 from durable message DB LEO", second.MessageSeq)
	}
}

func TestNodeWithChannelsOptionOverridesDefault(t *testing.T) {
	channelID := channelv2.ChannelID{ID: "room", Type: 1}
	svc, err := channels.NewService(channels.Config{
		LocalNode: 1,
		Store:     channelstore.NewMemoryFactory(),
		MetaSource: channels.NewStaticMetaSource([]channelv2.Meta{{
			ID:          channelID,
			Epoch:       1,
			LeaderEpoch: 1,
			Leader:      1,
			Replicas:    []channelv2.NodeID{1},
			ISR:         []channelv2.NodeID{1},
			MinISR:      1,
			Status:      channelv2.StatusActive,
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

	res, err := node.AppendChannel(context.Background(), channelv2.AppendRequest{
		ChannelID:            channelID,
		CommitMode:           channelv2.CommitModeLocal,
		ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch:  1,
		Message:              channelv2.Message{MessageID: 100, Payload: []byte("hello")},
	})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if res.MessageSeq == 0 {
		t.Fatal("AppendChannel() MessageSeq = 0, want committed sequence")
	}
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
	_, err = node.AppendChannel(context.Background(), channelv2.AppendRequest{ChannelID: channelv2.ChannelID{ID: "room", Type: 1}})
	if err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}
	if runtime.appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", runtime.appendCalls)
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

func applyDefaultChannelMeta(t *testing.T, node *Node, channelID channelv2.ChannelID) {
	t.Helper()
	svc, ok := node.channels.(*channels.Service)
	if !ok {
		t.Fatalf("default channels type = %T, want *channels.Service", node.channels)
	}
	if err := svc.ApplyMeta(channelv2.Meta{
		Key:         channelv2.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channelv2.NodeID{1},
		ISR:         []channelv2.NodeID{1},
		MinISR:      1,
		Status:      channelv2.StatusActive,
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
}

type nodeChannelRuntime struct {
	appendCalls int
	closeCalls  int
}

func (r *nodeChannelRuntime) ApplyMeta(channelv2.Meta) error { return nil }
func (r *nodeChannelRuntime) Append(context.Context, channelv2.AppendRequest) (channelv2.AppendResult, error) {
	r.appendCalls++
	return channelv2.AppendResult{}, nil
}
func (r *nodeChannelRuntime) AppendBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	return channelv2.AppendBatchResult{}, nil
}
func (r *nodeChannelRuntime) Tick(context.Context) error { return nil }
func (r *nodeChannelRuntime) Close() error {
	r.closeCalls++
	return nil
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
