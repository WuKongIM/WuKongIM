package cluster

import (
	"context"
	"errors"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

func TestNodeChannelRuntimeSnapshotDelegatesToChannels(t *testing.T) {
	runtime := &nodeChannelRuntime{snapshot: channelruntime.RuntimeSnapshot{ActiveTotal: 3}}
	service, err := channels.NewService(channels.Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(service))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	got, err := node.ChannelRuntimeSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ChannelRuntimeSnapshot() error = %v", err)
	}
	if got.NodeID != channelruntime.NodeID(node.cfg.NodeID) || got.ActiveTotal != 3 {
		t.Fatalf("ChannelRuntimeSnapshot() = %#v, want delegated snapshot with node fallback", got)
	}
	if runtime.snapshotCalls != 1 {
		t.Fatalf("snapshot calls = %d, want 1", runtime.snapshotCalls)
	}
}

func TestNodeChannelRuntimeProbeDelegatesToChannels(t *testing.T) {
	channelID := channelruntime.ChannelID{ID: "probe", Type: 1}
	runtime := &nodeChannelRuntime{probe: channelruntime.RuntimeProbeResult{Checked: 1, Missing: []channelruntime.ChannelID{channelID}}}
	service, err := channels.NewService(channels.Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(service))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	got, err := node.ChannelRuntimeProbe(context.Background(), channelruntime.RuntimeSelector{ChannelIDs: []channelruntime.ChannelID{channelID}})
	if err != nil {
		t.Fatalf("ChannelRuntimeProbe() error = %v", err)
	}
	if got.Checked != 1 || len(got.Missing) != 1 || got.Missing[0] != channelID {
		t.Fatalf("ChannelRuntimeProbe() = %#v, want delegated probe result", got)
	}
	if runtime.probeCalls != 1 || len(runtime.lastProbe.ChannelIDs) != 1 || runtime.lastProbe.ChannelIDs[0] != channelID {
		t.Fatalf("probe calls=%d selector=%#v, want selector forwarded", runtime.probeCalls, runtime.lastProbe)
	}
}

func TestNodeChannelRuntimeEvictDelegatesToChannels(t *testing.T) {
	channelID := channelruntime.ChannelID{ID: "evict", Type: 1}
	runtime := &nodeChannelRuntime{evict: channelruntime.RuntimeEvictResult{Requested: 1, Evicted: 1}}
	service, err := channels.NewService(channels.Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	node, err := New(validNodeConfig(t), WithChannels(service))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	got, err := node.ChannelRuntimeEvict(context.Background(), channelruntime.RuntimeSelector{ChannelIDs: []channelruntime.ChannelID{channelID}})
	if err != nil {
		t.Fatalf("ChannelRuntimeEvict() error = %v", err)
	}
	if got.Requested != 1 || got.Evicted != 1 {
		t.Fatalf("ChannelRuntimeEvict() = %#v, want delegated evict result", got)
	}
	if runtime.evictCalls != 1 || len(runtime.lastEvict.ChannelIDs) != 1 || runtime.lastEvict.ChannelIDs[0] != channelID {
		t.Fatalf("evict calls=%d selector=%#v, want selector forwarded", runtime.evictCalls, runtime.lastEvict)
	}
}

func TestNodeChannelRuntimeRequiresStartedChannels(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.started.Store(true)

	if _, err := node.ChannelRuntimeSnapshot(context.Background()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ChannelRuntimeSnapshot() error = %v, want ErrNotStarted", err)
	}
	if _, err := node.ChannelRuntimeProbe(context.Background(), channelruntime.RuntimeSelector{}); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ChannelRuntimeProbe() error = %v, want ErrNotStarted", err)
	}
	if _, err := node.ChannelRuntimeEvict(context.Background(), channelruntime.RuntimeSelector{}); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ChannelRuntimeEvict() error = %v, want ErrNotStarted", err)
	}
}
