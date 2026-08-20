package message

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
)

func TestReplicaForegroundCommitLaneYieldsToLeaderLocalDurability(t *testing.T) {
	t.Parallel()

	if got := commitRowsPriority(commitLaneLeaderAppend); got != commit.PriorityHigh {
		t.Fatalf("leader append priority = %v, want high", got)
	}
	if got := commitRowsPriority(commitLaneReplicaForeground); got != commit.PriorityNormal {
		t.Fatalf("foreground replica priority = %v, want normal", got)
	}
	if got := commitRowsPriority(commitLaneReplicaTrailing); got != commit.PriorityNormal {
		t.Fatalf("trailing replica priority = %v, want normal", got)
	}
}
