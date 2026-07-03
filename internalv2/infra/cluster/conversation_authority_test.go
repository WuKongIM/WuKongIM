package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityClientUsesLocalAuthority(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{nodeID: 1, route: cluster.Route{HashSlot: 7, SlotID: 2, Leader: 1, LeaderTerm: 5, ConfigEpoch: 6, Revision: 3, AuthorityEpoch: 4}}
	client := NewConversationAuthorityClient(node, local)
	patch := conversationusecase.ActivePatch{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1}
	if err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{patch}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	wantTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 5, ConfigEpoch: 6, RouteRevision: 3, AuthorityEpoch: 4}
	if len(local.targets) != 1 || local.targets[0] != wantTarget {
		t.Fatalf("local targets = %#v, want %#v", local.targets, wantTarget)
	}
	if len(local.patches) != 1 || !reflect.DeepEqual(local.patches[0], patch) {
		t.Fatalf("local patches = %#v, want %#v", local.patches, patch)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchSplitsSenderAndReceiverTargets(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	senderTarget := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	receiverTarget := cluster.Route{HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]cluster.Route{
			"sender":   senderTarget,
			"receiver": receiverTarget,
		},
	}
	client := NewConversationAuthorityClient(node, local)

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "sender",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	batches := activeBatchesByHashSlot(local.activeBatches)
	senderBatch, ok := batches[1]
	if !ok {
		t.Fatalf("active batches = %#v, want sender target batch", local.activeBatches)
	}
	if senderBatch.SenderUID != "sender" || len(senderBatch.Recipients) != 0 {
		t.Fatalf("sender target batch = %#v, want SenderUID only", senderBatch)
	}
	receiverBatch, ok := batches[2]
	if !ok {
		t.Fatalf("active batches = %#v, want receiver target batch", local.activeBatches)
	}
	if receiverBatch.SenderUID != "" || !reflect.DeepEqual(receiverBatch.Recipients, []conversationactive.ActiveEntry{{UID: "receiver"}}) {
		t.Fatalf("receiver target batch = %#v, want receiver subset without SenderUID", receiverBatch)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchPreservesKind(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	target := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]cluster.Route{
			"sender":   target,
			"receiver": target,
		},
	}
	client := NewConversationAuthorityClient(node, local)

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindCMD,
		SenderUID:   "sender",
		ChannelID:   "g1____cmd",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if len(local.activeBatches) != 1 {
		t.Fatalf("active batches = %#v, want one local batch", local.activeBatches)
	}
	if got := local.activeBatches[0].batch.Kind; got != metadb.ConversationKindCMD {
		t.Fatalf("active batch kind = %v, want %v", got, metadb.ConversationKindCMD)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchKeepsSenderWithSameTargetRecipients(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	target := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]cluster.Route{
			"sender":   target,
			"receiver": target,
		},
	}
	client := NewConversationAuthorityClient(node, local)
	recipients := []conversationactive.ActiveEntry{
		{UID: "sender", IsSender: true},
		{UID: "receiver"},
	}

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "sender",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients:  recipients,
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if len(local.activeBatches) != 1 {
		t.Fatalf("active batches = %#v, want one same-target batch", local.activeBatches)
	}
	got := local.activeBatches[0]
	if got.target.HashSlot != 1 || got.batch.SenderUID != "sender" || !reflect.DeepEqual(got.batch.Recipients, recipients) {
		t.Fatalf("active batch = %#v, want sender plus same-target recipients", got)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchCachesSenderRecipientRoute(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	firstTarget := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	movedTarget := cluster.Route{HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUIDSequence: map[string][]cluster.Route{
			"sender": {firstTarget, movedTarget},
		},
	}
	client := NewConversationAuthorityClient(node, local)

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "sender",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients: []conversationactive.ActiveEntry{
			{UID: "sender"},
			{UID: "sender", IsSender: true},
		},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if got := node.routeKeyCallsForUID("sender"); got != 1 {
		t.Fatalf("RouteKey(sender) calls = %d, want 1 cached route lookup", got)
	}
	if len(local.activeBatches) != 1 {
		t.Fatalf("active batches = %#v, want one sender target batch", local.activeBatches)
	}
	got := local.activeBatches[0]
	wantRecipients := []conversationactive.ActiveEntry{{UID: "sender", IsSender: true}}
	if got.target.HashSlot != firstTarget.HashSlot || got.batch.SenderUID != "sender" || !reflect.DeepEqual(got.batch.Recipients, wantRecipients) {
		t.Fatalf("active batch = %#v, want first target with coalesced sender recipient", got)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchCoalescesDuplicateRecipients(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	firstTarget := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	movedTarget := cluster.Route{HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUIDSequence: map[string][]cluster.Route{
			"receiver": {firstTarget, movedTarget},
		},
	}
	client := NewConversationAuthorityClient(node, local)

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients: []conversationactive.ActiveEntry{
			{UID: "receiver"},
			{UID: "receiver", IsSender: true},
		},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if got := node.routeKeyCallsForUID("receiver"); got != 1 {
		t.Fatalf("RouteKey(receiver) calls = %d, want 1 cached route lookup", got)
	}
	if len(local.activeBatches) != 1 {
		t.Fatalf("active batches = %#v, want one receiver target batch", local.activeBatches)
	}
	got := local.activeBatches[0]
	wantRecipients := []conversationactive.ActiveEntry{{UID: "receiver", IsSender: true}}
	if got.target.HashSlot != firstTarget.HashSlot || got.batch.SenderUID != "" || !reflect.DeepEqual(got.batch.Recipients, wantRecipients) {
		t.Fatalf("active batch = %#v, want first target with coalesced receiver recipient", got)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchRetriesStaleRouteWithFreshRoute(t *testing.T) {
	local := &fakeConversationAuthorityLocal{activeBatchErrs: []error{conversationusecase.ErrStaleRoute, nil}}
	firstTarget := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	freshTarget := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUIDSequence: map[string][]cluster.Route{
			"receiver": {firstTarget, freshTarget},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if got := node.routeKeyCallsForUID("receiver"); got != 2 {
		t.Fatalf("RouteKey(receiver) calls = %d, want 2 for fresh retry", got)
	}
	if len(local.activeBatches) != 2 {
		t.Fatalf("active batch attempts = %d, want 2", len(local.activeBatches))
	}
	if local.activeBatches[0].target.RouteRevision != 10 || local.activeBatches[1].target.RouteRevision != 11 {
		t.Fatalf("active batch targets = %#v, want original then fresh route", local.activeBatches)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchRetriesOnlyFailedTargetGroup(t *testing.T) {
	local := &fakeConversationAuthorityLocal{
		activeBatchErrsByHashSlot: map[uint16][]error{
			2: {conversationusecase.ErrStaleRoute, nil},
		},
	}
	senderTarget := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	receiverFirstTarget := cluster.Route{HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21}
	receiverFreshTarget := cluster.Route{HashSlot: 2, SlotID: 2, Leader: 1, Revision: 12, AuthorityEpoch: 22}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUIDSequence: map[string][]cluster.Route{
			"sender":   {senderTarget},
			"receiver": {receiverFirstTarget, receiverFreshTarget},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		SenderUID:   "sender",
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if got := activeBatchAttemptCountsByHashSlot(local.activeBatches); got[1] != 1 || got[2] != 2 {
		t.Fatalf("active batch attempts by hash slot = %#v, want sender once and receiver twice", got)
	}
	if got := node.routeKeyCallsForUID("sender"); got != 1 {
		t.Fatalf("RouteKey(sender) calls = %d, want no retry after successful target group", got)
	}
	if got := node.routeKeyCallsForUID("receiver"); got != 2 {
		t.Fatalf("RouteKey(receiver) calls = %d, want retry only failed receiver group", got)
	}
}

func TestConversationAuthorityClientAdmitActiveBatchRetriesStaleRouteWithBoundedFreshRoutes(t *testing.T) {
	local := &fakeConversationAuthorityLocal{
		activeBatchErrs: []error{
			conversationusecase.ErrStaleRoute,
			conversationusecase.ErrStaleRoute,
			conversationusecase.ErrStaleRoute,
			nil,
		},
	}
	target := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUIDSequence: map[string][]cluster.Route{
			"receiver": {
				target,
				{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
				{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 12, AuthorityEpoch: 22},
				{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 13, AuthorityEpoch: 23},
			},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  9,
		ActiveAtMS:  100,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitActiveBatch() error = %v, want ErrStaleRoute after 3 attempts", err)
	}
	if got := node.routeKeyCallsForUID("receiver"); got != 3 {
		t.Fatalf("RouteKey(receiver) calls = %d, want fresh route per retry", got)
	}
	if len(local.activeBatches) != 3 {
		t.Fatalf("active batch attempts = %d, want 3 attempts", len(local.activeBatches))
	}
	for i, attempt := range local.activeBatches {
		wantRevision := uint64(10 + i)
		if attempt.target.RouteRevision != wantRevision {
			t.Fatalf("active batch attempt %d route revision = %d, want %d", i, attempt.target.RouteRevision, wantRevision)
		}
	}
}

func TestConversationAuthorityClientRoutesRemoteList(t *testing.T) {
	remoteAuthority := &fakeConversationAuthorityLocal{page: conversationusecase.ActiveViewPage{
		Rows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
		Done: true,
	}}
	adapter := accessnode.New(accessnode.Options{ConversationAuthority: remoteAuthority})
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		route:  cluster.Route{HashSlot: 7, SlotID: 2, Leader: 2, Revision: 3, AuthorityEpoch: 4},
		handler: nodeRPCHandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return adapter.HandleConversationAuthorityRPC(ctx, payload)
		}),
	}
	client := NewConversationAuthorityClient(node, nil)
	page, err := client.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" {
		t.Fatalf("page = %#v, want remote row", page)
	}
}

func TestConversationAuthorityClientGroupsByExactTarget(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]cluster.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2, ActiveAt: 20},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.targets) != 2 {
		t.Fatalf("targets = %#v, want two exact route targets", local.targets)
	}
}

func TestConversationAuthorityClientGroupsByFullTargetFields(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]cluster.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 20},
			"u3": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "g2", ChannelType: 2, ActiveAt: 20},
		{UID: "u3", Kind: metadb.ConversationKindNormal, ChannelID: "g3", ChannelType: 2, ActiveAt: 30},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.targets) != 3 {
		t.Fatalf("targets = %#v, want revision/epoch to split exact targets", local.targets)
	}
}

func TestConversationAuthorityClientGroupByTargetUsesEveryFencingField(t *testing.T) {
	base := cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}
	tests := []struct {
		name  string
		other cluster.Route
	}{
		{name: "hash slot", other: cluster.Route{HashSlot: 2, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20}},
		{name: "slot id", other: cluster.Route{HashSlot: 1, SlotID: 3, Leader: 1, Revision: 10, AuthorityEpoch: 20}},
		{name: "leader node", other: cluster.Route{HashSlot: 1, SlotID: 2, Leader: 2, Revision: 10, AuthorityEpoch: 20}},
		{name: "route revision", other: cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 20}},
		{name: "authority epoch", other: cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 21}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &fakeConversationAuthorityNode{
				nodeID: 1,
				routesByUID: map[string]cluster.Route{
					"u1": base,
					"u2": tt.other,
				},
			}
			client := NewConversationAuthorityClient(node, &fakeConversationAuthorityLocal{})

			groups, err := client.groupByTarget([]conversationusecase.ActivePatch{
				{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 10},
				{UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "g2", ChannelType: 2, ActiveAt: 20},
			})
			if err != nil {
				t.Fatalf("groupByTarget() error = %v", err)
			}
			if len(groups) != 2 {
				t.Fatalf("groups = %#v, want two groups when %s differs", groups, tt.name)
			}
		})
	}
}

func TestConversationAuthorityClientCoalescesIdenticalExactTargets(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]cluster.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
		},
	}
	client := NewConversationAuthorityClient(node, local)

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "g2", ChannelType: 2, ActiveAt: 20},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.targets) != 1 {
		t.Fatalf("targets = %#v, want one coalesced exact target", local.targets)
	}
	if got := deliveredConversationPatchUIDCounts(local.deliveredPatches); got["u1"] != 1 || got["u2"] != 1 {
		t.Fatalf("delivered patch counts = %#v, want both patches in one group", got)
	}
}

func TestConversationAuthorityClientAdmitPatchesDoesNotRetryStaleRoute(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitErrs: []error{conversationusecase.ErrStaleRoute, nil}}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []cluster.Route{
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("AdmitPatches should not sleep between retries")
		return nil
	}

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches() error = %v, want ErrStaleRoute", err)
	}
	if node.routeKeyCalls != 1 {
		t.Fatalf("RouteKey calls = %d, want 1", node.routeKeyCalls)
	}
	if len(local.targets) != 1 || local.targets[0].RouteRevision != 10 {
		t.Fatalf("targets = %#v, want one admit with original route revision 10", local.targets)
	}
}

func TestConversationAuthorityClientAdmitPatchesDoesNotRetryRouteNotReady(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitErrs: []error{conversationusecase.ErrRouteNotReady, nil}}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []cluster.Route{
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("AdmitPatches should not sleep between retries")
		return nil
	}

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("AdmitPatches() error = %v, want ErrRouteNotReady", err)
	}
	if node.routeKeyCalls != 1 {
		t.Fatalf("RouteKey calls = %d, want 1", node.routeKeyCalls)
	}
	if len(local.targets) != 1 || local.targets[0].RouteRevision != 10 {
		t.Fatalf("targets = %#v, want one admit with original route revision 10", local.targets)
	}
}

func TestConversationAuthorityClientRetriesRouteNotReadyListWithFreshRoute(t *testing.T) {
	local := &fakeConversationAuthorityLocal{
		listErrs: []error{conversationusecase.ErrRouteNotReady, nil},
		page: conversationusecase.ActiveViewPage{
			Rows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
			Done: true,
		},
	}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []cluster.Route{
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	page, err := client.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView() error = %v", err)
	}
	if node.routeKeyCalls != 2 {
		t.Fatalf("RouteKey calls = %d, want 2", node.routeKeyCalls)
	}
	if len(local.targets) != 2 || local.targets[1].RouteRevision != 11 {
		t.Fatalf("targets = %#v, want retry with fresh route revision 11", local.targets)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" {
		t.Fatalf("page = %#v, want retried row", page)
	}
}

func TestConversationAuthorityClientRetriesRawRemoteRouteErrorWithFreshRoute(t *testing.T) {
	remoteAuthority := &fakeConversationAuthorityLocal{page: conversationusecase.ActiveViewPage{
		Rows: []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
		Done: true,
	}}
	adapter := accessnode.New(accessnode.Options{ConversationAuthority: remoteAuthority})
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []cluster.Route{
			{HashSlot: 1, SlotID: 2, Leader: 2, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 2, Revision: 11, AuthorityEpoch: 21},
		},
		rpcErrs: []error{cluster.ErrNotLeader, nil},
		handler: nodeRPCHandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return adapter.HandleConversationAuthorityRPC(ctx, payload)
		}),
	}
	client := NewConversationAuthorityClient(node, nil)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	page, err := client.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActiveView() error = %v", err)
	}
	if node.routeKeyCalls != 2 || len(node.calls) != 2 {
		t.Fatalf("route/call counts = %d/%d, want retry through fresh route", node.routeKeyCalls, len(node.calls))
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" {
		t.Fatalf("page = %#v, want retried remote row", page)
	}
}

func TestConversationAuthorityClientAdmitPatchesDoesNotExhaustBoundedRetries(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitAlwaysErr: conversationusecase.ErrNotLeader}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		route:  cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, conversationusecase.ErrNotLeader) {
		t.Fatalf("AdmitPatches() error = %v, want ErrNotLeader", err)
	}
	if len(local.targets) != 1 {
		t.Fatalf("admit attempts = %d, want 1", len(local.targets))
	}
}

func TestConversationAuthorityClientAdmitPatchesReturnsContextCancellationBeforeRouting(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		route:  cluster.Route{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
	}
	client := NewConversationAuthorityClient(node, local)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.AdmitPatches(ctx, []conversationusecase.ActivePatch{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AdmitPatches() error = %v, want context.Canceled", err)
	}
	if node.routeKeyCalls != 0 {
		t.Fatalf("RouteKey calls = %d, want 0", node.routeKeyCalls)
	}
}

func TestConversationAuthorityClientAdmitPatchesDoesNotRetryNotLeader(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitErrs: []error{conversationusecase.ErrNotLeader, nil}}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routes: []cluster.Route{
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			{HashSlot: 1, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("AdmitPatches should not sleep between retries")
		return nil
	}

	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2, ActiveAt: 10}})
	if !errors.Is(err, conversationusecase.ErrNotLeader) {
		t.Fatalf("AdmitPatches() error = %v, want ErrNotLeader", err)
	}
	if node.routeKeyCalls != 1 {
		t.Fatalf("RouteKey calls = %d, want 1", node.routeKeyCalls)
	}
	if len(local.targets) != 1 || local.targets[0].RouteRevision != 10 {
		t.Fatalf("targets = %#v, want one admit with original route revision 10", local.targets)
	}
}

func TestConversationAuthorityClientAdmitPatchesStopsAtRetryableGroupError(t *testing.T) {
	local := &fakeConversationAuthorityLocal{admitErrs: []error{conversationusecase.ErrStaleRoute, nil, nil, nil}}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]cluster.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
			"u3": {HashSlot: 3, SlotID: 2, Leader: 1, Revision: 12, AuthorityEpoch: 22},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	client.routeRetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("AdmitPatches should not sleep between retries")
		return nil
	}
	patches := []conversationusecase.ActivePatch{
		{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", Kind: metadb.ConversationKindNormal, ChannelID: "g2", ChannelType: 2, ActiveAt: 20},
		{UID: "u3", Kind: metadb.ConversationKindNormal, ChannelID: "g3", ChannelType: 2, ActiveAt: 30},
	}

	err := client.AdmitPatches(context.Background(), patches)
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitPatches() error = %v, want ErrStaleRoute", err)
	}
	if len(local.targets) != 1 {
		t.Fatalf("targets = %#v, want only first group attempted", local.targets)
	}
	if len(local.deliveredPatches) != 0 {
		t.Fatalf("delivered patches = %#v, want none after first group failure", local.deliveredPatches)
	}
}

func TestConversationAuthorityClientDrainAuthority(t *testing.T) {
	local := &fakeConversationAuthorityLocal{drainResult: "drained"}
	node := &fakeConversationAuthorityNode{nodeID: 1}
	client := NewConversationAuthorityClient(node, local)
	target := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}

	got, err := client.DrainAuthority(context.Background(), target)
	if err != nil {
		t.Fatalf("DrainAuthority() error = %v", err)
	}
	if got != "drained" {
		t.Fatalf("DrainAuthority() = %q, want drained", got)
	}
	if len(local.drainTargets) != 1 || local.drainTargets[0] != target {
		t.Fatalf("drain targets = %#v, want %#v", local.drainTargets, target)
	}
}

func TestConversationAuthorityClientDrainAuthorityNilNodeRemoteTargetReturnsRouteNotReady(t *testing.T) {
	client := NewConversationAuthorityClient(nil, &fakeConversationAuthorityLocal{})
	target := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, RouteRevision: 3, AuthorityEpoch: 4}

	_, err := client.DrainAuthority(context.Background(), target)
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("DrainAuthority() error = %v, want ErrRouteNotReady", err)
	}
}

func TestConversationAuthorityClientMapsRouteErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "route not ready", err: cluster.ErrRouteNotReady, want: conversationusecase.ErrRouteNotReady},
		{name: "no slot leader", err: cluster.ErrNoSlotLeader, want: conversationusecase.ErrRouteNotReady},
		{name: "not leader", err: cluster.ErrNotLeader, want: conversationusecase.ErrNotLeader},
		{name: "proposal backpressure", err: propose.ErrProposalBackpressure, want: conversationusecase.ErrRouteNotReady},
		{name: "background proposal throttled", err: propose.ErrBackgroundProposalThrottled, want: conversationusecase.ErrRouteNotReady},
		{name: "context canceled", err: context.Canceled, want: context.Canceled},
		{name: "context deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &fakeConversationAuthorityNode{nodeID: 1, routeErr: tt.err}
			client := NewConversationAuthorityClient(node, &fakeConversationAuthorityLocal{})
			client.routeRetrySleep = func(context.Context, time.Duration) error { return nil }

			_, err := client.ListConversationActiveView(context.Background(), metadb.ConversationKindNormal, "u1", metadb.ConversationActiveCursor{}, 10)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ListConversationActiveView() error = %v, want %v", err, tt.want)
			}
		})
	}
}

type nodeRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f nodeRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

type fakeConversationAuthorityNode struct {
	nodeID              uint64
	route               cluster.Route
	routesByUID         map[string]cluster.Route
	routesByUIDSequence map[string][]cluster.Route
	routes              []cluster.Route
	routeErr            error
	rpcErrs             []error
	handler             cluster.NodeRPCHandler
	calls               []rpcCall
	routeKeyCalls       int
	routeKeyCallsByUID  map[string]int
	registered          map[uint8]cluster.NodeRPCHandler
	watch               chan cluster.RouteAuthorityEvent
}

func (f *fakeConversationAuthorityNode) NodeID() uint64 {
	return f.nodeID
}

func (f *fakeConversationAuthorityNode) RouteKey(uid string) (cluster.Route, error) {
	f.routeKeyCalls++
	if f.routeKeyCallsByUID == nil {
		f.routeKeyCallsByUID = make(map[string]int)
	}
	uidCallIndex := f.routeKeyCallsByUID[uid]
	f.routeKeyCallsByUID[uid] = uidCallIndex + 1
	if f.routeErr != nil {
		return cluster.Route{}, f.routeErr
	}
	if routes, ok := f.routesByUIDSequence[uid]; ok && len(routes) > 0 {
		if uidCallIndex >= len(routes) {
			uidCallIndex = len(routes) - 1
		}
		return routes[uidCallIndex], nil
	}
	if route, ok := f.routesByUID[uid]; ok {
		return route, nil
	}
	if len(f.routes) > 0 {
		idx := f.routeKeyCalls - 1
		if idx >= len(f.routes) {
			idx = len(f.routes) - 1
		}
		return f.routes[idx], nil
	}
	return f.route, nil
}

func (f *fakeConversationAuthorityNode) routeKeyCallsForUID(uid string) int {
	if f.routeKeyCallsByUID == nil {
		return 0
	}
	return f.routeKeyCallsByUID[uid]
}

func (f *fakeConversationAuthorityNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calls = append(f.calls, rpcCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	if len(f.rpcErrs) > 0 {
		err := f.rpcErrs[0]
		f.rpcErrs = f.rpcErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	if f.handler != nil {
		return f.handler.HandleRPC(ctx, payload)
	}
	if handler := f.registered[serviceID]; handler != nil {
		return handler.HandleRPC(ctx, payload)
	}
	return nil, errors.New("missing rpc handler")
}

func (f *fakeConversationAuthorityNode) RegisterRPC(serviceID uint8, handler cluster.NodeRPCHandler) {
	if f.registered == nil {
		f.registered = make(map[uint8]cluster.NodeRPCHandler)
	}
	f.registered[serviceID] = handler
}

func (f *fakeConversationAuthorityNode) WatchRouteAuthorities() <-chan cluster.RouteAuthorityEvent {
	if f.watch == nil {
		f.watch = make(chan cluster.RouteAuthorityEvent)
	}
	return f.watch
}

type fakeConversationAuthorityLocal struct {
	patches                   []conversationusecase.ActivePatch
	deliveredPatches          []conversationusecase.ActivePatch
	targets                   []conversationusecase.RouteTarget
	activeBatches             []activeBatchDelivery
	page                      conversationusecase.ActiveViewPage
	admitErrs                 []error
	admitAlwaysErr            error
	activeBatchErrs           []error
	activeBatchErrsByHashSlot map[uint16][]error
	activeBatchAlwaysErr      error
	listErrs                  []error
	drainResult               string
	drainTargets              []conversationusecase.RouteTarget
}

func (f *fakeConversationAuthorityLocal) AdmitPatches(_ context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	f.targets = append(f.targets, target)
	f.patches = append(f.patches, patches...)
	if f.admitAlwaysErr != nil {
		return f.admitAlwaysErr
	}
	if len(f.admitErrs) > 0 {
		err := f.admitErrs[0]
		f.admitErrs = f.admitErrs[1:]
		if err != nil {
			return err
		}
	}
	f.deliveredPatches = append(f.deliveredPatches, patches...)
	return nil
}

func (f *fakeConversationAuthorityLocal) AdmitActiveBatch(_ context.Context, target conversationusecase.RouteTarget, batch conversationactive.ActiveBatch) error {
	f.activeBatches = append(f.activeBatches, activeBatchDelivery{
		target: target,
		batch:  cloneConversationActiveBatch(batch),
	})
	if f.activeBatchAlwaysErr != nil {
		return f.activeBatchAlwaysErr
	}
	if len(f.activeBatchErrsByHashSlot[target.HashSlot]) > 0 {
		err := f.activeBatchErrsByHashSlot[target.HashSlot][0]
		f.activeBatchErrsByHashSlot[target.HashSlot] = f.activeBatchErrsByHashSlot[target.HashSlot][1:]
		if err != nil {
			return err
		}
	}
	if len(f.activeBatchErrs) > 0 {
		err := f.activeBatchErrs[0]
		f.activeBatchErrs = f.activeBatchErrs[1:]
		if err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeConversationAuthorityLocal) ListConversationActiveViewForTarget(_ context.Context, target conversationusecase.RouteTarget, _ metadb.ConversationKind, _ string, _ metadb.ConversationActiveCursor, _ int) (conversationusecase.ActiveViewPage, error) {
	f.targets = append(f.targets, target)
	if len(f.listErrs) > 0 {
		err := f.listErrs[0]
		f.listErrs = f.listErrs[1:]
		if err != nil {
			return conversationusecase.ActiveViewPage{}, err
		}
	}
	return f.page, nil
}

func (f *fakeConversationAuthorityLocal) DrainAuthority(_ context.Context, target conversationusecase.RouteTarget) (string, error) {
	f.drainTargets = append(f.drainTargets, target)
	return f.drainResult, nil
}

func deliveredConversationPatchUIDCounts(patches []conversationusecase.ActivePatch) map[string]int {
	out := make(map[string]int, len(patches))
	for _, patch := range patches {
		out[patch.UID]++
	}
	return out
}

type activeBatchDelivery struct {
	target conversationusecase.RouteTarget
	batch  conversationactive.ActiveBatch
}

func activeBatchesByHashSlot(deliveries []activeBatchDelivery) map[uint16]conversationactive.ActiveBatch {
	out := make(map[uint16]conversationactive.ActiveBatch, len(deliveries))
	for _, delivery := range deliveries {
		out[delivery.target.HashSlot] = delivery.batch
	}
	return out
}

func activeBatchAttemptCountsByHashSlot(deliveries []activeBatchDelivery) map[uint16]int {
	out := make(map[uint16]int, len(deliveries))
	for _, delivery := range deliveries {
		out[delivery.target.HashSlot]++
	}
	return out
}

func cloneConversationActiveBatch(batch conversationactive.ActiveBatch) conversationactive.ActiveBatch {
	batch.Recipients = append([]conversationactive.ActiveEntry(nil), batch.Recipients...)
	return batch
}
