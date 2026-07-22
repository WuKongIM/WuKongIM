package channelappend

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestRecipientProcessorPushesDeliveryWithPresenceAndPusher(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{}
	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{{UID: "u2"}},
	}, recipientPorts{
		presence:                 &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		pusher:                   pusher,
		deliveryRetryMaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := pusher.callCount(); got != 1 {
		t.Fatalf("push calls = %d, want delivery push", got)
	}
}

func TestRecipientProcessorCloudMediumPlanAllocationBudget(t *testing.T) {
	plan, resolver := benchmarkCloudMediumRecipientPlan(512, 221, 55)
	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver: resolver,
		OwnerPusher:      benchmarkRecipientPlanPusher{},
	})

	allocs := testing.AllocsPerRun(20, func() {
		if errs := processor.ProcessRecipientDeliveryPlan(context.Background(), plan); firstBenchmarkRecipientPlanError(errs) != nil {
			t.Fatalf("ProcessRecipientDeliveryPlan() errors = %#v", errs)
		}
	})
	if allocs > 80 {
		t.Fatalf("Cloud Medium recipient plan allocations = %.0f, want <= 80", allocs)
	}
}

func TestRecipientProcessorLegacyPresenceOwnerPushChunksStayBoundedAndOrdered(t *testing.T) {
	routes := []Route{
		{UID: "u1", OwnerNodeID: 3, SessionID: 10},
		{UID: "u2", OwnerNodeID: 3, SessionID: 20},
		{UID: "u3", OwnerNodeID: 3, SessionID: 30},
		{UID: "u4", OwnerNodeID: 3, SessionID: 40},
		{UID: "u5", OwnerNodeID: 3, SessionID: 50},
	}
	pusher := &recordingOwnerPusherForDeliveryTest{}
	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver:   &recordingPresenceResolverForDeliveryTest{routes: routes},
		OwnerPusher:        pusher,
		OwnerPushBatchSize: 2,
	})

	err := processor.ProcessRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{
			{UID: "u1"}, {UID: "u2"}, {UID: "u3"}, {UID: "u4"}, {UID: "u5"},
		},
	})
	if err != nil {
		t.Fatalf("ProcessRecipientBatch() error = %v", err)
	}
	commands := pusher.commandsSnapshot()
	if got, want := len(commands), 3; got != want {
		t.Fatalf("owner push calls = %d, want %d bounded chunks", got, want)
	}
	wantUIDs := [][]string{{"u1", "u2"}, {"u3", "u4"}, {"u5"}}
	for i, command := range commands {
		gotUIDs := make([]string, 0, len(command.Routes))
		for _, route := range command.Routes {
			gotUIDs = append(gotUIDs, route.UID)
		}
		if !reflect.DeepEqual(gotUIDs, wantUIDs[i]) {
			t.Fatalf("chunk %d routes = %#v, want %#v", i, gotUIDs, wantUIDs[i])
		}
	}
}

func TestRecipientProcessorSkipsOnlySameSenderOwnerSessionEcho(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{}
	resolver := &recordingPresenceResolverForDeliveryTest{routes: []Route{
		{UID: "u1", OwnerNodeID: 7, SessionID: 100, DeviceID: "same-session"},
		{UID: "u1", OwnerNodeID: 7, SessionID: 101, DeviceID: "same-owner-other-session"},
		{UID: "u1", OwnerNodeID: 8, SessionID: 100, DeviceID: "other-owner-same-session"},
		{UID: "u2", OwnerNodeID: 7, SessionID: 200, DeviceID: "other-user"},
	}}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{
			MessageID:         10,
			MessageSeq:        4,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "u1",
			SenderNodeID:      7,
			SenderSessionID:   100,
			ServerTimestampMS: 123,
		},
		Recipients: []Recipient{{UID: "u1"}, {UID: "u2"}},
	}, recipientPorts{
		presence:                 resolver,
		pusher:                   pusher,
		deliveryRetryMaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}

	got := deviceIDSetForDeliveryTest(pusher.deviceIDs())
	want := map[string]bool{"same-owner-other-session": true, "other-owner-same-session": true, "other-user": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pushed device ids = %#v, want %#v", got, want)
	}
}

func TestRetryableOwnerPushRoutesAreRetriedWithBoundedBackoff(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{
		results: []PushResult{
			{Retryable: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
			{Accepted: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		},
	}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{
			{UID: "u2"},
		},
	}, recipientPorts{
		presence:                    &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		pusher:                      pusher,
		deliveryRetryMaxAttempts:    3,
		deliveryRetryInitialBackoff: time.Millisecond,
		deliveryRetryMaxBackoff:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := pusher.callCount(); got != 2 {
		t.Fatalf("push calls = %d, want retry then success", got)
	}
}

func TestRetryableOwnerPushRoutesReturnErrorAfterMaxAttempts(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{
		results: []PushResult{
			{Retryable: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
			{Retryable: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		},
	}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{{UID: "u2"}},
	}, recipientPorts{
		presence:                    &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		pusher:                      pusher,
		deliveryRetryMaxAttempts:    2,
		deliveryRetryInitialBackoff: time.Millisecond,
		deliveryRetryMaxBackoff:     time.Millisecond,
	})
	if !errors.Is(err, ErrDeliveryRetryExhausted) {
		t.Fatalf("processRecipientBatch() error = %v, want ErrDeliveryRetryExhausted", err)
	}
	if got := pusher.callCount(); got != 2 {
		t.Fatalf("push calls = %d, want max attempts", got)
	}
}

func TestRecipientProcessorCoalescesOwnerPushesAcrossExactTargets(t *testing.T) {
	const targetCount = 7
	targets := make([]RecipientTargetBatch, 0, targetCount)
	results := make([]RecipientTargetPresenceResult, 0, targetCount)
	for i := 0; i < targetCount; i++ {
		uid := "u" + strconv.Itoa(i+1)
		ownerNodeID := uint64(i%3 + 1)
		targets = append(targets, RecipientTargetBatch{
			Target:     recipientAuthorityTargetForTest(uint16(i+1), uint64(i+10), uint64(i+100)),
			Recipients: []Recipient{{UID: uid}},
		})
		results = append(results, RecipientTargetPresenceResult{Routes: []Route{{
			UID:         uid,
			OwnerNodeID: ownerNodeID,
			SessionID:   uint64(i + 1000),
		}}})
	}
	resolver := &recordingTargetPresenceResolverForDeliveryWorkerTest{results: results}
	pusher := &recordingOwnerPusherForDeliveryTest{}
	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver: resolver,
		OwnerPusher:      pusher,
	})

	errs := processor.ProcessRecipientDeliveryPlan(context.Background(), RecipientDeliveryPlan{
		Event:   CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Targets: targets,
	})

	if got, want := errs, make([]error, targetCount); !reflect.DeepEqual(got, want) {
		t.Fatalf("ProcessRecipientDeliveryPlan() errors = %#v, want %#v", got, want)
	}
	commands := pusher.commandsSnapshot()
	if got, want := len(commands), 3; got != want {
		t.Fatalf("owner push calls = %d, want %d plan-wide owner batches", got, want)
	}
	wantUIDs := map[uint64][]string{
		1: {"u1", "u4", "u7"},
		2: {"u2", "u5"},
		3: {"u3", "u6"},
	}
	for _, command := range commands {
		if _, ok := wantUIDs[command.OwnerNodeID]; !ok {
			t.Fatalf("unexpected owner push: %+v", command)
		}
		gotUIDs := make([]string, 0, len(command.Routes))
		for _, route := range command.Routes {
			gotUIDs = append(gotUIDs, route.UID)
		}
		if want := wantUIDs[command.OwnerNodeID]; !reflect.DeepEqual(gotUIDs, want) {
			t.Fatalf("owner %d routes = %#v, want %#v", command.OwnerNodeID, gotUIDs, want)
		}
	}
}

func TestRecipientProcessorOverlapsIndependentOwnerPushesAndKeepsTargetResultsAligned(t *testing.T) {
	wantOwnerTwoErr := errors.New("owner two push failed")
	pusher := &overlapBarrierOwnerPusherForDeliveryTest{
		started: make(chan uint64, 2),
		release: make(chan struct{}),
		errs:    map[uint64]error{2: wantOwnerTwoErr},
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(pusher.release) })
	}
	defer release()

	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver: &recordingTargetPresenceResolverForDeliveryWorkerTest{results: []RecipientTargetPresenceResult{
			{Routes: []Route{{UID: "u1", OwnerNodeID: 1, SessionID: 10}}},
			{Routes: []Route{{UID: "u2", OwnerNodeID: 2, SessionID: 20}}},
		}},
		OwnerPusher:              pusher,
		DeliveryRetryMaxAttempts: 1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resultC := make(chan []error, 1)
	go func() {
		resultC <- processor.ProcessRecipientDeliveryPlan(ctx, RecipientDeliveryPlan{
			Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
			Targets: []RecipientTargetBatch{
				{Target: recipientAuthorityTargetForTest(1, 10, 100), Recipients: []Recipient{{UID: "u1"}}},
				{Target: recipientAuthorityTargetForTest(2, 20, 200), Recipients: []Recipient{{UID: "u2"}}},
			},
		})
	}()

	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	startedOwners := make(map[uint64]struct{}, 2)
	for len(startedOwners) < 2 {
		select {
		case ownerNodeID := <-pusher.started:
			startedOwners[ownerNodeID] = struct{}{}
		case <-timer.C:
			t.Fatalf("independent owner pushes did not overlap: started owners = %v", startedOwners)
		case <-ctx.Done():
			t.Fatalf("independent owner pushes did not overlap before context ended: %v", ctx.Err())
		}
	}
	release()

	select {
	case got := <-resultC:
		if len(got) != 2 || got[0] != nil || !errors.Is(got[1], wantOwnerTwoErr) {
			t.Fatalf("ProcessRecipientDeliveryPlan() errors = %#v, want [nil, owner two error] aligned to targets", got)
		}
	case <-ctx.Done():
		t.Fatalf("ProcessRecipientDeliveryPlan() did not finish after releasing owner pushes: %v", ctx.Err())
	}
}

func TestRecipientProcessorPlanRetryMapsOnlyFailedRouteTarget(t *testing.T) {
	firstRoute := Route{UID: "u1", OwnerNodeID: 3, SessionID: 10}
	secondRoute := Route{UID: "u2", OwnerNodeID: 3, SessionID: 20}
	resolver := &recordingTargetPresenceResolverForDeliveryWorkerTest{results: []RecipientTargetPresenceResult{
		{Routes: []Route{firstRoute}},
		{Routes: []Route{secondRoute}},
	}}
	pusher := &recordingOwnerPusherForDeliveryTest{results: []PushResult{
		{Accepted: []Route{firstRoute}, Retryable: []Route{secondRoute}},
		{Retryable: []Route{secondRoute}},
	}}
	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver:            resolver,
		OwnerPusher:                 pusher,
		DeliveryRetryMaxAttempts:    2,
		DeliveryRetryInitialBackoff: time.Millisecond,
		DeliveryRetryMaxBackoff:     time.Millisecond,
	})

	errs := processor.ProcessRecipientDeliveryPlan(context.Background(), RecipientDeliveryPlan{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Targets: []RecipientTargetBatch{
			{Target: recipientAuthorityTargetForTest(1, 10, 100), Recipients: []Recipient{{UID: "u1"}}},
			{Target: recipientAuthorityTargetForTest(2, 20, 200), Recipients: []Recipient{{UID: "u2"}}},
		},
	})

	if len(errs) != 2 || errs[0] != nil || !errors.Is(errs[1], ErrDeliveryRetryExhausted) {
		t.Fatalf("ProcessRecipientDeliveryPlan() errors = %#v, want only second target retry exhaustion", errs)
	}
	commands := pusher.commandsSnapshot()
	if got, want := len(commands), 2; got != want {
		t.Fatalf("owner push calls = %d, want initial push plus one narrowed retry", got)
	}
	if got, want := commands[0].Routes, []Route{firstRoute, secondRoute}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initial routes = %#v, want %#v", got, want)
	}
	if got, want := commands[1].Routes, []Route{secondRoute}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry routes = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorPlanRetryPanicMapsOnlyCurrentRoutes(t *testing.T) {
	firstRoute := Route{UID: "u1", OwnerNodeID: 3, SessionID: 10}
	secondRoute := Route{UID: "u2", OwnerNodeID: 3, SessionID: 20}
	resolver := &recordingTargetPresenceResolverForDeliveryWorkerTest{results: []RecipientTargetPresenceResult{
		{Routes: []Route{firstRoute}},
		{Routes: []Route{secondRoute}},
	}}
	pusher := &recordingOwnerPusherForDeliveryTest{
		results: []PushResult{{Accepted: []Route{firstRoute}, Retryable: []Route{secondRoute}}},
		panicAt: 2,
	}
	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver:            resolver,
		OwnerPusher:                 pusher,
		DeliveryRetryMaxAttempts:    3,
		DeliveryRetryInitialBackoff: time.Millisecond,
		DeliveryRetryMaxBackoff:     time.Millisecond,
	})

	errs := processor.ProcessRecipientDeliveryPlan(context.Background(), RecipientDeliveryPlan{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Targets: []RecipientTargetBatch{
			{Target: recipientAuthorityTargetForTest(1, 10, 100), Recipients: []Recipient{{UID: "u1"}}},
			{Target: recipientAuthorityTargetForTest(2, 20, 200), Recipients: []Recipient{{UID: "u2"}}},
		},
	})

	if len(errs) != 2 || errs[0] != nil || !errors.Is(errs[1], ErrEffectPanic) {
		t.Fatalf("ProcessRecipientDeliveryPlan() errors = %#v, want only second target panic", errs)
	}
	commands := pusher.commandsSnapshot()
	if got, want := len(commands), 2; got != want {
		t.Fatalf("owner push calls = %d, want initial push plus one narrowed retry", got)
	}
	if got, want := commands[1].Routes, []Route{secondRoute}; !reflect.DeepEqual(got, want) {
		t.Fatalf("panic attempt routes = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorPlanOwnerPushChunksStayBoundedAndOrdered(t *testing.T) {
	const routeCount = 5
	targets := make([]RecipientTargetBatch, 0, routeCount)
	results := make([]RecipientTargetPresenceResult, 0, routeCount)
	for i := 0; i < routeCount; i++ {
		uid := "u" + strconv.Itoa(i+1)
		targets = append(targets, RecipientTargetBatch{
			Target:     recipientAuthorityTargetForTest(uint16(i+1), uint64(i+10), uint64(i+100)),
			Recipients: []Recipient{{UID: uid}},
		})
		results = append(results, RecipientTargetPresenceResult{Routes: []Route{{
			UID:         uid,
			OwnerNodeID: 3,
			SessionID:   uint64(i + 1000),
		}}})
	}
	pusher := &recordingOwnerPusherForDeliveryTest{}
	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver:   &recordingTargetPresenceResolverForDeliveryWorkerTest{results: results},
		OwnerPusher:        pusher,
		OwnerPushBatchSize: 2,
	})

	errs := processor.ProcessRecipientDeliveryPlan(context.Background(), RecipientDeliveryPlan{
		Event:   CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Targets: targets,
	})

	if got, want := errs, make([]error, routeCount); !reflect.DeepEqual(got, want) {
		t.Fatalf("ProcessRecipientDeliveryPlan() errors = %#v, want %#v", got, want)
	}
	commands := pusher.commandsSnapshot()
	if got, want := len(commands), 3; got != want {
		t.Fatalf("owner push calls = %d, want %d bounded chunks", got, want)
	}
	wantUIDs := [][]string{{"u1", "u2"}, {"u3", "u4"}, {"u5"}}
	for i, command := range commands {
		gotUIDs := make([]string, 0, len(command.Routes))
		for _, route := range command.Routes {
			gotUIDs = append(gotUIDs, route.UID)
		}
		if !reflect.DeepEqual(gotUIDs, wantUIDs[i]) {
			t.Fatalf("chunk %d routes = %#v, want %#v", i, gotUIDs, wantUIDs[i])
		}
	}
}

func TestRecipientProcessorPlanCoalescingPreservesExactSenderEchoSuppression(t *testing.T) {
	resolver := &recordingTargetPresenceResolverForDeliveryWorkerTest{results: []RecipientTargetPresenceResult{
		{Routes: []Route{
			{UID: "sender", OwnerNodeID: 1, SessionID: 10, DeviceID: "exact-sender-session"},
			{UID: "sender", OwnerNodeID: 1, SessionID: 11, DeviceID: "same-owner-other-session"},
		}},
		{Routes: []Route{
			{UID: "sender", OwnerNodeID: 2, SessionID: 10, DeviceID: "other-owner-same-session"},
			{UID: "recipient", OwnerNodeID: 1, SessionID: 20, DeviceID: "recipient"},
		}},
	}}
	pusher := &recordingOwnerPusherForDeliveryTest{}
	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver: resolver,
		OwnerPusher:      pusher,
	})

	errs := processor.ProcessRecipientDeliveryPlan(context.Background(), RecipientDeliveryPlan{
		Event: CommittedEnvelope{
			MessageID:       10,
			MessageSeq:      4,
			ChannelID:       "g1",
			ChannelType:     2,
			FromUID:         "sender",
			SenderNodeID:    1,
			SenderSessionID: 10,
		},
		Targets: []RecipientTargetBatch{
			{Target: recipientAuthorityTargetForTest(1, 10, 100), Recipients: []Recipient{{UID: "sender"}}},
			{Target: recipientAuthorityTargetForTest(2, 20, 200), Recipients: []Recipient{{UID: "sender"}, {UID: "recipient"}}},
		},
	})

	if got, want := errs, make([]error, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("ProcessRecipientDeliveryPlan() errors = %#v, want %#v", got, want)
	}
	got := deviceIDSetForDeliveryTest(pusher.deviceIDs())
	want := map[string]bool{
		"same-owner-other-session": true,
		"other-owner-same-session": true,
		"recipient":                true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pushed device ids = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorObservesOfflineRecipientsAfterPresence(t *testing.T) {
	steps := &orderedStepsForDeliveryTest{}
	observer := &recordingOfflineRecipientObserverForDeliveryTest{steps: steps}
	pusher := &recordingOwnerPusherForDeliveryTest{steps: steps}
	batch := RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, Payload: []byte("hello")},
		Recipients: []Recipient{
			{UID: "u1"},
			{UID: "u2"},
			{UID: "u2"},
			{UID: "u3"},
		},
	}

	err := processRecipientBatch(context.Background(), batch, recipientPorts{
		presence: &recordingPresenceResolverForDeliveryTest{steps: steps, routes: []Route{
			{UID: "u1", OwnerNodeID: 3, SessionID: 20},
			{UID: "u3", OwnerNodeID: 4, SessionID: 30},
			{UID: "u3", OwnerNodeID: 4, SessionID: 31},
		}},
		pusher:                   pusher,
		offlineRecipientObserver: observer,
		deliveryRetryMaxAttempts: 2,
	})

	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got, want := observer.uids(), []string{"u2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("offline uids = %#v, want %#v", got, want)
	}
	if got, want := steps.snapshot(), []string{"presence", "offline", "push", "push"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("steps = %#v, want %#v", got, want)
	}
	if got := pusher.callCount(); got != 2 {
		t.Fatalf("push calls = %d, want two owner groups", got)
	}
}

func TestRecipientProcessorPrefersBatchOfflineObserver(t *testing.T) {
	observer := &recordingBatchOfflineRecipientObserverForDeliveryTest{}
	batch := RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{
			{UID: "u1"},
			{UID: "u2"},
			{UID: "u1"},
			{UID: "u3"},
			{UID: "u4"},
		},
	}

	err := processRecipientBatch(context.Background(), batch, recipientPorts{
		presence: &recordingPresenceResolverForDeliveryTest{routes: []Route{
			{UID: "u3", OwnerNodeID: 4, SessionID: 30},
		}},
		offlineRecipientObserver: observer,
	})

	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := observer.batchCallCount(); got != 1 {
		t.Fatalf("batch offline observer calls = %d, want 1", got)
	}
	if got := observer.fallbackCallCount(); got != 0 {
		t.Fatalf("single offline observer calls = %d, want none when batch observer is available", got)
	}
	if got, want := observer.batchUIDs(), []string{"u1", "u2", "u4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("batch offline uids = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorOptionsAcceptsBatchOnlyOfflineObserver(t *testing.T) {
	observer := &recordingBatchOnlyOfflineRecipientObserverForDeliveryTest{}
	processor := NewRecipientProcessor(RecipientProcessorOptions{
		PresenceResolver: &recordingPresenceResolverForDeliveryTest{routes: []Route{
			{UID: "u3", OwnerNodeID: 4, SessionID: 30},
		}},
		OfflineRecipientsObserver: observer,
	})

	err := processor.ProcessRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{
			{UID: "u1"},
			{UID: "u2"},
			{UID: "u1"},
			{UID: "u3"},
		},
	})

	if err != nil {
		t.Fatalf("ProcessRecipientBatch() error = %v", err)
	}
	if got := observer.batchCallCount(); got != 1 {
		t.Fatalf("batch offline observer calls = %d, want 1", got)
	}
	if got, want := observer.batchUIDs(), []string{"u1", "u2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("batch offline uids = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorFallsBackToSingleOfflineObserver(t *testing.T) {
	observer := &recordingOfflineRecipientObserverForDeliveryTest{}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{
			{UID: "u1"},
			{UID: "u2"},
			{UID: "u1"},
		},
	}, recipientPorts{
		presence:                 &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		offlineRecipientObserver: observer,
	})

	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got, want := observer.uids(), []string{"u1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback offline uids = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorBatchOfflineObserverReceivesCopiedUIDSlice(t *testing.T) {
	uids := []string{"u1", "u2", "u3"}
	observer := &recordingBatchOfflineRecipientObserverForDeliveryTest{aliasAgainst: uids}

	observeOfflineRecipients(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
	}, uids, []Route{{UID: "u3", OwnerNodeID: 4, SessionID: 30}}, observer, nil)

	if observer.sawUIDAlias() {
		t.Fatalf("batch offline observer received a UID slice alias")
	}
	if got, want := observer.batchUIDs(), []string{"u1", "u2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("batch offline uids = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorBatchOfflineObserverUsesOneCallForLargeFanout(t *testing.T) {
	const recipientCount = 100000
	recipients := make([]Recipient, 0, recipientCount)
	for i := 0; i < recipientCount; i++ {
		recipients = append(recipients, Recipient{UID: "u" + strconv.Itoa(i)})
	}
	observer := &recordingBatchOfflineRecipientObserverForDeliveryTest{}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: recipients,
	}, recipientPorts{
		presence:                 &recordingPresenceResolverForDeliveryTest{},
		offlineRecipientObserver: observer,
	})

	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := observer.batchCallCount(); got != 1 {
		t.Fatalf("batch offline observer calls = %d, want 1", got)
	}
	if got := observer.fallbackCallCount(); got != 0 {
		t.Fatalf("single offline observer calls = %d, want none when batch observer is available", got)
	}
	if got := observer.batchUIDCount(); got != recipientCount {
		t.Fatalf("batch offline UID count = %d, want %d", got, recipientCount)
	}
}

func TestRecipientProcessorOfflineObserverSkipsNonDurableCandidates(t *testing.T) {
	cases := []struct {
		name  string
		event CommittedEnvelope
	}{
		{
			name:  "zero message seq",
			event: CommittedEnvelope{MessageID: 10, ChannelID: "g1", ChannelType: 2},
		},
		{
			name:  "sync once",
			event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, SyncOnce: true},
		},
		{
			name:  "request scoped",
			event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, MessageScopedUIDs: []string{"u2"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observer := &recordingOfflineRecipientObserverForDeliveryTest{}
			err := processRecipientBatch(context.Background(), RecipientBatch{
				Event:      tc.event,
				Recipients: []Recipient{{UID: "u2"}},
			}, recipientPorts{
				presence:                 &recordingPresenceResolverForDeliveryTest{},
				pusher:                   &recordingOwnerPusherForDeliveryTest{},
				offlineRecipientObserver: observer,
			})
			if err != nil {
				t.Fatalf("processRecipientBatch() error = %v", err)
			}
			if got := observer.uids(); len(got) != 0 {
				t.Fatalf("offline uids = %#v, want none", got)
			}
		})
	}
}

type orderedStepsForDeliveryTest struct {
	mu    sync.Mutex
	steps []string
}

func (s *orderedStepsForDeliveryTest) add(step string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

func (s *orderedStepsForDeliveryTest) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

type recordingPresenceResolverForDeliveryTest struct {
	steps  *orderedStepsForDeliveryTest
	routes []Route
}

func (r *recordingPresenceResolverForDeliveryTest) EndpointsByUIDs(_ context.Context, _ []string) ([]Route, error) {
	r.steps.add("presence")
	return append([]Route(nil), r.routes...), nil
}

type recordingOwnerPusherForDeliveryTest struct {
	steps    *orderedStepsForDeliveryTest
	mu       sync.Mutex
	commands []PushCommand
	results  []PushResult
	panicAt  int
}

type overlapBarrierOwnerPusherForDeliveryTest struct {
	started chan uint64
	release chan struct{}
	errs    map[uint64]error
}

func (p *overlapBarrierOwnerPusherForDeliveryTest) Push(ctx context.Context, cmd PushCommand) (PushResult, error) {
	select {
	case p.started <- cmd.OwnerNodeID:
	case <-ctx.Done():
		return PushResult{}, ctx.Err()
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return PushResult{}, ctx.Err()
	}
	if err := p.errs[cmd.OwnerNodeID]; err != nil {
		return PushResult{}, err
	}
	return PushResult{Accepted: append([]Route(nil), cmd.Routes...)}, nil
}

func (p *recordingOwnerPusherForDeliveryTest) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	p.steps.add("push")
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands = append(p.commands, cmd.Clone())
	if p.panicAt == len(p.commands) {
		panic("owner push panic")
	}
	if len(p.results) >= len(p.commands) {
		return p.results[len(p.commands)-1].Clone(), nil
	}
	return PushResult{Accepted: append([]Route(nil), cmd.Routes...)}, nil
}

type payloadAliasOwnerPusherForDeliveryTest struct {
	mu      sync.Mutex
	payload []byte
	calls   int
	aliased bool
}

func (p *payloadAliasOwnerPusherForDeliveryTest) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if len(p.payload) > 0 && len(cmd.Envelope.Payload) > 0 && &cmd.Envelope.Payload[0] == &p.payload[0] {
		p.aliased = true
	}
	return PushResult{Accepted: append([]Route(nil), cmd.Routes...)}, nil
}

func (p *payloadAliasOwnerPusherForDeliveryTest) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *payloadAliasOwnerPusherForDeliveryTest) sawAlias() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.aliased
}

func (p *recordingOwnerPusherForDeliveryTest) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.commands)
}

func (p *recordingOwnerPusherForDeliveryTest) commandsSnapshot() []PushCommand {
	p.mu.Lock()
	defer p.mu.Unlock()
	commands := make([]PushCommand, len(p.commands))
	for i, command := range p.commands {
		commands[i] = command.Clone()
	}
	return commands
}

func (p *recordingOwnerPusherForDeliveryTest) deviceIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, cmd := range p.commands {
		for _, route := range cmd.Routes {
			out = append(out, route.DeviceID)
		}
	}
	return out
}

func deviceIDSetForDeliveryTest(deviceIDs []string) map[string]bool {
	out := make(map[string]bool)
	for _, deviceID := range deviceIDs {
		out[deviceID] = true
	}
	return out
}

type recordingOfflineRecipientObserverForDeliveryTest struct {
	steps  *orderedStepsForDeliveryTest
	mu     sync.Mutex
	events []OfflineRecipientEvent
}

func (o *recordingOfflineRecipientObserverForDeliveryTest) ObserveOfflineRecipient(_ context.Context, event OfflineRecipientEvent) {
	o.steps.add("offline")
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingOfflineRecipientObserverForDeliveryTest) uids() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.events))
	for _, event := range o.events {
		out = append(out, event.UID)
	}
	return out
}

type recordingBatchOfflineRecipientObserverForDeliveryTest struct {
	steps          *orderedStepsForDeliveryTest
	aliasAgainst   []string
	mu             sync.Mutex
	batchEvents    []OfflineRecipientsEvent
	fallbackEvents []OfflineRecipientEvent
	aliased        bool
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) ObserveOfflineRecipients(_ context.Context, event OfflineRecipientsEvent) {
	o.steps.add("offline")
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.aliasAgainst) > 0 && len(event.UIDs) > 0 && &o.aliasAgainst[0] == &event.UIDs[0] {
		o.aliased = true
	}
	copied := event
	copied.UIDs = append([]string(nil), event.UIDs...)
	o.batchEvents = append(o.batchEvents, copied)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) ObserveOfflineRecipient(_ context.Context, event OfflineRecipientEvent) {
	o.steps.add("offline")
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fallbackEvents = append(o.fallbackEvents, event)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) batchCallCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.batchEvents)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) fallbackCallCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.fallbackEvents)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) batchUIDs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.batchEvents) == 0 {
		return nil
	}
	return append([]string(nil), o.batchEvents[0].UIDs...)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) batchUIDCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.batchEvents) == 0 {
		return 0
	}
	return len(o.batchEvents[0].UIDs)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) sawUIDAlias() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.aliased
}

type recordingBatchOnlyOfflineRecipientObserverForDeliveryTest struct {
	mu     sync.Mutex
	events []OfflineRecipientsEvent
}

func (o *recordingBatchOnlyOfflineRecipientObserverForDeliveryTest) ObserveOfflineRecipients(_ context.Context, event OfflineRecipientsEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	copied := event
	copied.UIDs = append([]string(nil), event.UIDs...)
	o.events = append(o.events, copied)
}

func (o *recordingBatchOnlyOfflineRecipientObserverForDeliveryTest) batchCallCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.events)
}

func (o *recordingBatchOnlyOfflineRecipientObserverForDeliveryTest) batchUIDs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.events) == 0 {
		return nil
	}
	return append([]string(nil), o.events[0].UIDs...)
}
