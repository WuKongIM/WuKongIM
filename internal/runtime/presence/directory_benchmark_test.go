package presence

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

var benchmarkExpiryRouteCounts = []int{10_000, 100_000, 1_000_000}

func BenchmarkDirectoryEndpointsByTargetsCloudMedium(b *testing.B) {
	dir, groups := benchmarkCloudMediumEndpointDirectory()
	b.Run("per_target", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(groups)), "target_groups/op")
		for i := 0; i < b.N; i++ {
			routeCount := 0
			for _, group := range groups {
				routes, err := dir.EndpointsByUIDs(group.Target, group.UIDs)
				if err != nil {
					b.Fatal(err)
				}
				routeCount += len(routes)
			}
			if routeCount != 55 {
				b.Fatalf("route count = %d, want 55", routeCount)
			}
		}
	})
	b.Run("shard_batch", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(groups)), "target_groups/op")
		for i := 0; i < b.N; i++ {
			results := dir.EndpointsByTargets(groups)
			routeCount := 0
			for _, result := range results {
				if result.Err != nil {
					b.Fatal(result.Err)
				}
				routeCount += len(result.Routes)
			}
			if routeCount != 55 {
				b.Fatalf("route count = %d, want 55", routeCount)
			}
		}
	})
	b.Run("per_target_parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(groups)), "target_groups/op")
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if routeCount := benchmarkEndpointsByTarget(dir, groups); routeCount != 55 {
					panic(fmt.Sprintf("route count = %d, want 55", routeCount))
				}
			}
		})
	})
	b.Run("shard_batch_parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(groups)), "target_groups/op")
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if routeCount := benchmarkEndpointsByShardBatch(dir, groups); routeCount != 55 {
					panic(fmt.Sprintf("route count = %d, want 55", routeCount))
				}
			}
		})
	})
}

func benchmarkEndpointsByTarget(dir *Directory, groups []EndpointLookupGroup) int {
	routeCount := 0
	for _, group := range groups {
		routes, err := dir.EndpointsByUIDs(group.Target, group.UIDs)
		if err != nil {
			panic(err)
		}
		routeCount += len(routes)
	}
	return routeCount
}

func benchmarkEndpointsByShardBatch(dir *Directory, groups []EndpointLookupGroup) int {
	results := dir.EndpointsByTargets(groups)
	routeCount := 0
	for _, result := range results {
		if result.Err != nil {
			panic(result.Err)
		}
		routeCount += len(result.Routes)
	}
	return routeCount
}

func benchmarkCloudMediumEndpointDirectory() (*Directory, []EndpointLookupGroup) {
	const (
		targetGroupCount = 221
		recipientCount   = 512
		onlineRouteCount = 55
	)
	dir := NewDirectory(DirectoryOptions{LocalNodeID: 1, ShardCount: 32})
	groups := make([]EndpointLookupGroup, targetGroupCount)
	recipientIndex := 0
	for groupIndex := range groups {
		target := RouteTarget{
			HashSlot:       uint16(groupIndex),
			SlotID:         uint32(groupIndex%10 + 1),
			LeaderNodeID:   1,
			LeaderTerm:     11,
			ConfigEpoch:    7,
			RouteRevision:  19,
			AuthorityEpoch: 23,
		}
		dir.BecomeAuthority(target)
		uidCount := recipientCount / targetGroupCount
		if groupIndex < recipientCount%targetGroupCount {
			uidCount++
		}
		uids := make([]string, uidCount)
		for uidIndex := range uids {
			uid := fmt.Sprintf("cloud-medium-user-%03d", recipientIndex)
			uids[uidIndex] = uid
			if recipientIndex < onlineRouteCount {
				_, err := dir.RegisterRoute(target, Route{
					UID:         uid,
					OwnerNodeID: uint64(recipientIndex%3 + 1),
					OwnerBootID: 1,
					OwnerSeq:    1,
					SessionID:   uint64(recipientIndex + 1),
				})
				if err != nil {
					panic(err)
				}
			}
			recipientIndex++
		}
		groups[groupIndex] = EndpointLookupGroup{Target: target, UIDs: uids}
	}
	return dir, groups
}

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
