package chatlifecycle

import (
	"context"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

// A successful pending Channel burst must count once, even when a later grant
// uses the Channel after promotion. Verify both completed traffic and the
// published expectation so a missing SEND cannot satisfy the assertion.
func TestEngineCountsPendingPersonCreateOnceThroughPromotion(t *testing.T) {
	t.Run("counted_initial_send_survives_promotion", func(t *testing.T) {
		testPendingPersonCreate(t, pendingCreateCase{initialCounted: true})
	})
	t.Run("uncounted_follower_does_not_suppress_first_create", func(t *testing.T) {
		testPendingPersonCreate(t, pendingCreateCase{})
	})
	t.Run("follower_preserves_inflight_candidate_through_promotion", func(t *testing.T) {
		testPendingPersonCreate(t, pendingCreateCase{candidateInFlight: true})
	})
}

type pendingCreateCase struct {
	initialCounted    bool
	candidateInFlight bool
}

func testPendingPersonCreate(t *testing.T, scenario pendingCreateCase) {
	t.Helper()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 4, SessionDuration: 10 * time.Hour,
		HotSet: HotSetConfig{PersonChannels: 1, GroupChannels: 1},
		Groups: GroupCatalogConfig{Small: 1, FixedMembership: true},
	})
	fixture.factory.autoAck = true
	require.NoError(t, fixture.engine.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, fixture.engine.Stop()) })

	active, pending := fixture.graph.edge(0, 1), fixture.graph.edge(2, 3)
	for _, edge := range []RelationshipEdge{active, pending} {
		for _, endpoint := range []SessionLogin{
			{UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: edge.OwnerIndex},
			{UID: edge.PeerUID, UserIndex: edge.PeerIndex, LoginOrdinal: edge.PeerIndex},
		} {
			_, err := fixture.engine.Login(context.Background(), endpoint)
			require.NoError(t, err)
		}
	}
	activeOrdinal, activeSchedule := findLifecycleSchedule(t, fixture.schedule, active, LifecycleRotating)
	pendingOrdinal, pendingSchedule := findLifecycleSchedule(t, fixture.schedule, pending, LifecycleLong)
	start := fixture.clock.Now()
	activate := func(edge RelationshipEdge, ordinal uint64) {
		t.Helper()
		activated, err := fixture.engine.ActivateRelationship(edge, ordinal)
		require.NoError(t, err)
		require.True(t, activated)
	}
	activate(active, activeOrdinal)
	bursts := []InitialBurstSchedule{activeSchedule.InitialBurst}
	if scenario.initialCounted {
		activate(pending, pendingOrdinal)
		bursts = append(bursts, pendingSchedule.InitialBurst)
	}
	initialCount := 0
	for _, burst := range bursts {
		initialCount += burst.MessageCount
	}
	for ordinal := uint64(0); ordinal <= uint64(initialCount+1); ordinal++ {
		kind, err := fixture.traffic.TrafficFor(ordinal)
		require.NoError(t, err)
		require.Equal(t, TrafficPerson, kind, "fixture must route a person-only prefix")
	}
	burstGrants := make(map[time.Duration]uint64)
	for _, burst := range bursts {
		for index := 0; index < burst.MessageCount; index++ {
			due := burst.Window * time.Duration(index) / time.Duration(burst.MessageCount-1)
			burstGrants[due]++
		}
	}
	dueTimes := make([]time.Duration, 0, len(burstGrants))
	for due := range burstGrants {
		dueTimes = append(dueTimes, due)
	}
	sort.Slice(dueTimes, func(i, j int) bool { return dueTimes[i] < dueTimes[j] })
	grant := func(at time.Time, count uint64) {
		t.Helper()
		fixture.clock.Set(at)
		grant, err := fixture.engine.Tick(at, []uint64{count})
		require.NoError(t, err)
		require.Equal(t, count, grant.Person)
		_, err = fixture.engine.Advance(at)
		require.NoError(t, err)
	}
	var snapshot EngineSnapshot
	for _, due := range dueTimes {
		grant(start.Add(due), burstGrants[due])
		snapshot = waitForEngineCompletions(t, fixture.engine, "initial person grant")
	}

	want := MetaCreateHashSlotCounts{}
	activeSlot := lifecycleHashSlotForKey(active.PersonChannelID, formalHashSlots)
	pendingSlot := lifecycleHashSlotForKey(pending.PersonChannelID, formalHashSlots)
	want[activeSlot]++
	if scenario.initialCounted {
		want[pendingSlot]++
	}
	require.Equal(t, want, snapshot.MetaCreatePersonByHashSlot, "initial bursts")
	require.Equal(t, uint64(initialCount), fixture.verifier.Snapshot().Acknowledged)

	promotion := start.Add(activeSchedule.ActiveFor)
	var candidate engineSentRoute
	deliver := func(route engineSentRoute, order uint64) {
		t.Helper()
		for _, client := range fixture.factory.clients() {
			if client.uid == route.uid {
				client.frames <- &frame.SendackPacket{
					ClientSeq: route.packet.ClientSeq, ClientMsgNo: route.packet.ClientMsgNo,
					MessageID: int64(order), MessageSeq: order, ReasonCode: frame.ReasonSuccess,
				}
				return
			}
		}
		t.Fatal("missing exact fixture sender")
	}
	routeAt := func(index int) engineSentRoute {
		t.Helper()
		for range 10_000 {
			routes := fixture.factory.sentRoutes()
			if len(routes) > index {
				return routes[index]
			}
			runtime.Gosched()
		}
		t.Fatalf("SEND %d did not reach the transport", index)
		return engineSentRoute{}
	}
	if !scenario.initialCounted {
		// A non-candidate SEND may be acknowledged before the scheduled candidate
		// is admitted. It must not stand in for the accounted candidate on promotion.
		beforePromotion := promotion.Add(-time.Nanosecond)
		fixture.clock.Set(beforePromotion)
		_, err := fixture.engine.Advance(beforePromotion)
		require.NoError(t, err)
		activate(pending, pendingOrdinal)
		if scenario.candidateInFlight {
			fixture.factory.autoAck = false
			grant(beforePromotion, 1)
			candidate = routeAt(initialCount)
		}
		follower := fixture.intent(t, pending.OwnerUID, pending.PeerUID, 1_000, TrafficPerson)
		follower.ChannelID = pending.PersonChannelID
		follower.ColdAttempt = true
		require.NoError(t, fixture.engine.SubmitGranted(follower, beforePromotion))
		_, err = fixture.engine.Advance(beforePromotion)
		require.NoError(t, err)
		if scenario.candidateInFlight {
			deliver(routeAt(initialCount+1), 10_000)
			for range 10_000 {
				snapshot, err = fixture.engine.Snapshot()
				require.NoError(t, err)
				if fixture.verifier.Snapshot().Acknowledged == uint64(initialCount+1) && snapshot.InflightCurrent == 1 {
					break
				}
				runtime.Gosched()
			}
			require.Equal(t, 1, snapshot.InflightCurrent, "candidate must still await its ACK")
		} else {
			snapshot = waitForEngineCompletions(t, fixture.engine, "pending follower")
		}
		initialCount++
		require.Equal(t, uint64(initialCount), fixture.verifier.Snapshot().Acknowledged)
		require.Equal(t, want, snapshot.MetaCreatePersonByHashSlot, "non-candidate SEND")
		want[pendingSlot]++
	}
	require.Equal(t, 1, snapshot.ActiveHotChannels)
	require.Equal(t, 1, snapshot.PendingHotChannels)
	fixture.clock.Set(promotion)
	_, err := fixture.engine.Advance(promotion)
	require.NoError(t, err)
	snapshot, err = fixture.engine.Snapshot()
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.ActiveHotChannels)
	require.Zero(t, snapshot.PendingHotChannels)
	grant(promotion, 1)
	lastIndex := initialCount
	if scenario.candidateInFlight {
		lastIndex++
		later := routeAt(lastIndex)
		deliver(candidate, 10_001)
		deliver(later, 10_002)
	}
	snapshot = waitForEngineCompletions(t, fixture.engine, "promoted person grant")
	require.Equal(t, uint64(lastIndex+1), fixture.verifier.Snapshot().Acknowledged)
	last := routeAt(lastIndex)
	require.True(t, last.uid == pending.OwnerUID && last.packet.ChannelID == pending.PeerUID ||
		last.uid == pending.PeerUID && last.packet.ChannelID == pending.OwnerUID, "later SEND must use the promoted Channel")
	require.Equal(t, want[pendingSlot], snapshot.MetaCreatePersonByHashSlot[pendingSlot], "promoted Channel create expectation")
}
