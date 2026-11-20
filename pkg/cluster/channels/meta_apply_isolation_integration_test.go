//go:build integration

package channels_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelservice "github.com/WuKongIM/WuKongIM/pkg/channel/service"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// TestMetaApplyIndependentChannels uses a real single-node cluster Channel
// runtime. Delaying one completed call must not prevent another Channel from
// installing metadata; the same Channel must still yield without waiting.
func TestMetaApplyIndependentChannels(t *testing.T) {
	actual, err := channelservice.New(channelservice.Config{
		LocalNode: 1, ReactorCount: 1, MaxChannels: 8, Store: store.NewMemoryFactory(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := actual.Close(); err != nil {
			t.Errorf("close real Channel runtime: %v", err)
		}
	})
	// These are distinct regression identities, not a test of any hash function.
	slowID := ch.ChannelID{ID: "meta-isolation-5", Type: 2}
	otherID := ch.ChannelID{ID: "meta-isolation-70", Type: 2}
	adapter := &heldMetaRuntime{
		Cluster: actual, Server: actual.(transport.Server), held: slowID,
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	svc, err := channels.NewService(channels.Config{LocalNode: 1, Runtime: adapter})
	if err != nil {
		t.Fatal(err)
	}
	meta := func(id ch.ChannelID) ch.Meta {
		return ch.Meta{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1,
			Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	}
	done := make(chan error, 1)
	go func() { done <- svc.ApplyMeta(meta(slowID)) }()
	t.Cleanup(func() {
		close(adapter.release)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("held metadata call: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("held metadata call did not join")
		}
	})
	select {
	case <-adapter.entered:
	case err := <-done:
		done <- err
		t.Fatalf("real metadata application did not reach the controlled return: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("real metadata application did not enter within the fixture guard")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.ApplyMetaContext(canceled, meta(otherID)); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled metadata application: %v", err)
	}
	if err := svc.ApplyMetaContext(context.Background(), meta(slowID)); !errors.Is(err, ch.ErrNotReady) {
		t.Errorf("same Channel must yield while its application is active: %v", err)
	}
	if err := svc.ApplyMetaContext(context.Background(), meta(otherID)); err != nil {
		t.Errorf("unrelated Channel metadata was blocked by the held Channel: %v", err)
	}
}

// heldMetaRuntime delegates all work to the real runtime, then holds exactly
// one successful ApplyMeta return. Its guard invalidates an abandoned fixture.
type heldMetaRuntime struct {
	ch.Cluster
	transport.Server
	held    ch.ChannelID
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (r *heldMetaRuntime) ApplyMeta(meta ch.Meta) error {
	if err := r.Cluster.ApplyMeta(meta); err != nil {
		return err
	}
	var heldErr error
	if meta.ID == r.held {
		r.once.Do(func() {
			close(r.entered)
			select {
			case <-r.release:
			case <-time.After(2 * time.Second):
				heldErr = errors.New("controlled metadata return exceeded fixture guard")
			}
		})
	}
	return heldErr
}

func (r *heldMetaRuntime) ApplyMetaContext(ctx context.Context, meta ch.Meta) error {
	return r.Cluster.(interface {
		ApplyMetaContext(context.Context, ch.Meta) error
	}).ApplyMetaContext(ctx, meta)
}
