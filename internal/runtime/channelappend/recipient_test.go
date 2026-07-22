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
	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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

func TestRecipientDeliveryDoesNotWaitForActiveBatchAdmission(t *testing.T) {
	steps := &orderedStepsForDeliveryTest{}
	active := &recordingActiveAdmitterForRecipientTest{steps: steps}
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{steps: steps}
	event := CommittedEnvelope{
		MessageID:         1,
		MessageSeq:        42,
		ChannelID:         "scoped",
		ChannelType:       2,
		FromUID:           "sender",
		ServerTimestampMS: 1234,
		MessageScopedUIDs: []string{"u2", "u3"},
	}

	err := dispatchCommittedRecipients(context.Background(), event, commitPorts{
		activeAdmitter:             active,
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 7},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
		subscriberPageSize:         2,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}

	if got := steps.snapshot(); !reflect.DeepEqual(got, []string{"delivery", "active"}) {
		t.Fatalf("steps = %#v, want delivery before active projection", got)
	}
	if len(active.batches) != 1 {
		t.Fatalf("active batches = %d, want 1", len(active.batches))
	}
	batch := active.batches[0]
	if batch.SenderUID != "sender" || batch.ChannelID != "scoped" || batch.ChannelType != 2 ||
		batch.MessageSeq != 42 || batch.ActiveAtMS != 1234 {
		t.Fatalf("active batch metadata = %#v, want committed event metadata", batch)
	}
	if got := active.recipientUIDs(); !reflect.DeepEqual(got, []string{"u2", "u3"}) {
		t.Fatalf("active recipient uids = %#v, want expanded recipient set", got)
	}
	for _, recipient := range batch.Recipients {
		if recipient.IsSender {
			t.Fatalf("active recipient %#v sets IsSender, want sender handled by SenderUID", recipient)
		}
	}
}

func TestActiveBatchAdmittedWithoutRecipientEnqueuer(t *testing.T) {
	active := &recordingActiveAdmitterForRecipientTest{}
	event := CommittedEnvelope{
		MessageID:         1,
		MessageSeq:        7,
		ChannelID:         "g1",
		ChannelType:       2,
		FromUID:           "sender",
		ServerTimestampMS: 99,
	}

	err := dispatchCommittedRecipients(context.Background(), event, commitPorts{
		activeAdmitter: active,
		subscribers: &recordingSubscriberSourceForRecipientTest{
			pages: []SubscriberPage{{Recipients: []Recipient{{UID: "u2"}, {UID: "u3"}}, Done: true}},
		},
		subscriberPageSize: 2,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}

	if got := active.recipientUIDs(); !reflect.DeepEqual(got, []string{"u2", "u3"}) {
		t.Fatalf("active recipient uids = %#v, want subscriber page recipients", got)
	}
}

func TestRecipientProcessorAdmitsNormalConversationKind(t *testing.T) {
	active := &recordingActiveAdmitterForRecipientTest{}
	event := CommittedEnvelope{
		MessageID:         1,
		MessageSeq:        7,
		ChannelID:         "g1",
		ChannelType:       2,
		FromUID:           "sender",
		ServerTimestampMS: 99,
		MessageScopedUIDs: []string{"u2"},
	}

	err := dispatchCommittedRecipients(context.Background(), event, commitPorts{
		activeAdmitter: active,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}
	if len(active.batches) != 1 || active.batches[0].Kind != metadb.ConversationKindNormal {
		t.Fatalf("active batches = %+v, want normal conversation kind", active.batches)
	}
}

func TestRecipientProcessorAdmitsCMDConversationKind(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		syncOnce  bool
	}{
		{name: "sync_once", channelID: "g1", syncOnce: true},
		{name: "command_channel", channelID: runtimechannelid.ToCommandChannel("g1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			active := &recordingActiveAdmitterForRecipientTest{}
			event := CommittedEnvelope{
				MessageID:         1,
				MessageSeq:        7,
				ChannelID:         tt.channelID,
				ChannelType:       2,
				FromUID:           "sender",
				ServerTimestampMS: 99,
				SyncOnce:          tt.syncOnce,
				MessageScopedUIDs: []string{"u2"},
			}

			err := dispatchCommittedRecipients(context.Background(), event, commitPorts{
				activeAdmitter: active,
			})
			if err != nil {
				t.Fatalf("dispatchCommittedRecipients() error = %v", err)
			}
			if len(active.batches) != 1 || active.batches[0].Kind != metadb.ConversationKindCMD {
				t.Fatalf("active batches = %+v, want CMD conversation kind", active.batches)
			}
		})
	}
}

func TestActiveBatchFailureDoesNotStopRecipientEnqueuer(t *testing.T) {
	activeErr := errors.New("active unavailable")
	active := &recordingActiveAdmitterForRecipientTest{err: activeErr}
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}

	dispatch, err := dispatchCommittedRecipientsForTarget(context.Background(), AuthorityTarget{
		ChannelID: ChannelID{ID: "g1", Type: 2},
		Large:     true,
	}, CommittedEnvelope{
		MessageID:         1,
		MessageSeq:        7,
		ChannelID:         "g1",
		ChannelType:       2,
		FromUID:           "sender",
		ServerTimestampMS: 99,
		MessageScopedUIDs: []string{"u2"},
	}, subscriberCache{}, commitPorts{
		activeAdmitter:             active,
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 7},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipientsForTarget() delivery error = %v, want nil", err)
	}
	if !errors.Is(dispatch.activeErr, activeErr) {
		t.Fatalf("active projection error = %v, want %v", dispatch.activeErr, activeErr)
	}
	detail := postCommitFailureDetailFromError(dispatch.activeErr)
	if detail.Phase != "conversation_active" || detail.UID != "u2" || detail.RecipientCount != 1 {
		t.Fatalf("post-commit failure detail = %#v, want conversation_active detail", detail)
	}
	if got := enqueuer.callCount(); got != 1 {
		t.Fatalf("enqueuer calls = %d, want 1 despite active failure", got)
	}
}

func TestActiveBatchFailureDoesNotStopLargeSubscriberPages(t *testing.T) {
	activeErr := errors.New("active unavailable")
	active := &recordingActiveAdmitterForRecipientTest{err: activeErr}
	enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
	source := &recordingSubscriberSourceForRecipientTest{pages: []SubscriberPage{
		{Recipients: []Recipient{{UID: "u2"}}, Cursor: "next"},
		{Recipients: []Recipient{{UID: "u3"}}, Done: true},
	}}

	dispatch, err := dispatchCommittedRecipientsForTarget(context.Background(), AuthorityTarget{
		ChannelID: ChannelID{ID: "g1", Type: 2},
		Large:     true,
	}, CommittedEnvelope{
		MessageID:   1,
		MessageSeq:  7,
		ChannelID:   "g1",
		ChannelType: 2,
		FromUID:     "sender",
	}, subscriberCache{}, commitPorts{
		activeAdmitter:             active,
		subscribers:                source,
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 7},
		deliveryEnqueuer:           enqueuer,
		recipientBatchSize:         16,
		subscriberPageSize:         1,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipientsForTarget() delivery error = %v, want nil", err)
	}
	if !errors.Is(dispatch.activeErr, activeErr) {
		t.Fatalf("active projection error = %v, want %v", dispatch.activeErr, activeErr)
	}
	if source.calls != 2 {
		t.Fatalf("subscriber page calls = %d, want all 2 pages", source.calls)
	}
	if got := enqueuer.allUIDs(); !reflect.DeepEqual(got, []string{"u2", "u3"}) {
		t.Fatalf("enqueued recipients = %#v, want all large-channel pages", got)
	}
}

func TestActiveBatchFailureStillCommitsSubscriberSnapshot(t *testing.T) {
	activeErr := errors.New("active unavailable")
	target := AuthorityTarget{
		ChannelID:                 ChannelID{ID: "g1", Type: 2},
		SubscriberMutationVersion: 9,
	}
	dispatch, err := dispatchCommittedRecipientsForTarget(context.Background(), target, CommittedEnvelope{
		MessageID:   1,
		MessageSeq:  7,
		ChannelID:   "g1",
		ChannelType: 2,
		FromUID:     "sender",
	}, subscriberCache{}, commitPorts{
		activeAdmitter: &recordingActiveAdmitterForRecipientTest{err: activeErr},
		subscribers: &recordingSubscriberSourceForRecipientTest{pages: []SubscriberPage{{
			Recipients: []Recipient{{UID: "u2"}},
			Done:       true,
		}}},
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipientsForTarget() delivery error = %v, want nil", err)
	}
	if !errors.Is(dispatch.activeErr, activeErr) {
		t.Fatalf("active projection error = %v, want %v", dispatch.activeErr, activeErr)
	}
	if !dispatch.subscriberCache.matches(target) {
		t.Fatalf("subscriber cache = %#v, want ready version %d despite active failure", dispatch.subscriberCache, target.SubscriberMutationVersion)
	}
}

func TestSubscriberSnapshotDoubleFailurePreservesIndependentActiveError(t *testing.T) {
	target := AuthorityTarget{
		ChannelID:                 ChannelID{ID: "g1", Type: 2},
		SubscriberMutationVersion: 9,
	}
	dispatch, err := dispatchCommittedRecipientsForTarget(context.Background(), target, CommittedEnvelope{
		MessageID:   1,
		MessageSeq:  7,
		ChannelID:   "g1",
		ChannelType: 2,
		FromUID:     "sender",
	}, subscriberCache{}, commitPorts{
		activeAdmitter:             &recordingActiveAdmitterForRecipientTest{err: context.DeadlineExceeded},
		subscribers:                &recordingSubscriberSourceForRecipientTest{pages: []SubscriberPage{{Recipients: []Recipient{{UID: "u2"}}, Done: true}}},
		recipientAuthorityResolver: failingRecipientAuthorityResolverForRecipientTest{err: ErrRouteNotReady},
		deliveryEnqueuer:           &recordingRecipientEnqueuerForRecipientTest{},
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("delivery error = %v, want %v", err, ErrRouteNotReady)
	}
	if !errors.Is(dispatch.activeErr, context.DeadlineExceeded) {
		t.Fatalf("active projection error = %v, want deadline exceeded", dispatch.activeErr)
	}
	if dispatch.subscriberCache.ready {
		t.Fatal("subscriber cache committed despite delivery failure")
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

func TestGroupRecipientAuthoritiesBuildsExactDisjointSlices(t *testing.T) {
	first := recipientAuthorityTargetForTest(1, 10, 100)
	second := recipientAuthorityTargetForTest(2, 20, 200)
	set := normalizeRecipientsForAuthorityResolution("sender", []Recipient{
		{UID: "u1"},
		{UID: " "},
		{UID: "u2"},
		{UID: "u3"},
	}, true)
	targets := map[string]RecipientAuthorityTarget{
		"sender": first,
		"u1":     first,
		"u2":     second,
		"u3":     first,
	}
	results := make([]RecipientAuthorityResult, len(set.authorityUIDs))
	for index, uid := range set.authorityUIDs {
		results[index].Target = targets[uid]
	}

	grouping, err := groupRecipientAuthorities(set, results, "sender")
	if err != nil {
		t.Fatalf("groupRecipientAuthorities() error = %v", err)
	}
	if len(grouping.groups) != 2 {
		t.Fatalf("authority groups = %d, want 2", len(grouping.groups))
	}

	firstGroup := grouping.groups[0]
	secondGroup := grouping.groups[1]
	if got := recipientUIDs(firstGroup.recipients); !reflect.DeepEqual(got, []string{"u1", "u3"}) {
		t.Fatalf("first delivery group = %#v, want exact non-empty u1,u3", got)
	}
	if got := recipientUIDs(secondGroup.recipients); !reflect.DeepEqual(got, []string{"u2"}) {
		t.Fatalf("second delivery group = %#v, want exact non-empty u2", got)
	}
	if len(firstGroup.recipients) != 2 || cap(firstGroup.recipients) != 2 ||
		len(secondGroup.recipients) != 1 || cap(secondGroup.recipients) != 1 {
		t.Fatalf("delivery slice len/cap = first %d/%d second %d/%d, want 2/2 and 1/1",
			len(firstGroup.recipients), cap(firstGroup.recipients), len(secondGroup.recipients), cap(secondGroup.recipients))
	}
	if got := activeRecipientUIDsForTarget(ConversationActiveTargetBatch{Batch: conversationactive.ActiveBatch{Recipients: firstGroup.activeRecipients}}); !reflect.DeepEqual(got, []string{"u1", "u3"}) {
		t.Fatalf("first active group = %#v, want exact non-empty u1,u3", got)
	}
	if got := activeRecipientUIDsForTarget(ConversationActiveTargetBatch{Batch: conversationactive.ActiveBatch{Recipients: secondGroup.activeRecipients}}); !reflect.DeepEqual(got, []string{"u2"}) {
		t.Fatalf("second active group = %#v, want exact non-empty u2", got)
	}
	if len(firstGroup.activeRecipients) != 2 || cap(firstGroup.activeRecipients) != 2 ||
		len(secondGroup.activeRecipients) != 1 || cap(secondGroup.activeRecipients) != 1 {
		t.Fatalf("active slice len/cap = first %d/%d second %d/%d, want 2/2 and 1/1",
			len(firstGroup.activeRecipients), cap(firstGroup.activeRecipients), len(secondGroup.activeRecipients), cap(secondGroup.activeRecipients))
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
		t.Fatalf("enqueued batches = %d, want 2", len(enqueuer.batches))
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

	if enqueuer.legacyCalls != 0 {
		t.Fatalf("legacy enqueue calls = %d, want 0 when plan admission is available", enqueuer.legacyCalls)
	}
	if len(enqueuer.plans) != 1 {
		t.Fatalf("delivery plans = %d, want one recipient-page plan", len(enqueuer.plans))
	}
	plan := enqueuer.plans[0]
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

func (e *payloadAliasRecipientEnqueuerForRecipientTest) EnqueueRecipientBatch(_ context.Context, _ RecipientAuthorityTarget, batch RecipientBatch) error {
	if len(batch.Event.Payload) > 0 && len(e.payload) > 0 && &batch.Event.Payload[0] == &e.payload[0] {
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

func TestDispatchRecipientSetSharesAlignedAuthoritySnapshotWithRoutedActive(t *testing.T) {
	senderTarget := authority.Target{HashSlot: 1, SlotID: 7, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}
	firstTarget := authority.Target{HashSlot: 2, SlotID: 7, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}
	secondTarget := authority.Target{HashSlot: 12, SlotID: 7, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}
	resolver := &alignedRecipientAuthorityResolverForRecipientTest{results: map[string]RecipientAuthorityResult{
		"sender": {Target: senderTarget},
		"u1":     {Target: firstTarget},
		"u2":     {Target: secondTarget},
	}}
	delivery := &recordingRecipientPlanEnqueuerForRecipientTest{}
	active := &recordingRoutedActiveAdmitterForRecipientTest{}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{
		MessageID:         1,
		MessageSeq:        9,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "sender",
		ServerTimestampMS: 123,
	}, []Recipient{{UID: " u1 "}, {UID: "u2"}, {UID: "u1"}}, commitPorts{
		activeAdmitter:             active,
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           delivery,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}
	if resolver.singleCalls != 0 || resolver.batchCalls != 1 {
		t.Fatalf("resolver calls = single:%d batch:%d, want one aligned batch only", resolver.singleCalls, resolver.batchCalls)
	}
	if !reflect.DeepEqual(resolver.batchUIDs, []string{"sender", "u1", "u2"}) {
		t.Fatalf("resolver UIDs = %#v, want sender then unique trimmed recipients", resolver.batchUIDs)
	}
	if active.legacyCalls != 0 || active.routedCalls != 1 {
		t.Fatalf("active calls = legacy:%d routed:%d, want one routed call only", active.legacyCalls, active.routedCalls)
	}
	if len(active.groups) != 3 {
		t.Fatalf("active groups = %d, want sender plus two physical hash-slot groups", len(active.groups))
	}
	if active.groups[0].Target != senderTarget || active.groups[0].Batch.SenderUID != "sender" || len(active.groups[0].Batch.Recipients) != 0 {
		t.Fatalf("sender active group = %#v, want sender-only exact target", active.groups[0])
	}
	if active.groups[1].Target != firstTarget || active.groups[2].Target != secondTarget {
		t.Fatalf("recipient active targets = %#v, want distinct physical hash slots despite one logical Slot", active.groups[1:])
	}
	if got := activeRecipientUIDsForTarget(active.groups[1]); !reflect.DeepEqual(got, []string{"u1"}) {
		t.Fatalf("first active recipients = %#v, want coalesced u1", got)
	}
	if got := activeRecipientUIDsForTarget(active.groups[2]); !reflect.DeepEqual(got, []string{"u2"}) {
		t.Fatalf("second active recipients = %#v, want u2", got)
	}
	if len(delivery.plans) != 1 || len(delivery.plans[0].Targets) != 2 {
		t.Fatalf("delivery plans = %#v, want one plan with two recipient targets", delivery.plans)
	}
	if delivery.plans[0].Targets[0].Target != firstTarget || delivery.plans[0].Targets[1].Target != secondTarget {
		t.Fatalf("delivery targets = %#v, want aligned exact targets", delivery.plans[0].Targets)
	}
	if got := recipientUIDs(delivery.plans[0].Targets[0].Recipients); !reflect.DeepEqual(got, []string{"u1", "u1"}) {
		t.Fatalf("delivery first target recipients = %#v, want duplicate delivery rows preserved", got)
	}
	activeRows := 0
	for _, group := range active.groups {
		activeRows += len(group.Batch.Recipients)
		for _, recipient := range group.Batch.Recipients {
			if recipient.UID == "" {
				t.Fatalf("active group contains empty UID: %#v", group)
			}
		}
	}
	if activeRows != 2 {
		t.Fatalf("active recipient rows = %d, want exactly two unique recipients", activeRows)
	}
	deliveryRows := 0
	for _, target := range delivery.plans[0].Targets {
		deliveryRows += len(target.Recipients)
		for _, recipient := range target.Recipients {
			if recipient.UID == "" {
				t.Fatalf("delivery target contains empty UID: %#v", target)
			}
		}
	}
	if deliveryRows != 3 {
		t.Fatalf("delivery recipient rows = %d, want exactly three normalized rows", deliveryRows)
	}
}

func TestDispatchRecipientSetSenderRouteFailureKeepsDeliveryAndFallsBackActive(t *testing.T) {
	senderErr := errors.New("sender route unavailable")
	activeErr := errors.New("active fallback unavailable")
	recipientTarget := recipientAuthorityTargetForTest(9, 20, 200)
	resolver := &alignedRecipientAuthorityResolverForRecipientTest{results: map[string]RecipientAuthorityResult{
		"sender": {Err: senderErr},
		"u1":     {Target: recipientTarget},
	}}
	delivery := &recordingRecipientPlanEnqueuerForRecipientTest{}
	active := &recordingRoutedActiveAdmitterForRecipientTest{err: activeErr}

	dispatch, err := dispatchRecipientSetResult(context.Background(), CommittedEnvelope{
		MessageID: 1,
		FromUID:   "sender",
	}, []Recipient{{UID: "u1"}}, commitPorts{
		activeAdmitter:             active,
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           delivery,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("delivery error = %v, want sender-only route failure isolated", err)
	}
	if !errors.Is(dispatch.activeErr, activeErr) {
		t.Fatalf("active fallback error = %v, want %v", dispatch.activeErr, activeErr)
	}
	detail := postCommitFailureDetailFromError(dispatch.activeErr)
	if detail.UID != "u1" || detail.UIDCount != 1 || detail.RecipientCount != 1 {
		t.Fatalf("active fallback detail = %#v, want recipient-only diagnostics", detail)
	}
	if len(delivery.plans) != 1 || delivery.plans[0].RecipientCount() != 1 || delivery.plans[0].Targets[0].Target != recipientTarget {
		t.Fatalf("delivery plans = %#v, want successful u1 delivery", delivery.plans)
	}
	if active.routedCalls != 0 || active.legacyCalls != 1 {
		t.Fatalf("active calls = routed:%d legacy:%d, want one compatibility fallback", active.routedCalls, active.legacyCalls)
	}
	if resolver.batchCalls != 1 || !reflect.DeepEqual(resolver.batchUIDs, []string{"sender", "u1"}) {
		t.Fatalf("resolver calls/uids = %d %#v, want one aligned sender+recipient snapshot", resolver.batchCalls, resolver.batchUIDs)
	}
}

func TestDispatchRecipientSetRecipientRouteFailureSkipsDeliveryAndFallsBackActive(t *testing.T) {
	recipientErr := errors.New("recipient route unavailable")
	resolver := &alignedRecipientAuthorityResolverForRecipientTest{results: map[string]RecipientAuthorityResult{
		"sender": {Target: recipientAuthorityTargetForTest(1, 10, 100)},
		"u1":     {Err: recipientErr},
	}}
	delivery := &recordingRecipientPlanEnqueuerForRecipientTest{}
	active := &recordingRoutedActiveAdmitterForRecipientTest{}

	dispatch, err := dispatchRecipientSetResult(context.Background(), CommittedEnvelope{
		MessageID: 1,
		FromUID:   "sender",
	}, []Recipient{{UID: "u1"}}, commitPorts{
		activeAdmitter:             active,
		recipientAuthorityResolver: resolver,
		deliveryEnqueuer:           delivery,
		recipientBatchSize:         16,
	})
	if !errors.Is(err, recipientErr) {
		t.Fatalf("delivery error = %v, want recipient route error", err)
	}
	if dispatch.activeErr != nil {
		t.Fatalf("active fallback error = %v, want nil", dispatch.activeErr)
	}
	if len(delivery.plans) != 0 {
		t.Fatalf("delivery plans = %#v, want all-or-nothing skip", delivery.plans)
	}
	if active.routedCalls != 0 || active.legacyCalls != 1 {
		t.Fatalf("active calls = routed:%d legacy:%d, want one compatibility fallback", active.routedCalls, active.legacyCalls)
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

func TestRecipientDispatchesDifferentAuthorityTargetsConcurrently(t *testing.T) {
	first := recipientAuthorityTargetForTest(1, 10, 100)
	second := recipientAuthorityTargetForTest(2, 20, 200)
	enqueuer := newBlockingRecipientEnqueuerForRecipientTest()
	defer enqueuer.release()
	errC := make(chan error, 1)

	go func() {
		errC <- dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
			{UID: "u1"},
			{UID: "u2"},
		}, commitPorts{
			recipientAuthorityResolver: mapRecipientAuthorityResolverForRecipientTest{
				targets: map[string]RecipientAuthorityTarget{"u1": first, "u2": second},
			},
			deliveryEnqueuer:             enqueuer,
			recipientBatchSize:           1,
			recipientDispatchConcurrency: 2,
		})
	}()

	started := enqueuer.waitStartedTargets(t, 2)
	if len(started) != 2 {
		t.Fatalf("started targets = %d, want 2", len(started))
	}
	if !containsRecipientTargetForTest(started, first) || !containsRecipientTargetForTest(started, second) {
		t.Fatalf("started targets = %#v, want both authority targets", started)
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
			deliveryEnqueuer:             enqueuer,
			recipientBatchSize:           1,
			recipientDispatchConcurrency: 2,
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
	targets []RecipientAuthorityTarget
	batches []RecipientBatch
}

func (r *recordingRecipientEnqueuerForRecipientTest) EnqueueRecipientBatch(_ context.Context, target RecipientAuthorityTarget, batch RecipientBatch) error {
	r.steps.add("delivery")
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = append(r.targets, target)
	r.batches = append(r.batches, batch.Clone())
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
	for i, batch := range r.batches {
		target := r.targets[i]
		for _, recipient := range batch.Recipients {
			out[target] = append(out[target], recipient.UID)
		}
	}
	return out
}

type recordingRecipientDeliveryEnqueuerForRecipientTest struct {
	mu      sync.Mutex
	steps   *orderedStepsForDeliveryTest
	targets []RecipientAuthorityTarget
	batches []RecipientBatch
}

type recordingRecipientPlanEnqueuerForRecipientTest struct {
	legacyCalls int
	plans       []RecipientDeliveryPlan
}

func (e *recordingRecipientPlanEnqueuerForRecipientTest) EnqueueRecipientBatch(_ context.Context, _ RecipientAuthorityTarget, _ RecipientBatch) error {
	e.legacyCalls++
	return nil
}

func (e *recordingRecipientPlanEnqueuerForRecipientTest) EnqueueRecipientDeliveryPlan(_ context.Context, plan RecipientDeliveryPlan) error {
	e.plans = append(e.plans, plan.Clone())
	return nil
}

func (e *recordingRecipientDeliveryEnqueuerForRecipientTest) EnqueueRecipientBatch(_ context.Context, target RecipientAuthorityTarget, batch RecipientBatch) error {
	e.steps.add("delivery")
	e.mu.Lock()
	defer e.mu.Unlock()
	e.targets = append(e.targets, target)
	e.batches = append(e.batches, batch.Clone())
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
	for i, batch := range e.batches {
		target := e.targets[i]
		for _, recipient := range batch.Recipients {
			out[target] = append(out[target], recipient.UID)
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

func (r *blockingRecipientEnqueuerForRecipientTest) EnqueueRecipientBatch(ctx context.Context, target RecipientAuthorityTarget, _ RecipientBatch) error {
	r.mu.Lock()
	r.targets = append(r.targets, target)
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

type recordingActiveAdmitterForRecipientTest struct {
	steps   *orderedStepsForDeliveryTest
	err     error
	batches []conversationactive.ActiveBatch
}

type recordingRoutedActiveAdmitterForRecipientTest struct {
	legacyCalls int
	routedCalls int
	groups      []ConversationActiveTargetBatch
	err         error
}

func (a *recordingRoutedActiveAdmitterForRecipientTest) AdmitActiveBatch(_ context.Context, _ conversationactive.ActiveBatch) error {
	a.legacyCalls++
	return a.err
}

func (a *recordingRoutedActiveAdmitterForRecipientTest) AdmitRoutedActiveBatches(_ context.Context, groups []ConversationActiveTargetBatch) error {
	a.routedCalls++
	a.groups = append([]ConversationActiveTargetBatch(nil), groups...)
	for index := range a.groups {
		a.groups[index].Batch.Recipients = append([]conversationactive.ActiveEntry(nil), groups[index].Batch.Recipients...)
	}
	return a.err
}

func activeRecipientUIDsForTarget(group ConversationActiveTargetBatch) []string {
	uids := make([]string, 0, len(group.Batch.Recipients))
	for _, recipient := range group.Batch.Recipients {
		uids = append(uids, recipient.UID)
	}
	return uids
}

func (a *recordingActiveAdmitterForRecipientTest) AdmitActiveBatch(_ context.Context, batch conversationactive.ActiveBatch) error {
	a.steps.add("active")
	a.batches = append(a.batches, batch)
	return a.err
}

func (a *recordingActiveAdmitterForRecipientTest) recipientUIDs() []string {
	var out []string
	for _, batch := range a.batches {
		for _, recipient := range batch.Recipients {
			out = append(out, recipient.UID)
		}
	}
	return out
}
