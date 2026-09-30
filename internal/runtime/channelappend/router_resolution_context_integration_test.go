//go:build integration

package channelappend

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type resolutionContextResolver func(context.Context, ChannelID) (AuthorityTarget, error)

func (f resolutionContextResolver) ResolveAppendAuthority(ctx context.Context, id ChannelID) (AuthorityTarget, error) {
	return f(ctx, id)
}

// A same-Channel lookup must survive one caller's deadline, honor item-only
// deadlines when all callers end, and leave expired items out of admission.
func TestOrderedHotLaneAuthorityResolutionKeepsIndependentDeadlines(t *testing.T) {
	blocker := make(chan struct{})
	blockerEntered := make(chan struct{})
	lookup := make(chan context.Context, 1)
	releaseLookup := make(chan struct{})
	var blockerOnce, lookupOnce sync.Once
	var calls atomic.Int32
	target := routerTarget("hot", 2, 7)
	resolver := resolutionContextResolver(func(ctx context.Context, _ ChannelID) (AuthorityTarget, error) {
		if calls.Add(1) == 1 {
			close(blockerEntered)
			<-blocker
			return target, nil
		}
		lookup <- ctx
		select {
		case <-ctx.Done():
			return AuthorityTarget{}, ctx.Err()
		case <-releaseLookup:
			return target, nil
		}
	})
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: routerImmediateLocalSubmitter{}})
	s, err := NewOrderedSubmitter(OrderedSubmitterOptions{Workers: 1, Capacity: 4, PayloadCapacity: 4, BatchMaxRecords: 4, BatchMaxBytes: 4}, router)
	if err != nil {
		t.Fatal(err)
	}
	defer orderedClose(t, s)
	defer blockerOnce.Do(func() { close(blocker) })
	defer lookupOnce.Do(func() { close(releaseLookup) })
	if err := s.Submit([]SendBatchItem{orderedItem("hot", "blocker")}, func([]SendBatchItemResult) {}); err != nil {
		t.Fatal(err)
	}
	orderedWait(t, blockerEntered)
	first, cancelFirst := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelFirst()
	second, cancelSecond := context.WithTimeout(context.Background(), time.Second)
	defer cancelSecond()
	results := make(chan SendBatchItemResult, 2)
	for i, ctx := range []context.Context{first, second} {
		item := orderedItem("hot", "request")
		item.Command.ClientSeq = uint64(i + 1)
		item.Context = ctx
		if err := s.Submit([]SendBatchItem{item}, func(r []SendBatchItemResult) { results <- r[0] }); err != nil {
			t.Fatal(err)
		}
	}
	blockerOnce.Do(func() { close(blocker) })
	shared := orderedWait(t, lookup)
	orderedWait(t, first.Done())
	if err := shared.Err(); err != nil {
		t.Fatalf("live second caller lost shared authority lookup: %v", err)
	}
	lookupOnce.Do(func() { close(releaseLookup) })
	if got := orderedWait(t, results); !errors.Is(got.Err, context.DeadlineExceeded) {
		t.Fatalf("first deadline lost: %+v", got)
	}
	if got := orderedWait(t, results); got.Err != nil || got.Result.Reason != ReasonSuccess {
		t.Fatalf("live peer failed: %+v", got)
	}
}

func TestRouterAuthorityResolutionJoinsAllItemOnlyDeadlines(t *testing.T) {
	lookup := make(chan context.Context, 1)
	release := make(chan struct{})
	var once sync.Once
	resolver := resolutionContextResolver(func(ctx context.Context, _ ChannelID) (AuthorityTarget, error) {
		lookup <- ctx
		select {
		case <-ctx.Done():
			return AuthorityTarget{}, ctx.Err()
		case <-release:
			return AuthorityTarget{}, ErrRouteNotReady
		}
	})
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: routerImmediateLocalSubmitter{}})
	items := []SendBatchItem{orderedItem("hot", "1"), orderedItem("hot", "2")}
	for i := range items {
		items[i].Context = context.Background()
		items[i].Deadline = time.Now().Add(time.Duration(30+i*20) * time.Millisecond)
	}
	done := make(chan []SendBatchItemResult, 1)
	defer once.Do(func() { close(release) })
	go func() { done <- router.SendBatch(items) }()
	ctx := orderedWait(t, lookup)
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("all item-only deadlines left authority lookup running")
	}
	for _, result := range orderedWait(t, done) {
		if !errors.Is(result.Err, context.DeadlineExceeded) {
			t.Fatalf("item deadline replaced: %+v", result)
		}
	}
}
