package runtime

import "testing"

func BenchmarkRuntimeReplicationScheduling(b *testing.B) {
	b.Run("groups=256/peers=8", func(b *testing.B) {
		cfg := pressureConfig{
			groups:               defaultPressureGroups,
			peers:                defaultPressurePeers,
			seed:                 defaultPressureSeed,
			snapshotInterval:     defaultPressureSnapshotInterval,
			backpressureInterval: defaultPressureBackpressureInterval,
		}
		h := newPressureHarness(b, cfg, nil)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			h.enqueueReplication(i, i)
			h.runSchedulingTicks(1)

			if (i+1)%cfg.groups == 0 {
				b.StopTimer()
				h.deliverFetchResponses(0)
				b.StartTimer()
			}
		}
		b.StopTimer()
		h.deliverFetchResponses(0)
		h.assertSomeGroupMadeProgress(b)
		h.assertPeerQueuesDrained(b)
	})
}

func BenchmarkRuntimePeerQueueDrain(b *testing.B) {
	b.Run("groups=256/peers=8", func(b *testing.B) {
		cfg := pressureConfig{
			groups:               defaultPressureGroups,
			peers:                defaultPressurePeers,
			seed:                 defaultPressureSeed,
			snapshotInterval:     defaultPressureSnapshotInterval,
			backpressureInterval: defaultPressureBackpressureInterval,
		}
		h := newPressureHarness(b, cfg, func(cfg *Config) {
			cfg.Limits.MaxFetchInflightPeer = 1
		})
		peer := h.peerIDs[0]

		h.enqueueReplicationToPeer(0, peer)
		h.enqueueReplicationToPeer(1, peer)
		h.runSchedulingTicks(1)
		if h.runtime.queuedPeerRequests(peer) == 0 {
			b.Fatal("expected initial queued request")
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			h.enqueueReplicationToPeer((i+2)%cfg.groups, peer)
			h.runSchedulingTicks(1)
			if got := h.deliverPeerFetchResponses(peer, 1); got != 1 {
				b.Fatalf("deliverPeerFetchResponses() = %d, want 1", got)
			}
		}
		b.StopTimer()
		h.deliverFetchResponses(0)
		h.assertSomeGroupMadeProgress(b)
		h.assertPeerQueuesDrained(b)
	})
}

func BenchmarkRuntimeMixedReplicationAndSnapshot(b *testing.B) {
	b.Run("groups=256/peers=8", func(b *testing.B) {
		cfg := pressureConfig{
			groups:               defaultPressureGroups,
			peers:                defaultPressurePeers,
			seed:                 defaultPressureSeed,
			snapshotInterval:     defaultPressureSnapshotInterval,
			backpressureInterval: defaultPressureBackpressureInterval,
		}
		h := newPressureHarness(b, cfg, func(cfg *Config) {
			cfg.Limits.MaxSnapshotInflight = 1
		})

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			h.enqueueReplication(i, i)
			if (i+1)%cfg.snapshotInterval == 0 {
				h.queueSnapshot(i, 128)
			}
			h.runSchedulingTicks(1)

			if (i+1)%cfg.groups == 0 {
				b.StopTimer()
				h.deliverFetchResponses(0)
				b.StartTimer()
			}
		}
		b.StopTimer()
		h.deliverFetchResponses(0)
		h.assertSomeGroupMadeProgress(b)
		h.assertPeerQueuesDrained(b)
		if h.snapshotWaitingHighWatermark > cfg.groups {
			b.Fatalf("snapshot waiting high watermark = %d, want <= %d", h.snapshotWaitingHighWatermark, cfg.groups)
		}
	})
}
