package channelappend

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
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

func TestRouterResolvesSameChannelBatchOnce(t *testing.T) {
	target := routerTarget("same", 2, 7)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	local := &routerLocalSubmitterForTest{results: []SendBatchItemResult{
		{Result: SendResult{MessageID: 10, MessageSeq: 1, Reason: ReasonSuccess}},
		{Result: SendResult{MessageID: 11, MessageSeq: 2, Reason: ReasonSuccess}},
	}}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local})

	results := router.SendBatch([]SendBatchItem{
		routerItem("u1", "same", 2),
		routerItem("u2", "same", 2),
	})

	if len(results) != 2 || results[0].Result.MessageID != 10 || results[1].Result.MessageID != 11 {
		t.Fatalf("results = %#v, want both local successes", results)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want one authority resolve for same channel batch", resolver.calls)
	}
	if local.calls != 1 || len(local.items) != 2 {
		t.Fatalf("local calls/items = %d/%d, want one submit with both items", local.calls, len(local.items))
	}
}

func TestRouterAllItemsContextDoesNotStartWaitersForPlainItems(t *testing.T) {
	items := make([]SendBatchItem, 128)
	for i := range items {
		items[i] = routerItem("u1", "room", 2)
		items[i].Context = context.Background()
	}

	before := runtime.NumGoroutine()
	ctx, cancel := routerAllItemsContext(items)
	defer cancel()
	if err := contextErr(ctx); err != nil {
		t.Fatalf("routerAllItemsContext() context error = %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	after := runtime.NumGoroutine()
	if delta := after - before; delta > 8 {
		cancel()
		t.Fatalf("routerAllItemsContext spawned %d goroutines for plain items, want <= 8", delta)
	}
}

func TestRouterAllItemsContextSingleDeadlineUsesNativeDeadline(t *testing.T) {
	item := routerItem("u1", "room", 2)
	item.Deadline = time.Now().Add(time.Hour)

	ctx, cancel := routerAllItemsContext([]SendBatchItem{item})
	defer cancel()
	if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(item.Deadline) {
		t.Fatalf("context deadline = %v/%v, want item deadline %v", deadline, ok, item.Deadline)
	}
	time.Sleep(10 * time.Millisecond)
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	if strings.Contains(string(buf[:n]), "watchRouterItemTerminal") {
		t.Fatalf("single deadline item started router watcher goroutine")
	}
}

func TestRouterRoutesRequestScopedSendByCanonicalChannel(t *testing.T) {
	scoped, err := runtimechannelid.RequestSubscriberChannelFor([]string{" u2 ", "u1", "u2"})
	if err != nil {
		t.Fatalf("RequestSubscriberChannelFor() error = %v", err)
	}
	canonical := ChannelID{ID: scoped.CommandChannelID, Type: scoped.ChannelType}
	target := routerTarget(canonical.ID, canonical.Type, 7)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{canonical: target}}
	local := &routerLocalSubmitterForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 20, Reason: ReasonSuccess}}}}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local})

	item := routerItem("u1", "", 0)
	item.Command.RequestScoped = true
	item.Command.SyncOnce = true
	item.Command.MessageScopedUIDs = []string{" u2 ", "u1", "u2"}

	results := router.SendBatch([]SendBatchItem{item})
	if len(results) != 1 || results[0].Err != nil || results[0].Result.MessageID != 20 {
		t.Fatalf("results = %#v, want request-scoped local success", results)
	}
	if resolver.lastID != canonical {
		t.Fatalf("resolved channel = %#v, want canonical request-scoped channel %#v", resolver.lastID, canonical)
	}
	if local.target.ChannelID != canonical {
		t.Fatalf("local target channel = %#v, want canonical request-scoped channel %#v", local.target.ChannelID, canonical)
	}
	if len(local.items) != 1 || local.items[0].Command.ChannelID != "" || !local.items[0].Command.RequestScoped {
		t.Fatalf("submitted item command = %#v, want original request-scoped command preserved for authority writer prepare", local.items)
	}
}

func TestRouterRoutesNormalizePersonSendByCanonicalChannel(t *testing.T) {
	canonicalID, err := runtimechannelid.NormalizePersonChannel("u1", "u2")
	if err != nil {
		t.Fatalf("NormalizePersonChannel() error = %v", err)
	}
	canonical := ChannelID{ID: canonicalID, Type: channelTypePerson}
	target := routerTarget(canonical.ID, canonical.Type, 7)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{canonical: target}}
	local := &routerLocalSubmitterForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 21, Reason: ReasonSuccess}}}}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local})

	item := routerItem("u1", "u2", channelTypePerson)
	item.Command.NormalizePersonChannel = true

	results := router.SendBatch([]SendBatchItem{item})
	if len(results) != 1 || results[0].Err != nil || results[0].Result.MessageID != 21 {
		t.Fatalf("results = %#v, want normalize-person local success", results)
	}
	if resolver.lastID != canonical {
		t.Fatalf("resolved channel = %#v, want canonical person channel %#v", resolver.lastID, canonical)
	}
	if local.target.ChannelID != canonical {
		t.Fatalf("local target channel = %#v, want canonical person channel %#v", local.target.ChannelID, canonical)
	}
	if len(local.items) != 1 || local.items[0].Command.ChannelID != "u2" || !local.items[0].Command.NormalizePersonChannel {
		t.Fatalf("submitted item command = %#v, want original normalize-person command preserved for authority writer prepare", local.items)
	}
}

func TestRouterRoutesNoPersistSyncOnceByCommandChannel(t *testing.T) {
	commandChannelID := runtimechannelid.ToCommandChannel("room")
	canonical := ChannelID{ID: commandChannelID, Type: 2}
	target := routerTarget(commandChannelID, 2, 7)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{canonical: target}}
	local := &routerLocalSubmitterForTest{results: []SendBatchItemResult{{Result: SendResult{MessageID: 22, Reason: ReasonSuccess}}}}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local})

	item := routerItem("u1", "room", 2)
	item.Command.NoPersist = true
	item.Command.SyncOnce = true

	results := router.SendBatch([]SendBatchItem{item})
	if len(results) != 1 || results[0].Err != nil || results[0].Result.MessageID != 22 {
		t.Fatalf("results = %#v, want no-persist sync-once local success", results)
	}
	if resolver.lastID != canonical {
		t.Fatalf("resolved channel = %#v, want command channel %#v", resolver.lastID, canonical)
	}
	if local.target.ChannelID != canonical {
		t.Fatalf("local target channel = %#v, want command channel %#v", local.target.ChannelID, canonical)
	}
	if len(local.items) != 1 || local.items[0].Command.ChannelID != "room" || !local.items[0].Command.NoPersist || !local.items[0].Command.SyncOnce {
		t.Fatalf("submitted item command = %#v, want original no-persist sync-once command preserved for authority writer prepare", local.items)
	}
}

func TestRouterRejectsInvalidCommandWithoutRouteLookup(t *testing.T) {
	target := routerTarget("invalid", 2, 7)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver})

	results := router.SendBatch([]SendBatchItem{{
		Context: context.Background(),
		Command: SendCommand{
			FromUID:     "u1",
			ChannelID:   "invalid",
			ChannelType: 2,
		},
	}})
	if len(results) != 1 || results[0].Result.Reason != ReasonInvalidRequest {
		t.Fatalf("results = %#v, want invalid request", results)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want invalid command to avoid route lookup", resolver.calls)
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

func TestRouterItemCancellationDoesNotPoisonSameAuthorityBatch(t *testing.T) {
	target := routerTarget("ctx-batch", 2, 7)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	local := &routerLocalSubmitterForTest{
		results: []SendBatchItemResult{
			{Result: SendResult{MessageID: 1, Reason: ReasonSuccess}},
			{Result: SendResult{MessageID: 2, Reason: ReasonSuccess}},
		},
		completeDelay: 5 * time.Millisecond,
		onSubmit:      cancelFirst,
	}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local})

	first := routerItem("u1", "ctx-batch", 2)
	first.Context = firstCtx
	second := routerItem("u2", "ctx-batch", 2)

	results := router.SendBatch([]SendBatchItem{first, second})
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	if results[0].Err != nil || results[0].Result.MessageID != 1 {
		t.Fatalf("first result = %#v, want accepted success preserved", results[0])
	}
	if results[1].Err != nil || results[1].Result.MessageID != 2 {
		t.Fatalf("second result = %#v, want unaffected success", results[1])
	}
	if local.calls != 1 || len(local.batches) != 1 || len(local.batches[0]) != 2 {
		t.Fatalf("local batches = %#v, want both initially-active items submitted once", local.batches)
	}
}

func TestRouterItemDeadlineDoesNotPoisonSameAuthorityBatch(t *testing.T) {
	target := routerTarget("deadline-batch", 2, 7)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	local := &routerLocalSubmitterForTest{
		results: []SendBatchItemResult{
			{Result: SendResult{MessageID: 1, Reason: ReasonSuccess}},
			{Result: SendResult{MessageID: 2, Reason: ReasonSuccess}},
		},
		completeDelay: 15 * time.Millisecond,
	}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local})

	first := routerItem("u1", "deadline-batch", 2)
	first.Deadline = time.Now().Add(5 * time.Millisecond)
	second := routerItem("u2", "deadline-batch", 2)

	results := router.SendBatch([]SendBatchItem{first, second})
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	if results[0].Err != nil || results[0].Result.MessageID != 1 {
		t.Fatalf("first result = %#v, want accepted success preserved", results[0])
	}
	if results[1].Err != nil || results[1].Result.MessageID != 2 {
		t.Fatalf("second result = %#v, want unaffected success", results[1])
	}
}

func TestRouterMapsTerminalCanceledAfterItemDeadlineToTimeout(t *testing.T) {
	target := routerTarget("deadline-timeout", 2, 8)
	resolver := &routerResolverForTest{targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	remote := &routerRemoteForTest{waitContextDone: true}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Remote: remote})

	item := routerItem("u1", "deadline-timeout", 2)
	item.Deadline = time.Now().Add(5 * time.Millisecond)

	results := router.SendBatch([]SendBatchItem{item})
	if len(results) != 1 || !errors.Is(results[0].Err, context.DeadlineExceeded) {
		t.Fatalf("results = %#v, want deadline exceeded", results)
	}
}

func TestRouterRetryBackoffWakesOnCanceledPendingItem(t *testing.T) {
	target := routerTarget("retry-cancel", 2, 7)
	ctx, cancel := context.WithCancel(context.Background())
	resolver := &routerResolverForTest{errs: []error{ErrRouteNotReady, ErrRouteNotReady}, targetsByChannel: map[ChannelID]AuthorityTarget{target.ChannelID: target}}
	router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, RetryBackoff: time.Hour})
	item := routerItem("u1", "retry-cancel", 2)
	item.Context = ctx
	item.Deadline = time.Now().Add(time.Second)
	cancelSoon := time.AfterFunc(10*time.Millisecond, cancel)
	defer cancelSoon.Stop()

	started := time.Now()
	results := router.SendBatch([]SendBatchItem{item})
	elapsed := time.Since(started)
	if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("results = %#v, want context canceled", results)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("retry wait elapsed %s, want prompt wake after cancellation", elapsed)
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
	lastID           ChannelID
	calls            int
}

func (r *routerResolverForTest) ResolveAppendAuthority(_ context.Context, id ChannelID) (AuthorityTarget, error) {
	call := r.calls
	r.calls++
	r.lastID = id
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
	results       []SendBatchItemResult
	errs          []error
	target        AuthorityTarget
	items         []SendBatchItem
	batches       [][]SendBatchItem
	calls         int
	onSubmit      func()
	completeDelay time.Duration
}

func (s *routerLocalSubmitterForTest) SubmitLocal(_ context.Context, target AuthorityTarget, items []SendBatchItem) (*Future, error) {
	call := s.calls
	s.calls++
	s.target = target
	s.items = append(s.items, items...)
	s.batches = append(s.batches, append([]SendBatchItem(nil), items...))
	if s.onSubmit != nil {
		s.onSubmit()
	}
	if call < len(s.errs) && s.errs[call] != nil {
		return nil, s.errs[call]
	}
	future := newFuture(len(items))
	if s.completeDelay > 0 {
		go func() {
			time.Sleep(s.completeDelay)
			future.complete(s.results)
		}()
		return future, nil
	}
	future.complete(s.results)
	return future, nil
}

type routerRemoteForTest struct {
	mu              sync.Mutex
	results         []SendBatchItemResult
	target          AuthorityTarget
	items           []SendBatchItem
	calls           int
	entered         chan<- struct{}
	once            sync.Once
	release         <-chan struct{}
	waitContextDone bool
}

func (r *routerRemoteForTest) ForwardSendBatch(ctx context.Context, target AuthorityTarget, items []SendBatchItem) []SendBatchItemResult {
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
	if r.waitContextDone {
		<-ctx.Done()
		return routerErrorResults(len(items), ctx.Err())
	}
	return r.results
}
