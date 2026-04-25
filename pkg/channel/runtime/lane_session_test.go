package runtime

import (
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestLeaderLaneSessionDedupesReadyChannels(t *testing.T) {
	session := newLeaderLaneSession(1, 1)
	session.MarkDataReady("g1", 11)
	session.MarkDataReady("g1", 11)

	var selected int
	result, waiter := session.Poll(nil, nil, LanePollBudget{MaxChannels: 4}, func(key core.ChannelKey, _ LaneCursorDelta, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
		selected++
		return LeaderLaneReadyItem{
			ChannelKey:   key,
			ChannelEpoch: 11,
			ReadyMask:    mask,
			SizeBytes:    1,
		}, true
	})
	if waiter != nil {
		t.Fatal("unexpected parked waiter for ready lane")
	}
	if selected != 1 {
		t.Fatalf("selected = %d, want 1", selected)
	}
	if len(result.Items) != 1 || result.Items[0].ChannelKey != "g1" {
		t.Fatalf("result.Items = %+v, want single g1 item", result.Items)
	}
}

func TestLeaderLaneSessionSetsMoreReadyWhenBudgetTruncates(t *testing.T) {
	session := newLeaderLaneSession(1, 1)
	session.MarkDataReady("g1", 11)
	session.MarkDataReady("g2", 12)

	result, waiter := session.Poll(nil, nil, LanePollBudget{MaxChannels: 1}, func(key core.ChannelKey, _ LaneCursorDelta, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
		return LeaderLaneReadyItem{
			ChannelKey:   key,
			ChannelEpoch: 11,
			ReadyMask:    mask,
			SizeBytes:    1,
		}, true
	})
	if waiter != nil {
		t.Fatal("unexpected parked waiter for ready lane")
	}
	if len(result.Items) != 1 {
		t.Fatalf("item len = %d, want 1", len(result.Items))
	}
	if !result.MoreReady {
		t.Fatal("expected moreReady when budget truncates remaining items")
	}
}

func TestLeaderLaneSessionRequeuesHotChannelAtTail(t *testing.T) {
	session := newLeaderLaneSession(1, 1)
	session.MarkDataReady("hot", 11)
	session.MarkDataReady("cold", 12)

	result, waiter := session.Poll(nil, nil, LanePollBudget{MaxChannels: 1}, func(key core.ChannelKey, _ LaneCursorDelta, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
		return LeaderLaneReadyItem{
			ChannelKey:   key,
			ChannelEpoch: 11,
			ReadyMask:    mask,
			SizeBytes:    1,
		}, key != "hot"
	})
	if waiter != nil {
		t.Fatal("unexpected parked waiter for ready lane")
	}
	if len(result.Items) != 1 || result.Items[0].ChannelKey != "hot" {
		t.Fatalf("first poll items = %+v, want hot", result.Items)
	}

	result, waiter = session.Poll(nil, nil, LanePollBudget{MaxChannels: 1}, func(key core.ChannelKey, _ LaneCursorDelta, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
		return LeaderLaneReadyItem{
			ChannelKey:   key,
			ChannelEpoch: 12,
			ReadyMask:    mask,
			SizeBytes:    1,
		}, true
	})
	if waiter != nil {
		t.Fatal("unexpected parked waiter on second poll")
	}
	if len(result.Items) != 1 || result.Items[0].ChannelKey != "cold" {
		t.Fatalf("second poll items = %+v, want cold", result.Items)
	}
}

func TestLeaderLaneSessionParkedRequestWakesOnDataReady(t *testing.T) {
	session := newLeaderLaneSession(1, 1)

	result, waiter := session.Poll(nil, nil, LanePollBudget{MaxChannels: 1}, func(key core.ChannelKey, _ LaneCursorDelta, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
		return LeaderLaneReadyItem{}, true
	})
	if len(result.Items) != 0 {
		t.Fatalf("unexpected items while parking: %+v", result.Items)
	}
	if waiter == nil {
		t.Fatal("expected parked waiter when lane is idle")
	}

	session.MarkDataReady("g1", 11)
	select {
	case <-waiter.Ready():
	case <-time.After(time.Second):
		t.Fatal("parked waiter did not wake after data ready")
	}
}

func TestLeaderLaneSessionAppliesCursorDeltaBeforeSelectingItems(t *testing.T) {
	session := newLeaderLaneSession(1, 1)
	session.TrackChannel("g-delta", 11)
	session.MarkDataReady("g-ready", 12)

	var steps []string
	result, waiter := session.Poll([]LaneCursorDelta{
		{ChannelKey: "g-delta", ChannelEpoch: 11, MatchOffset: 9},
	}, func(delta LaneCursorDelta) {
		steps = append(steps, "apply:"+string(delta.ChannelKey))
	}, LanePollBudget{MaxChannels: 1}, func(key core.ChannelKey, _ LaneCursorDelta, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
		steps = append(steps, "select:"+string(key))
		return LeaderLaneReadyItem{
			ChannelKey:   key,
			ChannelEpoch: 12,
			ReadyMask:    mask,
			SizeBytes:    1,
		}, true
	})
	if waiter != nil {
		t.Fatal("unexpected parked waiter for ready lane")
	}
	if len(result.Items) != 1 || result.Items[0].ChannelKey != "g-ready" {
		t.Fatalf("result.Items = %+v, want g-ready", result.Items)
	}
	if len(steps) != 2 || steps[0] != "apply:g-delta" || steps[1] != "select:g-ready" {
		t.Fatalf("steps = %v, want apply before select", steps)
	}
}

func TestLeaderLaneSessionIgnoresStaleChannelEpochDeltas(t *testing.T) {
	session := newLeaderLaneSession(1, 1)
	session.TrackChannel("g1", 11)

	var applied int
	_, waiter := session.Poll([]LaneCursorDelta{
		{ChannelKey: "g1", ChannelEpoch: 10, MatchOffset: 7},
	}, func(delta LaneCursorDelta) {
		applied++
	}, LanePollBudget{MaxChannels: 1}, func(key core.ChannelKey, _ LaneCursorDelta, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
		return LeaderLaneReadyItem{}, true
	})
	if waiter == nil {
		t.Fatal("expected parked waiter for idle lane")
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want stale delta ignored", applied)
	}
}

func TestLeaderLaneSessionChannelCachesReplicationTargets(t *testing.T) {
	ch := newChannel("g1", 1, &fakeReplica{}, core.Meta{Key: "g1"}, time.Now, nil, nil, nil)
	targets := []PeerLaneKey{{Peer: 2, LaneID: 1}, {Peer: 3, LaneID: 2}}

	ch.setReplicationTargets(targets)
	got := ch.replicationTargetsSnapshot()
	if len(got) != len(targets) {
		t.Fatalf("target len = %d, want %d", len(got), len(targets))
	}
	got[0].LaneID = 99
	again := ch.replicationTargetsSnapshot()
	if again[0].LaneID != 1 {
		t.Fatalf("replicationTargetsSnapshot leaked internal slice, got %+v", again)
	}
}

func TestLeaderLaneSessionLaneDirectoryTracksTargetsAndSessions(t *testing.T) {
	directory := newLaneDirectory()
	session := newLeaderLaneSession(7, 8)
	key := PeerLaneKey{Peer: 2, LaneID: 1}

	directory.SetReplicationTargets("g1", []PeerLaneKey{key})
	directory.RegisterSession(key, session)

	targets := directory.ReplicationTargets("g1")
	if len(targets) != 1 || targets[0] != key {
		t.Fatalf("targets = %+v, want %+v", targets, []PeerLaneKey{key})
	}
	targets[0].LaneID = 9
	again := directory.ReplicationTargets("g1")
	if again[0].LaneID != 1 {
		t.Fatalf("ReplicationTargets leaked internal slice, got %+v", again)
	}
	gotSession, ok := directory.Session(key)
	if !ok || gotSession != session {
		t.Fatalf("Session(%+v) = (%v,%v), want (%v,true)", key, gotSession, ok, session)
	}
}
