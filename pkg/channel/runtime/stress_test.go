package runtime

import (
	"testing"
	"time"
)

const pressureStressStep = 10 * time.Millisecond

func TestRuntimeStressMixedScheduling(t *testing.T) {
	cfg, err := loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if !cfg.stressEnabled {
		t.Skip("set MULTIISR_STRESS=1 to enable")
	}

	h := newPressureHarness(t, cfg, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
		cfg.Limits.MaxSnapshotInflight = 1
	})
	rounds := pressureStressRounds(cfg.duration)

	for round := 0; round < rounds; round++ {
		h.clearPeerBackpressure()

		window := round / cfg.backpressureInterval
		hotPeer := h.peerIDs[(window/2)%len(h.peerIDs)]
		if window%2 == 0 {
			h.setPeerBackpressure(hotPeer, BackpressureHard)
		}

		h.enqueueReplicationRound(round)
		if (round+1)%cfg.snapshotInterval == 0 {
			h.queueSnapshot(round, 128)
		}
		h.runSchedulingTicks(1)
		h.deliverAvailableFetchResponses()
		h.clock.Advance(pressureStressStep)
	}

	h.clearPeerBackpressure()
	h.enqueueReplicationRound(rounds)
	h.runSchedulingTicks(1)
	h.deliverFetchResponses(0)

	h.assertAllGroupsMadeProgress(t)
	h.assertPeerQueuesDrained(t)
	h.assertSnapshotQueueDrained(t)
}

func TestRuntimeStressPeerQueueRecovery(t *testing.T) {
	cfg, err := loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if !cfg.stressEnabled {
		t.Skip("set MULTIISR_STRESS=1 to enable")
	}

	h := newPressureHarness(t, cfg, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	rounds := pressureStressRounds(cfg.duration)
	hotPeers := h.peerIDs[:minInt(2, len(h.peerIDs))]

	for round := 0; round < rounds; round++ {
		h.clearPeerBackpressure()
		hardPhase := (round/cfg.backpressureInterval)%2 == 0
		if hardPhase {
			for _, peer := range hotPeers {
				h.setPeerBackpressure(peer, BackpressureHard)
			}
		}

		for groupIndex := range h.groupIDs {
			peer := hotPeers[(groupIndex+round)%len(hotPeers)]
			h.enqueueReplicationToPeer(groupIndex, peer)
		}
		h.runSchedulingTicks(1)
		if !hardPhase {
			h.deliverAvailableFetchResponses()
		}
		h.clock.Advance(pressureStressStep)
	}

	h.clearPeerBackpressure()
	for groupIndex := range h.groupIDs {
		peer := hotPeers[groupIndex%len(hotPeers)]
		h.enqueueReplicationToPeer(groupIndex, peer)
	}
	h.runSchedulingTicks(1)
	h.deliverFetchResponses(0)

	if h.maxQueuedPeerRequests() == 0 {
		t.Fatal("expected queued peer requests during hard backpressure")
	}
	h.assertAllGroupsMadeProgress(t)
	h.assertPeerQueuesDrained(t)
}

func TestRuntimeStressSnapshotInterference(t *testing.T) {
	cfg, err := loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if !cfg.stressEnabled {
		t.Skip("set MULTIISR_STRESS=1 to enable")
	}

	h := newPressureHarness(t, cfg, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
		cfg.Limits.MaxSnapshotInflight = 1
	})
	rounds := pressureStressRounds(cfg.duration)
	releaseInterval := maxInt(1, cfg.snapshotInterval*2)

	h.reserveSnapshotSlot(t)
	for round := 0; round < rounds; round++ {
		h.enqueueReplicationRound(round)
		if (round+1)%cfg.snapshotInterval == 0 {
			h.queueSnapshot(round, 256)
		}
		h.runSchedulingTicks(1)
		h.deliverAvailableFetchResponses()

		if (round+1)%releaseInterval == 0 {
			h.releaseSnapshotSlot()
			h.runSchedulingTicks(1)
			h.reserveSnapshotSlot(t)
		}
		h.clock.Advance(pressureStressStep)
	}

	h.releaseSnapshotSlot()
	for i := 0; i < len(h.groupIDs)+rounds; i++ {
		h.runSchedulingTicks(1)
		h.deliverFetchResponses(0)
		if h.runtime.queuedSnapshotGroups() == 0 {
			break
		}
	}

	if h.snapshotWaitingHighWatermark == 0 {
		t.Fatal("expected snapshot waiting queue during interference")
	}
	h.assertAllGroupsMadeProgress(t)
	h.assertPeerQueuesDrained(t)
	h.assertSnapshotQueueDrained(t)
}

func pressureStressRounds(duration time.Duration) int {
	if duration <= 0 {
		return 1
	}
	rounds := int(duration / pressureStressStep)
	if duration%pressureStressStep != 0 {
		rounds++
	}
	if rounds < 1 {
		return 1
	}
	return rounds
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
