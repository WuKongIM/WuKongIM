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
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.PeerUID, UserIndex: edge.PeerIndex, LoginOrdinal: 2, NewIdentity: true}); err != nil {
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
	if tick, err := fixture.engine.Tick(fixture.clock.Now(), fixture.demand(1)); err != nil || tick.Released != 1 {
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
	step, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("bootstrap Step = %+v, %v", step, err)
	}
	step = fixture.settleScheduledLogins(t, now, step)
	if step.Online != 100 {
		t.Fatalf("settled bootstrap Step = %+v", step)
	}
	tick, err := fixture.engine.Tick(now, fixture.demand(1_000))
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
	bootstrap, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("bootstrap Step: %v", err)
	}
	fixture.settleScheduledLogins(t, now, bootstrap)
	for index := uint64(0); index < 50; index++ {
		if err := fixture.engine.Logout(fixture.identity.UID(index)); err != nil {
			t.Fatalf("Logout(%d): %v", index, err)
		}
	}
	for index := uint64(100); index < 150; index++ {
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: fixture.identity.UID(index), UserIndex: index, LoginOrdinal: index, NewIdentity: true}); err != nil {
			t.Fatalf("replacement Login(%d): %v", index, err)
		}
		if _, _, err := fixture.engine.ObserveNewUser(index); err != nil {
			t.Fatalf("ObserveNewUser(%d): %v", index, err)
		}
	}
	before := fixture.factory.sentCount()
	tick, err := fixture.engine.Tick(now, fixture.demand(1_000))
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
		if _, err := fixture.engine.Tick(now, fixture.demand(1)); err != nil {
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
		future        bool
		offered       bool
		wantAbandoned uint64
		wantCanceled  uint64
	}{
		{name: "pending_is_closed", wantAbandoned: 1},
		{name: "pending_preserves_terminal_product", terminal: true, wantAbandoned: 1},
		{name: "future_unoffered_is_cleanly_canceled", future: true, wantCanceled: 1},
		{name: "future_already_offered_is_under_delivered", future: true, offered: true, wantAbandoned: 1},
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
			due := now
			if test.future {
				due = now.Add(30 * time.Second)
			}
			added := make(chan error, 1)
			if err := fixture.engine.enqueue(engineCommand{run: func() {
				added <- fixture.engine.addActivity(&engineWork{
					due: due, kind: engineWorkSend, offered: test.offered,
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
			if snapshot.ActivityCurrent != 0 || snapshot.ActivityUnderDelivered != test.wantAbandoned || snapshot.ActivityFutureCanceled != test.wantCanceled || snapshot.HarnessInvalid != wantHarness || snapshot.Classification != wantClassification {
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

func TestEngineApprovedFullyOfflineRevisitExpiresOnceAtEligibilityBoundary(t *testing.T) {
	const eligibilityWindow = 10 * time.Nanosecond
	fixture := newEngineTestFixture(t, engineTestLimits{
		WorkCapacity: 256, MaxWorkPerAdvance: 256, ActivityEligibilityWindow: eligibilityWindow,
	})
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
	if err := fixture.engine.Logout(edge.OwnerUID); err != nil {
		t.Fatalf("owner Logout: %v", err)
	}
	if err := fixture.engine.Logout(edge.PeerUID); err != nil {
		t.Fatalf("peer Logout: %v", err)
	}

	due := fixture.clock.Now().Add(schedule.RevisitAfter)
	deadline := due.Add(eligibilityWindow)
	justBeforeExpiry := deadline.Add(-2 * time.Nanosecond)
	fixture.clock.Set(justBeforeExpiry)
	if _, err := fixture.engine.Advance(justBeforeExpiry); err != nil {
		t.Fatalf("Advance before eligibility boundary: %v", err)
	}
	retained, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot retained: %v", err)
	}
	if retained.ActiveLifecycleTimers != 1 || retained.ColdEvidencePending != 1 || retained.ActivityUnderDelivered != 0 || retained.HarnessInvalid != 0 {
		t.Fatalf("revisit before eligibility expiry = %+v", retained)
	}

	expiry := deadline.Add(-time.Nanosecond)
	fixture.clock.Set(expiry)
	if _, err := fixture.engine.Advance(expiry); err == nil {
		t.Fatal("Advance at eligibility boundary returned nil, want under-delivery")
	}
	expired, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot expired: %v", err)
	}
	if expired.ActiveLifecycleTimers != 0 || expired.ColdEvidencePending != 0 || expired.ActivityUnderDelivered != 1 || expired.HarnessInvalid != 1 || expired.Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("expired offline revisit = %+v", expired)
	}
	assertUnderDeliveryEvidence(t, fixture.evidence.Snapshot(), 1)

	afterLongIdle := deadline.Add(72 * time.Hour)
	fixture.clock.Set(afterLongIdle)
	if _, err := fixture.engine.Advance(afterLongIdle); err != nil {
		t.Fatalf("Advance after long idle: %v", err)
	}
	stable, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot stable: %v", err)
	}
	if stable.ActiveLifecycleTimers != 0 || stable.ColdEvidencePending != 0 || stable.ActivityUnderDelivered != 1 || stable.HarnessInvalid != 1 {
		t.Fatalf("expired revisit changed after long idle = %+v", stable)
	}
	assertUnderDeliveryEvidence(t, fixture.evidence.Snapshot(), 1)
}

func TestEngineRelationshipActivityRetainsOnlyRetargetMetadata(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 256})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()

	edge := fixture.graph.Incoming(18).Items[0]
	relationshipOrdinal, schedule := findLifecycleSchedule(t, fixture.schedule, edge, LifecycleOneShot)
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
	if activated, err := fixture.engine.ActivateRelationship(edge, relationshipOrdinal); err != nil || !activated {
		t.Fatalf("ActivateRelationship = %v, %v", activated, err)
	}

	checked := make(chan error, 1)
	if err := fixture.engine.enqueue(engineCommand{run: func() {
		found := 0
		for _, activity := range fixture.engine.activity {
			if activity.intent.ChannelID != edge.PersonChannelID {
				continue
			}
			found++
			intent := activity.intent
			if intent.Packet != nil || intent.PayloadBytes != 0 || intent.Logical.ClientMsgNo != "" || intent.Logical.LogicalSend != 0 || intent.Logical.WorkerID != 0 {
				checked <- fmt.Errorf("activity retained prebuilt send state: %+v", intent)
				return
			}
			if intent.Logical.Sender == "" || intent.Logical.Target == "" || intent.Kind != TrafficPerson || intent.ChannelID == "" || intent.Domain != LogicalDomainLifecycle {
				checked <- fmt.Errorf("activity lost retarget metadata: %+v", intent)
				return
			}
		}
		if found != schedule.InitialBurst.MessageCount {
			checked <- fmt.Errorf("retained activity count = %d, want %d", found, schedule.InitialBurst.MessageCount)
			return
		}
		checked <- nil
	}}); err != nil {
		t.Fatalf("inspect relationship activity: %v", err)
	}
	if err := <-checked; err != nil {
		t.Fatal(err)
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
		planned, err := fixture.graph.ReturningCandidate(fixture.schedule, 100, ordinal, 100)
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
		if _, err := fixture.engine.Tick(due, fixture.demand(1)); err != nil {
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
				planned, err := fixture.graph.ReturningCandidate(fixture.schedule, 1_000, ordinal, 1_000)
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
	clientSeqs := make(map[uint64]struct{}, len(packets))
	for index, packet := range packets {
		if packet.ClientMsgNo != intent.Logical.ClientMsgNo {
			t.Fatalf("attempt %d client_msg_no = %q, want %q", index, packet.ClientMsgNo, intent.Logical.ClientMsgNo)
		}
		if packet.ClientSeq == 0 {
			t.Fatalf("attempt %d ClientSeq = 0", index)
		}
		if _, exists := clientSeqs[packet.ClientSeq]; exists {
			t.Fatalf("attempt %d reused ClientSeq %d", index, packet.ClientSeq)
		}
		clientSeqs[packet.ClientSeq] = struct{}{}
	}
	ack := &frame.SendackPacket{
		ClientSeq: packets[len(packets)-1].ClientSeq, ClientMsgNo: intent.Logical.ClientMsgNo,
		MessageID: 901, MessageSeq: 77, ReasonCode: frame.ReasonSuccess,
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
	ack := &frame.SendackPacket{
		ClientSeq:   fixture.factory.sentPackets()[0].ClientSeq,
		ClientMsgNo: intent.Logical.ClientMsgNo, ReasonCode: frame.ReasonAuthFail,
	}
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
		ClientSeq: fixture.factory.sentPackets()[0].ClientSeq, ClientMsgNo: intent.Logical.ClientMsgNo,
		MessageID: 902, MessageSeq: 78, ReasonCode: frame.ReasonSuccess,
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
	clientSeq := fixture.factory.sentPackets()[0].ClientSeq
	client.readErrors <- &engineFakeReadError{kind: wkproto.ReadErrorNonTerminal, clientSeq: clientSeq, clientMsgNo: intent.Logical.ClientMsgNo}
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

func TestEngineOverlappingAttemptsKeepCurrentOwnershipAndAcceptEitherSuccess(t *testing.T) {
	type overlapFixture struct {
		fixture engineTestFixture
		intent  TrafficIntent
		now     time.Time
		first   uint64
		current uint64
	}
	startOverlap := func(t *testing.T) overlapFixture {
		t.Helper()
		fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: time.Millisecond})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() { _ = fixture.engine.Stop() })
		uid := fixture.identity.UID(9)
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 9, LoginOrdinal: 8}); err != nil {
			t.Fatalf("Login: %v", err)
		}
		intent := fixture.intent(t, uid, "overlap-group", 148, TrafficGroup)
		now := fixture.clock.Now()
		if err := fixture.engine.SubmitGranted(intent, now); err != nil {
			t.Fatalf("SubmitGranted: %v", err)
		}
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("attempt zero Advance: %v", err)
		}
		now = now.Add(time.Millisecond)
		fixture.clock.Set(now)
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("attempt zero timeout Advance: %v", err)
		}
		retry, err := fixture.retry.Attempt(intent.Logical, 1)
		if err != nil {
			t.Fatalf("retry Attempt: %v", err)
		}
		now = now.Add(retry.Delay)
		fixture.clock.Set(now)
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("attempt one Advance: %v", err)
		}
		packets := fixture.factory.sentPackets()
		if len(packets) != 2 || packets[0].ClientSeq == packets[1].ClientSeq || packets[0].ClientMsgNo != packets[1].ClientMsgNo {
			t.Fatalf("overlapping packets = %+v", packets)
		}
		return overlapFixture{
			fixture: fixture, intent: intent, now: now,
			first: packets[0].ClientSeq, current: packets[1].ClientSeq,
		}
	}
	observeAck := func(t *testing.T, overlap overlapFixture, clientSeq uint64, reason frame.ReasonCode) error {
		t.Helper()
		ack := &frame.SendackPacket{
			ClientSeq: clientSeq, ClientMsgNo: overlap.intent.Logical.ClientMsgNo,
			ReasonCode: reason,
		}
		if reason == frame.ReasonSuccess {
			ack.MessageID, ack.MessageSeq = 701, 801
		}
		verificationErr := overlap.fixture.verifier.HandleSendack(ack)
		return overlap.fixture.engine.ObserveSendack(overlap.intent.Logical.Sender, ack, verificationErr)
	}
	assertCurrent := func(t *testing.T, overlap overlapFixture) {
		t.Helper()
		snapshot, err := overlap.fixture.engine.Snapshot()
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if snapshot.InflightCurrent != 1 || snapshot.RetryQueueDepth != 0 || snapshot.FutureCurrent != 1 || snapshot.FinalFailures != 0 {
			t.Fatalf("current attempt ownership changed: %+v", snapshot)
		}
	}
	assertCompleteWithoutAttemptEvidence := func(t *testing.T, overlap overlapFixture, releasedAttempts int) {
		t.Helper()
		snapshot, err := overlap.fixture.engine.Snapshot()
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		verifier := overlap.fixture.verifier.Snapshot()
		if snapshot.InflightCurrent != 0 || snapshot.FutureCurrent != 0 || snapshot.FinalFailures != 0 ||
			verifier.UnknownSendacks != 0 || verifier.DuplicateCompletions != 0 || verifier.ConflictingCompletions != 0 || verifier.ReleasedAttemptCurrent != releasedAttempts {
			t.Fatalf("completed overlap engine=%+v verifier=%+v", snapshot, verifier)
		}
	}

	t.Run("stale_rejection", func(t *testing.T) {
		overlap := startOverlap(t)
		if err := observeAck(t, overlap, overlap.first, frame.ReasonRateLimit); err != nil {
			t.Fatalf("stale rejection: %v", err)
		}
		assertCurrent(t, overlap)
		if err := observeAck(t, overlap, overlap.current, frame.ReasonSuccess); err != nil {
			t.Fatalf("current success: %v", err)
		}
		assertCompleteWithoutAttemptEvidence(t, overlap, 0)
	})

	t.Run("stale_async_error", func(t *testing.T) {
		overlap := startOverlap(t)
		overlap.fixture.engine.sessionAsyncSendError(overlap.intent.Logical.Sender, overlap.first, overlap.intent.Logical.ClientMsgNo)
		assertCurrent(t, overlap)
		if err := observeAck(t, overlap, overlap.current, frame.ReasonSuccess); err != nil {
			t.Fatalf("current success: %v", err)
		}
		assertCompleteWithoutAttemptEvidence(t, overlap, 0)
	})

	t.Run("stale_timeout", func(t *testing.T) {
		overlap := startOverlap(t)
		added := make(chan error, 1)
		if err := overlap.fixture.engine.enqueue(engineCommand{run: func() {
			added <- overlap.fixture.engine.addWork(&engineWork{
				due: overlap.now, kind: engineWorkTimeout, intent: overlap.intent,
				attempt: 0, clientSeq: overlap.first,
			})
		}}); err != nil {
			t.Fatalf("enqueue stale timeout: %v", err)
		}
		if err := <-added; err != nil {
			t.Fatalf("add stale timeout: %v", err)
		}
		if _, err := overlap.fixture.engine.Advance(overlap.now); err != nil {
			t.Fatalf("stale timeout Advance: %v", err)
		}
		assertCurrent(t, overlap)
		if err := observeAck(t, overlap, overlap.current, frame.ReasonSuccess); err != nil {
			t.Fatalf("current success: %v", err)
		}
		assertCompleteWithoutAttemptEvidence(t, overlap, 1)
	})

	for _, testCase := range []struct {
		name        string
		winnerFirst bool
	}{
		{name: "older_attempt_success", winnerFirst: true},
		{name: "current_attempt_success", winnerFirst: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			overlap := startOverlap(t)
			winner, sibling := overlap.current, overlap.first
			if testCase.winnerFirst {
				winner, sibling = overlap.first, overlap.current
			}
			if err := observeAck(t, overlap, winner, frame.ReasonSuccess); err != nil {
				t.Fatalf("winner success: %v", err)
			}
			if err := observeAck(t, overlap, sibling, frame.ReasonSuccess); err != nil {
				t.Fatalf("released sibling success: %v", err)
			}
			assertCompleteWithoutAttemptEvidence(t, overlap, 0)
		})
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
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index, NewIdentity: true}); err != nil {
				t.Fatalf("Login(%d): %v", index, err)
			}
			if _, _, err := fixture.engine.ObserveNewUser(index); err != nil {
				t.Fatalf("ObserveNewUser(%d): %v", index, err)
			}
		}
		before := len(fixture.factory.sentPackets())
		now := fixture.clock.Now().Add(time.Minute)
		fixture.clock.Set(now)
		if tick, err := fixture.engine.Tick(now, fixture.demand(1_000)); err != nil || tick.Released != 100 {
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

func TestEngineContextControlsCancelBehindOwnerWithoutLateRateMutation(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *Engine) error
	}{
		{
			name: "snapshot",
			call: func(ctx context.Context, engine *Engine) error {
				_, err := engine.SnapshotContext(ctx)
				return err
			},
		},
		{
			name: "worker runtime snapshot",
			call: func(ctx context.Context, engine *Engine) error {
				_, err := engine.WorkerRuntimeSnapshotContext(ctx)
				return err
			},
		},
		{
			name: "advance",
			call: func(ctx context.Context, engine *Engine) error {
				_, err := engine.AdvanceContext(ctx, time.Unix(1_700_000_001, 0))
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			entered, release := blockEngineOwner(t, fixture.engine)
			<-entered
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() { result <- test.call(ctx, fixture.engine) }()
			waitForEngineQueuedCommand(t, fixture.engine)
			cancel()
			var callErr error
			returnedBeforeRelease := false
			select {
			case callErr = <-result:
				returnedBeforeRelease = true
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			if !returnedBeforeRelease {
				callErr = <-result
			}
			if err := fixture.engine.Stop(); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if !returnedBeforeRelease || !errors.Is(callErr, context.Canceled) {
				t.Fatalf("cancelable %s returned_before_release=%v error=%v", test.name, returnedBeforeRelease, callErr)
			}
		})
	}

	t.Run("scheduled rate", func(t *testing.T) {
		fixture := newEngineTestFixture(t, engineTestLimits{})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		originalRate := fixture.engine.generator.allocator.rate
		originalBurst := fixture.engine.generator.allocator.burst
		entered, release := blockEngineOwner(t, fixture.engine)
		<-entered
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- fixture.engine.ScheduleRateContext(ctx, originalRate+1, 2*(originalRate+1)) }()
		waitForEngineQueuedCommand(t, fixture.engine)
		cancel()
		callErr := <-result
		close(release)
		if _, err := fixture.engine.Snapshot(); err != nil {
			t.Fatalf("owner barrier Snapshot: %v", err)
		}
		allocator := fixture.engine.generator.allocator
		if err := fixture.engine.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if !errors.Is(callErr, context.Canceled) {
			t.Fatalf("ScheduleRateContext error = %v, want context cancellation", callErr)
		}
		if allocator.rate != originalRate || allocator.burst != originalBurst || allocator.hasPending {
			t.Fatalf("canceled late rate mutated allocator: rate=%d burst=%d pending=%v", allocator.rate, allocator.burst, allocator.hasPending)
		}
	})

	t.Run("scheduled rate generation fence", func(t *testing.T) {
		fixture := newEngineTestFixture(t, engineTestLimits{})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		originalRate := fixture.engine.generator.allocator.rate
		originalBurst := fixture.engine.generator.allocator.burst
		generationCtx := fixture.engine.generationCtx
		entered, release := blockEngineOwner(t, fixture.engine)
		<-entered
		rateResult := make(chan error, 1)
		go func() {
			rateResult <- fixture.engine.ScheduleRateContext(context.Background(), originalRate+1, 2*(originalRate+1))
		}()
		waitForEngineQueuedCommand(t, fixture.engine)
		stopResult := make(chan error, 1)
		go func() { stopResult <- fixture.engine.Stop() }()
		select {
		case <-generationCtx.Done():
		case <-time.After(time.Second):
			close(release)
			<-stopResult
			t.Fatal("Stop did not fence queued control generation")
		}
		close(release)
		if err := <-rateResult; !errors.Is(err, errEngineNotRunning) {
			t.Fatalf("stale generation ScheduleRateContext error = %v, want %v", err, errEngineNotRunning)
		}
		if err := <-stopResult; err != nil {
			t.Fatalf("Stop: %v", err)
		}
		allocator := fixture.engine.generator.allocator
		if allocator.rate != originalRate || allocator.burst != originalBurst || allocator.hasPending {
			t.Fatalf("stale generation rate mutated allocator: rate=%d burst=%d pending=%v", allocator.rate, allocator.burst, allocator.hasPending)
		}
	})
}

func blockEngineOwner(t *testing.T, engine *Engine) (<-chan struct{}, chan<- struct{}) {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := engine.enqueueBlocking(engineCommand{run: func() {
		close(entered)
		<-release
	}}); err != nil {
		t.Fatalf("enqueue owner blocker: %v", err)
	}
	return entered, release
}

func waitForEngineQueuedCommand(t *testing.T, engine *Engine) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(engine.commands) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("context control did not enqueue behind blocked owner")
		}
		runtime.Gosched()
	}
}

func TestEngineStartGenerationUsesExactExternalFenceAndRejectsInvalidValues(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{})

	if err := fixture.engine.StartGeneration(context.Background(), 7); err != nil {
		t.Fatalf("StartGeneration(7): %v", err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("generation seven Snapshot: %v", err)
	}
	if snapshot.Generation != 7 {
		t.Fatalf("generation = %d, want 7", snapshot.Generation)
	}
	if err := fixture.engine.Stop(); err != nil {
		t.Fatalf("Stop generation seven: %v", err)
	}

	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("ordinary Start after generation seven: %v", err)
	}
	snapshot, err = fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("generation eight Snapshot: %v", err)
	}
	if snapshot.Generation != 8 {
		t.Fatalf("ordinary Start generation = %d, want 8", snapshot.Generation)
	}
	if err := fixture.engine.Stop(); err != nil {
		t.Fatalf("Stop generation eight: %v", err)
	}

	for name, generation := range map[string]uint64{
		"zero":     0,
		"rollback": 7,
		"reuse":    8,
		"overflow": maxLogicalGeneration + 1,
	} {
		t.Run(name, func(t *testing.T) {
			if err := fixture.engine.StartGeneration(context.Background(), generation); !errors.Is(err, errEngineConfig) {
				t.Fatalf("StartGeneration(%d) error = %v, want %v", generation, err, errEngineConfig)
			}
		})
	}

	if err := fixture.engine.StartGeneration(context.Background(), 10); err != nil {
		t.Fatalf("StartGeneration(10): %v", err)
	}
	defer fixture.engine.Stop()
	snapshot, err = fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("generation ten Snapshot: %v", err)
	}
	if snapshot.Generation != 10 {
		t.Fatalf("generation = %d, want 10", snapshot.Generation)
	}
}

func TestEngineStopFencesAndJoinsWholeBlockedStepBeforeRestart(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{OnlineUsers: 1, NewUsersPerDay: 250_000})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start generation one: %v", err)
	}

	fixture.engine.stepMu.Lock()
	stepStarted := make(chan struct{})
	stepDone := make(chan error, 1)
	now := fixture.clock.Now().Add(time.Second)
	go func() {
		close(stepStarted)
		_, err := fixture.engine.Step(context.Background(), now, nil)
		stepDone <- err
	}()
	<-stepStarted
	deadline := time.Now().Add(time.Second)
	for fixture.engine.activeSteps.Load() != 1 {
		if time.Now().After(deadline) {
			fixture.engine.stepMu.Unlock()
			t.Fatal("Step did not acquire its generation lease")
		}
		runtime.Gosched()
	}
	stopDone := make(chan error, 1)
	go func() { stopDone <- fixture.engine.Stop() }()

	select {
	case err := <-stopDone:
		fixture.engine.stepMu.Unlock()
		<-stepDone
		t.Fatalf("Stop returned before blocked Step joined: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	fixture.engine.stepMu.Unlock()
	if err := <-stepDone; !errors.Is(err, context.Canceled) && !errors.Is(err, errEngineNotRunning) {
		t.Fatalf("fenced Step error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop generation one: %v", err)
	}
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start generation two: %v", err)
	}
	defer fixture.engine.Stop()
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("generation two Snapshot: %v", err)
	}
	if snapshot.Generation != 2 || snapshot.Online != 0 || snapshot.LoginPlannedNew != 0 || snapshot.LoginCompletedNew != 0 {
		t.Fatalf("old Step mutated generation two: %+v", snapshot)
	}
}

func TestEngineStepStartsLoginsConcurrentlyWithoutBlockingTrafficAndStopJoinsThem(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 11, NewUsersPerDay: 250_000, StartingCapacity: 10,
		WorkCapacity: 64, MaxWorkPerAdvance: 64,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	uid := fixture.identity.UID(100)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 100, LoginOrdinal: 100}); err != nil {
		t.Fatalf("seed Login: %v", err)
	}
	intent := fixture.intent(t, uid, "step-traffic", 77, TrafficGroup)
	if err := fixture.engine.SubmitGranted(intent, fixture.clock.Now()); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}

	connectStarted := make(chan context.Context, 10)
	connectRelease := make(chan struct{})
	fixture.factory.connectStarted = connectStarted
	fixture.factory.connectRelease = connectRelease
	now := fixture.clock.Now().Add(3 * time.Second)
	fixture.clock.Set(now)
	stepDone := make(chan struct {
		result EngineStepSnapshot
		err    error
	}, 1)
	go func() {
		result, err := fixture.engine.Step(context.Background(), now, nil)
		stepDone <- struct {
			result EngineStepSnapshot
			err    error
		}{result: result, err: err}
	}()

	firstCtx := <-connectStarted
	var stepResult EngineStepSnapshot
	select {
	case result := <-stepDone:
		if result.err != nil {
			close(connectRelease)
			t.Fatalf("nonblocking Step: %v", result.err)
		}
		stepResult = result.result
	case <-time.After(20 * time.Millisecond):
		close(connectRelease)
		<-stepDone
		_ = fixture.engine.Stop()
		t.Fatal("Step blocked on the first login instead of advancing traffic")
	}
	if stepResult.Advanced != 1 || fixture.factory.sentCount() != 1 {
		close(connectRelease)
		_ = fixture.engine.Stop()
		t.Fatalf("Step did not advance admitted traffic: result=%+v sent=%d", stepResult, fixture.factory.sentCount())
	}
	contexts := []context.Context{firstCtx}
	for len(contexts) < 10 {
		select {
		case loginCtx := <-connectStarted:
			contexts = append(contexts, loginCtx)
		case <-time.After(time.Second):
			close(connectRelease)
			_ = fixture.engine.Stop()
			t.Fatalf("concurrent starts = %d, want 10", len(contexts))
		}
	}
	if snapshot := fixture.pool.Snapshot(); snapshot.Starting != 10 {
		close(connectRelease)
		_ = fixture.engine.Stop()
		t.Fatalf("starting sessions = %+v, want 10", snapshot)
	}
	stopDone := make(chan error, 1)
	go func() { stopDone <- fixture.engine.Stop() }()
	for index, loginCtx := range contexts {
		select {
		case <-loginCtx.Done():
		case <-time.After(time.Second):
			close(connectRelease)
			t.Fatalf("startup %d was not canceled", index)
		}
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop: %v", err)
	}
	stopped, _ := fixture.engine.Snapshot()
	if stopped.Running || stopped.ActiveLoops != 0 || stopped.LoginStarting != 0 {
		t.Fatalf("stopped startup ownership = %+v", stopped)
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
	bootstrap = fixture.settleScheduledLogins(t, now, bootstrap)
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
	steady = fixture.settleScheduledLogins(t, now, steady)
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
	scheduler := fixture.engine.scheduler
	scheduler.nextNewIndex = 750_000
	scheduler.bootstrapping = false
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

func TestEngineWorkerSchedulersPartitionFormalOnlineTargetAndUIDs(t *testing.T) {
	seen := make(map[string]struct{}, 300)
	targetTotal := 0
	for workerID := uint64(0); workerID < 3; workerID++ {
		fixture := newEngineTestFixture(t, engineTestLimits{Formal: true, WorkerID: workerID, WorkerCount: 3})
		targetTotal += fixture.engine.onlineTarget
		wantTarget := 3_333
		if workerID == 0 {
			wantTarget = 3_334
		}
		if fixture.engine.onlineTarget != wantTarget {
			t.Fatalf("worker %d online target = %d, want %d", workerID, fixture.engine.onlineTarget, wantTarget)
		}
		for localIndex := uint64(0); localIndex < 100; localIndex++ {
			login, actualKind, _, available, err := fixture.engine.scheduler.planLogin(
				fixture.pool, fixture.graph, fixture.schedule, fixture.engine.generator.catalog,
				localIndex, LoginNew,
			)
			if err != nil || !available || actualKind != LoginNew {
				t.Fatalf("worker %d local %d planLogin = %+v kind=%d available=%v err=%v", workerID, localIndex, login, actualKind, available, err)
			}
			wantGlobal, err := fixture.identity.GlobalIndex(workerID, localIndex)
			if err != nil {
				t.Fatalf("GlobalIndex(%d, %d): %v", workerID, localIndex, err)
			}
			if login.UserIndex != wantGlobal || login.UID != fixture.identity.UID(wantGlobal) {
				t.Fatalf("worker %d local %d login = %+v, want global %d", workerID, localIndex, login, wantGlobal)
			}
			if _, duplicate := seen[login.UID]; duplicate {
				t.Fatalf("duplicate worker UID %q", login.UID)
			}
			seen[login.UID] = struct{}{}
			fixture.engine.scheduler.nextNewIndex++
		}
	}
	if targetTotal != 10_000 || len(seen) != 300 {
		t.Fatalf("partition totals target=%d unique_uids=%d", targetTotal, len(seen))
	}
}

func TestEngineWorkerLocalRelationshipActivationPreservesAggregateHistoryAndInitialBursts(t *testing.T) {
	const (
		workerCount    = 3
		usersPerWorker = 100
	)
	var aggregateConsidered, aggregateActivated, aggregateInitialBursts, aggregateExpected int
	for workerID := uint64(0); workerID < workerCount; workerID++ {
		fixture := newEngineTestFixture(t, engineTestLimits{
			WorkerID: workerID, WorkerCount: workerCount, OnlineUsers: workerCount * usersPerWorker,
			WorkCapacity: 8_192, MaxWorkPerAdvance: 8_192,
		})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("worker %d Start: %v", workerID, err)
		}
		newOrdinals := make([]uint64, 0, usersPerWorker)
		for newOrdinal := uint64(0); len(newOrdinals) < usersPerWorker; newOrdinal++ {
			loginOrdinal, err := fixture.schedule.loginOrdinalForNewOrdinal(newOrdinal)
			if err != nil {
				t.Fatalf("worker %d loginOrdinalForNewOrdinal(%d): %v", workerID, newOrdinal, err)
			}
			if loginOrdinal%workerCount == workerID {
				newOrdinals = append(newOrdinals, newOrdinal)
			}
		}
		for localIndex := uint64(0); localIndex < usersPerWorker; localIndex++ {
			globalIndex, err := fixture.identity.GlobalIndex(workerID, localIndex)
			if err != nil {
				t.Fatalf("worker %d GlobalIndex(%d): %v", workerID, localIndex, err)
			}
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{
				UID: fixture.identity.UID(globalIndex), UserIndex: globalIndex, LoginOrdinal: globalIndex, NewIdentity: true,
			}); err != nil {
				t.Fatalf("worker %d Login(%d): %v", workerID, localIndex, err)
			}
		}

		workerExpectedInitial := 0
		for localIndex := uint64(0); localIndex < usersPerWorker; localIndex++ {
			globalIndex, err := fixture.identity.GlobalIndex(workerID, localIndex)
			if err != nil {
				t.Fatalf("worker %d GlobalIndex(%d): %v", workerID, localIndex, err)
			}
			for distance := uint64(1); distance <= MaxForwardRelationships && distance <= localIndex; distance++ {
				ownerOrdinal := newOrdinals[localIndex-distance]
				edge, available, edgeErr := fixture.graph.IncomingEdgeForOrdinal(globalIndex, distance, ownerOrdinal)
				if edgeErr != nil {
					t.Fatalf("worker %d IncomingEdgeForOrdinal(%d, %d): %v", workerID, localIndex, distance, edgeErr)
				}
				if !available {
					continue
				}
				aggregateExpected++
				ownerWorker, _ := fixture.identity.Owner(edge.OwnerIndex)
				peerWorker, _ := fixture.identity.Owner(edge.PeerIndex)
				if ownerWorker != workerID || peerWorker != workerID {
					t.Fatalf("worker %d incoming edge crosses pools: %+v owners=%d/%d", workerID, edge, ownerWorker, peerWorker)
				}
				ordinal := ownerOrdinal*MaxForwardRelationships + distance - 1
				schedule, scheduleErr := fixture.schedule.Channel(ordinal, edge.OwnerIndex, edge.PeerIndex)
				if scheduleErr != nil {
					t.Fatalf("worker %d Channel(%d): %v", workerID, ordinal, scheduleErr)
				}
				workerExpectedInitial += schedule.InitialBurst.MessageCount
			}
			considered, activated, observeErr := fixture.engine.ObserveNewUserForOrdinal(globalIndex, newOrdinals[localIndex])
			if observeErr != nil {
				_ = fixture.engine.Stop()
				t.Fatalf("worker %d ObserveNewUser(%d): %v", workerID, localIndex, observeErr)
			}
			aggregateConsidered += considered
			aggregateActivated += activated
		}
		snapshot, err := fixture.engine.Snapshot()
		if err != nil {
			_ = fixture.engine.Stop()
			t.Fatalf("worker %d Snapshot: %v", workerID, err)
		}
		if snapshot.ActivityCurrent != workerExpectedInitial {
			_ = fixture.engine.Stop()
			t.Fatalf("worker %d initial activity = %d, want %d", workerID, snapshot.ActivityCurrent, workerExpectedInitial)
		}
		aggregateInitialBursts += workerExpectedInitial
		if err := fixture.engine.Stop(); err != nil {
			t.Fatalf("worker %d Stop: %v", workerID, err)
		}
	}
	if aggregateExpected != 1_169 || aggregateConsidered != 1_169 || aggregateActivated != 1_169 || aggregateInitialBursts != 5_932 {
		t.Fatalf("aggregate relationship history expected/considered/activated/initial = %d/%d/%d/%d, want 1169/1169/1169/5932", aggregateExpected, aggregateConsidered, aggregateActivated, aggregateInitialBursts)
	}
}

func TestEnginePlannedNewRelationshipsIgnoreAsyncCompletionOrder(t *testing.T) {
	type relationshipTotals struct {
		completedNew, activity, future, lifecycle, hot, pending, cold int
	}
	run := func(name string, reverse bool) relationshipTotals {
		t.Helper()
		const users = 13
		fixture := newEngineTestFixture(t, engineTestLimits{
			WorkerID: 0, WorkerCount: 3, OnlineUsers: 3 * users,
			WorkCapacity: 8_192, MaxWorkPerAdvance: 8_192,
		})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("%s Start: %v", name, err)
		}
		defer fixture.engine.Stop()

		newOrdinals := make([]uint64, 0, users)
		for newOrdinal := uint64(0); len(newOrdinals) < users; newOrdinal++ {
			loginOrdinal, err := fixture.schedule.loginOrdinalForNewOrdinal(newOrdinal)
			if err != nil {
				t.Fatalf("%s loginOrdinalForNewOrdinal(%d): %v", name, newOrdinal, err)
			}
			if loginOrdinal%fixture.identity.Workers() == fixture.engine.workerID {
				newOrdinals = append(newOrdinals, newOrdinal)
			}
		}
		for localIndex, newOrdinal := range newOrdinals {
			userIndex, err := fixture.identity.GlobalIndex(fixture.engine.workerID, uint64(localIndex))
			if err != nil {
				t.Fatalf("%s GlobalIndex(%d): %v", name, localIndex, err)
			}
			uid := fixture.identity.UID(userIndex)
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{
				UID: uid, UserIndex: userIndex, LoginOrdinal: newOrdinal, NewIdentity: true,
			}); err != nil {
				t.Fatalf("%s Login(%d): %v", name, localIndex, err)
			}
		}
		for position := range newOrdinals {
			localIndex := position
			if reverse {
				localIndex = len(newOrdinals) - 1 - position
			}
			userIndex, _ := fixture.identity.GlobalIndex(fixture.engine.workerID, uint64(localIndex))
			fixture.engine.loginResults <- engineLoginResult{
				login: SessionLogin{
					UID: fixture.identity.UID(userIndex), UserIndex: userIndex,
				},
				kind: LoginNew, globalNewOrdinal: newOrdinals[localIndex],
			}
		}
		step, err := fixture.engine.Step(context.Background(), fixture.clock.Now(), nil)
		if err != nil {
			t.Fatalf("%s Step: %v", name, err)
		}
		snapshot, err := fixture.engine.Snapshot()
		if err != nil {
			t.Fatalf("%s Snapshot: %v", name, err)
		}
		return relationshipTotals{
			completedNew: step.CompletedNew, activity: snapshot.ActivityCurrent,
			future: snapshot.FutureCurrent, lifecycle: snapshot.ActiveLifecycleTimers,
			hot: snapshot.ActiveHotChannels, pending: snapshot.PendingHotChannels,
			cold: snapshot.ColdEvidencePending,
		}
	}

	forward := run("forward", false)
	reversed := run("reversed", true)
	if forward != reversed || forward.completedNew != 13 || forward.activity == 0 {
		t.Fatalf("planned new relationship totals forward=%+v reversed=%+v, want identical nonzero plans", forward, reversed)
	}
}

func TestEngineRealAsyncNewLoginOrderPublishesOneIdenticalRelationship(t *testing.T) {
	run := func(name string, reverse bool) EngineSnapshot {
		t.Helper()
		fixture := newEngineTestFixture(t, engineTestLimits{
			Formal: true, WorkerID: 0, WorkerCount: 3, OnlineUsers: 6,
			StartingCapacity: 4, WorkCapacity: 512, MaxWorkPerAdvance: 512,
		})
		lowIndex, err := fixture.identity.GlobalIndex(0, 0)
		if err != nil {
			t.Fatalf("%s low GlobalIndex: %v", name, err)
		}
		highIndex, err := fixture.identity.GlobalIndex(0, 1)
		if err != nil {
			t.Fatalf("%s high GlobalIndex: %v", name, err)
		}
		lowUID := fixture.identity.UID(lowIndex)
		highUID := fixture.identity.UID(highIndex)
		lowRelease := make(chan struct{})
		highRelease := make(chan struct{})
		fixture.factory.connectStartedUID = make(chan string, 2)
		fixture.factory.readStartedUID = make(chan string, 2)
		fixture.factory.connectReleaseUID = map[string]<-chan struct{}{
			lowUID: lowRelease, highUID: highRelease,
		}
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("%s Start: %v", name, err)
		}
		defer fixture.engine.Stop()
		fixture.engine.scheduler.bootstrapping = false

		now := fixture.clock.Now().Add(7 * time.Second)
		fixture.clock.Set(now)
		for attempts := 0; attempts < 32 && fixture.pool.Counts().Starting < 2; attempts++ {
			if _, err := fixture.engine.Step(context.Background(), now, nil); err != nil {
				t.Fatalf("%s scheduling Step %d: %v", name, attempts, err)
			}
			runtime.Gosched()
		}
		if counts := fixture.pool.Counts(); counts.Starting != 2 || counts.Online != 0 {
			t.Fatalf("%s reserved startup counts = %+v, want two starting and none online", name, counts)
		}
		started := map[string]bool{}
		for attempts := 0; attempts < 10_000 && len(started) < 2; attempts++ {
			select {
			case uid := <-fixture.factory.connectStartedUID:
				started[uid] = true
			default:
				runtime.Gosched()
			}
		}
		if !started[lowUID] || !started[highUID] {
			t.Fatalf("%s CONNECT starts = %v, want low/high", name, started)
		}

		firstUID, secondUID := lowUID, highUID
		firstRelease, secondRelease := lowRelease, highRelease
		if reverse {
			firstUID, secondUID = highUID, lowUID
			firstRelease, secondRelease = highRelease, lowRelease
		}
		drainOne := func(uid string, release chan struct{}) {
			t.Helper()
			close(release)
			readStarted := false
			for attempts := 0; attempts < 10_000 && !readStarted; attempts++ {
				select {
				case got := <-fixture.factory.readStartedUID:
					if got != uid {
						t.Fatalf("%s released %s but read drain started for %s", name, uid, got)
					}
					readStarted = true
				default:
					runtime.Gosched()
				}
			}
			if !readStarted {
				t.Fatalf("%s login %s never became traffic-ready", name, uid)
			}
			completed := 0
			for attempts := 0; attempts < 10_000 && completed == 0; attempts++ {
				step, stepErr := fixture.engine.Step(context.Background(), now, nil)
				if stepErr != nil {
					t.Fatalf("%s completion Step for %s: %v", name, uid, stepErr)
				}
				completed += step.CompletedNew
				if completed == 0 {
					runtime.Gosched()
				}
			}
			if completed != 1 {
				t.Fatalf("%s completed new for %s = %d, want one", name, uid, completed)
			}
		}
		drainOne(firstUID, firstRelease)
		drainOne(secondUID, secondRelease)

		snapshot, err := fixture.engine.Snapshot()
		if err != nil {
			t.Fatalf("%s Snapshot: %v", name, err)
		}
		for localNewIndex, userIndex := range []uint64{lowIndex, highIndex} {
			globalNewOrdinal, ordinalErr := fixture.schedule.GlobalNewOrdinalFor(0, uint64(localNewIndex))
			if ordinalErr != nil {
				t.Fatalf("%s duplicate ordinal %d: %v", name, localNewIndex, ordinalErr)
			}
			considered, activated, observeErr := fixture.engine.ObserveNewUserForOrdinal(userIndex, globalNewOrdinal)
			if observeErr != nil || considered != 0 || activated != 0 {
				t.Fatalf("%s duplicate ObserveNewUserForOrdinal(%d) = %d/%d, %v", name, userIndex, considered, activated, observeErr)
			}
		}
		afterDuplicate, err := fixture.engine.Snapshot()
		if err != nil {
			t.Fatalf("%s duplicate Snapshot: %v", name, err)
		}
		if afterDuplicate.ActivityCurrent != snapshot.ActivityCurrent || afterDuplicate.FutureCurrent != snapshot.FutureCurrent {
			t.Fatalf("%s duplicate observation changed activity before=%+v after=%+v", name, snapshot, afterDuplicate)
		}
		return snapshot
	}

	forward := run("forward", false)
	reversed := run("reversed", true)
	if forward.ActivityCurrent != 3 || forward.FutureCurrent != 3 ||
		forward.ActivityCurrent != reversed.ActivityCurrent ||
		forward.FutureCurrent != reversed.FutureCurrent || forward.ActiveLifecycleTimers != reversed.ActiveLifecycleTimers {
		t.Fatalf("real async relationship totals forward=%+v reversed=%+v", forward, reversed)
	}
}

func TestEngineStopCancelsBlockedStepSendBeforeClosingSessions(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 1, WorkCapacity: 64, MaxWorkPerAdvance: 64,
	})
	fixture.factory.sendStarted = make(chan context.Context, 1)
	fixture.factory.sendCanceled = make(chan struct{}, 1)
	fixture.factory.sendReturn = make(chan struct{})
	fixture.factory.sendAbort = make(chan struct{})
	fixture.factory.closeCalled = make(chan struct{}, 1)
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	sender := fixture.identity.UID(0)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{
		UID: sender, UserIndex: 0, LoginOrdinal: 0,
	}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	now := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(fixture.intent(t, sender, "group-1", 1, TrafficGroup), now); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}

	stepDone := make(chan error, 1)
	go func() {
		_, err := fixture.engine.Step(context.Background(), now, nil)
		stepDone <- err
	}()
	var sendCtx context.Context
	select {
	case sendCtx = <-fixture.factory.sendStarted:
	case <-time.After(time.Second):
		close(fixture.factory.sendAbort)
		t.Fatal("Step never reached the blocking SessionClient.Send")
	}
	if sendCtx == nil || sendCtx.Done() == nil {
		close(fixture.factory.sendAbort)
		t.Fatal("SessionClient.Send received a non-cancelable context")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- fixture.engine.Stop() }()
	select {
	case <-fixture.factory.sendCanceled:
	case <-time.After(time.Second):
		close(fixture.factory.sendAbort)
		<-stopDone
		t.Fatal("Stop did not cancel the blocked SessionClient.Send")
	}
	select {
	case <-fixture.factory.closeCalled:
		close(fixture.factory.sendAbort)
		t.Fatal("SessionClient.Close ran before the blocked Step joined")
	default:
	}
	close(fixture.factory.sendReturn)
	if err := <-stepDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Step error = %v, want nil or context cancellation", err)
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(time.Second):
		close(fixture.factory.sendAbort)
		t.Fatal("Stop remained blocked after the canceled Send returned")
	}
	select {
	case <-fixture.factory.closeCalled:
	default:
		t.Fatal("Stop returned without closing the session after Step joined")
	}
	if evidence := fixture.evidence.Snapshot(); evidence.Classification != "" || len(evidence.Classes) != 0 {
		t.Fatalf("generation cancellation recorded evidence: %+v", evidence)
	}
}

func TestEngineWorkerSchedulersPartitionOneGlobalLoginCreditStream(t *testing.T) {
	const workerCount = 3
	shares := [workerCount]int{}
	for workerID := uint64(0); workerID < workerCount; workerID++ {
		fixture := newEngineTestFixture(t, engineTestLimits{
			Formal: true, WorkerID: workerID, WorkerCount: workerCount,
			StartingCapacity: 512, WorkCapacity: 8_192, MaxWorkPerAdvance: 512,
		})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("worker %d Start: %v", workerID, err)
		}
		now := fixture.clock.Now().Add(100 * time.Second)
		fixture.clock.Set(now)
		step, err := fixture.engine.Step(context.Background(), now, nil)
		if err != nil {
			_ = fixture.engine.Stop()
			t.Fatalf("worker %d Step: %v", workerID, err)
		}
		shares[workerID] = step.PlannedNew + step.PlannedReturning
		if err := fixture.engine.Stop(); err != nil {
			t.Fatalf("worker %d Stop: %v", workerID, err)
		}
	}

	total := shares[0] + shares[1] + shares[2]
	if total != 361 {
		t.Fatalf("100-second global login credit = %d (%v), want exact 361", total, shares)
	}
	minimum, maximum := shares[0], shares[0]
	for _, share := range shares[1:] {
		if share < minimum {
			minimum = share
		}
		if share > maximum {
			maximum = share
		}
	}
	if maximum-minimum > 1 {
		t.Fatalf("100-second worker login shares = %v, want difference <= 1", shares)
	}
}

func TestSessionSchedulersPreserveExactFormalDailyGrowthAndCombinedEightyTwenty(t *testing.T) {
	const workerCount = 3
	cfg := FormalConfig()
	identity, err := NewIdentitySpace("scheduler-daily-partition", 101, workerCount)
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	schedule, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel: %v", err)
	}
	start := time.Unix(1_700_000_000, 0)
	schedulers := [workerCount]sessionScheduler{}
	shares := [workerCount]uint64{}
	var plannedNew, plannedReturning uint64
	for workerID := uint64(0); workerID < workerCount; workerID++ {
		schedulers[workerID] = sessionScheduler{
			workload: cfg.Workload, workerID: workerID, workerCount: workerCount, onlineTarget: 1_000_000,
		}
		schedulers[workerID].reset(start)
	}

	for second := 1; second <= secondsPerDay; second++ {
		now := start.Add(time.Duration(second) * time.Second)
		for workerID := uint64(0); workerID < workerCount; workerID++ {
			scheduler := &schedulers[workerID]
			budget, releaseErr := scheduler.release(now)
			if releaseErr != nil {
				t.Fatalf("worker %d release(%d): %v", workerID, second, releaseErr)
			}
			for ; budget > 0; budget-- {
				ordinal, ordinalErr := scheduler.nextGlobalLoginOrdinal()
				if ordinalErr != nil {
					t.Fatalf("worker %d nextGlobalLoginOrdinal: %v", workerID, ordinalErr)
				}
				login, loginErr := schedule.Login(ordinal)
				if loginErr != nil {
					t.Fatalf("worker %d Login(%d): %v", workerID, ordinal, loginErr)
				}
				if login.Identity == LoginNew {
					plannedNew++
				} else {
					plannedReturning++
				}
				scheduler.consumeOne()
				shares[workerID]++
			}
		}
	}

	total := plannedNew + plannedReturning
	if total != 312_500 || plannedNew != 250_000 || plannedReturning != 62_500 {
		t.Fatalf("formal daily login stream total/new/returning = %d/%d/%d shares=%v, want 312500/250000/62500", total, plannedNew, plannedReturning, shares)
	}
	minimum, maximum := shares[0], shares[0]
	for _, share := range shares[1:] {
		if share < minimum {
			minimum = share
		}
		if share > maximum {
			maximum = share
		}
	}
	if maximum-minimum > 1 {
		t.Fatalf("formal daily worker shares = %v, want difference <= 1", shares)
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
	scheduler := sessionScheduler{workload: cfg.Workload, workerCount: 1, onlineTarget: cfg.Workload.OnlineUsers}
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
	scheduler := fixture.engine.scheduler
	scheduler.nextNewIndex = 750_000
	scheduler.bootstrapping = false
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
		sampled, sampleErr := fixture.verifier.ShouldCorrelate(LogicalSend{
			LogicalSend: candidate, WorkerID: 0, Kind: TrafficGroup,
		})
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

func TestEngineThreeWorkerPoolsRouteSampledGroupsOnlyOnOwnerWithPairedRoster(t *testing.T) {
	const workerCount = 3
	fixtures := [workerCount]engineTestFixture{}
	for workerID := uint64(0); workerID < workerCount; workerID++ {
		fixtures[workerID] = newEngineTestFixture(t, engineTestLimits{
			Formal: true, WorkerID: workerID, WorkerCount: workerCount,
			WorkCapacity: 1_024, MaxWorkPerAdvance: 1_024,
		})
		if err := fixtures[workerID].engine.Start(context.Background()); err != nil {
			t.Fatalf("worker %d Start: %v", workerID, err)
		}
	}
	defer func() {
		for workerID := range fixtures {
			_ = fixtures[workerID].engine.Stop()
		}
	}()

	type routeResult struct {
		intent TrafficIntent
		err    error
	}
	for owner := uint64(0); owner < workerCount; owner++ {
		fixture := &fixtures[owner]
		first, ok, err := fixture.engine.scheduler.nextGroupReturningMember(fixture.engine.generator.catalog, 0, ^uint64(0))
		if err != nil || !ok {
			t.Fatalf("worker %d first returning member = %+v, %v, %v", owner, first, ok, err)
		}
		second, ok, err := fixture.engine.scheduler.nextGroupReturningMember(fixture.engine.generator.catalog, 0, ^uint64(0))
		if err != nil || !ok {
			t.Fatalf("worker %d second returning member = %+v, %v, %v", owner, second, ok, err)
		}
		groupOwner, err := fixture.engine.generator.catalog.GroupOwner(first.Group.Index)
		if err != nil || groupOwner != owner || second.Group.Index != first.Group.Index || second.UserIndex == first.UserIndex {
			t.Fatalf("worker %d returning pair = %+v / %+v group_owner=%d err=%v", owner, first, second, groupOwner, err)
		}
		for pairIndex, member := range []GroupReturningMember{first, second} {
			memberOwner, _ := fixture.identity.Owner(member.UserIndex)
			if memberOwner != owner {
				t.Fatalf("worker %d pair member %d owner = %d: %+v", owner, pairIndex, memberOwner, member)
			}
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{
				UID: fixture.identity.UID(member.UserIndex), UserIndex: member.UserIndex, LoginOrdinal: uint64(pairIndex),
			}); err != nil {
				t.Fatalf("worker %d pair Login(%d): %v", owner, pairIndex, err)
			}
			_, hasRecipient := fixture.pool.onlineGroupMember(first.Group, uint64(pairIndex), true)
			if (pairIndex == 0 && hasRecipient) || (pairIndex == 1 && !hasRecipient) {
				t.Fatalf("worker %d sampled recipient readiness after member %d = %v", owner, pairIndex+1, hasRecipient)
			}
		}

		var sampledLogical uint64
		for raw := uint64(0); raw < 1_000; raw++ {
			candidate, scopedErr := scopedLogicalOrdinal(1, LogicalDomainGroup, raw)
			if scopedErr != nil {
				t.Fatalf("worker %d scoped group ordinal: %v", owner, scopedErr)
			}
			sampled, sampleErr := fixture.verifier.ShouldCorrelate(LogicalSend{
				LogicalSend: candidate, WorkerID: uint32(owner), Kind: TrafficGroup,
			})
			if sampleErr != nil {
				t.Fatalf("worker %d ShouldCorrelate: %v", owner, sampleErr)
			}
			if sampled {
				sampledLogical = candidate
				break
			}
		}
		if sampledLogical == 0 {
			t.Fatalf("worker %d found no sampled group logical ordinal", owner)
		}
		grant := TrafficIntent{
			Logical: LogicalSend{LogicalSend: sampledLogical, WorkerID: uint32(owner), Kind: TrafficGroup},
			Kind:    TrafficGroup, ChannelID: first.Group.ID, GroupCategory: first.Group.Category,
			PayloadBytes: 256, Domain: LogicalDomainGroup,
		}
		route := func(target *engineTestFixture) (TrafficIntent, error) {
			response := make(chan routeResult, 1)
			if err := target.engine.enqueue(engineCommand{run: func() {
				intent, routeErr := target.engine.routeGroupGrant(grant)
				response <- routeResult{intent: intent, err: routeErr}
			}}); err != nil {
				return TrafficIntent{}, err
			}
			result := <-response
			return result.intent, result.err
		}
		routed, err := route(fixture)
		senderIndex, senderOK := fixture.identity.IndexFromUID(routed.Logical.Sender)
		if err != nil || routed.Packet == nil || !senderOK || !first.Group.ContainsIndex(senderIndex) {
			t.Fatalf("worker %d owned sampled route = %+v sender=%d/%v err=%v", owner, routed, senderIndex, senderOK, err)
		}
		for other := uint64(0); other < workerCount; other++ {
			if other == owner {
				continue
			}
			if _, err := route(&fixtures[other]); !errors.Is(err, errEngineConfig) {
				t.Fatalf("worker %d routed group owned by %d: %v", other, owner, err)
			}
			if evidence := fixtures[other].evidence.Snapshot(); evidenceCountForClass(evidence, FailureClassHarness) != 0 {
				t.Fatalf("worker %d recorded responsibility evidence for owner %d: %+v", other, owner, evidence)
			}
		}
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
	bootstrap, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("bootstrap Step = %+v, %v", bootstrap, err)
	}
	bootstrap = fixture.settleScheduledLogins(t, now, bootstrap)
	if bootstrap.Online != 20 {
		t.Fatalf("settled bootstrap Step = %+v", bootstrap)
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
	replacement = fixture.settleScheduledLogins(t, now, replacement)
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
	bootstrap, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("bootstrap Step = %+v, %v", bootstrap, err)
	}
	bootstrap = fixture.settleScheduledLogins(t, now, bootstrap)
	if bootstrap.Online != 20 {
		t.Fatalf("settled bootstrap Step = %+v", bootstrap)
	}
	now = now.Add(time.Minute)
	fixture.clock.Set(now)
	replacement, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("expiry replacement Step: %v", err)
	}
	replacement = fixture.settleScheduledLogins(t, now, replacement)
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
	if step.AdmittedNew != 1 || fixture.evidence.Snapshot().Classification != SyncClassificationHarnessInvalid {
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
	bootstrap, err := fixture.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("bootstrap Step: %v", err)
	}
	fixture.settleScheduledLogins(t, now, bootstrap)
	now = now.Add(30 * time.Second)
	fixture.clock.Set(now)
	for iteration := 0; iteration < 100; iteration++ {
		snapshot, _ := fixture.engine.Snapshot()
		if snapshot.ActivityCurrent == 0 {
			break
		}
		if _, err := fixture.engine.Tick(now, fixture.demand(1_000)); err != nil {
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
	activeTick, err := fixture.engine.Tick(now, fixture.demand(1_000))
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
	_, err = fixture.engine.Tick(now, fixture.demand(1_000))
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
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index, NewIdentity: true}); err != nil {
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
	if full.ActiveHotChannels != fixture.engine.generator.hotSet.PersonChannels || full.ActiveHotChannels != 80 {
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
	if refilled.ActiveHotChannels != 80 || refilled.ActiveHotChannels > fixture.engine.generator.hotSet.PersonChannels {
		t.Fatalf("rotated active hot set = %+v", refilled)
	}
}

func TestEnginePartitionsFormalGlobalPersonHotSetAcrossWorkers(t *testing.T) {
	want := []int{2_667, 2_667, 2_666}
	totalActive := 0
	totalPending := 0
	for workerID, wantActive := range want {
		fixture := newEngineTestFixture(t, engineTestLimits{
			Formal: true, WorkerID: uint64(workerID), WorkerCount: uint64(len(want)),
			WorkCapacity: 512, MaxWorkPerAdvance: 512,
		})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("worker %d Start: %v", workerID, err)
		}
		t.Cleanup(func() { _ = fixture.engine.Stop() })

		userIndex, err := fixture.identity.GlobalIndex(uint64(workerID), 18)
		if err != nil {
			t.Fatalf("worker %d GlobalIndex: %v", workerID, err)
		}
		edge := fixture.graph.Incoming(userIndex).Items[0]
		for _, endpoint := range []struct {
			uid   string
			index uint64
		}{{edge.OwnerUID, edge.OwnerIndex}, {edge.PeerUID, edge.PeerIndex}} {
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{
				UID: endpoint.uid, UserIndex: endpoint.index, LoginOrdinal: endpoint.index,
			}); err != nil {
				t.Fatalf("worker %d Login(%q): %v", workerID, endpoint.uid, err)
			}
		}

		filled := make(chan struct{}, 1)
		if err := fixture.engine.enqueue(engineCommand{run: func() {
			for index := 0; index < wantActive; index++ {
				fixture.engine.addActiveChannel(engineActiveChannel{edge: RelationshipEdge{
					PersonChannelID: fmt.Sprintf("worker-%d-occupied-%d", workerID, index),
				}})
			}
			filled <- struct{}{}
		}}); err != nil {
			t.Fatalf("worker %d fill active set: %v", workerID, err)
		}
		<-filled
		relationshipOrdinal, _ := findLifecycleSchedule(t, fixture.schedule, edge, LifecycleRotating)
		if activated, err := fixture.engine.ActivateRelationship(edge, relationshipOrdinal); err != nil || !activated {
			t.Fatalf("worker %d extra activation = %v, %v", workerID, activated, err)
		}
		snapshot, err := fixture.engine.Snapshot()
		if err != nil {
			t.Fatalf("worker %d Snapshot: %v", workerID, err)
		}
		if snapshot.ActiveHotChannels != wantActive || snapshot.PendingHotChannels != 1 {
			t.Fatalf("worker %d hot-set cap = active %d pending %d, want %d/1", workerID, snapshot.ActiveHotChannels, snapshot.PendingHotChannels, wantActive)
		}
		totalActive += snapshot.ActiveHotChannels
		totalPending += snapshot.PendingHotChannels
	}
	if totalActive != 8_000 || totalPending != len(want) {
		t.Fatalf("aggregate hot set = active %d pending %d, want 8000/%d", totalActive, totalPending, len(want))
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
		for index := 0; index < fixture.engine.generator.hotSet.PersonChannels; index++ {
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
	if snapshot.ActivityCurrent != schedule.InitialBurst.MessageCount || snapshot.ActiveHotChannels != fixture.engine.generator.hotSet.PersonChannels || snapshot.PendingHotChannels != 1 || snapshot.ActiveLifecycleTimers != 2 {
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
	if promoted.ActiveHotChannels != fixture.engine.generator.hotSet.PersonChannels || promoted.PendingHotChannels != 0 || promoted.ActiveLifecycleTimers != 1 {
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
	// Two fixed-roster members per group are online so the exact sampled group
	// grants exercise a real distinct delivery recipient.
	for index := uint64(0); index < 4_000; index++ {
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
	tick, err := fixture.engine.Tick(now, fixture.demand(10_000))
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
	WorkerID                  uint64
	WorkerCount               uint64
	StartingCapacity          int
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

func newEngineTestFixture(t testing.TB, limits engineTestLimits) engineTestFixture {
	t.Helper()
	cfg := LocalConfig()
	if limits.Formal {
		cfg = FormalConfig()
	}
	workerCount := limits.WorkerCount
	if workerCount == 0 {
		workerCount = 1
	}
	cfg.Workload.Workers = int(workerCount)
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
		PendingCapacity: 512, SequenceCapacity: 512, CorrelationCapacity: 512, CorrelationDeadline: time.Minute,
	}, evidence)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	clock := &sessionFakeClock{now: time.Unix(1_700_000_000, 0)}
	factory := &engineFakeFactory{}
	startingCapacity := limits.StartingCapacity
	if startingCapacity == 0 {
		startingCapacity = 128
	}
	pool, err := NewSessionPool(SessionPoolConfig{
		Identity: identity, Schedule: schedule, Catalog: catalog, Factory: factory, Syncer: engineSyncer{},
		Verifier: verifier, Clock: clock, DeviceID: "engine-test", StartingCapacity: startingCapacity,
	})
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	generator, err := NewTrafficGenerator(TrafficGeneratorConfig{
		Identity: identity, Model: traffic, Catalog: catalog, Workload: cfg.Workload, Start: clock.Now(),
		WorkerID: limits.WorkerID, WorkerCount: workerCount,
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
		WorkerID: limits.WorkerID, WorkerCount: workerCount,
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

func (f engineTestFixture) demand(perWorker uint64) []uint64 {
	demand := make([]uint64, f.identity.Workers())
	for worker := range demand {
		demand[worker] = perWorker
	}
	return demand
}

func (f engineTestFixture) settleScheduledLogins(t *testing.T, now time.Time, initial EngineStepSnapshot) EngineStepSnapshot {
	t.Helper()
	f.engine.loginOps.Wait()
	completion, err := f.engine.Step(context.Background(), now, nil)
	if err != nil {
		t.Fatalf("settle scheduled logins: %v", err)
	}
	initial.PlannedNew += completion.PlannedNew
	initial.PlannedReturning += completion.PlannedReturning
	initial.AdmittedNew += completion.AdmittedNew
	initial.AdmittedReturning += completion.AdmittedReturning
	initial.CompletedNew += completion.CompletedNew
	initial.CompletedReturning += completion.CompletedReturning
	initial.LoginsCompleted += completion.LoginsCompleted
	initial.BootstrapNew += completion.BootstrapNew
	initial.LoginsSkipped += completion.LoginsSkipped
	initial.ReplacementLogins += completion.ReplacementLogins
	initial.Expired += completion.Expired
	initial.Traffic.Add(completion.Traffic)
	initial.Advanced += completion.Advanced
	initial.Online = completion.Online
	return initial
}

func (f engineTestFixture) intent(t testing.TB, sender, target string, ordinal uint64, kind TrafficKind) TrafficIntent {
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
	connectStartedUID chan string
	connectReleaseUID map[string]<-chan struct{}
	readContexts      chan<- context.Context
	readStartedUID    chan string
	readCycles        chan<- struct{}
	sendStarted       chan context.Context
	sendCanceled      chan struct{}
	sendReturn        chan struct{}
	sendAbort         chan struct{}
	closeCalled       chan struct{}
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
	if c.factory.connectStartedUID != nil {
		c.factory.connectStartedUID <- c.uid
		c.factory.mu.Lock()
		release := c.factory.connectReleaseUID[c.uid]
		c.factory.mu.Unlock()
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-release:
			return nil
		}
	}
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
		if c.factory.readStartedUID != nil {
			c.factory.readStartedUID <- c.uid
		}
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
func (c *engineFakeClient) Send(ctx context.Context, packet *frame.SendPacket) error {
	c.mu.Lock()
	c.sent = append(c.sent, packet)
	messageID := int64(len(c.sent))
	c.mu.Unlock()
	c.factory.recordRoute(engineSentRoute{uid: c.uid, packet: packet})
	if c.factory.sendStarted != nil {
		c.factory.sendStarted <- ctx
		select {
		case <-ctx.Done():
			c.factory.sendCanceled <- struct{}{}
		case <-c.factory.sendAbort:
			return errors.New("test send abort")
		}
		select {
		case <-c.factory.sendReturn:
			return context.Cause(ctx)
		case <-c.factory.sendAbort:
			return errors.New("test send abort")
		}
	}
	err := c.factory.nextSendError()
	if err == nil && c.factory.autoAck {
		c.frames <- &frame.SendackPacket{
			ClientSeq: packet.ClientSeq, ClientMsgNo: packet.ClientMsgNo,
			MessageID: messageID, MessageSeq: uint64(messageID), ReasonCode: frame.ReasonSuccess,
		}
	}
	return err
}
func (c *engineFakeClient) AckRecv(context.Context, *frame.RecvackPacket) error { return nil }
func (c *engineFakeClient) Close() error {
	c.closeOnce.Do(func() {
		close(c.stop)
		if c.factory.closeCalled != nil {
			c.factory.closeCalled <- struct{}{}
		}
	})
	return nil
}
func (c *engineFakeClient) QueueSnapshot() SessionQueueSnapshot { return SessionQueueSnapshot{} }
func (c *engineFakeClient) ReadErrorInfo(err error) (wkproto.ReadErrorInfo, bool) {
	var readErr *engineFakeReadError
	if !errors.As(err, &readErr) {
		return wkproto.ReadErrorInfo{}, false
	}
	return wkproto.ReadErrorInfo{Kind: readErr.kind, ClientSeq: readErr.clientSeq, ClientMsgNo: readErr.clientMsgNo}, true
}

type engineFakeReadError struct {
	kind        wkproto.ReadErrorKind
	clientSeq   uint64
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
