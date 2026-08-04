package chatlifecycle

import (
	"strconv"
	"testing"
)

func BenchmarkSessionPoolSnapshot10000(b *testing.B) {
	const sessionCount = 10_000
	client := &sessionFakeClient{}
	pool := &SessionPool{online: make(map[string]*onlineSession, sessionCount)}
	for index := 0; index < sessionCount; index++ {
		uid := "benchmark-user-" + strconv.Itoa(index)
		pool.online[uid] = &onlineSession{
			snapshot: SessionSnapshot{UID: uid, TrafficReady: true},
			client:   client,
		}
	}

	b.ReportAllocs()
	b.ReportMetric(sessionCount, "sessions/op")
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		snapshot := pool.Snapshot()
		if snapshot.Online != sessionCount || snapshot.TrafficReady != sessionCount {
			b.Fatalf("Snapshot = %+v", snapshot)
		}
	}
}
