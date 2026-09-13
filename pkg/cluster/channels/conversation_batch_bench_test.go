package channels

import (
	"context"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

// BenchmarkConversationHeadBatchRouting isolates the measured request grouping
// and wire-allocation path; the process-level diagnostic measures full storage.
func BenchmarkConversationHeadBatchRouting(b *testing.B) {
	ids := make([]ch.ChannelID, 100)
	metas := make([]ch.Meta, len(ids))
	for i := range ids {
		ids[i] = ch.ChannelID{ID: fmt.Sprintf("batch-%d", i), Type: 2}
		metas[i] = ch.Meta{ID: ids[i], Epoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	}
	forward := &recordingConversationHeadsForward{response: ConversationHeadsResponse{Items: make([]ConversationHeadResult, len(ids))}}
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: NewStaticMetaSource(metas), Store: channelstore.NewMemoryFactory(), Forward: forward})
	require.NoError(b, err)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := svc.ReadPersistedConversationHeads(ctx, ids, "reader")
		if err != nil {
			b.Fatal(err)
		}
		raw, err := encodeConversationHeadsRequest(forward.request)
		if err != nil {
			b.Fatal(err)
		}
		if _, err = decodeConversationHeadsRequest(raw); err != nil {
			b.Fatal(err)
		}
	}
}
