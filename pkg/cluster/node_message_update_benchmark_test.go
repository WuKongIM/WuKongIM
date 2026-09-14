package cluster

import (
	"fmt"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"testing"
)

// BenchmarkApplyMessageUpdatePages isolates matching from quorum/network cost.
func BenchmarkApplyMessageUpdatePages(b *testing.B) {
	for _, shape := range []struct {
		name                     string
		channels, records, edits int
	}{{"absent", 100, 1, 0}, {"tails", 100, 1, 1}, {"recents", 66, 3, 1}, {"hot_channel", 1, 200, 200}} {
		b.Run(shape.name, func(b *testing.B) {
			var msgs []*ch.Message
			reads := make([]metadb.MessageUpdateRead, shape.channels)
			pages := make([]metadb.MessageUpdatePage, shape.channels)
			groups := map[metadb.ChannelKey]int{}
			for i := range reads {
				id := fmt.Sprintf("bench-%d", i)
				reads[i] = metadb.MessageUpdateRead{ChannelID: id, ChannelType: 2}
				groups[metadb.ChannelKey{ChannelID: id, ChannelType: 2}] = i
				for j := 0; j < shape.records; j++ {
					msgs = append(msgs, &ch.Message{ChannelID: id, ChannelType: 2, MessageID: uint64(j + 1), MessageSeq: uint64(j + 1)})
				}
				for j := 0; j < shape.edits; j++ {
					pages[i].Updates = append(pages[i].Updates, metadb.MessageUpdate{ChannelID: id, ChannelType: 2, MessageID: uint64(j + 1), MessageSeq: uint64(j + 1), Version: 1, Payload: []byte("edited")})
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				applyMessageUpdatePages(msgs, pages, groups)
			}
		})
	}
}
