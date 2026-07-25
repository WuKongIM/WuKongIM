package backup

import (
	"fmt"
	"testing"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestMessageBoundaryViewUsesAllocationFreeLookupAtLargeScale(t *testing.T) {
	const channelCount = 100_000
	baseline := make([]backupartifact.ChannelBoundary, channelCount)
	for index := range baseline {
		baseline[index] = backupartifact.ChannelBoundary{
			ChannelID:   fmt.Sprintf("channel-%06d", index),
			ChannelType: 2,
			Epoch:       1,
			HW:          uint64(index + 1),
		}
	}
	identity := messageSourceIdentity{
		channelID: "channel-099999", channelType: 2,
	}
	view := messageBoundaryView{
		baseline: baseline,
		updates: []backupartifact.ChannelBoundary{{
			ChannelID: identity.channelID, ChannelType: identity.channelType,
			Epoch: 2, HW: channelCount + 1,
		}},
	}
	if boundary := view.lookup(identity); boundary.Epoch != 2 {
		t.Fatalf("lookup() = %#v, want delta override", boundary)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		if boundary := view.lookup(identity); boundary.Epoch != 2 {
			t.Fatalf("lookup() = %#v, want delta override", boundary)
		}
	})
	if allocations != 0 {
		t.Fatalf("lookup allocations = %f, want 0", allocations)
	}
}
