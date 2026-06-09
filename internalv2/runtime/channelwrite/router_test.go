package channelwrite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRouterLocalPathCallsSubmitLocal(t *testing.T) {
	target := routerTarget("local", 2, 7)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	local := &routerLocalSubmitterForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 10, MessageSeq: 3, Reason: ReasonSuccess}}}}
	remote := &routerRemoteForTest{}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local, Remote: remote})

	results := router.SendBatch([]SendBatchItem{routerItem("u1", "local", 2)})
	if len(results) != 1 || results[0].Result.MessageID != 10 || results[0].Result.MessageSeq != 3 {
		t.Fatalf("results = %#v, want local result", results)
	}
	if local.calls != 1 || local.target != target || len(local.items) != 1 {
		t.Fatalf("local calls/target/items = %d/%#v/%d, want one local submit", local.calls, local.target, len(local.items))
	}
	if remote.calls != 0 {
		t.Fatalf("remote calls = %d, want 0", remote.calls)
	}
}

func TestRouterRemotePathCallsForwardSendBatch(t *testing.T) {
	target := routerTarget("remote", 2, 8)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	local := &routerLocalSubmitterForTest{}
	remote := &routerRemoteForTest{results: []SendBatchItemResult{{Result: SendResult{Reason: ReasonUnsupported}}}}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local, Remote: remote})

	results := router.SendBatch([]SendBatchItem{routerItem("u1", "remote", 2)})
	if len(results) != 1 || results[0].Result.Reason != ReasonUnsupported {
		t.Fatalf("results = %#v, want remote result preserved", results)
	}
	if remote.calls != 1 || remote.target != target || len(remote.items) != 1 {
		t.Fatalf("remote calls/target/items = %d/%#v/%d, want one remote forward", remote.calls, remote.target, len(remote.items))
	}
	if local.calls != 0 {
		t.Fatalf("local calls = %d, want 0", local.calls)
	}
}

func TestRouterRetriesRouteErrorsWithinDeadline(t *testing.T) {
	for _, retryErr := range []error{ErrStaleRoute, ErrNotChannelAuthority, ErrNotLeader, ErrRouteNotReady} {
		t.Run(retryErr.Error(), func(t *testing.T) {
			target := routerTarget("retry", 2, 7)
			resolver := &routerResolverForTest{targets: []AuthorityTarget{target, target}}
			local := &routerLocalSubmitterForTest{
				errs:    []error{retryErr, nil},
				results: []SendBatchItemResult{{Result: SendResult{MessageID: 12, MessageSeq: 5, Reason: ReasonSuccess}}},
			}
			router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local, RetryBackoff: time.Millisecond})
			item := routerItem("u1", "retry", 2)
			item.Deadline = time.Now().Add(time.Second)

			results := router.SendBatch([]SendBatchItem{item})
			if len(results) != 1 || results[0].Err != nil || results[0].Result.MessageID != 12 {
				t.Fatalf("results = %#v, want retry success", results)
			}
			if resolver.calls != 2 || local.calls != 2 {
				t.Fatalf("resolver/local calls = %d/%d, want retry through fresh resolve and submit", resolver.calls, local.calls)
			}
		})
	}
}

func TestRouterRemoteTargetNeverCreatesLocalChannelState(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 7})
	target := routerTarget("remote-state", 2, 8)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	remote := &routerRemoteForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 33, MessageSeq: 9, Reason: ReasonSuccess}}}}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: group, Remote: remote})

	results := router.SendBatch([]SendBatchItem{routerItem("u1", "remote-state", 2)})
	if len(results) != 1 || results[0].Err != nil || results[0].Result.MessageID != 33 {
		t.Fatalf("results = %#v, want remote success", results)
	}
	if group.StateCountForTest() != 0 {
		t.Fatalf("remote route created %d local states, want 0", group.StateCountForTest())
	}
}

func TestRouterOutboundLimitIsKeyedByLeaderNodeID(t *testing.T) {
	targetA := routerTarget("remote-a", 2, 8)
	targetB := routerTarget("remote-b", 2, 8)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{
		targetA.ChannelID: targetA,
		targetB.ChannelID: targetB,
	}}
	entered := make(chan struct{})
	release := make(chan struct{})
	remote := &routerRemoteForTest{
		entered: entered,
		release: release,
		results: []SendBatchItemResult{{Result: SendResult{MessageID: 1, Reason: ReasonSuccess}}},
	}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Remote: remote, MaxOutboundPerNode: 1})

	firstDone := make(chan []SendBatchItemResult, 1)
	go func() {
		firstDone <- router.SendBatch([]SendBatchItem{routerItem("u1", "remote-a", 2)})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatalf("first remote call did not enter")
	}

	second := router.SendBatch([]SendBatchItem{routerItem("u2", "remote-b", 2)})
	if len(second) != 1 || !errors.Is(second[0].Err, ErrBackpressured) {
		t.Fatalf("second results = %#v, want shared leader outbound backpressure", second)
	}
	close(release)
	select {
	case results := <-firstDone:
		if len(results) != 1 || results[0].Err != nil || results[0].Result.MessageID != 1 {
			t.Fatalf("first results = %#v, want success after release", results)
		}
	case <-time.After(time.Second):
		t.Fatalf("first remote call did not finish")
	}
	if remote.calls != 1 {
		t.Fatalf("remote calls = %d, want second channel on same leader to share limit", remote.calls)
	}
}

func TestRouterRejectsMissingChannelWithoutResolve(t *testing.T) {
	resolver := &routerResolverForTest{}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver})

	results := router.SendBatch([]SendBatchItem{{Context: context.Background(), Command: SendCommand{FromUID: "u1", ChannelType: 2, Payload: []byte("x")}}})
	if len(results) != 1 || results[0].Result.Reason != ReasonInvalidRequest {
		t.Fatalf("results = %#v, want item-level invalid request", results)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolver.calls)
	}
}

func routerTarget(channelID string, channelType uint8, leader uint64) AuthorityTarget {
	return AuthorityTarget{
		ChannelID:    ChannelID{ID: channelID, Type: channelType},
		ChannelKey:   channelKey(ChannelID{ID: channelID, Type: channelType}),
		LeaderNodeID: leader,
		Epoch:        1,
		LeaderEpoch:  1,
	}
}

func routerItem(uid, channelID string, channelType uint8) SendBatchItem {
	return SendBatchItem{
		Context: context.Background(),
		Command: SendCommand{
			FromUID:     uid,
			ChannelID:   channelID,
			ChannelType: channelType,
			Payload:     []byte("hello"),
			ClientMsgNo: uid + "-msg",
		},
	}
}

type routerResolverForTest struct {
	targetsByChannel map[ChannelID]AuthorityTarget
	targets          []AuthorityTarget
	errs             []error
	calls            int
}

func (r *routerResolverForTest) ResolveAppendAuthority(_ context.Context, id ChannelID) (AuthorityTarget, error) {
	call := r.calls
	r.calls++
	if call < len(r.errs) && r.errs[call] != nil {
		return AuthorityTarget{}, r.errs[call]
	}
	if call < len(r.targets) {
		return r.targets[call], nil
	}
	if r.targetsByChannel != nil {
		return r.targetsByChannel[id], nil
	}
	return AuthorityTarget{}, ErrRouteNotReady
}

type routerLocalSubmitterForTest struct {
	results []SendBatchItemResult
	errs    []error
	target  AuthorityTarget
	items   []SendBatchItem
	calls   int
}

func (s *routerLocalSubmitterForTest) SubmitLocal(_ context.Context, target AuthorityTarget, items []SendBatchItem) (*Future, error) {
	call := s.calls
	s.calls++
	s.target = target
	s.items = append(s.items, items...)
	if call < len(s.errs) && s.errs[call] != nil {
		return nil, s.errs[call]
	}
	future := newFuture(len(items))
	future.complete(s.results)
	return future, nil
}

type routerRemoteForTest struct {
	mu      sync.Mutex
	results []SendBatchItemResult
	target  AuthorityTarget
	items   []SendBatchItem
	calls   int
	entered chan<- struct{}
	once    sync.Once
	release <-chan struct{}
}

func (r *routerRemoteForTest) ForwardSendBatch(_ context.Context, target AuthorityTarget, items []SendBatchItem) []SendBatchItemResult {
	r.mu.Lock()
	r.calls++
	r.target = target
	r.items = append(r.items, items...)
	r.mu.Unlock()
	if r.entered != nil {
		r.once.Do(func() { close(r.entered) })
	}
	if r.release != nil {
		<-r.release
	}
	return r.results
}
