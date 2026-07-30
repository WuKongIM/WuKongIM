package cluster

import (
	"context"
	"errors"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
)

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
