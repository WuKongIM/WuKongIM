//go:build integration

package channels

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// BenchmarkConversationPreviewStorage measures the real persisted-head storage
// path across 600 Channels, matching the release fixture's Channel count and
// payload size. Deeper histories and internal suffixes check scaling. Routing,
// network, JSON and edit hydration remain covered by the process-level gate.
func BenchmarkConversationPreviewStorage(b *testing.B) {
	for _, shape := range []struct {
		name             string
		ordinary, suffix int
	}{
		{"release", 3, 0}, {"history", 128, 0}, {"internal_suffix", 3, 65},
	} {
		b.Run(shape.name, func(b *testing.B) {
			ctx := context.Background()
			factory := channelstore.NewMessageDBFactory(b.TempDir())
			defer factory.Close()
			ids := make([]ch.ChannelID, 600)
			payload := bytes.Repeat([]byte("p"), 256)
			through := uint64(1 + shape.ordinary + shape.suffix)
			for i := range ids {
				ids[i] = ch.ChannelID{ID: fmt.Sprintf("cohort-%d-channel-%03d", i/200, i%200), Type: 2}
				store, err := factory.ChannelStore(ch.ChannelKeyForID(ids[i]), ids[i])
				if err != nil {
					b.Fatal(err)
				}
				records := make([]ch.Record, through)
				for j := range records {
					internal := j == 0 || j > shape.ordinary
					records[j] = ch.Record{ID: uint64(i)*1000 + uint64(j) + 1, FromUID: "sender", ClientMsgNo: fmt.Sprintf("fixture-%d-%d", i, j), SyncOnce: internal, Payload: payload, SizeBytes: len(payload)}
				}
				if _, err := store.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: records}); err != nil {
					b.Fatal(err)
				}
				if err := store.StoreCheckpoint(ctx, ch.Checkpoint{HW: through}); err != nil {
					b.Fatal(err)
				}
				if err := store.Close(); err != nil {
					b.Fatal(err)
				}
			}
			svc := &Service{store: factory}
			for _, stage := range []string{"head", "lease", "frontier", "retention", "sender", "rank", "tail"} {
				b.Run(stage, func(b *testing.B) {
					b.ReportAllocs()
					for n := 0; n < b.N; n++ {
						id := ids[(n*137)%len(ids)]
						if stage == "head" {
							head, activate, err := svc.readStoredConversationHead(ctx, id, "reader", 0, 2, 0, false, true)
							if err != nil || activate || !head.Found || head.ReadThroughSeq != through || head.Message.MessageSeq != uint64(shape.ordinary+1) || head.NonBusinessUnread != uint64(shape.suffix+1) {
								b.Fatalf("head=%+v activate=%v err=%v", head, activate, err)
							}
							continue
						}
						store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
						if err != nil {
							b.Fatal(err)
						}
						switch stage {
						case "frontier":
							_, err = store.Load(ctx)
						case "retention":
							_, err = store.LoadRetentionState(ctx)
						case "sender":
							_, _, err = store.(channelstore.SenderSequenceLookup).GetLastSenderMessageSeq(ctx, "reader", through)
						case "rank":
							_, err = store.(channelstore.OrdinaryMessageCounter).CountOrdinaryMessages(ctx, 0, through)
						case "tail":
							_, _, err = readLastOrdinaryThrough(ctx, store, through, 0, 1<<20)
						}
						closeErr := store.Close()
						if err != nil {
							b.Fatal(err)
						}
						if closeErr != nil {
							b.Fatal(closeErr)
						}
					}
				})
			}
		})
	}
}
