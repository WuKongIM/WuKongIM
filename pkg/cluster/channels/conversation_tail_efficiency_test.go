package channels

import (
	"context"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

// Exercise the stored-head call site: ordinary previews must not materialize
// older payloads, while a long internal suffix must retain bounded continuation.
func TestStoredConversationHeadReadsOnlyNeededTail(t *testing.T) {
	for _, internalSuffix := range []int{0, 130} {
		t.Run(fmt.Sprintf("internal_suffix_%d", internalSuffix), func(t *testing.T) {
			ctx := context.Background()
			id := ch.ChannelID{ID: "tail-efficiency", Type: 2}
			base := channelstore.NewMemoryFactory()
			store, err := base.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			records := make([]ch.Record, 200+internalSuffix)
			for i := range records {
				records[i] = ch.Record{ID: uint64(i + 1), FromUID: "sender", Payload: make([]byte, 256), SyncOnce: i >= 200}
			}
			_, err = store.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: records})
			require.NoError(t, err)
			require.NoError(t, store.Close())
			tracked := newLastVisibleTrackingFactory(base)
			svc := &Service{store: tracked}
			head, _, err := svc.readStoredConversationHead(ctx, id, "reader", 0, 2, 0, false, true)
			require.NoError(t, err)
			require.True(t, head.Found)
			require.Equal(t, uint64(200), head.Message.MessageSeq)
			if internalSuffix == 0 {
				require.Equal(t, 1, tracked.readRecords, "a preview must not decode 63 unused older messages")
			} else {
				require.LessOrEqual(t, tracked.readRecords, internalSuffix+64, "internal suffix scan must stay batched")
			}
			require.Equal(t, int64(1), tracked.closed.Load())
		})
	}
}
