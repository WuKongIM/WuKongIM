package cluster

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

func TestNodeAuthorityReadsFailClosedWithoutCompletePublication(t *testing.T) {
	node := &Node{
		cfg:                  Config{NodeID: 1},
		router:               routing.NewRouter(),
		routeAuthorityEpochs: make(map[uint16]uint64),
	}
	keys := []string{"first", "second"}

	if _, err := node.RouteAuthorities(keys); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("RouteAuthorities(before Start) error = %v, want ErrNotStarted", err)
	}
	node.started.Store(true)
	if _, err := node.RouteAuthorities(keys); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("RouteAuthorities(without table) error = %v, want ErrRouteNotReady", err)
	}
	if err := node.updateRouteAuthorityTable(func() error {
		return node.router.UpdateControlSnapshot(nodeControlSnapshot())
	}); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	if _, err := node.RouteAuthorities(keys); !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteAuthorities(without observed leader) error = %v, want ErrNoSlotLeader", err)
	}
	partial, err := node.RouteAuthoritiesPartial(keys)
	if err != nil {
		t.Fatalf("RouteAuthoritiesPartial() outer error = %v", err)
	}
	if len(partial) != len(keys) {
		t.Fatalf("RouteAuthoritiesPartial() results = %d, want %d", len(partial), len(keys))
	}
	for index, result := range partial {
		if !errors.Is(result.Err, ErrNoSlotLeader) {
			t.Fatalf("RouteAuthoritiesPartial()[%d] error = %v, want ErrNoSlotLeader", index, result.Err)
		}
	}

	node.maintenance.Store(true)
	if _, err := node.RouteAuthorities(keys); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("RouteAuthorities(during maintenance) error = %v, want ErrMaintenance", err)
	}
	node.stopping.Store(true)
	if _, err := node.RouteAuthorities(keys); !errors.Is(err, ErrStopping) {
		t.Fatalf("RouteAuthorities(during Stop) error = %v, want ErrStopping", err)
	}
}
