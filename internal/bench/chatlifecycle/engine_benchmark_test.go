package chatlifecycle

import (
	"context"
	"runtime"
	"testing"
)

func BenchmarkEngineAdvanceAutoAck2000(b *testing.B) {
	const workPerIteration = 2_000
	fixture := newEngineTestFixture(b, engineTestLimits{
		CommandCapacity: 4_096, WorkCapacity: 8_192,
		InflightCapacity: 4_096, MaxWorkPerAdvance: 4_096,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		b.Fatalf("Start: %v", err)
	}
	b.Cleanup(func() { _ = fixture.engine.Stop() })
	uid := fixture.identity.UID(23)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{
		UID: uid, UserIndex: 23, LoginOrdinal: 3,
	}); err != nil {
		b.Fatalf("Login: %v", err)
	}

	b.ReportAllocs()
	b.ReportMetric(workPerIteration, "work/op")
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		for offset := 0; offset < workPerIteration; offset++ {
			ordinal := uint64(iteration*workPerIteration + offset + 1)
			if err := fixture.engine.SubmitGranted(fixture.intent(b, uid, "benchmark-group", ordinal, TrafficGroup), fixture.clock.Now()); err != nil {
				b.Fatalf("SubmitGranted(%d): %v", ordinal, err)
			}
		}
		b.StartTimer()
		processed, err := fixture.engine.Advance(fixture.clock.Now())
		if err != nil || processed != workPerIteration {
			b.Fatalf("Advance = %d, %v", processed, err)
		}
		for spin := 0; ; spin++ {
			snapshot, snapshotErr := fixture.engine.Snapshot()
			if snapshotErr != nil {
				b.Fatalf("Snapshot: %v", snapshotErr)
			}
			if snapshot.InflightCurrent == 0 {
				break
			}
			if spin >= 10_000 {
				b.Fatalf("auto-ACK completion did not settle: %+v", snapshot)
			}
			runtime.Gosched()
		}
		b.StopTimer()
		fixture.factory.mu.Lock()
		fixture.factory.routesV = nil
		fixture.factory.mu.Unlock()
		for _, client := range fixture.factory.clients() {
			client.mu.Lock()
			client.sent = nil
			client.mu.Unlock()
		}
	}
}
