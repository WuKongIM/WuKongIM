//go:build integration

package channels

import (
	"context"
	"runtime"
	"strconv"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// TestAppendRouteRetainedHeap measures storage owned by the Channel service,
// without a growing source, runtime, or caller-owned result set.
func TestAppendRouteRetainedHeap(t *testing.T) {
	const channels = 50_000
	const retainedBudget = channels * 300
	ctx := context.Background()
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: uniformRouteSource{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	heap := func() uint64 {
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	baseline := heap()
	for i := range channels {
		id := ch.ChannelID{ID: "retained-route-" + strconv.Itoa(i), Type: 2}
		if route, err := service.ResolveAppendAuthority(ctx, id); err != nil || route.ID != id || route.RouteGeneration != 10 {
			t.Fatalf("resolve %d: generation=%d error=%v", i, route.RouteGeneration, err)
		}
	}
	retained := int64(heap()) - int64(baseline)
	t.Logf("channels=%d retained_heap_bytes=%d budget_bytes=%d", channels, retained, retainedBudget)
	if retained > retainedBudget {
		t.Fatalf("retained heap %d bytes exceeds %d", retained, retainedBudget)
	}
}

type uniformRouteSource struct{}

func (uniformRouteSource) ResolveChannelMeta(_ context.Context, id ch.ChannelID) (ch.Meta, error) {
	return ch.Meta{
		ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, Leader: 1, RouteGeneration: 10,
		Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive,
	}, nil
}
