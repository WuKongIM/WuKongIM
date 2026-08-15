package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestDataNodeViewWaitsForExactControlRevision(t *testing.T) {
	var view dataNodeView
	view.UpdateAtRevision(1, []uint64{1, 2, 3})

	type result struct {
		nodes []uint64
		err   error
	}
	done := make(chan result, 1)
	go func() {
		nodes, err := view.PlacementDataNodes(context.Background(), 2)
		done <- result{nodes: nodes, err: err}
	}()

	select {
	case got := <-done:
		t.Fatalf("PlacementDataNodes() returned before revision 2 was published: %#v", got)
	case <-time.After(20 * time.Millisecond):
	}
	view.UpdateAtRevision(2, []uint64{2, 3, 4})

	select {
	case got := <-done:
		if got.err != nil || !equalUint64s(got.nodes, []uint64{2, 3, 4}) {
			t.Fatalf("PlacementDataNodes() = nodes %v error %v, want revision-2 nodes", got.nodes, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("PlacementDataNodes() did not wake after revision 2 was published")
	}
}

func TestDataNodeViewRejectsRouteFromOlderControlRevision(t *testing.T) {
	var view dataNodeView
	view.UpdateAtRevision(3, []uint64{1, 2, 3})

	_, err := view.PlacementDataNodes(context.Background(), 2)
	if !errors.Is(err, ch.ErrStaleMeta) {
		t.Fatalf("PlacementDataNodes() error = %v, want ErrStaleMeta", err)
	}
}
