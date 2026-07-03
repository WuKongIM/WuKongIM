package cluster

import (
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestBuildRuntimeViewIncludesObservedConfigEpoch(t *testing.T) {
	now := time.Unix(1710017000, 0)

	view := buildRuntimeView(
		now,
		multiraft.SlotID(1),
		multiraft.Status{LeaderID: multiraft.NodeID(2)},
		[]multiraft.NodeID{1, 2, 3},
		7,
	)

	if got, want := view.ObservedConfigEpoch, uint64(7); got != want {
		t.Fatalf("buildRuntimeView() ObservedConfigEpoch = %d, want %d", got, want)
	}
}

func TestBuildRuntimeViewIncludesCurrentVoters(t *testing.T) {
	view := buildRuntimeView(
		time.Unix(1710017001, 0),
		multiraft.SlotID(7),
		multiraft.Status{
			LeaderID:      multiraft.NodeID(2),
			CurrentVoters: []multiraft.NodeID{1, 2, 3},
		},
		[]multiraft.NodeID{1, 2, 3, 4},
		5,
	)

	if got, want := view.CurrentVoters, []uint64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buildRuntimeView() CurrentVoters = %v, want %v", got, want)
	}
	if got, want := view.CurrentPeers, []uint64{1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buildRuntimeView() CurrentPeers = %v, want %v", got, want)
	}
}

func TestBuildRuntimeViewHealthyVotersUsesCurrentVotersWhenKnown(t *testing.T) {
	view := buildRuntimeView(
		time.Unix(1710017002, 0),
		multiraft.SlotID(8),
		multiraft.Status{
			LeaderID:      multiraft.NodeID(2),
			CurrentVoters: []multiraft.NodeID{1, 2, 3},
		},
		[]multiraft.NodeID{1, 2, 3, 4},
		6,
	)

	if got, want := view.HealthyVoters, uint32(3); got != want {
		t.Fatalf("buildRuntimeView() HealthyVoters = %d, want %d", got, want)
	}
}
