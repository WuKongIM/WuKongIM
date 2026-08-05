package channelappend

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestScopedUIDsBypassSubscriberScan(t *testing.T) {
	source := &recordingSubscriberSourceForRecipientTest{failOnCall: true}
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	event := CommittedEnvelope{
		MessageID:         1,
		ChannelID:         "scoped",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u2", "u3"},
	}

	err := dispatchCommittedRecipients(context.Background(), event, commitPorts{
		subscribers:                source,
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 7},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
		subscriberPageSize:         2,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}

	if source.calls != 0 {
		t.Fatalf("subscriber page calls = %d, want 0", source.calls)
	}
	got := enqueuer.allUIDs()
	if !reflect.DeepEqual(got, []string{"u2", "u3"}) {
		t.Fatalf("recipient uids = %#v, want scoped u2,u3", got)
	}
}

func TestPersonChannelDerivesExactlyCanonicalParticipants(t *testing.T) {
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	left, right, err := runtimechannelid.DecodePersonChannel(channelID)
	if err != nil {
		t.Fatalf("DecodePersonChannel() error = %v", err)
	}

	err = dispatchCommittedRecipients(context.Background(), CommittedEnvelope{
		MessageID:   1,
		ChannelID:   channelID,
		ChannelType: 1,
	}, commitPorts{
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 7},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
		subscriberPageSize:         2,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}

	got := enqueuer.allUIDs()
	if !reflect.DeepEqual(got, []string{left, right}) {
		t.Fatalf("person recipients = %#v, want canonical participants %#v", got, []string{left, right})
	}
}

func TestGroupChannelPagesSubscribersBeforeDispatchingNextPage(t *testing.T) {
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	source := &recordingSubscriberSourceForRecipientTest{
		enqueuer: enqueuer,
		pages: []SubscriberPage{
			{Recipients: []Recipient{{UID: "u1"}, {UID: "u2"}}, Cursor: "next"},
			{Recipients: []Recipient{{UID: "u3"}}, Done: true},
		},
	}

	err := dispatchCommittedRecipients(context.Background(), CommittedEnvelope{
		MessageID:   1,
		ChannelID:   "g1",
		ChannelType: 2,
	}, commitPorts{
		subscribers:                source,
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 7},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
		subscriberPageSize:         2,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}

	if !reflect.DeepEqual(source.limits, []int{2, 2}) {
		t.Fatalf("subscriber page limits = %#v, want bounded page size 2", source.limits)
	}
	if !source.secondPageAfterDispatch {
		t.Fatalf("second page was loaded before first page recipients were dispatched")
	}
	if got := enqueuer.allUIDs(); !reflect.DeepEqual(got, []string{"u1", "u2", "u3"}) {
		t.Fatalf("recipient uids = %#v, want paged subscribers", got)
	}
}

func TestRecipientBatchesAreGroupedByRecipientAuthorityTarget(t *testing.T) {
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	resolver := mapRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{
			"u1": recipientAuthorityTargetForTest(1, 10, 100),
			"u2": recipientAuthorityTargetForTest(2, 20, 200),
			"u3": recipientAuthorityTargetForTest(1, 10, 100),
		},
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: "u2"},
		{UID: "u3"},
	}, commitPorts{
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}

	got := enqueuer.byTarget()
	target10 := recipientAuthorityTargetForTest(1, 10, 100)
	target20 := recipientAuthorityTargetForTest(2, 20, 200)
	if !reflect.DeepEqual(got[target10], []string{"u1", "u3"}) {
		t.Fatalf("target 10 recipients = %#v, want u1,u3", got[target10])
	}
	if !reflect.DeepEqual(got[target20], []string{"u2"}) {
		t.Fatalf("target 20 recipients = %#v, want u2", got[target20])
	}
}

func TestRecipientDeliveryBatchesAreEnqueuedByRecipientAuthorityTarget(t *testing.T) {
	enqueuer := &recordingRecipientDeliveryEnqueuerForRecipientTest{}
	target10 := recipientAuthorityTargetForTest(1, 10, 100)
	target20 := recipientAuthorityTargetForTest(2, 20, 200)
	resolver := mapRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{
			"u1": target10,
			"u2": target20,
			"u3": target10,
		},
	}
	payload := []byte("before")
	event := CommittedEnvelope{
		MessageID:  1,
		MessageSeq: 9,
		Payload:    payload,
	}
	recipients := []Recipient{{UID: "u1"}, {UID: "u2"}, {UID: "u3"}}

	err := dispatchRecipientSet(context.Background(), event, recipients, commitPorts{
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}
	payload[0] = 'X'
	event.MessageID = 99
	event.Payload[1] = 'Y'
	recipients[0].UID = "changed"

	got := enqueuer.byTarget()
	if !reflect.DeepEqual(got[target10], []string{"u1", "u3"}) {
		t.Fatalf("target 10 recipients = %#v, want u1,u3", got[target10])
	}
	if !reflect.DeepEqual(got[target20], []string{"u2"}) {
		t.Fatalf("target 20 recipients = %#v, want u2", got[target20])
	}
	if len(enqueuer.batches) != 2 {
		t.Fatalf("enqueued target batches = %d, want 2", len(enqueuer.batches))
	}
	for _, batch := range enqueuer.batches {
		if batch.Event.MessageID != 1 {
			t.Fatalf("enqueued event MessageID = %d, want cloned original 1", batch.Event.MessageID)
		}
		if string(batch.Event.Payload) != "before" {
			t.Fatalf("enqueued payload = %q, want cloned original before", batch.Event.Payload)
		}
		for _, recipient := range batch.Recipients {
			if recipient.UID == "changed" {
				t.Fatalf("enqueued recipients were aliased to caller slice: %#v", batch.Recipients)
			}
		}
	}
}

func TestRecipientDeliveryPageUsesOneBoundedPlanAcrossAuthorityTargets(t *testing.T) {
	enqueuer := &recordingRecipientPlanEnqueuerForRecipientTest{}
	first := authority.Target{HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}
	second := authority.Target{HashSlot: 2, SlotID: 12, LeaderNodeID: 10, LeaderTerm: 102, ConfigEpoch: 1002, RouteRevision: 101, AuthorityEpoch: 1001}
	third := authority.Target{HashSlot: 3, SlotID: 13, LeaderNodeID: 20, LeaderTerm: 103, ConfigEpoch: 1003, RouteRevision: 102, AuthorityEpoch: 1002}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: "u2"},
		{UID: "u3"},
		{UID: "u4"},
	}, commitPorts{
		recipientAuthorityResolver: mapRecipientAuthorityResolverForRecipientTest{targets: map[string]RecipientAuthorityTarget{
			"u1": first,
			"u2": second,
			"u3": first,
			"u4": third,
		}},
		deliveryEnqueuer:   enqueuer,
		recipientBatchSize: 4,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}

	if len(enqueuer.plans) != 1 {
		t.Fatalf("delivery plans = %d, want one recipient-page plan", len(enqueuer.plans))
	}
	plan := enqueuer.plans[0]
	if plan.Mode != onlinedelivery.ModeDurable {
		t.Fatalf("delivery mode = %v, want durable", plan.Mode)
	}
	if plan.Event.MessageID != 1 || plan.RecipientCount() != 4 {
		t.Fatalf("delivery plan = %#v, want message 1 and 4 recipients", plan)
	}
	if len(plan.Targets) != 3 {
		t.Fatalf("target groups = %d, want 3 exact fenced targets", len(plan.Targets))
	}
	if plan.Targets[0].Target != first || plan.Targets[1].Target != second || plan.Targets[2].Target != third {
		t.Fatalf("target order = %#v, want first-seen exact targets", plan.Targets)
	}
	if got := recipientUIDs(plan.Targets[0].Recipients); !reflect.DeepEqual(got, []string{"u1", "u3"}) {
		t.Fatalf("first target recipients = %#v, want u1,u3", got)
	}
}

func TestTransientRecipientDeliveryPlanCarriesExplicitMode(t *testing.T) {
	enqueuer := &recordingRecipientPlanEnqueuerForRecipientTest{}
	target := AuthorityTarget{
		ChannelID: ChannelID{ID: "room", Type: 2},
		Large:     true,
	}

	_, err := dispatchRecipientsForTarget(context.Background(), onlinedelivery.ModeTransient, target, CommittedEnvelope{
		MessageID:         7,
		MessageScopedUIDs: []string{"u1"},
	}, subscriberCache{}, commitPorts{
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 1},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientsForTarget() error = %v", err)
	}
	if len(enqueuer.plans) != 1 {
		t.Fatalf("delivery plans = %d, want 1", len(enqueuer.plans))
	}
	if enqueuer.plans[0].Mode != onlinedelivery.ModeTransient {
		t.Fatalf("delivery mode = %v, want transient", enqueuer.plans[0].Mode)
	}
}

func TestRecipientDeliveryPlansBoundTotalRecipientsAcrossTargets(t *testing.T) {
	enqueuer := &recordingRecipientPlanEnqueuerForRecipientTest{}
	targets := make(map[string]RecipientAuthorityTarget, 5)
	recipients := make([]Recipient, 0, 5)
	for i := 0; i < 5; i++ {
		uid := fmt.Sprintf("u%d", i+1)
		targets[uid] = recipientAuthorityTargetForTest(uint16(i+1), uint64(i+1), 100)
		recipients = append(recipients, Recipient{UID: uid})
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 2}, recipients, commitPorts{
		recipientAuthorityResolver: mapRecipientAuthorityResolverForRecipientTest{targets: targets},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         2,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}
	if len(enqueuer.plans) != 3 {
		t.Fatalf("delivery plans = %d, want 3 bounded plans", len(enqueuer.plans))
	}
	for i, plan := range enqueuer.plans {
		if got := plan.RecipientCount(); got < 1 || got > 2 {
			t.Fatalf("plan %d recipients = %d, want 1..2", i, got)
		}
	}
}

func TestDispatchRecipientSetSharesImmutablePayloadBeforeDeliveryQueue(t *testing.T) {
	payload := []byte("payload")
	enqueuer := &payloadAliasRecipientEnqueuerForRecipientTest{payload: payload}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{
		MessageID:  1,
		MessageSeq: 9,
		Payload:    payload,
	}, []Recipient{{UID: "u1"}}, commitPorts{
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{target: recipientAuthorityTargetForTest(1, 10, 100)},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}
	if !enqueuer.sawAlias {
		t.Fatalf("recipient batch payload did not share immutable committed payload before delivery queue")
	}
}

type payloadAliasRecipientEnqueuerForRecipientTest struct {
	payload  []byte
	sawAlias bool
}

func (e *payloadAliasRecipientEnqueuerForRecipientTest) EnqueueRecipientDeliveryPlan(_ context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	if len(plan.Event.Payload) > 0 && len(e.payload) > 0 && &plan.Event.Payload[0] == &e.payload[0] {
		e.sawAlias = true
	}
	return nil
}

func TestRecipientBatchesKeepSameLeaderDifferentFenceTargetsSeparate(t *testing.T) {
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	first := authority.Target{HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}
	second := authority.Target{HashSlot: 2, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}
	resolver := mapRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{"u1": first, "u2": second},
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: "u2"},
	}, commitPorts{
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}

	got := enqueuer.byTarget()
	if len(got) != 2 {
		t.Fatalf("target groups = %d, want 2 exact fenced targets", len(got))
	}
	if !reflect.DeepEqual(got[first], []string{"u1"}) || !reflect.DeepEqual(got[second], []string{"u2"}) {
		t.Fatalf("target groups = %#v, want separate same-leader targets", got)
	}
}

func TestRecipientAuthorityBatchResolverResolvesUniqueTrimmedUIDsOnce(t *testing.T) {
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	target10 := recipientAuthorityTargetForTest(1, 10, 100)
	target20 := recipientAuthorityTargetForTest(2, 20, 200)
	resolver := &batchRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{
			"u1": target10,
			"u2": target20,
		},
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: " u1 "},
		{UID: "u2"},
		{UID: "u1"},
		{UID: " "},
	}, commitPorts{
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}

	if resolver.singleCalls != 0 {
		t.Fatalf("single resolver calls = %d, want 0 when batch resolver is available", resolver.singleCalls)
	}
	if resolver.batchCalls != 1 {
		t.Fatalf("batch resolver calls = %d, want 1", resolver.batchCalls)
	}
	if !reflect.DeepEqual(resolver.batchUIDs, []string{"u1", "u2"}) {
		t.Fatalf("batch resolver uids = %#v, want unique trimmed u1,u2", resolver.batchUIDs)
	}
	got := enqueuer.byTarget()
	if !reflect.DeepEqual(got[target10], []string{"u1", "u1"}) {
		t.Fatalf("target 10 recipients = %#v, want duplicate u1 recipients preserved", got[target10])
	}
	if !reflect.DeepEqual(got[target20], []string{"u2"}) {
		t.Fatalf("target 20 recipients = %#v, want u2", got[target20])
	}
}

func TestRecipientAuthorityFallbackResolverReusesDuplicateUIDTarget(t *testing.T) {
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	target := recipientAuthorityTargetForTest(1, 10, 100)
	resolver := &countingRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{"u1": target},
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: " u1 "},
	}, commitPorts{
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         1,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}

	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1 for duplicate UID", resolver.calls)
	}
	if got := enqueuer.allUIDs(); !reflect.DeepEqual(got, []string{"u1", "u1"}) {
		t.Fatalf("recipient uids = %#v, want duplicate trimmed u1 recipients", got)
	}
}

func TestRecipientDispatchKeepsDifferentAuthorityTargetsInOneBoundedPlan(t *testing.T) {
	first := recipientAuthorityTargetForTest(1, 10, 100)
	second := recipientAuthorityTargetForTest(2, 20, 200)
	enqueuer := &recordingRecipientPlanEnqueuerForRecipientTest{}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: "u2"},
	}, commitPorts{
		recipientAuthorityResolver: mapRecipientAuthorityResolverForRecipientTest{
			targets: map[string]RecipientAuthorityTarget{"u1": first, "u2": second},
		},
		deliveryEnqueuer:   enqueuer,
		recipientBatchSize: 2,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}
	if len(enqueuer.plans) != 1 || len(enqueuer.plans[0].Targets) != 2 {
		t.Fatalf("plans = %#v, want one plan with two exact targets", enqueuer.plans)
	}
	targets := []RecipientAuthorityTarget{enqueuer.plans[0].Targets[0].Target, enqueuer.plans[0].Targets[1].Target}
	if !containsRecipientTargetForTest(targets, first) || !containsRecipientTargetForTest(targets, second) {
		t.Fatalf("plan targets = %#v, want both authority targets", targets)
	}
}

func TestRecipientDispatchKeepsSameAuthorityTargetBatchesSequential(t *testing.T) {
	target := recipientAuthorityTargetForTest(1, 10, 100)
	enqueuer := newBlockingRecipientEnqueuerForRecipientTest()
	defer enqueuer.release()
	errC := make(chan error, 1)

	go func() {
		errC <- dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
			{UID: "u1"},
			{UID: "u2"},
		}, commitPorts{
			recipientAuthorityResolver: mapRecipientAuthorityResolverForRecipientTest{
				targets: map[string]RecipientAuthorityTarget{"u1": target, "u2": target},
			},
			deliveryEnqueuer:   enqueuer,
			recipientBatchSize: 1,
		})
	}()

	enqueuer.waitStartedTargets(t, 1)
	time.Sleep(20 * time.Millisecond)
	if got := enqueuer.startedCount(); got != 1 {
		t.Fatalf("started batches before release = %d, want 1 same-target batch in flight", got)
	}
	enqueuer.release()
	select {
	case err := <-errC:
		if err != nil {
			t.Fatalf("dispatchRecipientSet() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("dispatchRecipientSet() did not finish")
	}
}

func TestInvalidRecipientAuthorityTargetMapsRouteNotReady(t *testing.T) {
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	resolver := mapRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{"u1": {}},
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{{UID: "u1"}}, commitPorts{
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("dispatchRecipientSet() error = %v, want ErrRouteNotReady", err)
	}
	detail := postCommitFailureDetailFromError(err)
	if detail.Phase != "recipient_target_validate" || detail.UID != "u1" || detail.RecipientCount != 1 ||
		detail.TargetLeaderNodeID != 0 {
		t.Fatalf("post-commit failure detail = %#v, want invalid target detail for u1", detail)
	}
	if enqueuer.callCount() != 0 {
		t.Fatalf("enqueuer calls = %d, want 0 for invalid target", enqueuer.callCount())
	}
}

func TestRecipientAuthorityResolveFailureCarriesPostCommitFailureDetail(t *testing.T) {
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	resolver := failingRecipientAuthorityResolverForRecipientTest{err: ErrRouteNotReady}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: "u2"},
		{UID: "u1"},
	}, commitPorts{
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("dispatchRecipientSet() error = %v, want ErrRouteNotReady", err)
	}
	detail := postCommitFailureDetailFromError(err)
	if detail.Phase != "recipient_route_resolve" || detail.UID != "u1" || detail.UIDCount != 2 ||
		detail.RecipientCount != 3 {
		t.Fatalf("post-commit failure detail = %#v, want resolver detail with sample uid and counts", detail)
	}
	if enqueuer.callCount() != 0 {
		t.Fatalf("enqueuer calls = %d, want 0 when authority resolution fails", enqueuer.callCount())
	}
}

func TestSubscriberPageInvalidCursorReturnsError(t *testing.T) {
	for _, tt := range []struct {
		name string
		page SubscriberPage
	}{
		{name: "empty", page: SubscriberPage{Recipients: []Recipient{{UID: "u1"}}}},
		{name: "repeated", page: SubscriberPage{Recipients: []Recipient{{UID: "u1"}}, Cursor: "same"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
			source := &recordingSubscriberSourceForRecipientTest{
				pages: []SubscriberPage{
					{Recipients: []Recipient{{UID: "first"}}, Cursor: "same"},
					tt.page,
				},
			}
			err := dispatchCommittedRecipients(context.Background(), CommittedEnvelope{
				MessageID:   1,
				ChannelID:   "g1",
				ChannelType: 2,
			}, commitPorts{
				subscribers:                source,
				recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{target: recipientAuthorityTargetForTest(1, 7, 1)},
				deliveryEnqueuer:           enqueuer,
				recipientBatchSize:         16,
				subscriberPageSize:         2,
			})
			if !errors.Is(err, ErrInvalidSubscriberCursor) {
				t.Fatalf("dispatchCommittedRecipients() error = %v, want ErrInvalidSubscriberCursor", err)
			}
			if got := enqueuer.allUIDs(); !reflect.DeepEqual(got, []string{"first"}) {
				t.Fatalf("enqueued recipients = %#v, want only prior valid page before invalid cursor", got)
			}
		})
	}
}

type staticRecipientAuthorityResolverForRecipientTest struct {
	nodeID uint64
	target RecipientAuthorityTarget
}

func (r staticRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthority(_ context.Context, _ string) (RecipientAuthorityTarget, error) {
	if r.target != (RecipientAuthorityTarget{}) {
		return r.target, nil
	}
	return recipientAuthorityTargetForTest(1, r.nodeID, 1), nil
}

type mapRecipientAuthorityResolverForRecipientTest struct {
	targets map[string]RecipientAuthorityTarget
}

func (r mapRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthority(_ context.Context, uid string) (RecipientAuthorityTarget, error) {
	return r.targets[uid], nil
}

type failingRecipientAuthorityResolverForRecipientTest struct {
	err error
}

func (r failingRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthority(context.Context, string) (RecipientAuthorityTarget, error) {
	return RecipientAuthorityTarget{}, r.err
}

type countingRecipientAuthorityResolverForRecipientTest struct {
	targets map[string]RecipientAuthorityTarget
	calls   int
}

func (r *countingRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthority(_ context.Context, uid string) (RecipientAuthorityTarget, error) {
	r.calls++
	return r.targets[uid], nil
}

type batchRecipientAuthorityResolverForRecipientTest struct {
	targets     map[string]RecipientAuthorityTarget
	singleCalls int
	batchCalls  int
	batchUIDs   []string
}

type alignedRecipientAuthorityResolverForRecipientTest struct {
	results     map[string]RecipientAuthorityResult
	singleCalls int
	batchCalls  int
	batchUIDs   []string
}

func (r *alignedRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthority(_ context.Context, uid string) (RecipientAuthorityTarget, error) {
	r.singleCalls++
	result := r.results[uid]
	return result.Target, result.Err
}

func (r *alignedRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthorities(_ context.Context, uids []string) ([]RecipientAuthorityResult, error) {
	r.batchCalls++
	r.batchUIDs = append([]string(nil), uids...)
	results := make([]RecipientAuthorityResult, len(uids))
	for index, uid := range uids {
		results[index] = r.results[uid]
	}
	return results, nil
}

func (r *batchRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthority(_ context.Context, uid string) (RecipientAuthorityTarget, error) {
	r.singleCalls++
	return r.targets[uid], nil
}

func (r *batchRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthorities(_ context.Context, uids []string) ([]RecipientAuthorityResult, error) {
	r.batchCalls++
	r.batchUIDs = append([]string(nil), uids...)
	out := make([]RecipientAuthorityResult, len(uids))
	for index, uid := range uids {
		out[index].Target = r.targets[uid]
	}
	return out, nil
}

type recordingSubscriberSourceForRecipientTest struct {
	enqueuer                *recordingRecipientEnqueuerForRecipientTest
	pages                   []SubscriberPage
	calls                   int
	limits                  []int
	failOnCall              bool
	secondPageAfterDispatch bool
}

func (s *recordingSubscriberSourceForRecipientTest) NextSubscriberPage(_ context.Context, req SubscriberPageRequest) (SubscriberPage, error) {
	if s.failOnCall {
		s.calls++
		return SubscriberPage{}, nil
	}
	if s.calls == 1 && s.enqueuer != nil && s.enqueuer.callCount() > 0 {
		s.secondPageAfterDispatch = true
	}
	s.limits = append(s.limits, req.Limit)
	if s.calls >= len(s.pages) {
		return SubscriberPage{Done: true}, nil
	}
	page := s.pages[s.calls].Clone()
	s.calls++
	return page, nil
}

type recordingRecipientEnqueuerForRecipientTest struct {
	mu      sync.Mutex
	steps   *orderedStepsForDeliveryTest
	batches []recordedRecipientBatchForRecipientTest
}

type recordedRecipientBatchForRecipientTest struct {
	Event      CommittedEnvelope
	Target     RecipientAuthorityTarget
	Recipients []Recipient
}

func (r *recordingRecipientEnqueuerForRecipientTest) EnqueueRecipientDeliveryPlan(_ context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	r.steps.add("delivery")
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, target := range plan.Targets {
		r.batches = append(r.batches, recordedRecipientBatchForRecipientTest{
			Event:      plan.Event.Clone(),
			Target:     target.Target,
			Recipients: append([]Recipient(nil), target.Recipients...),
		})
	}
	return nil
}

func (r *recordingRecipientEnqueuerForRecipientTest) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.batches)
}

func (r *recordingRecipientEnqueuerForRecipientTest) allUIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, batch := range r.batches {
		for _, recipient := range batch.Recipients {
			out = append(out, recipient.UID)
		}
	}
	return out
}

func (r *recordingRecipientEnqueuerForRecipientTest) byTarget() map[RecipientAuthorityTarget][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[RecipientAuthorityTarget][]string)
	for _, batch := range r.batches {
		for _, recipient := range batch.Recipients {
			out[batch.Target] = append(out[batch.Target], recipient.UID)
		}
	}
	return out
}

type recordingRecipientDeliveryEnqueuerForRecipientTest struct {
	mu      sync.Mutex
	steps   *orderedStepsForDeliveryTest
	batches []recordedRecipientBatchForRecipientTest
}

type recordingRecipientPlanEnqueuerForRecipientTest struct {
	plans []onlinedelivery.RecipientDeliveryPlan
}

func (e *recordingRecipientPlanEnqueuerForRecipientTest) EnqueueRecipientDeliveryPlan(_ context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	e.plans = append(e.plans, plan.Clone())
	return nil
}

func (e *recordingRecipientDeliveryEnqueuerForRecipientTest) EnqueueRecipientDeliveryPlan(_ context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	e.steps.add("delivery")
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, target := range plan.Targets {
		e.batches = append(e.batches, recordedRecipientBatchForRecipientTest{
			Event:      plan.Event.Clone(),
			Target:     target.Target,
			Recipients: append([]Recipient(nil), target.Recipients...),
		})
	}
	return nil
}

func (e *recordingRecipientDeliveryEnqueuerForRecipientTest) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.batches)
}

func (e *recordingRecipientDeliveryEnqueuerForRecipientTest) allUIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, batch := range e.batches {
		for _, recipient := range batch.Recipients {
			out = append(out, recipient.UID)
		}
	}
	return out
}

func (e *recordingRecipientDeliveryEnqueuerForRecipientTest) byTarget() map[RecipientAuthorityTarget][]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[RecipientAuthorityTarget][]string)
	for _, batch := range e.batches {
		for _, recipient := range batch.Recipients {
			out[batch.Target] = append(out[batch.Target], recipient.UID)
		}
	}
	return out
}

type blockingRecipientEnqueuerForRecipientTest struct {
	mu       sync.Mutex
	cond     *sync.Cond
	targets  []RecipientAuthorityTarget
	releaseC chan struct{}
	once     sync.Once
}

func newBlockingRecipientEnqueuerForRecipientTest() *blockingRecipientEnqueuerForRecipientTest {
	r := &blockingRecipientEnqueuerForRecipientTest{releaseC: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *blockingRecipientEnqueuerForRecipientTest) EnqueueRecipientDeliveryPlan(ctx context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	r.mu.Lock()
	for _, target := range plan.Targets {
		r.targets = append(r.targets, target.Target)
	}
	r.cond.Broadcast()
	r.mu.Unlock()
	select {
	case <-r.releaseC:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *blockingRecipientEnqueuerForRecipientTest) waitStartedTargets(t *testing.T, want int) []RecipientAuthorityTarget {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		r.mu.Lock()
		if len(r.targets) >= want {
			out := append([]RecipientAuthorityTarget(nil), r.targets...)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		if time.Now().After(deadline) {
			r.mu.Lock()
			out := append([]RecipientAuthorityTarget(nil), r.targets...)
			r.mu.Unlock()
			t.Fatalf("started targets = %d, want %d", len(out), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func recipientUIDs(recipients []Recipient) []string {
	uids := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID != "" {
			uids = append(uids, recipient.UID)
		}
	}
	return uids
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

func (r *blockingRecipientEnqueuerForRecipientTest) startedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.targets)
}

func (r *blockingRecipientEnqueuerForRecipientTest) release() {
	r.once.Do(func() {
		close(r.releaseC)
	})
}

func containsRecipientTargetForTest(targets []RecipientAuthorityTarget, want RecipientAuthorityTarget) bool {
	for _, target := range targets {
		if target == want {
			return true
		}
	}
	return false
}

func recipientAuthorityTargetForTest(hashSlot uint16, leader uint64, epoch uint64) RecipientAuthorityTarget {
	return authority.Target{
		HashSlot:       hashSlot,
		SlotID:         uint32(hashSlot + 100),
		LeaderNodeID:   leader,
		LeaderTerm:     epoch + 10000,
		ConfigEpoch:    uint64(hashSlot) + 20000,
		RouteRevision:  uint64(hashSlot + 1000),
		AuthorityEpoch: epoch,
	}
}
