package runtime

import (
	"fmt"
	"hash/fnv"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestFollowerLaneAssignsStableLaneByHash(t *testing.T) {
	mgr := newPeerLaneManager(PeerLaneManagerConfig{
		Peer:      2,
		LaneCount: 8,
		MaxWait:   time.Millisecond,
		Budget: LanePollBudget{
			MaxBytes:    64 * 1024,
			MaxChannels: 64,
		},
	})

	keys := []core.ChannelKey{"alpha", "bravo", "charlie", "delta"}
	for _, key := range keys {
		got := mgr.LaneFor(key)
		want := testFollowerLaneFor(key, 8)
		if got != want {
			t.Fatalf("LaneFor(%q) = %d, want %d", key, got, want)
		}
		if again := mgr.LaneFor(key); again != want {
			t.Fatalf("second LaneFor(%q) = %d, want %d", key, again, want)
		}
	}
}

func TestFollowerLaneCoalescesCursorDeltaBeforeNextSend(t *testing.T) {
	mgr := newPeerLaneManager(PeerLaneManagerConfig{
		Peer:      2,
		LaneCount: 4,
		MaxWait:   time.Millisecond,
		Budget: LanePollBudget{
			MaxBytes:    64 * 1024,
			MaxChannels: 64,
		},
	})

	key := core.ChannelKey("cursor-room")
	mgr.UpsertChannel(key, 11)
	laneID := mgr.LaneFor(key)

	openReq, ok := mgr.NextRequest(laneID)
	if !ok {
		t.Fatal("expected initial open request")
	}
	if openReq.Op != LanePollOpOpen {
		t.Fatalf("initial op = %v, want open", openReq.Op)
	}
	mgr.ApplyResponse(LanePollResponseEnvelope{
		LaneID:       laneID,
		Status:       LanePollStatusOK,
		SessionID:    101,
		SessionEpoch: 1,
	})

	mgr.MarkCursorDelta(LaneCursorDelta{ChannelKey: key, ChannelEpoch: 11, MatchOffset: 7, OffsetEpoch: 11})
	mgr.MarkCursorDelta(LaneCursorDelta{ChannelKey: key, ChannelEpoch: 11, MatchOffset: 9, OffsetEpoch: 11})

	req, ok := mgr.NextRequest(laneID)
	if !ok {
		t.Fatal("expected poll request after cursor delta")
	}
	if req.Op != LanePollOpPoll {
		t.Fatalf("op = %v, want poll", req.Op)
	}
	if len(req.CursorDelta) != 1 {
		t.Fatalf("cursor delta len = %d, want 1", len(req.CursorDelta))
	}
	if req.CursorDelta[0].MatchOffset != 9 {
		t.Fatalf("cursor delta = %+v, want latest offset 9", req.CursorDelta[0])
	}
	if len(req.FullMembership) != 0 {
		t.Fatalf("poll request unexpectedly carried full membership: %+v", req.FullMembership)
	}
}

func TestFollowerLaneMembershipChangeTouchesAffectedLaneOnly(t *testing.T) {
	mgr := newPeerLaneManager(PeerLaneManagerConfig{
		Peer:      2,
		LaneCount: 4,
		MaxWait:   time.Millisecond,
		Budget: LanePollBudget{
			MaxBytes:    64 * 1024,
			MaxChannels: 64,
		},
	})

	firstKey := testChannelKeyForLane(t, 0, 4, "first")
	secondKey := testChannelKeyForLane(t, 1, 4, "second")

	mgr.UpsertChannel(firstKey, 11)
	mgr.UpsertChannel(secondKey, 21)

	firstLane := mgr.LaneFor(firstKey)
	secondLane := mgr.LaneFor(secondKey)
	if firstLane == secondLane {
		t.Fatalf("expected distinct lanes, both mapped to %d", firstLane)
	}

	if _, ok := mgr.NextRequest(firstLane); !ok {
		t.Fatal("expected initial open on first lane")
	}
	mgr.ApplyResponse(LanePollResponseEnvelope{
		LaneID:       firstLane,
		Status:       LanePollStatusOK,
		SessionID:    201,
		SessionEpoch: 1,
	})
	if _, ok := mgr.NextRequest(secondLane); !ok {
		t.Fatal("expected initial open on second lane")
	}
	mgr.ApplyResponse(LanePollResponseEnvelope{
		LaneID:       secondLane,
		Status:       LanePollStatusOK,
		SessionID:    202,
		SessionEpoch: 1,
	})

	mgr.UpsertChannel(firstKey, 12)

	req, ok := mgr.NextRequest(firstLane)
	if !ok {
		t.Fatal("expected affected lane to reopen with fresh membership")
	}
	if req.Op != LanePollOpOpen {
		t.Fatalf("affected lane op = %v, want open", req.Op)
	}
	if len(req.FullMembership) != 1 || req.FullMembership[0].ChannelKey != firstKey || req.FullMembership[0].ChannelEpoch != 12 {
		t.Fatalf("affected lane membership = %+v, want updated snapshot for %q", req.FullMembership, firstKey)
	}
	if req, ok := mgr.NextRequest(secondLane); ok && req.Op != LanePollOpPoll {
		t.Fatalf("unaffected lane request = %+v, want normal poll or no request", req)
	}
}

func TestFollowerLaneMembershipChangeDuringInflightOpenForcesReopen(t *testing.T) {
	mgr := newPeerLaneManager(PeerLaneManagerConfig{
		Peer:      2,
		LaneCount: 4,
		MaxWait:   time.Millisecond,
		Budget: LanePollBudget{
			MaxBytes:    64 * 1024,
			MaxChannels: 64,
		},
	})

	firstKey := testChannelKeyForLane(t, 0, 4, "first-inflight")
	secondKey := testChannelKeyForLane(t, 0, 4, "second-inflight")
	if firstKey == secondKey {
		t.Fatal("expected distinct keys on same lane")
	}

	laneID := mgr.LaneFor(firstKey)
	mgr.UpsertChannel(firstKey, 11)

	req, ok := mgr.NextRequest(laneID)
	if !ok {
		t.Fatal("expected initial open request")
	}
	if req.Op != LanePollOpOpen {
		t.Fatalf("initial op = %v, want open", req.Op)
	}
	if len(req.FullMembership) != 1 || req.FullMembership[0].ChannelKey != firstKey {
		t.Fatalf("initial membership = %+v, want only %q", req.FullMembership, firstKey)
	}

	mgr.UpsertChannel(secondKey, 21)
	if mgr.LaneFor(secondKey) != laneID {
		t.Fatalf("second key lane = %d, want %d", mgr.LaneFor(secondKey), laneID)
	}

	mgr.ApplyResponse(LanePollResponseEnvelope{
		LaneID:       laneID,
		Status:       LanePollStatusOK,
		SessionID:    301,
		SessionEpoch: 1,
	})

	req, ok = mgr.NextRequest(laneID)
	if !ok {
		t.Fatal("expected reopen after inflight membership change")
	}
	if req.Op != LanePollOpOpen {
		t.Fatalf("op after inflight membership change = %v, want open", req.Op)
	}
	if len(req.FullMembership) != 2 {
		t.Fatalf("membership len after inflight membership change = %d, want 2", len(req.FullMembership))
	}
	if req.FullMembership[0].ChannelKey != firstKey || req.FullMembership[1].ChannelKey != secondKey {
		t.Fatalf("membership after inflight membership change = %+v, want [%q %q]", req.FullMembership, firstKey, secondKey)
	}
}

func testFollowerLaneFor(key core.ChannelKey, laneCount int) uint16 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return uint16(hasher.Sum32() % uint32(laneCount))
}

func testChannelKeyForLane(t *testing.T, wantLane uint16, laneCount int, prefix string) core.ChannelKey {
	t.Helper()

	for i := 0; i < 4096; i++ {
		key := core.ChannelKey(fmt.Sprintf("%s-%d", prefix, i))
		if testFollowerLaneFor(key, laneCount) == wantLane {
			return key
		}
	}
	t.Fatalf("failed to find channel key for lane %d", wantLane)
	return ""
}
