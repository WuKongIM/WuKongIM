package store

import (
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

// A writer fenced by the replica's authority promise must surface as stale
// metadata, which the replication exchange reports as ReplicateStaleFence.
func TestMessageDBAdapterMapsStaleAuthorityToStaleMeta(t *testing.T) {
	if err := mapMessageDBAdapterError(channel.ErrStaleAuthority); !errors.Is(err, ch.ErrStaleMeta) {
		t.Fatalf("mapped error = %v, want %v", err, ch.ErrStaleMeta)
	}
}
