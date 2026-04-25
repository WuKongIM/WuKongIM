package cluster

import (
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
