package channelwrite

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

func TestScopedUIDsBypassSubscriberScan(t *testing.T) {
	source := &recordingSubscriberSourceForRecipientTest{failOnCall: true}
	router := &recordingRecipientRouterForRecipientTest{}
	event := CommittedEnvelope{
		MessageID:         1,
		ChannelID:         "scoped",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u2", "u3"},
	}

	err := dispatchCommittedRecipients(context.Background(), event, commitPorts{
		subscribers:                source,
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 7},
		recipientRouter:            router,
		recipientBatchSize:         16,
		subscriberPageSize:         2,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}

	if source.calls != 0 {
		t.Fatalf("subscriber page calls = %d, want 0", source.calls)
	}
	got := router.allUIDs()
	if !reflect.DeepEqual(got, []string{"u2", "u3"}) {
		t.Fatalf("recipient uids = %#v, want scoped u2,u3", got)
	}
}

func TestPersonChannelDerivesExactlyCanonicalParticipants(t *testing.T) {
	router := &recordingRecipientRouterForRecipientTest{}
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
		recipientRouter:            router,
		recipientBatchSize:         16,
		subscriberPageSize:         2,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}

	got := router.allUIDs()
	if !reflect.DeepEqual(got, []string{left, right}) {
		t.Fatalf("person recipients = %#v, want canonical participants %#v", got, []string{left, right})
	}
}

func TestGroupChannelPagesSubscribersBeforeDispatchingNextPage(t *testing.T) {
	router := &recordingRecipientRouterForRecipientTest{}
	source := &recordingSubscriberSourceForRecipientTest{
		router: router,
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
		recipientRouter:            router,
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
	if got := router.allUIDs(); !reflect.DeepEqual(got, []string{"u1", "u2", "u3"}) {
		t.Fatalf("recipient uids = %#v, want paged subscribers", got)
	}
}

func TestRecipientBatchesAreGroupedByRecipientAuthorityTarget(t *testing.T) {
	router := &recordingRecipientRouterForRecipientTest{}
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
		recipientRouter:            router,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}

	got := router.byTarget()
	target10 := recipientAuthorityTargetForTest(1, 10, 100)
	target20 := recipientAuthorityTargetForTest(2, 20, 200)
	if !reflect.DeepEqual(got[target10], []string{"u1", "u3"}) {
		t.Fatalf("target 10 recipients = %#v, want u1,u3", got[target10])
	}
	if !reflect.DeepEqual(got[target20], []string{"u2"}) {
		t.Fatalf("target 20 recipients = %#v, want u2", got[target20])
	}
}

func TestRecipientBatchesKeepSameLeaderDifferentFenceTargetsSeparate(t *testing.T) {
	router := &recordingRecipientRouterForRecipientTest{}
	first := authority.Target{HashSlot: 1, SlotID: 11, LeaderNodeID: 10, RouteRevision: 100, AuthorityEpoch: 1000}
	second := authority.Target{HashSlot: 2, SlotID: 11, LeaderNodeID: 10, RouteRevision: 100, AuthorityEpoch: 1000}
	resolver := mapRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{"u1": first, "u2": second},
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: "u2"},
	}, commitPorts{
		recipientAuthorityResolver: resolver,
		recipientRouter:            router,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}

	got := router.byTarget()
	if len(got) != 2 {
		t.Fatalf("target groups = %d, want 2 exact fenced targets", len(got))
	}
	if !reflect.DeepEqual(got[first], []string{"u1"}) || !reflect.DeepEqual(got[second], []string{"u2"}) {
		t.Fatalf("target groups = %#v, want separate same-leader targets", got)
	}
}

func TestInvalidRecipientAuthorityTargetMapsRouteNotReady(t *testing.T) {
	router := &recordingRecipientRouterForRecipientTest{}
	resolver := mapRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{"u1": {}},
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{{UID: "u1"}}, commitPorts{
		recipientAuthorityResolver: resolver,
		recipientRouter:            router,
		recipientBatchSize:         16,
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("dispatchRecipientSet() error = %v, want ErrRouteNotReady", err)
	}
	if router.callCount() != 0 {
		t.Fatalf("router calls = %d, want 0 for invalid target", router.callCount())
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
			router := &recordingRecipientRouterForRecipientTest{}
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
				recipientRouter:            router,
				recipientBatchSize:         16,
				subscriberPageSize:         2,
			})
			if !errors.Is(err, ErrInvalidSubscriberCursor) {
				t.Fatalf("dispatchCommittedRecipients() error = %v, want ErrInvalidSubscriberCursor", err)
			}
			if got := router.allUIDs(); !reflect.DeepEqual(got, []string{"first"}) {
				t.Fatalf("dispatched recipients = %#v, want only prior valid page before invalid cursor", got)
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

type recordingSubscriberSourceForRecipientTest struct {
	router                  *recordingRecipientRouterForRecipientTest
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
	if s.calls == 1 && s.router != nil && s.router.callCount() > 0 {
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

type recordingRecipientRouterForRecipientTest struct {
	mu      sync.Mutex
	targets []RecipientAuthorityTarget
	batches []RecipientBatch
}

func (r *recordingRecipientRouterForRecipientTest) DispatchRecipientBatch(_ context.Context, target RecipientAuthorityTarget, batch RecipientBatch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = append(r.targets, target)
	r.batches = append(r.batches, batch.Clone())
	return nil
}

func (r *recordingRecipientRouterForRecipientTest) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.batches)
}

func (r *recordingRecipientRouterForRecipientTest) allUIDs() []string {
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

func (r *recordingRecipientRouterForRecipientTest) byTarget() map[RecipientAuthorityTarget][]string {
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

func recipientAuthorityTargetForTest(hashSlot uint16, leader uint64, epoch uint64) RecipientAuthorityTarget {
	return authority.Target{
		HashSlot:       hashSlot,
		SlotID:         uint32(hashSlot + 100),
		LeaderNodeID:   leader,
		RouteRevision:  uint64(hashSlot + 1000),
		AuthorityEpoch: epoch,
	}
}
