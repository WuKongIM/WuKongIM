package channels

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

func TestSlotPlacementBatchUsesOneRevisionAndPreservesInputAlignment(t *testing.T) {
	resolver := NewSlotPlacementResolver(nil, fakeDataNodeProvider{
		revision: 17,
		nodes:    []uint64{4, 2, 3, 1, 2},
	}, 4)
	ids := []ch.ChannelID{{ID: "first", Type: 1}, {ID: "second", Type: 2}}
	routes := []routing.Route{
		{Revision: 17, PreferredLeader: 4},
		{Revision: 17, PreferredLeader: 2},
	}

	placements, err := resolver.ResolveChannelPlacementBatch(context.Background(), ids, routes)
	if err != nil {
		t.Fatalf("ResolveChannelPlacementBatch() error = %v", err)
	}
	if len(placements) != len(ids) {
		t.Fatalf("placements len = %d, want %d", len(placements), len(ids))
	}
	for i, wantLeader := range []ch.NodeID{4, 2} {
		if placements[i].Leader != wantLeader || placements[i].MinISR != 3 || len(placements[i].Replicas) != 4 {
			t.Fatalf("placement[%d] = %#v, want leader=%d four replicas MinISR=3", i, placements[i], wantLeader)
		}
	}
	if placements[0].Replicas == nil || &placements[0].Replicas[0] == &placements[1].Replicas[0] {
		t.Fatal("aligned placements must own independent replica slices")
	}
}

func TestSlotPlacementBatchRejectsUnalignedOrMixedRevisionEvidence(t *testing.T) {
	resolver := NewSlotPlacementResolver(nil, fakeDataNodeProvider{revision: 17, nodes: []uint64{1}}, 1)
	id := ch.ChannelID{ID: "room", Type: 1}

	if _, err := resolver.ResolveChannelPlacementBatch(context.Background(), []ch.ChannelID{id}, nil); !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("unaligned batch error = %v, want ErrInvalidConfig", err)
	}
	if _, err := resolver.ResolveChannelPlacementBatch(context.Background(), []ch.ChannelID{id, id}, []routing.Route{{Revision: 17}, {Revision: 18}}); !errors.Is(err, ch.ErrStaleMeta) {
		t.Fatalf("mixed revision batch error = %v, want ErrStaleMeta", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.ResolveChannelPlacementBatch(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled batch error = %v, want context.Canceled", err)
	}

	empty, err := resolver.ResolveChannelPlacementBatch(context.Background(), nil, nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty batch = %#v, %v; want non-nil empty result", empty, err)
	}
}
