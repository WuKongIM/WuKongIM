package presence

import (
	"strconv"
	"testing"
	"time"
)

var benchmarkExpiryRouteCounts = []int{10_000, 100_000, 1_000_000}

func BenchmarkDirectoryExpireRoutesFresh(b *testing.B) {
	for _, routeCount := range benchmarkExpiryRouteCounts {
		b.Run(strconv.Itoa(routeCount), func(b *testing.B) {
			b.StopTimer()
			dir := benchmarkExpiryDirectory(routeCount, 1_000)
			b.ReportAllocs()
			b.StartTimer()

			var candidates int
			var expired int
			for i := 0; i < b.N; i++ {
				result := dir.ExpireRoutesDetailed(time.Unix(1_000, 0), 5*time.Second)
				candidates += result.Examined
				expired += result.Expired
			}

			b.StopTimer()
			b.ReportMetric(float64(candidates)/float64(b.N), "candidates/op")
			b.ReportMetric(float64(expired)/float64(b.N), "expired/op")
		})
	}
}

func BenchmarkDirectoryExpireRoutesDue(b *testing.B) {
	for _, routeCount := range benchmarkExpiryRouteCounts {
		b.Run(strconv.Itoa(routeCount), func(b *testing.B) {
			b.ReportAllocs()
			var candidates int
			var expired int
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				dir := benchmarkExpiryDirectory(routeCount, 100)
				b.StartTimer()

				result := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second)
				candidates += result.Examined
				expired += result.Expired
			}

			b.StopTimer()
			b.ReportMetric(float64(candidates)/float64(b.N), "candidates/op")
			b.ReportMetric(float64(expired)/float64(b.N), "expired/op")
		})
	}
}

func benchmarkExpiryDirectory(routeCount int, seenUnix int64) *Directory {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 0, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	slot := dir.shards[0].slots[target.HashSlot]
	for i := 0; i < routeCount; i++ {
		slot.upsertActiveLocked(Route{
			UID:          "benchmark-user",
			OwnerNodeID:  1,
			OwnerBootID:  1,
			OwnerSeq:     1,
			SessionID:    uint64(i + 1),
			LastSeenUnix: seenUnix,
		})
	}
	return dir
}
