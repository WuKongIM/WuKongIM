package channelwrite

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

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

func TestRecipientAuthorityBatchResolverResolvesUniqueTrimmedUIDsOnce(t *testing.T) {
	router := &recordingRecipientRouterForRecipientTest{}
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
		recipientRouter:            router,
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
	got := router.byTarget()
	if !reflect.DeepEqual(got[target10], []string{"u1", "u1"}) {
		t.Fatalf("target 10 recipients = %#v, want duplicate u1 recipients preserved", got[target10])
	}
	if !reflect.DeepEqual(got[target20], []string{"u2"}) {
		t.Fatalf("target 20 recipients = %#v, want u2", got[target20])
	}
}

func TestRecipientAuthorityFallbackResolverReusesDuplicateUIDTarget(t *testing.T) {
	router := &recordingRecipientRouterForRecipientTest{}
	target := recipientAuthorityTargetForTest(1, 10, 100)
	resolver := &countingRecipientAuthorityResolverForRecipientTest{
		targets: map[string]RecipientAuthorityTarget{"u1": target},
	}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: " u1 "},
	}, commitPorts{
		recipientAuthorityResolver: resolver,
		recipientRouter:            router,
		recipientBatchSize:         1,
	})
	if err != nil {
		t.Fatalf("dispatchRecipientSet() error = %v", err)
	}

	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1 for duplicate UID", resolver.calls)
	}
	if got := router.allUIDs(); !reflect.DeepEqual(got, []string{"u1", "u1"}) {
		t.Fatalf("recipient uids = %#v, want duplicate trimmed u1 recipients", got)
	}
}

func TestRecipientDispatchesDifferentAuthorityTargetsConcurrently(t *testing.T) {
	first := recipientAuthorityTargetForTest(1, 10, 100)
	second := recipientAuthorityTargetForTest(2, 20, 200)
	router := newBlockingRecipientRouterForRecipientTest()
	defer router.release()
	errC := make(chan error, 1)

	go func() {
		errC <- dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
			{UID: "u1"},
			{UID: "u2"},
		}, commitPorts{
			recipientAuthorityResolver: mapRecipientAuthorityResolverForRecipientTest{
				targets: map[string]RecipientAuthorityTarget{"u1": first, "u2": second},
			},
			recipientRouter:              router,
			recipientBatchSize:           1,
			recipientDispatchConcurrency: 2,
		})
	}()

	started := router.waitStartedTargets(t, 2)
	if len(started) != 2 {
		t.Fatalf("started targets = %d, want 2", len(started))
	}
	if !containsRecipientTargetForTest(started, first) || !containsRecipientTargetForTest(started, second) {
		t.Fatalf("started targets = %#v, want both authority targets", started)
	}
	router.release()
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
	router := newBlockingRecipientRouterForRecipientTest()
	defer router.release()
	errC := make(chan error, 1)

	go func() {
		errC <- dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
			{UID: "u1"},
			{UID: "u2"},
		}, commitPorts{
			recipientAuthorityResolver: mapRecipientAuthorityResolverForRecipientTest{
				targets: map[string]RecipientAuthorityTarget{"u1": target, "u2": target},
			},
			recipientRouter:              router,
			recipientBatchSize:           1,
			recipientDispatchConcurrency: 2,
		})
	}()

	router.waitStartedTargets(t, 1)
	time.Sleep(20 * time.Millisecond)
	if got := router.startedCount(); got != 1 {
		t.Fatalf("started batches before release = %d, want 1 same-target batch in flight", got)
	}
	router.release()
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
	detail := postCommitFailureDetailFromError(err)
	if detail.Phase != "recipient_target_validate" || detail.UID != "u1" || detail.RecipientCount != 1 ||
		detail.TargetLeaderNodeID != 0 {
		t.Fatalf("post-commit failure detail = %#v, want invalid target detail for u1", detail)
	}
	if router.callCount() != 0 {
		t.Fatalf("router calls = %d, want 0 for invalid target", router.callCount())
	}
}

func TestRecipientAuthorityResolveFailureCarriesPostCommitFailureDetail(t *testing.T) {
	router := &recordingRecipientRouterForRecipientTest{}
	resolver := failingRecipientAuthorityResolverForRecipientTest{err: ErrRouteNotReady}

	err := dispatchRecipientSet(context.Background(), CommittedEnvelope{MessageID: 1}, []Recipient{
		{UID: "u1"},
		{UID: "u2"},
		{UID: "u1"},
	}, commitPorts{
		recipientAuthorityResolver: resolver,
		recipientRouter:            router,
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
	if router.callCount() != 0 {
		t.Fatalf("router calls = %d, want 0 when authority resolution fails", router.callCount())
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

func (r *batchRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthority(_ context.Context, uid string) (RecipientAuthorityTarget, error) {
	r.singleCalls++
	return r.targets[uid], nil
}

func (r *batchRecipientAuthorityResolverForRecipientTest) ResolveRecipientAuthorities(_ context.Context, uids []string) (map[string]RecipientAuthorityTarget, error) {
	r.batchCalls++
	r.batchUIDs = append([]string(nil), uids...)
	out := make(map[string]RecipientAuthorityTarget, len(uids))
	for _, uid := range uids {
		out[uid] = r.targets[uid]
	}
	return out, nil
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

type blockingRecipientRouterForRecipientTest struct {
	mu       sync.Mutex
	cond     *sync.Cond
	targets  []RecipientAuthorityTarget
	releaseC chan struct{}
	once     sync.Once
}

func newBlockingRecipientRouterForRecipientTest() *blockingRecipientRouterForRecipientTest {
	r := &blockingRecipientRouterForRecipientTest{releaseC: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *blockingRecipientRouterForRecipientTest) DispatchRecipientBatch(ctx context.Context, target RecipientAuthorityTarget, _ RecipientBatch) error {
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

func (r *blockingRecipientRouterForRecipientTest) waitStartedTargets(t *testing.T, want int) []RecipientAuthorityTarget {
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

func (r *blockingRecipientRouterForRecipientTest) startedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.targets)
}

func (r *blockingRecipientRouterForRecipientTest) release() {
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
		RouteRevision:  uint64(hashSlot + 1000),
		AuthorityEpoch: epoch,
	}
}
