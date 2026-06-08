package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

func TestSenderAuthorityRouterUsesLocalSubmitterForLocalTarget(t *testing.T) {
	local := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 7, RouteRevision: 3, AuthorityEpoch: 4}
	resolver := &senderAuthorityResolverForTest{target: local}
	submitter := &senderAuthoritySubmitterForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 10, MessageSeq: 2, Reason: ReasonSuccess}}}}
	remote := &senderAuthorityRemoteForTest{}

	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{
		LocalNodeID: 7,
		Resolver:    resolver,
		Local:       submitter,
		Remote:      remote,
	})

	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || results[0].Result.MessageID != 10 {
		t.Fatalf("results = %#v, want local result", results)
	}
	if submitter.calls != 1 {
		t.Fatalf("local calls = %d, want 1", submitter.calls)
	}
	if remote.calls != 0 {
		t.Fatalf("remote calls = %d, want 0", remote.calls)
	}
}

func TestSenderAuthorityRouterUsesRemoteForRemoteTarget(t *testing.T) {
	remoteTarget := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 8, RouteRevision: 3, AuthorityEpoch: 4}
	resolver := &senderAuthorityResolverForTest{target: remoteTarget}
	local := &senderAuthoritySubmitterForTest{}
	remote := &senderAuthorityRemoteForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 11, MessageSeq: 3, Reason: ReasonSuccess}}}}

	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{
		LocalNodeID: 7,
		Resolver:    resolver,
		Local:       local,
		Remote:      remote,
	})

	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || results[0].Result.MessageID != 11 {
		t.Fatalf("results = %#v, want remote result", results)
	}
	if remote.calls != 1 || remote.target != remoteTarget {
		t.Fatalf("remote calls/target = %d/%#v, want 1/%#v", remote.calls, remote.target, remoteTarget)
	}
	if local.calls != 0 {
		t.Fatalf("local calls = %d, want 0", local.calls)
	}
}

func TestSenderAuthorityRouterMapsResolveError(t *testing.T) {
	resolver := &senderAuthorityResolverForTest{err: ErrRouteNotReady}
	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{LocalNodeID: 7, Resolver: resolver})
	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || !errors.Is(results[0].Err, ErrRouteNotReady) {
		t.Fatalf("result err = %v, want ErrRouteNotReady", results[0].Err)
	}
}

func TestSenderAuthorityRouterPreservesInputOrderAcrossTargets(t *testing.T) {
	localTarget := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 7, RouteRevision: 3, AuthorityEpoch: 4}
	remoteTarget := authority.Target{HashSlot: 2, SlotID: 3, LeaderNodeID: 8, RouteRevision: 3, AuthorityEpoch: 5}
	resolver := &senderAuthorityResolverForTest{targetsByUID: map[string]authority.Target{"local": localTarget, "remote": remoteTarget}}
	local := &senderAuthoritySubmitterForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 1, MessageSeq: 1, Reason: ReasonSuccess}}}}
	remote := &senderAuthorityRemoteForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 2, MessageSeq: 2, Reason: ReasonSuccess}}}}
	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local, Remote: remote})

	results := router.SendBatch([]SendBatchItem{
		{Context: context.Background(), Command: SendCommand{FromUID: "remote"}},
		{Context: context.Background(), Command: SendCommand{FromUID: "local"}},
	})
	if results[0].Result.MessageID != 2 || results[1].Result.MessageID != 1 {
		t.Fatalf("ordered message ids = %d/%d, want 2/1", results[0].Result.MessageID, results[1].Result.MessageID)
	}
}

func TestSenderAuthorityRouterRejectsEmptySenderWithoutResolve(t *testing.T) {
	resolver := &senderAuthorityResolverForTest{target: authority.Target{LeaderNodeID: 7}}
	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{LocalNodeID: 7, Resolver: resolver})

	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{}}})
	if len(results) != 1 || results[0].Result.Reason != ReasonAuthFail {
		t.Fatalf("results = %#v, want auth failure", results)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolver.calls)
	}
}

func TestSenderAuthorityRouterMapsShortSubmitterResult(t *testing.T) {
	localTarget := authority.Target{HashSlot: 1, SlotID: 2, LeaderNodeID: 7, RouteRevision: 3, AuthorityEpoch: 4}
	resolver := &senderAuthorityResolverForTest{target: localTarget}
	local := &senderAuthoritySubmitterForTest{}
	router := NewSenderAuthorityRouter(SenderAuthorityRouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local})

	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || !errors.Is(results[0].Err, ErrAppendResultMissing) {
		t.Fatalf("result err = %v, want ErrAppendResultMissing", results[0].Err)
	}
}

type senderAuthorityResolverForTest struct {
	target       authority.Target
	targetsByUID map[string]authority.Target
	err          error
	calls        int
}

func (r *senderAuthorityResolverForTest) ResolveUIDAuthority(ctx context.Context, uid string) (authority.Target, error) {
	r.calls++
	if r.err != nil {
		return authority.Target{}, r.err
	}
	if r.targetsByUID != nil {
		return r.targetsByUID[uid], nil
	}
	return r.target, nil
}

type senderAuthoritySubmitterForTest struct {
	results []SendBatchItemResult
	items   []SendBatchItem
	calls   int
}

func (s *senderAuthoritySubmitterForTest) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	s.calls++
	s.items = append(s.items, items...)
	return s.results
}

type senderAuthorityRemoteForTest struct {
	results []SendBatchItemResult
	target  authority.Target
	items   []SendBatchItem
	calls   int
}

func (r *senderAuthorityRemoteForTest) SendBatchToAuthority(ctx context.Context, target authority.Target, items []SendBatchItem) []SendBatchItemResult {
	r.calls++
	r.target = target
	r.items = append(r.items, items...)
	return r.results
}
