package chatlifecycle

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestEngineOwnsBoundedSessionsRelationshipsAndFutureWork(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{MaxWorkPerAdvance: 128})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()

	edge := fixture.graph.Incoming(12).Items[0]
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: 1}); err != nil {
		t.Fatalf("owner Login: %v", err)
	}
	if activated, err := fixture.engine.ActivateRelationship(edge, 100); err != nil || activated {
		t.Fatalf("offline ActivateRelationship = %v, %v", activated, err)
	}
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.PeerUID, UserIndex: edge.PeerIndex, LoginOrdinal: 2}); err != nil {
		t.Fatalf("peer Login: %v", err)
	}
	considered, activated, err := fixture.engine.ObserveNewUser(edge.PeerIndex)
	if err != nil || activated != 1 || considered > MaxForwardRelationships {
		t.Fatalf("ObserveNewUser = considered %d, activated %d, err %v", considered, activated, err)
	}
	relationshipOrdinal := edge.OwnerIndex * MaxForwardRelationships
	schedule, err := fixture.schedule.Channel(relationshipOrdinal, edge.OwnerIndex, edge.PeerIndex)
	if err != nil {
		t.Fatalf("Channel schedule: %v", err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Online != 2 || snapshot.ActivityCurrent != schedule.InitialBurst.MessageCount ||
		snapshot.FutureCurrent < schedule.InitialBurst.MessageCount || snapshot.FutureCurrent > schedule.InitialBurst.MessageCount+1 {
		t.Fatalf("engine snapshot = %+v, burst = %+v", snapshot, schedule.InitialBurst)
	}
	if snapshot.RelationshipLookback != MaxForwardRelationships || snapshot.ActiveLifecycleTimers > 1 {
		t.Fatalf("relationship/lifecycle bounds = %+v", snapshot)
	}
	if _, err := fixture.engine.Advance(fixture.clock.Now()); err != nil {
		t.Fatalf("Advance before global grant: %v", err)
	}
	if got := fixture.factory.sentCount(); got != 0 {
		t.Fatalf("relationship SEND bypassed global grant: %d", got)
	}
	if tick, err := fixture.engine.Tick(fixture.clock.Now(), []uint64{1, 0, 0}); err != nil || tick.Released != 1 {
		t.Fatalf("Tick = %+v, %v", tick, err)
	}
	afterTick, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after Tick: %v", err)
	}
	if afterTick.ActivityCurrent != schedule.InitialBurst.MessageCount-1 {
		t.Fatalf("due activity did not consume one person grant: %+v", afterTick)
	}
	if _, err := fixture.engine.Advance(fixture.clock.Now()); err != nil {
		t.Fatalf("Advance granted traffic: %v", err)
	}
	if got := fixture.factory.sentCount(); got != 1 {
		t.Fatalf("sparse-online transport sends = %d, want one online-routed grant", got)
	}
}

func TestEngineTickAdmitsOneGlobalGrantWithoutHistoryRetention(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 100, NewUsersPerDay: 250_000, WorkCapacity: 8_192, MaxWorkPerAdvance: 8_192,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now().Add(30 * time.Second)
	fixture.clock.Set(now)
	if step, err := fixture.engine.Step(context.Background(), now, nil); err != nil || step.Online != 100 {
		t.Fatalf("bootstrap Step = %+v, %v", step, err)
	}
	tick, err := fixture.engine.Tick(now, []uint64{1_000, 1_000, 1_000})
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if tick.Released != 100 || tick.Person != 90 || tick.Group != 10 {
		t.Fatalf("local global grant = %+v", tick)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.QueueCurrent != 100 || snapshot.FutureCurrent < 100 || snapshot.FutureCurrent > snapshot.QueueCapacity || snapshot.InflightCurrent != 0 {
		t.Fatalf("tick ownership = %+v", snapshot)
	}
}

func TestEngineTickRoutesEveryGrantThroughCurrentlyOnlineEligibleSender(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 100, NewUsersPerDay: 250_000, WorkCapacity: 8_192, InflightCapacity: 128, MaxWorkPerAdvance: 256,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now().Add(30 * time.Second)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Step(context.Background(), now, nil); err != nil {
		t.Fatalf("bootstrap Step: %v", err)
	}
	for index := uint64(0); index < 50; index++ {
		if err := fixture.engine.Logout(fixture.identity.UID(index)); err != nil {
			t.Fatalf("Logout(%d): %v", index, err)
		}
	}
	for index := uint64(100); index < 150; index++ {
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: fixture.identity.UID(index), UserIndex: index, LoginOrdinal: index}); err != nil {
			t.Fatalf("replacement Login(%d): %v", index, err)
		}
		if _, _, err := fixture.engine.ObserveNewUser(index); err != nil {
			t.Fatalf("ObserveNewUser(%d): %v", index, err)
		}
	}
	before := fixture.factory.sentCount()
	tick, err := fixture.engine.Tick(now, []uint64{1_000, 1_000, 1_000})
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if tick.Released != 100 {
		t.Fatalf("released grants = %+v", tick)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if sent := fixture.factory.sentCount() - before; sent != 100 {
		t.Fatalf("actual first-attempt SENDs = %d, want one per 100 grants", sent)
	}
	if fixture.evidence.Snapshot().Classification == SyncClassificationProductFailure {
		t.Fatalf("offline routing became product evidence: %+v", fixture.evidence.Snapshot())
	}
}

func TestEngineExactOnePercentSampledPersonRoutesHaveOnlineRecipient(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 128, SessionDuration: 10 * time.Hour,
		WorkCapacity: 4_096, InflightCapacity: 256, MaxWorkPerAdvance: 512,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	for index := uint64(0); index < 100; index++ {
		uid := fixture.identity.UID(index)
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index}); err != nil {
			t.Fatalf("group member Login(%d): %v", index, err)
		}
	}
	activate := func(owner uint64) RelationshipEdge {
		t.Helper()
		edges, err := fixture.graph.Outgoing(owner)
		if err != nil || edges.Count == 0 {
			t.Fatalf("Outgoing(%d): %+v, %v", owner, edges, err)
		}
		edge := edges.Items[0]
		for _, endpoint := range []struct {
			uid   string
			index uint64
		}{{edge.OwnerUID, edge.OwnerIndex}, {edge.PeerUID, edge.PeerIndex}} {
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: endpoint.uid, UserIndex: endpoint.index, LoginOrdinal: endpoint.index}); err != nil {
				t.Fatalf("endpoint Login(%d): %v", endpoint.index, err)
			}
		}
		for ordinal := uint64(0); ordinal < 10_000; ordinal++ {
			schedule, err := fixture.schedule.Channel(ordinal, edge.OwnerIndex, edge.PeerIndex)
			if err != nil {
				t.Fatalf("Channel(%d): %v", ordinal, err)
			}
			if schedule.Class == LifecycleRotating || schedule.Class == LifecycleLong {
				activated, err := fixture.engine.ActivateRelationship(edge, ordinal)
				if err != nil || !activated {
					t.Fatalf("ActivateRelationship(%d) = %v, %v", owner, activated, err)
				}
				return edge
			}
		}
		t.Fatalf("no active schedule for owner %d", owner)
		return RelationshipEdge{}
	}
	offlineTarget := activate(200)
	_ = activate(210)
	if err := fixture.engine.Logout(offlineTarget.PeerUID); err != nil {
		t.Fatalf("offline target Logout: %v", err)
	}

	now := fixture.clock.Now()
	personRoutes := make([]engineSentRoute, 0, 100)
	seenRoutes := 0
	for len(personRoutes) < 100 {
		if _, err := fixture.engine.Tick(now, []uint64{1, 0, 0}); err != nil {
			t.Fatalf("Tick at person route %d: %v", len(personRoutes), err)
		}
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("Advance at person route %d: %v", len(personRoutes), err)
		}
		routes := fixture.factory.sentRoutes()
		for _, route := range routes[seenRoutes:] {
			if route.packet.ChannelType == uint8(frame.ChannelTypePerson) {
				personRoutes = append(personRoutes, route)
			}
		}
		seenRoutes = len(routes)
	}
	personRoutes = personRoutes[:100]
	sampled := 0
	nonsampledOffline := 0
	for index, route := range personRoutes {
		marker, err := DecodePayloadMarker(route.packet.Payload)
		if err != nil {
			t.Fatalf("DecodePayloadMarker(%d): %v", index, err)
		}
		correlate, err := fixture.verifier.ShouldCorrelate(LogicalSend{LogicalSend: marker.LogicalSend, WorkerID: marker.WorkerID})
		if err != nil {
			t.Fatalf("ShouldCorrelate(%d): %v", index, err)
		}
		targetOnline := fixture.pool.IsOnline(route.packet.ChannelID)
		if correlate {
			sampled++
			if !targetOnline {
				t.Fatalf("sampled person route %d has offline recipient %q", index, route.packet.ChannelID)
			}
		} else if !targetOnline {
			nonsampledOffline++
		}
	}
	if sampled != 1 || nonsampledOffline == 0 {
		t.Fatalf("100 person routes sampled=%d non-sampled-offline=%d, want exactly 1 and >0", sampled, nonsampledOffline)
	}
}

func TestEngineSampledGroupRouteRequiresDistinctOnlineRecipient(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 256, InflightCapacity: 16, MaxWorkPerAdvance: 16})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	group, err := fixture.engine.generator.catalog.Group(0)
	if err != nil {
		t.Fatalf("Group(0): %v", err)
	}
	firstIndex, err := group.MemberIndex(0)
	if err != nil {
		t.Fatalf("first MemberIndex: %v", err)
	}
	firstUID := fixture.identity.UID(firstIndex)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: firstUID, UserIndex: firstIndex, LoginOrdinal: 0}); err != nil {
		t.Fatalf("first member Login: %v", err)
	}
	var grant TrafficIntent
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		scoped, err := scopedLogicalOrdinal(1, LogicalDomainGroup, ordinal)
		if err != nil {
			t.Fatalf("scopedLogicalOrdinal(%d): %v", ordinal, err)
		}
		candidate := LogicalSend{LogicalSend: scoped, WorkerID: 0, Kind: TrafficGroup}
		sampled, err := fixture.verifier.ShouldCorrelate(candidate)
		if err != nil {
			t.Fatalf("ShouldCorrelate(%d): %v", ordinal, err)
		}
		if sampled {
			grant = TrafficIntent{Logical: candidate, Kind: TrafficGroup, ChannelID: group.ID, PayloadBytes: 256, Domain: LogicalDomainGroup}
			break
		}
	}
	if grant.Logical.LogicalSend == 0 {
		t.Fatal("exact 100-cycle produced no sampled group grant")
	}
	route := func() (TrafficIntent, error) {
		t.Helper()
		response := make(chan struct {
			intent TrafficIntent
			err    error
		}, 1)
		if err := fixture.engine.enqueue(engineCommand{run: func() {
			intent, routeErr := fixture.engine.routeGroupGrant(grant)
			response <- struct {
				intent TrafficIntent
				err    error
			}{intent: intent, err: routeErr}
		}}); err != nil {
			return TrafficIntent{}, err
		}
		result := <-response
		return result.intent, result.err
	}
	if _, err := route(); err == nil {
		t.Fatal("sampled group grant routed with only its sender online")
	} else {
		assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	}
	if snapshot := fixture.verifier.Snapshot(); snapshot.PendingCurrent != 0 || snapshot.CorrelationCurrent != 0 {
		t.Fatalf("ineligible sampled group route registered verification state: %+v", snapshot)
	}

	secondIndex, err := group.MemberIndex(1)
	if err != nil {
		t.Fatalf("second MemberIndex: %v", err)
	}
	secondUID := fixture.identity.UID(secondIndex)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: secondUID, UserIndex: secondIndex, LoginOrdinal: 1}); err != nil {
		t.Fatalf("second member Login: %v", err)
	}
	routed, err := route()
	if err != nil {
		t.Fatalf("eligible sampled group route: %v", err)
	}
	validSender := routed.Logical.Sender == firstUID || routed.Logical.Sender == secondUID
	recipientUID := firstUID
	if routed.Logical.Sender == firstUID {
		recipientUID = secondUID
	}
	if !validSender || !fixture.pool.IsOnline(recipientUID) {
		t.Fatalf("sampled group route = %+v, distinct recipient %q online=%v", routed.Logical, recipientUID, fixture.pool.IsOnline(recipientUID))
	}
	now := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(routed, now); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if snapshot := fixture.verifier.Snapshot(); snapshot.Sampled != 1 || snapshot.CorrelationCurrent != 1 {
		t.Fatalf("eligible sampled group verification state = %+v", snapshot)
	}
}

func TestEngineDueActivityIsDeferredInsteadOfDroppedWhenRouteTemporarilyIneligible(t *testing.T) {
	for _, test := range []struct {
		name          string
		loginSender   bool
		loginTarget   bool
		requireSample bool
	}{
		{name: "sampled_target_offline", loginSender: true, requireSample: true},
		{name: "sender_offline", loginTarget: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 64, MaxWorkPerAdvance: 64})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer fixture.engine.Stop()
			senderIndex, targetIndex := uint64(500), uint64(501)
			sender, target := fixture.identity.UID(senderIndex), fixture.identity.UID(targetIndex)
			login := func(index uint64, uid string) {
				t.Helper()
				if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index}); err != nil {
					t.Fatalf("Login(%d): %v", index, err)
				}
			}
			if test.loginSender {
				login(senderIndex, sender)
			}
			if test.loginTarget {
				login(targetIndex, target)
			}
			now := fixture.clock.Now()
			response := make(chan error, 1)
			if err := fixture.engine.enqueue(engineCommand{run: func() {
				response <- fixture.engine.addActivity(&engineWork{due: now, kind: engineWorkSend, intent: TrafficIntent{
					Logical: LogicalSend{Sender: sender, Target: target}, Kind: TrafficPerson, Domain: LogicalDomainLifecycle,
				}})
			}}); err != nil {
				t.Fatalf("enqueue activity: %v", err)
			}
			if err := <-response; err != nil {
				t.Fatalf("addActivity: %v", err)
			}
			rawOrdinal := uint64(0)
			if test.requireSample {
				for ; rawOrdinal < 100; rawOrdinal++ {
					scoped, err := scopedLogicalOrdinal(1, LogicalDomainLifecycle, rawOrdinal)
					if err != nil {
						t.Fatalf("scopedLogicalOrdinal: %v", err)
					}
					sampled, err := fixture.verifier.ShouldCorrelate(LogicalSend{LogicalSend: scoped, WorkerID: 0})
					if err != nil {
						t.Fatalf("ShouldCorrelate: %v", err)
					}
					if sampled {
						break
					}
				}
				if rawOrdinal == 100 {
					t.Fatal("no sampled lifecycle grant in exact cycle")
				}
			}
			route := func(raw uint64, at time.Time) (TrafficIntent, error) {
				t.Helper()
				primary, err := scopedLogicalOrdinal(1, LogicalDomainPrimary, raw)
				if err != nil {
					return TrafficIntent{}, err
				}
				grant := TrafficIntent{Logical: LogicalSend{LogicalSend: primary, WorkerID: 0, Kind: TrafficPerson}, Kind: TrafficPerson, PayloadBytes: 256, Domain: LogicalDomainPrimary}
				type routeResult struct {
					intent TrafficIntent
					err    error
				}
				routed := make(chan routeResult, 1)
				if err := fixture.engine.enqueue(engineCommand{run: func() {
					intent, routeErr := fixture.engine.routePersonGrant(grant, at)
					routed <- routeResult{intent: intent, err: routeErr}
				}}); err != nil {
					return TrafficIntent{}, err
				}
				result := <-routed
				return result.intent, result.err
			}
			if _, err := route(rawOrdinal, now); err == nil {
				t.Fatal("temporarily ineligible activity unexpectedly routed")
			} else {
				assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
			}
			if snapshot, _ := fixture.engine.Snapshot(); snapshot.ActivityCurrent != 1 {
				t.Fatalf("temporarily ineligible activity was dropped: %+v", snapshot)
			}
			if !test.loginSender {
				login(senderIndex, sender)
			}
			if test.requireSample {
				nextScoped, err := scopedLogicalOrdinal(1, LogicalDomainLifecycle, rawOrdinal+1)
				if err != nil {
					t.Fatalf("next scoped ordinal: %v", err)
				}
				if sampled, err := fixture.verifier.ShouldCorrelate(LogicalSend{LogicalSend: nextScoped, WorkerID: 0}); err != nil || sampled {
					t.Fatalf("next lifecycle grant sampled=%v err=%v, want non-sampled", sampled, err)
				}
			}
			later := now.Add(time.Nanosecond)
			routed, err := route(rawOrdinal+1, later)
			if err != nil {
				t.Fatalf("deferred eligible route: %v", err)
			}
			if routed.Logical.Sender != sender || routed.Logical.Target != target {
				t.Fatalf("deferred route = %+v, want %q -> %q", routed.Logical, sender, target)
			}
			if snapshot, _ := fixture.engine.Snapshot(); snapshot.ActivityCurrent != 0 {
				t.Fatalf("successfully routed deferred activity retained: %+v", snapshot)
			}
		})
	}
}

func TestEnginePermanentlyOfflineMandatoryActivityExpiresWithoutBeingHiddenByActiveTraffic(t *testing.T) {
	const eligibilityWindow = 10 * time.Nanosecond
	fixture := newEngineTestFixture(t, engineTestLimits{
		WorkCapacity: 64, MaxWorkPerAdvance: 64, ActivityEligibilityWindow: eligibilityWindow,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	activeEdge := fixture.graph.edge(40, 41)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{
		UID: activeEdge.OwnerUID, UserIndex: activeEdge.OwnerIndex, LoginOrdinal: activeEdge.OwnerIndex,
	}); err != nil {
		t.Fatalf("active sender Login: %v", err)
	}
	now := fixture.clock.Now()
	added := make(chan error, 1)
	if err := fixture.engine.enqueue(engineCommand{run: func() {
		fixture.engine.addActiveChannel(engineActiveChannel{edge: activeEdge, direction: DirectionOneWay})
		added <- fixture.engine.addActivity(&engineWork{
			due: now, kind: engineWorkSend,
			intent: TrafficIntent{
				Logical: LogicalSend{Sender: fixture.identity.UID(500), Target: fixture.identity.UID(501)},
				Kind:    TrafficPerson, Domain: LogicalDomainLifecycle,
			},
		})
	}}); err != nil {
		t.Fatalf("enqueue setup: %v", err)
	}
	if err := <-added; err != nil {
		t.Fatalf("add mandatory activity: %v", err)
	}
	route := func(raw uint64, at time.Time) (TrafficIntent, error) {
		t.Helper()
		fixture.clock.Set(at)
		primary, err := scopedLogicalOrdinal(1, LogicalDomainPrimary, raw)
		if err != nil {
			return TrafficIntent{}, err
		}
		response := make(chan struct {
			intent TrafficIntent
			err    error
		}, 1)
		if err := fixture.engine.enqueue(engineCommand{run: func() {
			intent, routeErr := fixture.engine.routePersonGrant(TrafficIntent{
				Logical: LogicalSend{LogicalSend: primary, WorkerID: 0, Kind: TrafficPerson},
				Kind:    TrafficPerson, PayloadBytes: 256, Domain: LogicalDomainPrimary,
			}, at)
			response <- struct {
				intent TrafficIntent
				err    error
			}{intent: intent, err: routeErr}
		}}); err != nil {
			return TrafficIntent{}, err
		}
		result := <-response
		return result.intent, result.err
	}
	findNonSampled := func(start uint64) uint64 {
		t.Helper()
		for raw := start; raw < start+100; raw++ {
			scoped, err := scopedLogicalOrdinal(1, LogicalDomainLifecycle, raw)
			if err != nil {
				t.Fatalf("scoped lifecycle ordinal: %v", err)
			}
			sampled, err := fixture.verifier.ShouldCorrelate(LogicalSend{LogicalSend: scoped, WorkerID: 0})
			if err != nil {
				t.Fatalf("ShouldCorrelate: %v", err)
			}
			if !sampled {
				return raw
			}
		}
		t.Fatal("no non-sampled lifecycle grant in exact cycle")
		return 0
	}
	firstRaw := findNonSampled(0)
	for _, beforeDeadline := range []time.Duration{0, eligibilityWindow / 2, eligibilityWindow - 2*time.Nanosecond} {
		routed, err := route(firstRaw, now.Add(beforeDeadline))
		if err != nil || routed.Logical.Sender != activeEdge.OwnerUID {
			t.Fatalf("active fallback before deadline at %v = %+v, %v", beforeDeadline, routed.Logical, err)
		}
		if snapshot, _ := fixture.engine.Snapshot(); snapshot.ActivityCurrent != 1 || snapshot.ActivityUnderDelivered != 0 {
			t.Fatalf("mandatory activity was not retained inside eligibility window: %+v", snapshot)
		}
		firstRaw = findNonSampled(firstRaw + 1)
	}
	_, err := route(firstRaw, now.Add(eligibilityWindow-time.Nanosecond))
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	expired, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("expired Snapshot: %v", err)
	}
	if expired.ActivityCurrent != 0 || expired.ActivityUnderDelivered != 1 || expired.HarnessInvalid != 1 || expired.Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("expired mandatory activity = %+v", expired)
	}
	assertUnderDeliveryEvidence(t, fixture.evidence.Snapshot(), 1)
	nextRaw := findNonSampled(firstRaw + 1)
	routed, err := route(nextRaw, now.Add(eligibilityWindow+time.Nanosecond))
	if err != nil || routed.Logical.Sender != activeEdge.OwnerUID {
		t.Fatalf("active route after physical expiry = %+v, %v", routed.Logical, err)
	}
	stable, _ := fixture.engine.Snapshot()
	if stable.ActivityCurrent != 0 || stable.ActivityUnderDelivered != 1 || stable.HarnessInvalid != 1 || evidenceCountForClass(fixture.evidence.Snapshot(), FailureClassHarness) != 1 {
		t.Fatalf("expired activity was retried or re-recorded: %+v evidence=%+v", stable, fixture.evidence.Snapshot())
	}
	assertUnderDeliveryEvidence(t, fixture.evidence.Snapshot(), 1)
}

func TestEngineStopAccountsForPendingMandatoryActivityWithoutPollutingDrainedStop(t *testing.T) {
	for _, test := range []struct {
		name          string
		drain         bool
		terminal      bool
		wantAbandoned uint64
	}{
		{name: "pending_is_closed", wantAbandoned: 1},
		{name: "pending_preserves_terminal_product", terminal: true, wantAbandoned: 1},
		{name: "drained_is_clean", drain: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{
				WorkCapacity: 16, MaxWorkPerAdvance: 16, ActivityEligibilityWindow: time.Minute,
			})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			now := fixture.clock.Now()
			senderIndex := uint64(600)
			sender := fixture.identity.UID(senderIndex)
			if test.drain {
				if _, err := fixture.engine.Login(context.Background(), SessionLogin{
					UID: sender, UserIndex: senderIndex, LoginOrdinal: senderIndex,
				}); err != nil {
					t.Fatalf("sender Login: %v", err)
				}
			}
			added := make(chan error, 1)
			if err := fixture.engine.enqueue(engineCommand{run: func() {
				added <- fixture.engine.addActivity(&engineWork{
					due: now, kind: engineWorkSend,
					intent: TrafficIntent{
						Logical: LogicalSend{Sender: sender, Target: fixture.identity.UID(601)},
						Kind:    TrafficPerson, Domain: LogicalDomainLifecycle,
					},
				})
			}}); err != nil {
				t.Fatalf("enqueue mandatory activity: %v", err)
			}
			if err := <-added; err != nil {
				t.Fatalf("add mandatory activity: %v", err)
			}
			if test.drain {
				var raw uint64
				for ; raw < 100; raw++ {
					scoped, err := scopedLogicalOrdinal(1, LogicalDomainLifecycle, raw)
					if err != nil {
						t.Fatalf("scoped lifecycle: %v", err)
					}
					sampled, err := fixture.verifier.ShouldCorrelate(LogicalSend{LogicalSend: scoped, WorkerID: 0})
					if err != nil {
						t.Fatalf("ShouldCorrelate: %v", err)
					}
					if !sampled {
						break
					}
				}
				primary, err := scopedLogicalOrdinal(1, LogicalDomainPrimary, raw)
				if err != nil {
					t.Fatalf("scoped primary: %v", err)
				}
				routed := make(chan error, 1)
				if err := fixture.engine.enqueue(engineCommand{run: func() {
					_, routeErr := fixture.engine.routePersonGrant(TrafficIntent{
						Logical: LogicalSend{LogicalSend: primary, WorkerID: 0, Kind: TrafficPerson},
						Kind:    TrafficPerson, PayloadBytes: 256, Domain: LogicalDomainPrimary,
					}, now)
					routed <- routeErr
				}}); err != nil {
					t.Fatalf("enqueue route: %v", err)
				}
				if err := <-routed; err != nil {
					t.Fatalf("drain mandatory activity: %v", err)
				}
			}
			if test.terminal {
				terminalIndex := uint64(700)
				terminalUID := fixture.identity.UID(terminalIndex)
				if _, err := fixture.engine.Login(context.Background(), SessionLogin{
					UID: terminalUID, UserIndex: terminalIndex, LoginOrdinal: terminalIndex,
				}); err != nil {
					t.Fatalf("terminal Login: %v", err)
				}
				client := fixture.factory.clients()[0]
				fixture.pool.mu.RLock()
				drainDone := fixture.pool.online[terminalUID].done
				fixture.pool.mu.RUnlock()
				client.readErrors <- &engineFakeReadError{kind: wkproto.ReadErrorTerminal}
				<-drainDone
				if got := fixture.evidence.Snapshot().Classification; got != SyncClassificationProductFailure {
					t.Fatalf("terminal classification before Stop = %q", got)
				}
			}
			if err := fixture.engine.Stop(); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			snapshot, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("stopped Snapshot: %v", err)
			}
			wantHarness := test.wantAbandoned
			wantClassification := SyncClassification("")
			if wantHarness > 0 {
				wantClassification = SyncClassificationHarnessInvalid
			}
			if test.terminal {
				wantClassification = SyncClassificationProductFailure
			}
			if snapshot.ActivityCurrent != 0 || snapshot.ActivityUnderDelivered != test.wantAbandoned || snapshot.HarnessInvalid != wantHarness || snapshot.Classification != wantClassification {
				t.Fatalf("stopped mandatory activity accounting = %+v", snapshot)
			}
			assertUnderDeliveryEvidence(t, fixture.evidence.Snapshot(), wantHarness)
			if test.terminal && evidenceCountForClass(fixture.evidence.Snapshot(), FailureClassReceive) != 1 {
				t.Fatalf("terminal product evidence was lost or duplicated: %+v", fixture.evidence.Snapshot())
			}
		})
	}
}

func TestEngineRevisitRequiresExplicitColdRuntimeEvidence(t *testing.T) {
	t.Parallel()
	for _, confirm := range []bool{false, true} {
		confirm := confirm
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[confirm], func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer fixture.engine.Stop()
			edge := fixture.graph.Incoming(18).Items[0]
			relationshipOrdinal, schedule := findLifecycleSchedule(t, fixture.schedule, edge, LifecycleRevisit)
			ownerLogin := findLoginLongerThan(t, fixture.schedule, schedule.RevisitAfter)
			peerLogin := findLoginLongerThan(t, fixture.schedule, schedule.RevisitAfter+time.Minute)
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: ownerLogin}); err != nil {
				t.Fatalf("owner Login: %v", err)
			}
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.PeerUID, UserIndex: edge.PeerIndex, LoginOrdinal: peerLogin}); err != nil {
				t.Fatalf("peer Login: %v", err)
			}
			if activated, err := fixture.engine.ActivateRelationship(edge, relationshipOrdinal); err != nil || !activated {
				t.Fatalf("ActivateRelationship = %v, %v", activated, err)
			}
			before, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot before: %v", err)
			}
			if before.ColdEvidencePending != 1 {
				t.Fatalf("cold pending before = %+v", before)
			}
			if confirm {
				approved, err := fixture.engine.ApproveColdRevisit(edge.PersonChannelID)
				if err != nil || !approved {
					t.Fatalf("ApproveColdRevisit = %v, %v", approved, err)
				}
			}
			due := fixture.clock.Now().Add(schedule.RevisitAfter)
			fixture.clock.Set(due)
			if _, err := fixture.engine.Advance(due); err != nil {
				t.Fatalf("Advance revisit: %v", err)
			}
			after, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot after: %v", err)
			}
			wantActivity := before.ActivityCurrent
			if confirm {
				wantActivity += schedule.RevisitMessages
			}
			if after.ActivityCurrent != wantActivity || after.ColdEvidencePending != 0 {
				t.Fatalf("revisit evidence transition = before %+v after %+v want activity %d", before, after, wantActivity)
			}
		})
	}
}

func TestEngineApprovedRevisitUsesEitherOnlineEndpointAndRetainsFullyOfflineTimer(t *testing.T) {
	for _, test := range []struct {
		name        string
		logoutOwner bool
		logoutPeer  bool
	}{
		{name: "owner_only_online", logoutPeer: true},
		{name: "peer_only_online", logoutOwner: true},
		{name: "both_offline", logoutOwner: true, logoutPeer: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 256, MaxWorkPerAdvance: 256})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer fixture.engine.Stop()
			edge := fixture.graph.Incoming(18).Items[0]
			relationshipOrdinal, schedule := findLifecycleSchedule(t, fixture.schedule, edge, LifecycleRevisit)
			loginOrdinal := findLoginLongerThan(t, fixture.schedule, schedule.RevisitAfter+time.Minute)
			for _, endpoint := range []struct {
				uid   string
				index uint64
			}{{edge.OwnerUID, edge.OwnerIndex}, {edge.PeerUID, edge.PeerIndex}} {
				if _, err := fixture.engine.Login(context.Background(), SessionLogin{
					UID: endpoint.uid, UserIndex: endpoint.index, LoginOrdinal: loginOrdinal,
				}); err != nil {
					t.Fatalf("Login(%q): %v", endpoint.uid, err)
				}
			}
			if activated, err := fixture.engine.ActivateRelationship(edge, relationshipOrdinal); err != nil || !activated {
				t.Fatalf("ActivateRelationship = %v, %v", activated, err)
			}
			if approved, err := fixture.engine.ApproveColdRevisit(edge.PersonChannelID); err != nil || !approved {
				t.Fatalf("ApproveColdRevisit = %v, %v", approved, err)
			}
			before, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot before: %v", err)
			}
			if test.logoutOwner {
				if err := fixture.engine.Logout(edge.OwnerUID); err != nil {
					t.Fatalf("owner Logout: %v", err)
				}
			}
			if test.logoutPeer {
				if err := fixture.engine.Logout(edge.PeerUID); err != nil {
					t.Fatalf("peer Logout: %v", err)
				}
			}

			due := fixture.clock.Now().Add(schedule.RevisitAfter)
			fixture.clock.Set(due)
			if _, err := fixture.engine.Advance(due); err != nil {
				t.Fatalf("Advance revisit: %v", err)
			}
			after, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot after: %v", err)
			}
			if test.logoutOwner && test.logoutPeer {
				if after.ActivityCurrent != before.ActivityCurrent || after.ColdEvidencePending != 1 || after.ActiveLifecycleTimers != 1 {
					t.Fatalf("fully offline revisit was not retained: before=%+v after=%+v", before, after)
				}
				return
			}
			wantSender := edge.OwnerUID
			if test.logoutOwner {
				wantSender = edge.PeerUID
			}
			if after.ActivityCurrent != before.ActivityCurrent+schedule.RevisitMessages || after.ColdEvidencePending != 0 || after.ActiveLifecycleTimers != 0 {
				t.Fatalf("one-sided revisit transition: before=%+v after=%+v", before, after)
			}
			checked := make(chan error, 1)
			if err := fixture.engine.enqueue(engineCommand{run: func() {
				for _, activity := range fixture.engine.activity {
					if activity.intent.Domain == LogicalDomainRevisit && activity.intent.Logical.Sender != wantSender {
						checked <- fmt.Errorf("revisit sender = %q, want online endpoint %q", activity.intent.Logical.Sender, wantSender)
						return
					}
				}
				checked <- nil
			}}); err != nil {
				t.Fatalf("inspect activity: %v", err)
			}
			if err := <-checked; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEngineReturningCandidateColdRevisitUsesOldEdgeAndRevisitIdentityDomain(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 100, SessionDuration: 2 * time.Hour,
		WorkCapacity: 4_096, InflightCapacity: 128, MaxWorkPerAdvance: 4_096,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	for index := uint64(0); index < 100; index++ {
		uid := fixture.identity.UID(index)
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index}); err != nil {
			t.Fatalf("Login(%d): %v", index, err)
		}
	}
	var candidate ReturningCandidate
	var loginOrdinal uint64
	for ordinal := uint64(0); ordinal < 1_000; ordinal++ {
		planned, err := fixture.graph.ReturningCandidate(100, ordinal, 100)
		if err != nil {
			t.Fatalf("ReturningCandidate(%d): %v", ordinal, err)
		}
		if planned.Available && planned.ConversationCount > 0 {
			candidate, loginOrdinal = planned, ordinal
			break
		}
	}
	if !candidate.Available {
		t.Fatal("no deterministic returning candidate was available")
	}
	now := fixture.clock.Now()
	if err := fixture.engine.scheduleReturningCandidate(candidate, loginOrdinal, now); err != nil {
		t.Fatalf("scheduleReturningCandidate: %v", err)
	}
	conversation := candidate.Conversations[0]
	approved, err := fixture.engine.ApproveColdRevisit(conversation.PersonChannelID)
	if err != nil || !approved {
		t.Fatalf("ApproveColdRevisit = %v, %v", approved, err)
	}
	delay, err := fixture.schedule.durationInRange(
		"returning-login-revisit-delay/v1", minimumRevisitDelay, maximumRevisitDelay,
		loginOrdinal, candidate.UserIndex, 0,
	)
	if err != nil {
		t.Fatalf("revisit delay: %v", err)
	}
	due := now.Add(delay)
	fixture.clock.Set(due)
	if _, err := fixture.engine.Advance(due); err != nil {
		t.Fatalf("revisit deadline Advance: %v", err)
	}
	if snapshot, _ := fixture.engine.Snapshot(); snapshot.ActivityCurrent < 2 || snapshot.ActivityCurrent > 5 {
		t.Fatalf("returning revisit activity = %+v, want 2..5", snapshot)
	}
	before := len(fixture.factory.sentRoutes())
	for tick := 0; tick < 10; tick++ {
		if _, err := fixture.engine.Tick(due, []uint64{1, 0, 0}); err != nil {
			t.Fatalf("revisit Tick(%d): %v", tick, err)
		}
		if _, err := fixture.engine.Advance(due); err != nil {
			t.Fatalf("revisit send Advance(%d): %v", tick, err)
		}
		routes := fixture.factory.sentRoutes()[before:]
		for _, route := range routes {
			marker, err := DecodePayloadMarker(route.packet.Payload)
			if err != nil {
				t.Fatalf("DecodePayloadMarker: %v", err)
			}
			domain := LogicalDomain(marker.LogicalSend >> (logicalGenerationBits + logicalOrdinalBits))
			if domain == LogicalDomainRevisit {
				oldEdge := (route.uid == candidate.UserUID && route.packet.ChannelID == conversation.PeerUID) ||
					(route.uid == conversation.PeerUID && route.packet.ChannelID == candidate.UserUID)
				if route.packet.ChannelType != uint8(frame.ChannelTypePerson) || !oldEdge {
					t.Fatalf("revisit route = %q -> %q/%d, want old edge %q <-> %q", route.uid, route.packet.ChannelID, route.packet.ChannelType, candidate.UserUID, conversation.PeerUID)
				}
				return
			}
		}
	}
	t.Fatal("approved returning cold revisit did not produce a revisit-domain SEND")
}

func TestEngineReturningColdRevisitUsesOnlineReturningSenderWhenOldPeerIsOffline(t *testing.T) {
	for _, returningOfflineAtDue := range []bool{false, true} {
		name := map[bool]string{false: "peer_only_offline", true: "returning_temporarily_offline"}[returningOfflineAtDue]
		t.Run(name, func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{SessionDuration: 2 * time.Hour, WorkCapacity: 256, MaxWorkPerAdvance: 256})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer fixture.engine.Stop()
			var candidate ReturningCandidate
			var loginOrdinal uint64
			for ordinal := uint64(0); ordinal < 10_000; ordinal++ {
				planned, err := fixture.graph.ReturningCandidate(1_000, ordinal, 1_000)
				if err != nil {
					t.Fatalf("ReturningCandidate(%d): %v", ordinal, err)
				}
				if planned.Available && planned.ConversationCount == 1 && planned.UserIndex >= 100 && planned.Conversations[0].PeerIndex >= 100 {
					candidate, loginOrdinal = planned, ordinal
					break
				}
			}
			if !candidate.Available {
				t.Fatal("no isolated returning candidate available")
			}
			loginReturning := func() {
				t.Helper()
				if _, err := fixture.engine.Login(context.Background(), SessionLogin{
					UID: candidate.UserUID, UserIndex: candidate.UserIndex, LoginOrdinal: loginOrdinal,
				}); err != nil {
					t.Fatalf("returning Login: %v", err)
				}
			}
			loginReturning()
			now := fixture.clock.Now()
			if err := fixture.engine.scheduleReturningCandidate(candidate, loginOrdinal, now); err != nil {
				t.Fatalf("scheduleReturningCandidate: %v", err)
			}
			conversation := candidate.Conversations[0]
			if approved, err := fixture.engine.ApproveColdRevisit(conversation.PersonChannelID); err != nil || !approved {
				t.Fatalf("ApproveColdRevisit = %v, %v", approved, err)
			}
			delay, err := fixture.schedule.durationInRange(
				"returning-login-revisit-delay/v1", minimumRevisitDelay, maximumRevisitDelay,
				loginOrdinal, candidate.UserIndex, 0,
			)
			if err != nil {
				t.Fatalf("revisit delay: %v", err)
			}
			due := now.Add(delay)
			if returningOfflineAtDue {
				if err := fixture.engine.Logout(candidate.UserUID); err != nil {
					t.Fatalf("returning Logout: %v", err)
				}
			}
			fixture.clock.Set(due)
			if _, err := fixture.engine.Advance(due); err != nil {
				t.Fatalf("deadline Advance: %v", err)
			}
			if returningOfflineAtDue {
				snapshot, _ := fixture.engine.Snapshot()
				if snapshot.ActivityCurrent != 0 || snapshot.ColdEvidencePending != 1 || snapshot.ActiveLifecycleTimers != 1 {
					t.Fatalf("offline returning work was not retained: %+v", snapshot)
				}
				loginReturning()
				due = due.Add(time.Nanosecond)
				fixture.clock.Set(due)
				if _, err := fixture.engine.Advance(due); err != nil {
					t.Fatalf("relogin Advance: %v", err)
				}
			}
			snapshot, _ := fixture.engine.Snapshot()
			if snapshot.ActivityCurrent < 2 || snapshot.ActivityCurrent > 5 || snapshot.ColdEvidencePending != 0 {
				t.Fatalf("returning-only revisit activity = %+v, want 2..5 and no pending timer", snapshot)
			}
			var raw uint64
			for ; raw < 100; raw++ {
				scoped, err := scopedLogicalOrdinal(1, LogicalDomainRevisit, raw)
				if err != nil {
					t.Fatalf("scoped revisit: %v", err)
				}
				sampled, err := fixture.verifier.ShouldCorrelate(LogicalSend{LogicalSend: scoped, WorkerID: 0})
				if err != nil {
					t.Fatalf("ShouldCorrelate: %v", err)
				}
				if !sampled {
					break
				}
			}
			primary, err := scopedLogicalOrdinal(1, LogicalDomainPrimary, raw)
			if err != nil {
				t.Fatalf("scoped primary: %v", err)
			}
			routed := make(chan struct {
				intent TrafficIntent
				err    error
			}, 1)
			if err := fixture.engine.enqueue(engineCommand{run: func() {
				intent, routeErr := fixture.engine.routePersonGrant(TrafficIntent{
					Logical: LogicalSend{LogicalSend: primary, WorkerID: 0, Kind: TrafficPerson},
					Kind:    TrafficPerson, PayloadBytes: 256, Domain: LogicalDomainPrimary,
				}, due)
				routed <- struct {
					intent TrafficIntent
					err    error
				}{intent: intent, err: routeErr}
			}}); err != nil {
				t.Fatalf("enqueue route: %v", err)
			}
			result := <-routed
			if result.err != nil {
				t.Fatalf("route returning revisit: %v", result.err)
			}
			if result.intent.Logical.Sender != candidate.UserUID || result.intent.Logical.Target != conversation.PeerUID || fixture.pool.IsOnline(conversation.PeerUID) {
				t.Fatalf("returning revisit route = %+v peer_online=%v", result.intent.Logical, fixture.pool.IsOnline(conversation.PeerUID))
			}
		})
	}
}

func findLifecycleSchedule(t *testing.T, model ScheduleModel, edge RelationshipEdge, class LifecycleClass) (uint64, ChannelSchedule) {
	t.Helper()
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		schedule, err := model.Channel(ordinal, edge.OwnerIndex, edge.PeerIndex)
		if err != nil {
			t.Fatalf("Channel(%d): %v", ordinal, err)
		}
		if schedule.Class == class {
			return ordinal, schedule
		}
	}
	t.Fatalf("no lifecycle class %d in exact cycle", class)
	return 0, ChannelSchedule{}
}

func findLoginLongerThan(t *testing.T, model ScheduleModel, minimum time.Duration) uint64 {
	t.Helper()
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		schedule, err := model.Login(ordinal)
		if err != nil {
			t.Fatalf("Login(%d): %v", ordinal, err)
		}
		if schedule.SessionDuration > minimum {
			return ordinal
		}
	}
	t.Fatalf("no session exceeds %v", minimum)
	return 0
}

func TestEngineDelayedCompletionUsesExactlyThreeStableRetries(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	fixture.factory.sendErrors = []error{errors.New("temporary one"), errors.New("temporary two"), errors.New("temporary three")}
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()

	uid := fixture.identity.UID(4)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 4, LoginOrdinal: 3}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	intent := fixture.intent(t, uid, "retry-group", 41, TrafficGroup)
	if err := fixture.engine.SubmitGranted(intent, fixture.clock.Now()); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	now := fixture.clock.Now()
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("attempt zero: %v", err)
	}
	for completed := uint8(0); completed < 3; completed++ {
		attempt, err := fixture.retry.Attempt(intent.Logical, completed+1)
		if err != nil {
			t.Fatalf("retry Attempt(%d): %v", completed+1, err)
		}
		now = now.Add(attempt.Delay)
		fixture.clock.Set(now)
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("retry %d Advance: %v", completed+1, err)
		}
	}
	packets := fixture.factory.sentPackets()
	if len(packets) != 4 {
		t.Fatalf("SEND attempts = %d, want 4", len(packets))
	}
	for index, packet := range packets {
		if packet.ClientMsgNo != intent.Logical.ClientMsgNo {
			t.Fatalf("attempt %d client_msg_no = %q, want %q", index, packet.ClientMsgNo, intent.Logical.ClientMsgNo)
		}
	}
	ack := &frame.SendackPacket{
		ClientMsgNo: intent.Logical.ClientMsgNo, MessageID: 901, MessageSeq: 77, ReasonCode: frame.ReasonSuccess,
	}
	verificationErr := fixture.verifier.HandleSendack(ack)
	if verificationErr != nil {
		t.Fatalf("HandleSendack: %v", verificationErr)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, verificationErr); err != nil {
		t.Fatalf("ObserveSendack: %v", err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.RetryAttempts != 3 || snapshot.InflightCurrent != 0 || snapshot.FutureCurrent != 0 || snapshot.FinalFailures != 0 {
		t.Fatalf("retry snapshot = %+v", snapshot)
	}
	verifierSnapshot := fixture.verifier.Snapshot()
	if verifierSnapshot.Attempts != 4 || verifierSnapshot.RetryAttempts != 3 || verifierSnapshot.Acknowledged != 1 {
		t.Fatalf("verifier attempts = %+v", verifierSnapshot)
	}
}

func TestEngineNonRetriableSendackFailsWithoutRetry(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(5)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 5, LoginOrdinal: 5}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	intent := fixture.intent(t, uid, "non-retry-group", 45, TrafficGroup)
	if err := fixture.engine.SubmitGranted(intent, fixture.clock.Now()); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	if _, err := fixture.engine.Advance(fixture.clock.Now()); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	ack := &frame.SendackPacket{ClientMsgNo: intent.Logical.ClientMsgNo, ReasonCode: frame.ReasonAuthFail}
	verificationErr := fixture.verifier.HandleSendack(ack)
	var rejected *SendackRejectedError
	if !errors.As(verificationErr, &rejected) {
		t.Fatalf("HandleSendack = %v, want rejection decision", verificationErr)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, verificationErr); err == nil {
		t.Fatal("non-retriable rejection did not return terminal product failure")
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.RetryQueueDepth != 0 || snapshot.RetryAttempts != 0 || snapshot.InflightCurrent != 0 || snapshot.FinalFailures != 1 {
		t.Fatalf("non-retriable snapshot = %+v", snapshot)
	}
}

func TestEngineLateSuccessfulSendackCancelsScheduledRetry(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 6}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	intent := fixture.intent(t, uid, "late-ack-group", 46, TrafficGroup)
	now := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, now); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("initial Advance: %v", err)
	}
	now = now.Add(20 * time.Millisecond)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("timeout Advance: %v", err)
	}
	if snapshot, _ := fixture.engine.Snapshot(); snapshot.RetryQueueDepth != 1 {
		t.Fatalf("retry queue before late ACK = %+v", snapshot)
	}
	ack := &frame.SendackPacket{
		ClientMsgNo: intent.Logical.ClientMsgNo, MessageID: 902, MessageSeq: 78, ReasonCode: frame.ReasonSuccess,
	}
	verificationErr := fixture.verifier.HandleSendack(ack)
	if verificationErr != nil {
		t.Fatalf("HandleSendack: %v", verificationErr)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, verificationErr); err != nil {
		t.Fatalf("ObserveSendack: %v", err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.RetryQueueDepth != 0 || snapshot.InflightCurrent != 0 || snapshot.RetryAttempts != 0 {
		t.Fatalf("late ACK snapshot = %+v", snapshot)
	}
}

func TestEngineNonTerminalAsyncSendErrorSchedulesOwnedRetryWithoutDisconnect(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{})
	readCycles := make(chan struct{})
	fixture.factory.readCycles = readCycles
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(8)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 8, LoginOrdinal: 7}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	<-readCycles
	intent := fixture.intent(t, uid, "async-error-group", 47, TrafficGroup)
	now := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, now); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	client := fixture.factory.clients()[0]
	client.readErrors <- &engineFakeReadError{kind: wkproto.ReadErrorNonTerminal, clientMsgNo: intent.Logical.ClientMsgNo}
	<-client.readReturned
	<-readCycles
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.RetryQueueDepth != 1 || snapshot.InflightCurrent != 1 || snapshot.Online != 1 || snapshot.FinalFailures != 0 {
		t.Fatalf("async SEND error snapshot = %+v", snapshot)
	}
	if fixture.evidence.Snapshot().Classification == SyncClassificationProductFailure {
		t.Fatalf("async SEND error became product evidence: %+v", fixture.evidence.Snapshot())
	}
}

func TestEngineQueueAndCPUSaturationAreHarnessInvalid(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 2, MaxWorkPerAdvance: 1})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(6)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 6, LoginOrdinal: 4}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	now := fixture.clock.Now()
	for ordinal := uint64(1); ordinal <= 3; ordinal++ {
		err := fixture.engine.SubmitGranted(fixture.intent(t, uid, "capacity-group", ordinal, TrafficGroup), now)
		if ordinal <= 2 && err != nil {
			t.Fatalf("Submit(%d): %v", ordinal, err)
		}
		if ordinal == 3 {
			assertRuntimeFailure(t, err, RuntimeFailureEngineQueueSaturated)
		}
	}
	_, err := fixture.engine.Advance(now)
	assertRuntimeFailure(t, err, RuntimeFailureEngineCPUSaturated)
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.QueuePeak != 2 || snapshot.QueueCurrent != 1 || snapshot.InflightCurrent != 1 || snapshot.HarnessInvalid != 2 {
		t.Fatalf("saturation snapshot = %+v", snapshot)
	}
	if snapshot.Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("classification = %q, want harness_invalid", snapshot.Classification)
	}
}

func TestEngineRepeatedStartStopReturnsToBaselineWithoutRetainedIdentity(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 1})
	firstUID := fixture.identity.UID(21)
	secondUID := fixture.identity.UID(22)
	for run, uid := range []string{firstUID, secondUID} {
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("Start(%d): %v", run, err)
		}
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: uint64(21 + run), LoginOrdinal: uint64(run)}); err != nil {
			t.Fatalf("Login(%d): %v", run, err)
		}
		if run == 0 {
			due := fixture.clock.Now().Add(time.Hour)
			if err := fixture.engine.SubmitGranted(fixture.intent(t, uid, "first-generation", 901, TrafficGroup), due); err != nil {
				t.Fatalf("first generation Submit: %v", err)
			}
			assertRuntimeFailure(t, fixture.engine.SubmitGranted(fixture.intent(t, uid, "first-generation", 902, TrafficGroup), due), RuntimeFailureEngineQueueSaturated)
		} else {
			snapshot, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("second generation Snapshot: %v", err)
			}
			if snapshot.Classification != "" || snapshot.HarnessInvalid != 0 {
				t.Fatalf("second generation retained first evidence: %+v", snapshot)
			}
		}
		if run == 1 && fixture.pool.IsOnline(firstUID) {
			t.Fatal("second start retained first-run identity")
		}
		if err := fixture.engine.Stop(); err != nil {
			t.Fatalf("Stop(%d): %v", run, err)
		}
		snapshot, err := fixture.engine.Snapshot()
		if err != nil {
			t.Fatalf("stopped Snapshot(%d): %v", run, err)
		}
		if snapshot.Running || snapshot.ActiveLoops != 0 || snapshot.Online != 0 || snapshot.QueueCurrent != 0 || snapshot.RetryQueueDepth != 0 || snapshot.InflightCurrent != 0 {
			t.Fatalf("stopped baseline(%d) = %+v", run, snapshot)
		}
	}
	if snapshot, _ := fixture.engine.Snapshot(); snapshot.Generation != 2 {
		t.Fatalf("generation = %d, want 2", snapshot.Generation)
	}
	for _, client := range fixture.factory.clients() {
		select {
		case <-client.readExited:
		default:
			t.Fatal("Stop returned before client drain exited")
		}
	}
}

func TestEngineGenerationScopesRealPrimaryLifecycleGroupAndCanaryIdentities(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		WorkCapacity: 8_192, InflightCapacity: 256, MaxWorkPerAdvance: 512,
	})
	fixture.factory.autoAck = true
	run := func() map[string]struct{} {
		t.Helper()
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		for index := uint64(0); index < 100; index++ {
			uid := fixture.identity.UID(index)
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index}); err != nil {
				t.Fatalf("Login(%d): %v", index, err)
			}
			if _, _, err := fixture.engine.ObserveNewUser(index); err != nil {
				t.Fatalf("ObserveNewUser(%d): %v", index, err)
			}
		}
		before := len(fixture.factory.sentPackets())
		now := fixture.clock.Now().Add(time.Minute)
		fixture.clock.Set(now)
		if tick, err := fixture.engine.Tick(now, []uint64{1_000, 1_000, 1_000}); err != nil || tick.Released != 100 {
			t.Fatalf("Tick = %+v, %v", tick, err)
		}
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("Advance: %v", err)
		}
		packets := fixture.factory.sentPackets()[before:]
		if len(packets) != 101 {
			t.Fatalf("primary+canary packets = %d, want 101", len(packets))
		}
		identities := make(map[string]struct{}, len(packets))
		domains := make(map[LogicalDomain]int)
		for _, packet := range packets {
			if _, duplicate := identities[packet.ClientMsgNo]; duplicate {
				t.Fatalf("duplicate identity inside generation: %q", packet.ClientMsgNo)
			}
			identities[packet.ClientMsgNo] = struct{}{}
			marker, err := DecodePayloadMarker(packet.Payload)
			if err != nil {
				t.Fatalf("DecodePayloadMarker: %v", err)
			}
			domains[LogicalDomain(marker.LogicalSend>>(logicalGenerationBits+logicalOrdinalBits))]++
		}
		for _, domain := range []LogicalDomain{LogicalDomainLifecycle, LogicalDomainGroup, LogicalDomainCanary} {
			if domains[domain] == 0 {
				t.Fatalf("generation did not emit domain %d: %v", domain, domains)
			}
		}
		if err := fixture.engine.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		return identities
	}
	first := run()
	second := run()
	for identity := range first {
		if _, reused := second[identity]; reused {
			t.Fatalf("client_msg_no reused across generations: %q", identity)
		}
	}
}

func TestEngineConcurrentSnapshotsAndStopsJoinWithoutStrandedCaller(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	const snapshots = 32
	started := make(chan struct{}, snapshots)
	results := make(chan error, snapshots+2)
	for range snapshots {
		go func() {
			started <- struct{}{}
			_, err := fixture.engine.Snapshot()
			if err != nil && !errors.Is(err, errEngineNotRunning) {
				results <- err
				return
			}
			results <- nil
		}()
	}
	for range snapshots {
		<-started
	}
	for range 2 {
		go func() { results <- fixture.engine.Stop() }()
	}
	for range snapshots + 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent snapshot/stop: %v", err)
		}
	}
	if snapshot, _ := fixture.engine.Snapshot(); snapshot.Running || snapshot.ActiveLoops != 0 {
		t.Fatalf("joined concurrent stop snapshot = %+v", snapshot)
	}
}

func TestEngineStopCancelsBlockedConnectBeforeWaitingForLogin(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{})
	connectStarted := make(chan context.Context, 1)
	connectRelease := make(chan struct{})
	fixture.factory.connectStarted = connectStarted
	fixture.factory.connectRelease = connectRelease
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	loginDone := make(chan error, 1)
	uid := fixture.identity.UID(29)
	go func() {
		_, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 29, LoginOrdinal: 9})
		loginDone <- err
	}()
	connectCtx := <-connectStarted
	stopDone := make(chan error, 1)
	go func() { stopDone <- fixture.engine.Stop() }()

	<-connectCtx.Done()
	if !errors.Is(context.Cause(connectCtx), context.Canceled) {
		close(connectRelease)
		t.Fatalf("CONNECT context cause = %v, want canceled generation", context.Cause(connectCtx))
	}
	if err := <-loginDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Login error = %v, want context canceled", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestEngineStopCancelsBlockedSessionFactoryBeforeWaitingForLogin(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{})
	factoryStarted := make(chan context.Context, 1)
	fixture.factory.newSessionStarted = factoryStarted
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	loginDone := make(chan error, 1)
	uid := fixture.identity.UID(30)
	go func() {
		_, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 30, LoginOrdinal: 10})
		loginDone <- err
	}()
	factoryCtx := <-factoryStarted
	stopDone := make(chan error, 1)
	go func() { stopDone <- fixture.engine.Stop() }()
	<-factoryCtx.Done()
	if !errors.Is(context.Cause(factoryCtx), context.Canceled) {
		t.Fatalf("factory context cause = %v, want canceled generation", context.Cause(factoryCtx))
	}
	if err := <-loginDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Login error = %v, want context canceled", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestEngineReceiveDrainIsChildOfGenerationContext(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{})
	readContexts := make(chan context.Context, 1)
	fixture.factory.readContexts = readContexts
	type generationContextKey struct{}
	generationCtx := context.WithValue(context.Background(), generationContextKey{}, "generation-owned")
	if err := fixture.engine.Start(generationCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = fixture.engine.Stop() })
	uid := fixture.identity.UID(31)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 31, LoginOrdinal: 11}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	readCtx := <-readContexts
	if got := readCtx.Value(generationContextKey{}); got != "generation-owned" {
		t.Fatalf("receive drain context value = %v, want generation-owned", got)
	}
}

func TestEngineStepBootstrapsThenCompletesSteadyLoginCycleAtEightyTwenty(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 100, NewUsersPerDay: 250_000, WorkCapacity: 8_192, MaxWorkPerAdvance: 256,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()

	now := fixture.clock.Now().Add(100 * time.Second)
	fixture.clock.Set(now)
	bootstrap, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("bootstrap Step: %v", err)
	}
	if bootstrap.LoginsCompleted != 100 || bootstrap.BootstrapNew == 0 || bootstrap.Online != 100 {
		t.Fatalf("bootstrap snapshot = %+v", bootstrap)
	}
	for index := uint64(0); index < 100; index++ {
		if err := fixture.engine.Logout(fixture.identity.UID(index)); err != nil {
			t.Fatalf("bootstrap Logout(%d): %v", index, err)
		}
	}

	now = now.Add(100 * time.Second)
	fixture.clock.Set(now)
	steady, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("steady Step: %v", err)
	}
	if steady.PlannedNew != 80 || steady.PlannedReturning != 20 ||
		steady.AdmittedNew != 80 || steady.AdmittedReturning != 20 ||
		steady.CompletedNew != 80 || steady.CompletedReturning != 20 ||
		steady.LoginsSkipped != 0 || steady.Online != 100 {
		t.Fatalf("steady login cycle = %+v", steady)
	}
	aggregate, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("aggregate Snapshot: %v", err)
	}
	if aggregate.LoginPlannedNew != uint64(bootstrap.PlannedNew+steady.PlannedNew) ||
		aggregate.LoginPlannedReturning != uint64(bootstrap.PlannedReturning+steady.PlannedReturning) ||
		aggregate.LoginAdmittedNew != uint64(bootstrap.AdmittedNew+steady.AdmittedNew) ||
		aggregate.LoginAdmittedReturning != uint64(bootstrap.AdmittedReturning+steady.AdmittedReturning) ||
		aggregate.LoginCompletedNew != uint64(bootstrap.CompletedNew+steady.CompletedNew) ||
		aggregate.LoginCompletedReturning != uint64(bootstrap.CompletedReturning+steady.CompletedReturning) ||
		aggregate.LoginSkipped != uint64(bootstrap.LoginsSkipped+steady.LoginsSkipped) {
		t.Fatalf("aggregate scheduler counters = %+v", aggregate)
	}
	if aggregate.ColdEvidencePending == 0 {
		t.Fatalf("returning logins did not schedule bounded cold revisits: %+v", aggregate)
	}
}

func TestSessionSchedulerAgedReturningMixFeedsEveryFixedGroupCategory(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{Formal: true})
	scheduler := sessionScheduler{workload: fixture.engine.generator.workload, nextNewIndex: 750_000}
	buckets := map[HistoryBucket]int{}
	categories := map[GroupCategory]int{}
	for ordinal := uint64(0); ordinal < 1_000; ordinal++ {
		login, actualKind, candidate, available, err := scheduler.planLogin(
			fixture.pool, fixture.graph, fixture.schedule, fixture.engine.generator.catalog, ordinal, LoginReturning,
		)
		if err != nil || !available || actualKind != LoginReturning {
			t.Fatalf("planLogin(%d) = %+v kind=%d candidate=%+v available=%v err=%v", ordinal, login, actualKind, candidate, available, err)
		}
		buckets[candidate.ActualBucket]++
		if candidate.ActualBucket != HistoryOlder {
			continue
		}
		catalog := fixture.engine.generator.catalog
		group, err := catalog.Group(candidate.UserIndex % uint64(catalog.Count()))
		if err != nil || !group.ContainsIndex(candidate.UserIndex) {
			t.Fatalf("older candidate %d is not in a fixed group roster: %+v group=%+v err=%v", ordinal, candidate, group, err)
		}
		if candidate.UserIndex/uint64(catalog.Count()) == 0 {
			t.Fatalf("older candidate %d reused member zero: %+v", ordinal, candidate)
		}
		categories[group.Category]++
	}
	if buckets[HistoryRecent] != 800 || buckets[HistoryOlder] != 200 {
		t.Fatalf("aged returning history mix = %v, want 800/200", buckets)
	}
	for _, category := range []GroupCategory{GroupSmall, GroupMedium, GroupLarge, GroupVeryLarge} {
		if categories[category] == 0 {
			t.Fatalf("aged roster category %d was not fed: %v", category, categories)
		}
	}
}

func TestFormalSchedulerBootstrapExitsUnderChurnAndBoundsCumulativeNewExcess(t *testing.T) {
	cfg := FormalConfig()
	identity, err := NewIdentitySpace("formal-bootstrap-churn", 101, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	schedule, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel: %v", err)
	}
	start := time.Unix(1_700_000_000, 0)
	scheduler := sessionScheduler{workload: cfg.Workload}
	scheduler.reset(start)
	var sessions engineWorkHeap
	heap.Init(&sessions)
	bootstrapPlanned := map[LoginIdentity]int{}
	bootstrapCompletedNew := 0
	exitSecond := -1
	for second := 1; second <= 7_000 && scheduler.bootstrapping; second++ {
		now := start.Add(time.Duration(second) * time.Second)
		expired := 0
		for len(sessions) > 0 && !sessions[0].due.After(now) {
			heap.Pop(&sessions)
			expired++
		}
		scheduler.addReplacements(uint64(expired))
		budget, releaseErr := scheduler.release(now)
		if releaseErr != nil {
			t.Fatalf("release(%d): %v", second, releaseErr)
		}
		for budget > 0 && len(sessions) < cfg.Workload.OnlineUsers {
			loginSchedule, scheduleErr := schedule.Login(scheduler.loginOrdinal)
			if scheduleErr != nil {
				t.Fatalf("Login(%d): %v", scheduler.loginOrdinal, scheduleErr)
			}
			bootstrapPlanned[loginSchedule.Identity]++
			bootstrapCompletedNew++
			scheduler.loginOrdinal++
			scheduler.nextNewIndex++
			scheduler.consumeOne()
			budget--
			heap.Push(&sessions, &engineWork{due: now.Add(loginSchedule.SessionDuration)})
		}
		if len(sessions) == cfg.Workload.OnlineUsers {
			scheduler.bootstrapping = false
			scheduler.credit = 0
			scheduler.replacements = 0
			exitSecond = second
		}
	}
	if exitSecond < 0 || exitSecond > 7_000 || len(sessions) != cfg.Workload.OnlineUsers {
		t.Fatalf("formal bootstrap did not reach 10k under churn: exit=%d online=%d", exitSecond, len(sessions))
	}
	if excess := bootstrapCompletedNew - bootstrapPlanned[LoginNew]; excess != bootstrapPlanned[LoginReturning] || excess <= 0 {
		t.Fatalf("bootstrap cumulative new excess = %d, planned=%v completed_new=%d", excess, bootstrapPlanned, bootstrapCompletedNew)
	}
	steady := map[LoginIdentity]int{}
	for offset := uint64(0); offset < 100; offset++ {
		loginSchedule, scheduleErr := schedule.Login(scheduler.loginOrdinal + offset)
		if scheduleErr != nil {
			t.Fatalf("steady Login(%d): %v", scheduler.loginOrdinal+offset, scheduleErr)
		}
		steady[loginSchedule.Identity]++
	}
	if steady[LoginNew] != 80 || steady[LoginReturning] != 20 {
		t.Fatalf("post-bootstrap login cycle remained all-new: %v exit_second=%d", steady, exitSecond)
	}
}

func TestEngineAgedRosterRoutesTwoHundredGroupGrantsAtFixedSharesAndCanary(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{
		Formal: true, WorkCapacity: 8_192, InflightCapacity: 512, MaxWorkPerAdvance: 8_192,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	scheduler := sessionScheduler{workload: fixture.engine.generator.workload, nextNewIndex: 750_000}
	completed := map[LoginIdentity]int{}
	loggedGroupMembers := 0
	for ordinal := uint64(0); ordinal < 10_000; ordinal++ {
		loginSchedule, err := fixture.schedule.Login(ordinal)
		if err != nil {
			t.Fatalf("Login schedule(%d): %v", ordinal, err)
		}
		login, actualKind, candidate, available, err := scheduler.planLogin(
			fixture.pool, fixture.graph, fixture.schedule, fixture.engine.generator.catalog, ordinal, loginSchedule.Identity,
		)
		if err != nil || !available {
			t.Fatalf("aged planLogin(%d) = %+v candidate=%+v available=%v err=%v", ordinal, login, candidate, available, err)
		}
		if _, err := fixture.engine.Login(context.Background(), login); err != nil {
			t.Fatalf("aged Login(%d): %v", ordinal, err)
		}
		completed[actualKind]++
		if actualKind == LoginNew {
			scheduler.nextNewIndex++
		} else if candidate.ActualBucket == HistoryOlder {
			loggedGroupMembers++
		}
	}
	if completed[LoginNew] != 8_000 || completed[LoginReturning] != 2_000 || loggedGroupMembers != 400 || fixture.pool.Snapshot().Online != 10_000 {
		t.Fatalf("aged steady online mix = completed %v group_anchors=%d pool=%+v", completed, loggedGroupMembers, fixture.pool.Snapshot())
	}

	type routeResult struct {
		intent TrafficIntent
		err    error
	}
	route := func(grant TrafficIntent) (TrafficIntent, error) {
		t.Helper()
		response := make(chan routeResult, 1)
		if err := fixture.engine.enqueue(engineCommand{run: func() {
			intent, routeErr := fixture.engine.routeGroupGrant(grant)
			response <- routeResult{intent: intent, err: routeErr}
		}}); err != nil {
			return TrafficIntent{}, err
		}
		result := <-response
		return result.intent, result.err
	}
	categories := map[GroupCategory]int{}
	retargeted := 0
	for ordinal := uint64(0); ordinal < 200; ordinal++ {
		group, err := fixture.engine.generator.catalog.PrimaryTarget(ordinal)
		if err != nil {
			t.Fatalf("PrimaryTarget(%d): %v", ordinal, err)
		}
		logicalOrdinal, err := scopedLogicalOrdinal(1, LogicalDomainGroup, ordinal)
		if err != nil {
			t.Fatalf("group logical(%d): %v", ordinal, err)
		}
		grant := TrafficIntent{
			Logical: LogicalSend{LogicalSend: logicalOrdinal, WorkerID: 0, Kind: TrafficGroup},
			Kind:    TrafficGroup, PayloadBytes: 256, ChannelID: group.ID, GroupCategory: group.Category, Domain: LogicalDomainGroup,
		}
		routed, err := route(grant)
		if err != nil {
			t.Fatalf("route group grant %d/%d: %v", ordinal, group.Category, err)
		}
		routedIndex, ok := fixture.engine.generator.catalog.IndexFromGroupID(routed.ChannelID)
		if !ok {
			t.Fatalf("routed group ID %q is outside fixed catalog", routed.ChannelID)
		}
		routedGroup, err := fixture.engine.generator.catalog.Group(routedIndex)
		if err != nil || routedGroup.Category != group.Category {
			t.Fatalf("routed group %d = %+v err=%v, requested category %d", ordinal, routedGroup, err, group.Category)
		}
		senderIndex, ok := fixture.identity.IndexFromUID(routed.Logical.Sender)
		if !ok || !fixture.pool.IsOnline(routed.Logical.Sender) || !routedGroup.ContainsIndex(senderIndex) {
			t.Fatalf("routed sender is not an actual online fixed member: %+v group=%+v index=%d/%v", routed.Logical, routedGroup, senderIndex, ok)
		}
		if routed.ChannelID != group.ID {
			retargeted++
		}
		categories[routedGroup.Category]++
	}
	if categories[GroupSmall] != 160 || categories[GroupMedium] != 30 || categories[GroupLarge] != 10 || retargeted == 0 {
		t.Fatalf("aged 200 group routes = categories %v retargeted=%d, want 160/30/10 and real retargeting", categories, retargeted)
	}
	canary, err := fixture.engine.generator.catalog.VeryLargeCanary(0)
	if err != nil {
		t.Fatalf("VeryLargeCanary: %v", err)
	}
	var canaryRaw uint64
	for ; canaryRaw < 100; canaryRaw++ {
		candidate, scopedErr := scopedLogicalOrdinal(1, LogicalDomainCanary, canaryRaw)
		if scopedErr != nil {
			t.Fatalf("candidate canary logical: %v", scopedErr)
		}
		sampled, sampleErr := fixture.verifier.ShouldCorrelate(LogicalSend{LogicalSend: candidate, WorkerID: 0})
		if sampleErr != nil {
			t.Fatalf("ShouldCorrelate canary: %v", sampleErr)
		}
		if sampled {
			break
		}
	}
	if canaryRaw == 100 {
		t.Fatal("no sampled canary ordinal in exact cycle")
	}
	canaryOrdinal, err := scopedLogicalOrdinal(1, LogicalDomainCanary, canaryRaw)
	if err != nil {
		t.Fatalf("sampled canary logical: %v", err)
	}
	routedCanary, err := route(TrafficIntent{
		Logical: LogicalSend{LogicalSend: canaryOrdinal, WorkerID: 0, Kind: TrafficGroup},
		Kind:    TrafficGroup, PayloadBytes: 256, ChannelID: canary.Group.ID, GroupCategory: GroupVeryLarge, Domain: LogicalDomainCanary,
	})
	if err != nil || routedCanary.ChannelID != canary.Group.ID {
		t.Fatalf("aged canary route = %+v, %v", routedCanary, err)
	}
	canarySender, ok := fixture.identity.IndexFromUID(routedCanary.Logical.Sender)
	if !ok || !canary.Group.ContainsIndex(canarySender) || !fixture.pool.IsOnline(routedCanary.Logical.Sender) {
		t.Fatalf("aged canary sender = %+v index=%d/%v", routedCanary.Logical, canarySender, ok)
	}
}

func TestEngineStepReplacesTerminalSessionWithoutWaitingForCallerCancellation(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 20, NewUsersPerDay: 250_000, WorkCapacity: 2_048, MaxWorkPerAdvance: 128,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now().Add(10 * time.Second)
	fixture.clock.Set(now)
	if bootstrap, err := fixture.engine.Step(context.Background(), now, nil); err != nil || bootstrap.Online != 20 {
		t.Fatalf("bootstrap Step = %+v, %v", bootstrap, err)
	}
	client := fixture.factory.clients()[0]
	fixture.pool.mu.RLock()
	drainDone := fixture.pool.online[client.uid].done
	fixture.pool.mu.RUnlock()
	client.readErrors <- &engineFakeReadError{kind: wkproto.ReadErrorTerminal}
	<-drainDone
	replacement, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("replacement Step: %v", err)
	}
	if replacement.ReplacementLogins != 1 || replacement.LoginsCompleted != 1 || replacement.Online != 20 {
		t.Fatalf("replacement snapshot = %+v", replacement)
	}
}

func TestEngineStepExpiresAndReplacesSessionsAtTheirFakeClockDeadline(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 20, NewUsersPerDay: 250_000, SessionDuration: time.Minute,
		WorkCapacity: 2_048, MaxWorkPerAdvance: 128,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now().Add(10 * time.Second)
	fixture.clock.Set(now)
	if bootstrap, err := fixture.engine.Step(context.Background(), now, nil); err != nil || bootstrap.Online != 20 {
		t.Fatalf("bootstrap Step = %+v, %v", bootstrap, err)
	}
	now = now.Add(time.Minute)
	fixture.clock.Set(now)
	replacement, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("expiry replacement Step: %v", err)
	}
	if replacement.Expired != 20 || replacement.ReplacementLogins != 20 || replacement.LoginsCompleted != 20 || replacement.Online != 20 {
		t.Fatalf("expiry replacement snapshot = %+v", replacement)
	}
	aggregate, err := fixture.engine.Snapshot()
	if err != nil || aggregate.SessionsExpired != 20 || aggregate.LoginReplacements != 20 {
		t.Fatalf("expiry aggregate = %+v, %v", aggregate, err)
	}
}

func TestEngineStepSchedulerCPUBudgetSaturationIsHarnessInvalid(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 100, NewUsersPerDay: 250_000, WorkCapacity: 1_024, MaxWorkPerAdvance: 1,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now().Add(100 * time.Second)
	fixture.clock.Set(now)
	step, err := fixture.engine.Step(context.Background(), now, nil)
	assertRuntimeFailure(t, err, RuntimeFailureSchedulerCPUSaturated)
	if step.LoginsCompleted != 1 || step.Online != 1 || fixture.evidence.Snapshot().Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("scheduler saturation step = %+v evidence=%+v", step, fixture.evidence.Snapshot())
	}
}

func TestEngineRotatingAndLongChannelsConsumePrimaryGrantsOnlyBeforeDeadline(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 100, NewUsersPerDay: 250_000, SessionDuration: 10 * time.Hour,
		WorkCapacity: 8_192, InflightCapacity: 256, MaxWorkPerAdvance: 8_192,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now().Add(30 * time.Second)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Step(context.Background(), now, nil); err != nil {
		t.Fatalf("bootstrap Step: %v", err)
	}
	for iteration := 0; iteration < 100; iteration++ {
		snapshot, _ := fixture.engine.Snapshot()
		if snapshot.ActivityCurrent == 0 {
			break
		}
		if _, err := fixture.engine.Tick(now, []uint64{1_000, 1_000, 1_000}); err != nil {
			t.Fatalf("drain activity Tick(%d): %v", iteration, err)
		}
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("drain activity Advance(%d): %v", iteration, err)
		}
	}
	beforeDeadline, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot before deadline: %v", err)
	}
	if beforeDeadline.ActivityCurrent != 0 || beforeDeadline.ActiveHotChannels == 0 {
		t.Fatalf("active lifecycle before deadline = %+v", beforeDeadline)
	}
	before := fixture.factory.sentCount()
	activeTick, err := fixture.engine.Tick(now, []uint64{1_000, 1_000, 1_000})
	if err != nil {
		t.Fatalf("active Tick: %v", err)
	}
	if activeTick.Released != 100 || activeTick.Person != 90 || activeTick.Group != 10 || activeTick.PayloadCounts != [4]uint64{70, 25, 4, 1} {
		t.Fatalf("active lifecycle changed primary denominators: %+v", activeTick)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("active Advance: %v", err)
	}
	if sent := fixture.factory.sentCount() - before; sent != 100 {
		t.Fatalf("active first-attempt sends = %d, want 100", sent)
	}
	activeRoutes := fixture.factory.sentRoutes()
	for _, route := range activeRoutes[len(activeRoutes)-100:] {
		marker, err := DecodePayloadMarker(route.packet.Payload)
		if err != nil {
			t.Fatalf("active DecodePayloadMarker: %v", err)
		}
		domain := LogicalDomain(marker.LogicalSend >> (logicalGenerationBits + logicalOrdinalBits))
		if route.packet.ChannelType == uint8(frame.ChannelTypePerson) && domain != LogicalDomainPrimary {
			t.Fatalf("active primary person domain = %d, want %d", domain, LogicalDomainPrimary)
		}
	}

	now = now.Add(5 * time.Hour)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("deadline Advance: %v", err)
	}
	afterDeadline, _ := fixture.engine.Snapshot()
	if afterDeadline.ActiveHotChannels != 0 {
		t.Fatalf("active lifecycle retained after deadline: %+v", afterDeadline)
	}
	_, err = fixture.engine.Tick(now, []uint64{1_000, 1_000, 1_000})
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
}

func TestEngineActiveHotSetRotatesAtFixedCapacityWithoutHistoricalGrowth(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 300, SessionDuration: 10 * time.Hour,
		WorkCapacity: 32_768, MaxWorkPerAdvance: 8_192,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	loginAndObserve := func(start, end uint64) {
		t.Helper()
		for index := start; index < end; index++ {
			uid := fixture.identity.UID(index)
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index}); err != nil {
				t.Fatalf("Login(%d): %v", index, err)
			}
			if _, _, err := fixture.engine.ObserveNewUser(index); err != nil {
				t.Fatalf("ObserveNewUser(%d): %v", index, err)
			}
		}
	}
	loginAndObserve(0, 200)
	full, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("full Snapshot: %v", err)
	}
	if full.ActiveHotChannels != fixture.engine.generator.workload.HotSet.PersonChannels || full.ActiveHotChannels != 80 {
		t.Fatalf("initial active hot set = %+v", full)
	}

	now := fixture.clock.Now().Add(41 * time.Minute)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("rotation Advance: %v", err)
	}
	cooled, _ := fixture.engine.Snapshot()
	if cooled.ActiveHotChannels <= 0 || cooled.ActiveHotChannels >= full.ActiveHotChannels {
		t.Fatalf("rotating expiry did not shrink while long channels remained: before=%+v after=%+v", full, cooled)
	}

	loginAndObserve(200, 300)
	refilled, _ := fixture.engine.Snapshot()
	if refilled.ActiveHotChannels != 80 || refilled.ActiveHotChannels > fixture.engine.generator.workload.HotSet.PersonChannels {
		t.Fatalf("rotated active hot set = %+v", refilled)
	}
}

func TestEngineFullHotSetRetainsMandatoryInitialBurst(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 512, MaxWorkPerAdvance: 512})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	edge := fixture.graph.Incoming(18).Items[0]
	for _, endpoint := range []struct {
		uid   string
		index uint64
	}{{edge.OwnerUID, edge.OwnerIndex}, {edge.PeerUID, edge.PeerIndex}} {
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{
			UID: endpoint.uid, UserIndex: endpoint.index, LoginOrdinal: endpoint.index,
		}); err != nil {
			t.Fatalf("Login(%q): %v", endpoint.uid, err)
		}
	}
	filled := make(chan struct{}, 1)
	if err := fixture.engine.enqueue(engineCommand{run: func() {
		for index := 0; index < fixture.engine.generator.workload.HotSet.PersonChannels; index++ {
			fixture.engine.addActiveChannel(engineActiveChannel{edge: RelationshipEdge{PersonChannelID: fmt.Sprintf("occupied-%d", index)}})
		}
		if err := fixture.engine.addWork(&engineWork{
			due: fixture.clock.Now().Add(time.Nanosecond), kind: engineWorkLifecycle,
			edge: RelationshipEdge{PersonChannelID: "occupied-0"}, schedule: ChannelSchedule{Class: LifecycleRotating},
		}); err != nil {
			t.Errorf("add occupied lifecycle: %v", err)
		}
		fixture.engine.activeLifecycleTimers++
		filled <- struct{}{}
	}}); err != nil {
		t.Fatalf("fill active hot set: %v", err)
	}
	<-filled
	relationshipOrdinal, schedule := findLifecycleSchedule(t, fixture.schedule, edge, LifecycleRotating)
	activated, err := fixture.engine.ActivateRelationship(edge, relationshipOrdinal)
	if err != nil || !activated {
		t.Fatalf("ActivateRelationship at full hot set = %v, %v", activated, err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.ActivityCurrent != schedule.InitialBurst.MessageCount || snapshot.ActiveHotChannels != fixture.engine.generator.workload.HotSet.PersonChannels || snapshot.PendingHotChannels != 1 || snapshot.ActiveLifecycleTimers != 2 {
		t.Fatalf("full hot-set activation dropped mandatory lifecycle work: %+v schedule=%+v", snapshot, schedule)
	}
	due := fixture.clock.Now().Add(time.Nanosecond)
	fixture.clock.Set(due)
	if _, err := fixture.engine.Advance(due); err != nil {
		t.Fatalf("Advance occupied expiry: %v", err)
	}
	promoted, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("promoted Snapshot: %v", err)
	}
	if promoted.ActiveHotChannels != fixture.engine.generator.workload.HotSet.PersonChannels || promoted.PendingHotChannels != 0 || promoted.ActiveLifecycleTimers != 1 {
		t.Fatalf("pending lifecycle was not promoted after capacity opened: %+v", promoted)
	}
}

func TestEngineFormalTickRoutesTwoThousandGrantsToOnlineEligibleSenders(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{
		Formal: true, CommandCapacity: 4_096, WorkCapacity: 8_192,
		InflightCapacity: 4_096, MaxWorkPerAdvance: 4_096,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	for index := uint64(0); index < 2_000; index++ {
		uid := fixture.identity.UID(index)
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index}); err != nil {
			t.Fatalf("Login(%d): %v", index, err)
		}
	}
	edges, err := fixture.graph.Outgoing(0)
	if err != nil || edges.Count == 0 {
		t.Fatalf("Outgoing(0): %+v, %v", edges, err)
	}
	edge := edges.Items[0]
	relationshipOrdinal, _ := findLifecycleSchedule(t, fixture.schedule, edge, LifecycleRotating)
	activated, err := fixture.engine.ActivateRelationship(edge, relationshipOrdinal)
	if err != nil || !activated {
		t.Fatalf("ActivateRelationship = %v, %v", activated, err)
	}

	now := fixture.clock.Now()
	before := len(fixture.factory.sentRoutes())
	tick, err := fixture.engine.Tick(now, []uint64{10_000, 10_000, 10_000})
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if tick.Released != 2_000 || tick.Person != 1_800 || tick.Group != 200 || tick.PayloadCounts != [4]uint64{1_400, 500, 80, 20} {
		t.Fatalf("formal tick = %+v", tick)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	routes := fixture.factory.sentRoutes()[before:]
	if len(routes) != 2_000 {
		t.Fatalf("first-attempt routes = %d, want 2000", len(routes))
	}
	for index, route := range routes {
		if !fixture.pool.IsOnline(route.uid) {
			t.Fatalf("route %d used offline sender %q", index, route.uid)
		}
		if route.packet.ChannelType != uint8(frame.ChannelTypeGroup) {
			continue
		}
		groupIndex, ok := fixture.engine.generator.catalog.IndexFromGroupID(route.packet.ChannelID)
		if !ok {
			t.Fatalf("route %d group %q is outside fixed catalog", index, route.packet.ChannelID)
		}
		group, err := fixture.engine.generator.catalog.Group(groupIndex)
		if err != nil {
			t.Fatalf("Group(%d): %v", groupIndex, err)
		}
		senderIndex, ok := fixture.identity.IndexFromUID(route.uid)
		if !ok || !group.ContainsIndex(senderIndex) {
			t.Fatalf("route %d sender index %d is not a member of group %d", index, senderIndex, groupIndex)
		}
	}
}

func TestEngineCompletionPressureCannotDeadlockBatchAdvance(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		CommandCapacity: 2, WorkCapacity: 256, InflightCapacity: 128, MaxWorkPerAdvance: 128,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(23)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 23, LoginOrdinal: 3}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	now := fixture.clock.Now()
	for ordinal := uint64(1_000); ordinal < 1_100; ordinal++ {
		if err := fixture.engine.SubmitGranted(fixture.intent(t, uid, "completion-pressure", ordinal, TrafficGroup), now); err != nil {
			t.Fatalf("SubmitGranted(%d): %v", ordinal, err)
		}
	}
	if processed, err := fixture.engine.Advance(now); err != nil || processed != 100 {
		t.Fatalf("Advance = %d, %v", processed, err)
	}
	var snapshot EngineSnapshot
	for range 10_000 {
		snapshot, _ = fixture.engine.Snapshot()
		if snapshot.InflightCurrent == 0 {
			break
		}
		runtime.Gosched()
	}
	if snapshot.InflightCurrent != 0 || snapshot.FutureCurrent != 0 || fixture.verifier.Snapshot().Acknowledged != 100 {
		t.Fatalf("completion pressure snapshot = engine %+v verifier %+v", snapshot, fixture.verifier.Snapshot())
	}
}

func assertRuntimeFailure(t *testing.T, err error, code RuntimeFailureCode) {
	t.Helper()
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Classification() != SyncClassificationHarnessInvalid || runtimeErr.Code() != code {
		t.Fatalf("runtime error = %#v, want %s harness_invalid", err, code)
	}
}

func assertUnderDeliveryEvidence(t *testing.T, snapshot EvidenceSnapshot, want uint64) {
	t.Helper()
	if got := evidenceCountForClass(snapshot, FailureClassHarness); got != want {
		t.Fatalf("under-delivery evidence count = %d, want %d: %+v", got, want, snapshot)
	}
	if want == 0 {
		return
	}
	for _, class := range snapshot.Classes {
		if class.Class != FailureClassHarness {
			continue
		}
		if len(class.First) == 0 || class.First[0].Code != FailureCodeOfferedLoadUnderDelivery {
			t.Fatalf("under-delivery evidence code = %+v, want %d", class.First, FailureCodeOfferedLoadUnderDelivery)
		}
		return
	}
	t.Fatal("under-delivery harness class is missing")
}

type engineTestLimits struct {
	Formal                    bool
	CommandCapacity           int
	WorkCapacity              int
	InflightCapacity          int
	MaxWorkPerAdvance         int
	AttemptTimeout            time.Duration
	ActivityEligibilityWindow time.Duration
	OnlineUsers               int
	NewUsersPerDay            int
	SessionDuration           time.Duration
}

type engineTestFixture struct {
	identity *IdentitySpace
	schedule ScheduleModel
	graph    RelationshipGraph
	traffic  TrafficModel
	retry    RetryPolicy
	verifier *Verifier
	evidence *EvidenceRecorder
	clock    *sessionFakeClock
	factory  *engineFakeFactory
	pool     *SessionPool
	engine   *Engine
}

func newEngineTestFixture(t *testing.T, limits engineTestLimits) engineTestFixture {
	t.Helper()
	cfg := LocalConfig()
	if limits.Formal {
		cfg = FormalConfig()
	}
	if limits.OnlineUsers > 0 {
		cfg.Workload.OnlineUsers = limits.OnlineUsers
	}
	if limits.NewUsersPerDay > 0 {
		cfg.Workload.NewUsersPerDay = limits.NewUsersPerDay
	}
	if limits.SessionDuration > 0 {
		cfg.Workload.Sessions = []DurationShare{{Percent: 100, Min: limits.SessionDuration, Max: limits.SessionDuration}}
	}
	identity, err := NewIdentitySpace("engine-test", 89, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	schedule, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel: %v", err)
	}
	graph, err := NewRelationshipGraph(identity)
	if err != nil {
		t.Fatalf("NewRelationshipGraph: %v", err)
	}
	traffic, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	retry, err := NewRetryPolicy(identity, cfg.Workload.Retry)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog: %v", err)
	}
	evidence, err := NewEvidenceRecorder(4, 4)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder: %v", err)
	}
	verifier, err := NewVerifier(traffic, VerifierConfig{
		PendingCapacity: 512, SequenceCapacity: 512, CorrelationCapacity: 64, CorrelationDeadline: time.Minute,
	}, evidence)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	clock := &sessionFakeClock{now: time.Unix(1_700_000_000, 0)}
	factory := &engineFakeFactory{}
	pool, err := NewSessionPool(SessionPoolConfig{
		Identity: identity, Schedule: schedule, Catalog: catalog, Factory: factory, Syncer: engineSyncer{},
		Verifier: verifier, Clock: clock, DeviceID: "engine-test", StartingCapacity: 128,
	})
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	generator, err := NewTrafficGenerator(TrafficGeneratorConfig{
		Identity: identity, Model: traffic, Catalog: catalog, Workload: cfg.Workload, Start: clock.Now(),
	})
	if err != nil {
		t.Fatalf("NewTrafficGenerator: %v", err)
	}
	workCapacity := limits.WorkCapacity
	if workCapacity == 0 {
		workCapacity = 256
	}
	maxWork := limits.MaxWorkPerAdvance
	if maxWork == 0 {
		maxWork = 64
	}
	attemptTimeout := limits.AttemptTimeout
	if attemptTimeout == 0 {
		attemptTimeout = time.Second
	}
	activityEligibilityWindow := limits.ActivityEligibilityWindow
	if activityEligibilityWindow == 0 {
		activityEligibilityWindow = 5 * time.Minute
	}
	commandCapacity := limits.CommandCapacity
	if commandCapacity == 0 {
		commandCapacity = 64
	}
	inflightCapacity := limits.InflightCapacity
	if inflightCapacity == 0 {
		inflightCapacity = 64
	}
	engine, err := NewEngine(EngineConfig{
		Clock: clock, Sessions: pool, Schedule: schedule, Graph: graph, Traffic: traffic,
		Generator: generator, Retry: retry, Verifier: verifier, Evidence: evidence,
		CommandCapacity: commandCapacity, WorkCapacity: workCapacity, RetryCapacity: 64,
		InflightCapacity: inflightCapacity, MaxWorkPerAdvance: maxWork, AttemptTimeout: attemptTimeout,
		ActivityEligibilityWindow: activityEligibilityWindow,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engineTestFixture{
		identity: identity, schedule: schedule, graph: graph, traffic: traffic, retry: retry,
		verifier: verifier, evidence: evidence, clock: clock, factory: factory, pool: pool, engine: engine,
	}
}

func (f engineTestFixture) intent(t *testing.T, sender, target string, ordinal uint64, kind TrafficKind) TrafficIntent {
	t.Helper()
	logical, err := f.traffic.NewLogicalSend(0, ordinal, kind, sender, target)
	if err != nil {
		t.Fatalf("NewLogicalSend: %v", err)
	}
	payload, err := f.traffic.BuildPayload(logical, 256)
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	return TrafficIntent{Logical: logical, Packet: packetForTrafficIntent(logical, payload), Kind: kind, PayloadBytes: len(payload), ChannelID: target}
}

type engineSyncer struct{}

func (engineSyncer) ConversationSync(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
	return nil, nil
}

type engineFakeFactory struct {
	mu                sync.Mutex
	sendErrors        []error
	clientsV          []*engineFakeClient
	routesV           []engineSentRoute
	autoAck           bool
	newSessionStarted chan context.Context
	connectStarted    chan context.Context
	connectRelease    <-chan struct{}
	readContexts      chan<- context.Context
	readCycles        chan<- struct{}
}

func (f *engineFakeFactory) NewSession(ctx context.Context, uid, _ string) (SessionClient, error) {
	if f.newSessionStarted != nil {
		f.newSessionStarted <- ctx
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}
	f.mu.Lock()
	client := &engineFakeClient{
		uid: uid, factory: f, frames: make(chan frame.Frame, 16), readErrors: make(chan error, 16),
		readReturned: make(chan struct{}, 16), stop: make(chan struct{}), readExited: make(chan struct{}),
		readContexts: f.readContexts,
	}
	f.clientsV = append(f.clientsV, client)
	f.mu.Unlock()
	return client, nil
}

func (f *engineFakeFactory) nextSendError() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sendErrors) == 0 {
		return nil
	}
	err := f.sendErrors[0]
	f.sendErrors = f.sendErrors[1:]
	return err
}

func (f *engineFakeFactory) clients() []*engineFakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*engineFakeClient(nil), f.clientsV...)
}

func (f *engineFakeFactory) sentPackets() []*frame.SendPacket {
	clients := f.clients()
	var packets []*frame.SendPacket
	for _, client := range clients {
		client.mu.Lock()
		packets = append(packets, client.sent...)
		client.mu.Unlock()
	}
	return packets
}

type engineSentRoute struct {
	uid    string
	packet *frame.SendPacket
}

func (f *engineFakeFactory) sentRoutes() []engineSentRoute {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]engineSentRoute(nil), f.routesV...)
}

func (f *engineFakeFactory) recordRoute(route engineSentRoute) {
	f.mu.Lock()
	f.routesV = append(f.routesV, route)
	f.mu.Unlock()
}

func (f *engineFakeFactory) sentCount() int { return len(f.sentPackets()) }

type engineFakeClient struct {
	mu           sync.Mutex
	uid          string
	factory      *engineFakeFactory
	frames       chan frame.Frame
	readErrors   chan error
	readReturned chan struct{}
	stop         chan struct{}
	readExited   chan struct{}
	readContexts chan<- context.Context
	readOnce     sync.Once
	closeOnce    sync.Once
	sent         []*frame.SendPacket
}

func (c *engineFakeClient) Connect(ctx context.Context, _, _ string) error {
	if c.factory.connectStarted == nil {
		return nil
	}
	c.factory.connectStarted <- ctx
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-c.factory.connectRelease:
		return nil
	}
}
func (c *engineFakeClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if c.factory.readCycles != nil {
		c.factory.readCycles <- struct{}{}
	}
	c.readOnce.Do(func() {
		if c.readContexts != nil {
			c.readContexts <- ctx
		}
	})
	select {
	case packet := <-c.frames:
		return packet, nil
	case err := <-c.readErrors:
		c.readReturned <- struct{}{}
		return nil, err
	case <-ctx.Done():
		c.closeReadExited()
		return nil, ctx.Err()
	case <-c.stop:
		c.closeReadExited()
		return nil, errors.New("closed")
	}
}
func (c *engineFakeClient) Send(_ context.Context, packet *frame.SendPacket) error {
	c.mu.Lock()
	c.sent = append(c.sent, packet)
	messageID := int64(len(c.sent))
	c.mu.Unlock()
	c.factory.recordRoute(engineSentRoute{uid: c.uid, packet: packet})
	err := c.factory.nextSendError()
	if err == nil && c.factory.autoAck {
		c.frames <- &frame.SendackPacket{
			ClientMsgNo: packet.ClientMsgNo, MessageID: messageID, MessageSeq: uint64(messageID), ReasonCode: frame.ReasonSuccess,
		}
	}
	return err
}
func (c *engineFakeClient) AckRecv(context.Context, *frame.RecvackPacket) error { return nil }
func (c *engineFakeClient) Close() error {
	c.closeOnce.Do(func() { close(c.stop) })
	return nil
}
func (c *engineFakeClient) QueueSnapshot() SessionQueueSnapshot { return SessionQueueSnapshot{} }
func (c *engineFakeClient) ReadErrorInfo(err error) (wkproto.ReadErrorInfo, bool) {
	var readErr *engineFakeReadError
	if !errors.As(err, &readErr) {
		return wkproto.ReadErrorInfo{}, false
	}
	return wkproto.ReadErrorInfo{Kind: readErr.kind, ClientMsgNo: readErr.clientMsgNo}, true
}

type engineFakeReadError struct {
	kind        wkproto.ReadErrorKind
	clientMsgNo string
}

func (e *engineFakeReadError) Error() string { return "redacted engine read error" }
func (c *engineFakeClient) closeReadExited() {
	select {
	case <-c.readExited:
	default:
		close(c.readExited)
	}
}
