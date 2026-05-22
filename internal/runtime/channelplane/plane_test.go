package channelplane

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestChannelPlaneMapsSameChannelToSameReactor(t *testing.T) {
	p, err := New(Options{ReactorCount: 8, LocalNode: 1, Resolver: staticResolver{route: localRoute("same")}, LocalOwner: noopOwner{}})
	require.NoError(t, err)

	id := channel.ChannelID{ID: "same", Type: 1}
	first := p.reactorIndex(id)
	for i := 0; i < 16; i++ {
		require.Equal(t, first, p.reactorIndex(id))
	}
}

func TestChannelCellSerializesSameChannelAppends(t *testing.T) {
	owner := newBlockingOwner()
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: staticResolver{route: localRoute("serial")}, LocalOwner: owner})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := p.AppendBatch(ctx, appendReq("serial", 1))
		firstDone <- err
	}()
	owner.waitStarted(t, 1)

	secondStarted := make(chan struct{})
	owner.setOnStart(func(n int) {
		if n == 2 {
			close(secondStarted)
		}
	})
	secondDone := make(chan error, 1)
	go func() {
		_, err := p.AppendBatch(ctx, appendReq("serial", 2))
		secondDone <- err
	}()

	select {
	case <-secondStarted:
		t.Fatal("second append started while first append was still inflight")
	case <-time.After(30 * time.Millisecond):
	}

	owner.releaseOne()
	require.NoError(t, <-firstDone)
	owner.waitStarted(t, 2)
	owner.releaseOne()
	require.NoError(t, <-secondDone)
}

func TestChannelPlaneCompletesFuturesInRequestOrder(t *testing.T) {
	owner := newSequenceOwner()
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: staticResolver{route: localRoute("ordered")}, LocalOwner: owner})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	futures := []*appendFuture{newAppendFuture(), newAppendFuture(), newAppendFuture()}
	for i := 0; i < 3; i++ {
		err := p.reactors[0].submit(ctx, &appendCommand{ctx: ctx, req: appendReq("ordered", uint64(i+1)), future: futures[i]})
		require.NoError(t, err)
	}
	results := make([]uint64, 3)
	for i, future := range futures {
		res, err := future.wait(ctx)
		require.NoError(t, err)
		require.Len(t, res.Items, 1)
		results[i] = res.Items[0].MessageSeq
	}

	require.Equal(t, []uint64{1, 2, 3}, results)
}

func TestRouteResolverSingleflightCoalescesConcurrentMisses(t *testing.T) {
	source := &countingRouteSource{route: localRoute("coalesce"), wait: make(chan struct{})}
	resolver := NewRouteResolver(source)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	const callers = 8
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := resolver.ResolveRoute(ctx, channel.ChannelID{ID: "coalesce", Type: 1})
			require.NoError(t, err)
		}()
	}
	source.waitCall(t)
	close(source.wait)
	wg.Wait()

	require.Equal(t, 1, source.calls())
}

func TestRouteResolverInvalidateRouteKeepsNewerGeneration(t *testing.T) {
	id := channel.ChannelID{ID: "invalidate-generation", Type: 1}
	first := localRoute(id.ID)
	first.RouteGeneration = 7
	second := first
	second.RouteGeneration = 8
	source := &sequenceResolver{routes: []ChannelRoute{first, second}}
	resolver := NewRouteResolver(source)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	route, err := resolver.ResolveRoute(ctx, id)
	require.NoError(t, err)
	require.Equal(t, uint64(7), route.RouteGeneration)

	resolver.InvalidateRoute(id, 6)
	route, err = resolver.ResolveRoute(ctx, id)
	require.NoError(t, err)
	require.Equal(t, uint64(7), route.RouteGeneration)
	require.Equal(t, 1, source.callCount())

	resolver.InvalidateRoute(id, 7)
	route, err = resolver.ResolveRoute(ctx, id)
	require.NoError(t, err)
	require.Equal(t, uint64(8), route.RouteGeneration)
	require.Equal(t, 2, source.callCount())
}

func TestChannelPlaneCancelsQueuedFutureOnContextDone(t *testing.T) {
	owner := newBlockingOwner()
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: staticResolver{route: localRoute("cancel")}, LocalOwner: owner})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	firstCtx, firstCancel := context.WithTimeout(context.Background(), time.Second)
	defer firstCancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := p.AppendBatch(firstCtx, appendReq("cancel", 1))
		firstDone <- err
	}()
	owner.waitStarted(t, 1)

	queuedCtx, queuedCancel := context.WithCancel(context.Background())
	queuedDone := make(chan error, 1)
	go func() {
		_, err := p.AppendBatch(queuedCtx, appendReq("cancel", 2))
		queuedDone <- err
	}()
	queuedCancel()

	require.ErrorIs(t, <-queuedDone, context.Canceled)
	owner.releaseOne()
	require.NoError(t, <-firstDone)
	require.Eventually(t, func() bool { return owner.startedCount() == 1 }, time.Second, 10*time.Millisecond)
}

func TestReactorSubmitAfterStopRejectsWithoutEnqueue(t *testing.T) {
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: staticResolver{route: localRoute("stop-submit")}, LocalOwner: noopOwner{}})
	require.NoError(t, err)
	r := p.reactors[0]
	r.stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 2000; i++ {
		err := r.submit(ctx, &appendCommand{ctx: ctx, req: appendReq("stop-submit", uint64(i+1)), future: newAppendFuture()})
		require.ErrorIs(t, err, ErrClosed)
		require.Empty(t, r.inbox)
	}
}

func TestChannelPlaneUsesPeerReactorForRemoteLeader(t *testing.T) {
	peer := &recordingPlanePeerClient{}
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: staticResolver{route: remoteRoute("remote", 2)}, LocalOwner: noopOwner{}, PeerClient: peer, PeerBatchMaxWait: time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = p.AppendBatch(ctx, appendReq("remote", 1))
	require.NoError(t, err)

	require.Equal(t, channel.NodeID(2), peer.nodeID)
	require.Len(t, peer.req.Batches, 1)
	require.Equal(t, uint64(7), peer.req.Batches[0].RouteEpoch.RouteGeneration)
	require.Equal(t, uint64(9), peer.req.Batches[0].Request.ExpectedChannelEpoch)
	require.Equal(t, uint64(11), peer.req.Batches[0].Request.ExpectedLeaderEpoch)
}

func TestChannelCellRefreshesRouteAndRetriesStaleLocalAppend(t *testing.T) {
	resolver := &sequenceResolver{routes: []ChannelRoute{
		localRoute("retry-stale"),
		{ChannelID: channel.ChannelID{ID: "retry-stale", Type: 1}, Leader: 1, RouteGeneration: 8, ChannelEpoch: 10, LeaderEpoch: 11, Status: channel.StatusActive},
	}}
	owner := &epochCheckingOwner{staleEpoch: 9, successEpoch: 10}
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: resolver, LocalOwner: owner})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := p.AppendBatch(ctx, appendReq("retry-stale", 1))

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, uint64(99), result.Items[0].MessageSeq)
	require.Equal(t, []uint64{9, 10}, owner.epochs())
	require.Equal(t, []uint64{7}, resolver.invalidatedGenerations())
	require.Equal(t, 2, resolver.callCount())
}

func TestChannelCellRefreshesRouteAndRetriesWriteFencedLocalAppend(t *testing.T) {
	resolver := &sequenceResolver{routes: []ChannelRoute{
		localRoute("retry-write-fenced-local"),
		{ChannelID: channel.ChannelID{ID: "retry-write-fenced-local", Type: 1}, Leader: 1, RouteGeneration: 8, ChannelEpoch: 10, LeaderEpoch: 12, Status: channel.StatusActive},
	}}
	owner := &epochErrorOwner{
		errEpoch:     9,
		err:          channel.ErrWriteFenced,
		successEpoch: 10,
	}
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: resolver, LocalOwner: owner})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := p.AppendBatch(ctx, appendReq("retry-write-fenced-local", 1))

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, uint64(100), result.Items[0].MessageSeq)
	require.Equal(t, []uint64{9, 10}, owner.epochs())
	require.Equal(t, []uint64{7}, resolver.invalidatedGenerations())
	require.Equal(t, 2, resolver.callCount())
}

func TestChannelCellRefreshesRouteAndRetriesWriteFencedRemoteAppend(t *testing.T) {
	first := remoteRoute("retry-write-fenced-remote", 2)
	second := first
	second.RouteGeneration = 8
	second.ChannelEpoch = 10
	second.LeaderEpoch = 12
	resolver := &sequenceResolver{routes: []ChannelRoute{first, second}}
	peer := &sequencePlanePeerClient{results: []AppendBatchRemoteResult{
		{Status: RemoteAppendStatusWriteFenced},
		{Status: RemoteAppendStatusOK, Result: channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: 200}}}},
	}}
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: resolver, LocalOwner: noopOwner{}, PeerClient: peer, PeerBatchMaxWait: time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := p.AppendBatch(ctx, appendReq("retry-write-fenced-remote", 1))

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, uint64(200), result.Items[0].MessageSeq)
	require.Equal(t, []uint64{7}, resolver.invalidatedGenerations())
	require.Equal(t, 2, resolver.callCount())
	require.Equal(t, []uint64{9, 10}, peer.channelEpochs())
}

func TestChannelCellRefreshesExpiredCachedRouteBeforeAppend(t *testing.T) {
	now := time.UnixMilli(1_700_001_000_000).UTC()
	clockNow := now
	firstRoute := localRoute("refresh-expired")
	firstRoute.LeaseUntil = now.Add(time.Millisecond)
	freshRoute := firstRoute
	freshRoute.RouteGeneration = 8
	freshRoute.ChannelEpoch = 10
	freshRoute.LeaderEpoch = 12
	freshRoute.LeaseUntil = now.Add(time.Minute)
	resolver := &sequenceResolver{routes: []ChannelRoute{firstRoute, freshRoute}}
	owner := &recordingOwner{}
	p, err := New(Options{
		ReactorCount: 1,
		LocalNode:    1,
		Resolver:     resolver,
		LocalOwner:   owner,
		Now:          func() time.Time { return clockNow },
	})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = p.AppendBatch(ctx, appendReq("refresh-expired", 1))
	require.NoError(t, err)

	clockNow = now.Add(2 * time.Millisecond)
	_, err = p.AppendBatch(ctx, appendReq("refresh-expired", 2))

	require.NoError(t, err)
	require.Equal(t, []uint64{9, 10}, owner.epochs())
	require.Equal(t, []uint64{7}, resolver.invalidatedGenerations())
	require.Equal(t, 2, resolver.callCount())
}

func appendReq(id string, seq uint64) channel.AppendBatchRequest {
	return channel.AppendBatchRequest{
		ChannelID: channel.ChannelID{ID: id, Type: 1},
		Messages:  []channel.Message{{MessageID: seq, Payload: []byte("payload")}},
	}
}

func localRoute(id string) ChannelRoute {
	return ChannelRoute{ChannelID: channel.ChannelID{ID: id, Type: 1}, Leader: 1, RouteGeneration: 7, ChannelEpoch: 9, LeaderEpoch: 11, Status: channel.StatusActive}
}

func remoteRoute(id string, leader channel.NodeID) ChannelRoute {
	route := localRoute(id)
	route.Leader = leader
	return route
}

func stopPlane(t *testing.T, p *Plane) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, p.Stop(ctx))
}

type staticResolver struct{ route ChannelRoute }

func (s staticResolver) ResolveRoute(context.Context, channel.ChannelID) (ChannelRoute, error) {
	return s.route, nil
}
func (s staticResolver) InvalidateRoute(channel.ChannelID, uint64) {}

type sequenceResolver struct {
	mu            sync.Mutex
	routes        []ChannelRoute
	calls         int
	invalidations []uint64
}

func (s *sequenceResolver) ResolveRoute(context.Context, channel.ChannelID) (ChannelRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.routes) == 0 {
		return ChannelRoute{}, channel.ErrInvalidConfig
	}
	index := s.calls
	if index >= len(s.routes) {
		index = len(s.routes) - 1
	}
	s.calls++
	return s.routes[index], nil
}

func (s *sequenceResolver) InvalidateRoute(_ channel.ChannelID, routeGeneration uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidations = append(s.invalidations, routeGeneration)
}

func (s *sequenceResolver) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *sequenceResolver) invalidatedGenerations() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint64(nil), s.invalidations...)
}

type noopOwner struct{}

func (noopOwner) AppendLocalBatch(context.Context, channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: 1}}}, nil
}

type epochCheckingOwner struct {
	mu           sync.Mutex
	staleEpoch   uint64
	successEpoch uint64
	seenEpochs   []uint64
}

func (o *epochCheckingOwner) AppendLocalBatch(_ context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	o.mu.Lock()
	o.seenEpochs = append(o.seenEpochs, req.ExpectedChannelEpoch)
	o.mu.Unlock()
	switch req.ExpectedChannelEpoch {
	case o.staleEpoch:
		return channel.AppendBatchResult{}, channel.ErrStaleMeta
	case o.successEpoch:
		return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: 99}}}, nil
	default:
		return channel.AppendBatchResult{}, channel.ErrInvalidArgument
	}
}

func (o *epochCheckingOwner) epochs() []uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]uint64(nil), o.seenEpochs...)
}

type epochErrorOwner struct {
	mu           sync.Mutex
	errEpoch     uint64
	err          error
	successEpoch uint64
	seenEpochs   []uint64
}

func (o *epochErrorOwner) AppendLocalBatch(_ context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	o.mu.Lock()
	o.seenEpochs = append(o.seenEpochs, req.ExpectedChannelEpoch)
	o.mu.Unlock()
	switch req.ExpectedChannelEpoch {
	case o.errEpoch:
		return channel.AppendBatchResult{}, o.err
	case o.successEpoch:
		return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: 100}}}, nil
	default:
		return channel.AppendBatchResult{}, channel.ErrInvalidArgument
	}
}

func (o *epochErrorOwner) epochs() []uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]uint64(nil), o.seenEpochs...)
}

type recordingOwner struct {
	mu        sync.Mutex
	seenEpoch []uint64
}

func (o *recordingOwner) AppendLocalBatch(_ context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	o.mu.Lock()
	o.seenEpoch = append(o.seenEpoch, req.ExpectedChannelEpoch)
	seq := uint64(len(o.seenEpoch))
	o.mu.Unlock()
	return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: seq}}}, nil
}

func (o *recordingOwner) epochs() []uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]uint64(nil), o.seenEpoch...)
}

type blockingOwner struct {
	mu      sync.Mutex
	started int
	release chan struct{}
	onStart func(int)
}

func newBlockingOwner() *blockingOwner { return &blockingOwner{release: make(chan struct{}, 8)} }

func (o *blockingOwner) AppendLocalBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	o.mu.Lock()
	o.started++
	started := o.started
	onStart := o.onStart
	o.mu.Unlock()
	if onStart != nil {
		onStart(started)
	}
	select {
	case <-o.release:
		return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: req.Messages[0].MessageID}}}, nil
	case <-ctx.Done():
		return channel.AppendBatchResult{}, ctx.Err()
	}
}

func (o *blockingOwner) waitStarted(t *testing.T, want int) {
	t.Helper()
	require.Eventually(t, func() bool { return o.startedCount() >= want }, time.Second, 10*time.Millisecond)
}

func (o *blockingOwner) startedCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.started
}

func (o *blockingOwner) setOnStart(fn func(int)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onStart = fn
}

func (o *blockingOwner) releaseOne() { o.release <- struct{}{} }

type sequenceOwner struct {
	mu   sync.Mutex
	next uint64
}

func newSequenceOwner() *sequenceOwner { return &sequenceOwner{} }

func (o *sequenceOwner) AppendLocalBatch(context.Context, channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	o.mu.Lock()
	o.next++
	seq := o.next
	o.mu.Unlock()
	return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: seq}}}, nil
}

type countingRouteSource struct {
	mu     sync.Mutex
	count  int
	route  ChannelRoute
	wait   chan struct{}
	called chan struct{}
}

func (s *countingRouteSource) ResolveRoute(ctx context.Context, id channel.ChannelID) (ChannelRoute, error) {
	s.mu.Lock()
	s.count++
	if s.called == nil {
		s.called = make(chan struct{})
	}
	if s.count == 1 {
		close(s.called)
	}
	s.mu.Unlock()
	select {
	case <-s.wait:
		return s.route, nil
	case <-ctx.Done():
		return ChannelRoute{}, ctx.Err()
	}
}

func (s *countingRouteSource) waitCall(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		s.mu.Lock()
		called := s.called
		s.mu.Unlock()
		if called == nil {
			return false
		}
		select {
		case <-called:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func (s *countingRouteSource) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

type recordingPlanePeerClient struct {
	mu     sync.Mutex
	nodeID channel.NodeID
	req    AppendBatchesRequest
}

func (r *recordingPlanePeerClient) AppendBatches(_ context.Context, nodeID channel.NodeID, req AppendBatchesRequest) (AppendBatchesResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodeID = nodeID
	r.req = req
	return AppendBatchesResponse{Results: []AppendBatchRemoteResult{{Status: RemoteAppendStatusOK, Result: channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: 1}}}}}}, nil
}

type sequencePlanePeerClient struct {
	mu      sync.Mutex
	results []AppendBatchRemoteResult
	epochs  []uint64
}

func (p *sequencePlanePeerClient) AppendBatches(_ context.Context, _ channel.NodeID, req AppendBatchesRequest) (AppendBatchesResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(req.Batches) != 1 {
		return AppendBatchesResponse{}, channel.ErrInvalidArgument
	}
	p.epochs = append(p.epochs, req.Batches[0].RouteEpoch.ChannelEpoch)
	if len(p.results) == 0 {
		return AppendBatchesResponse{}, channel.ErrInvalidConfig
	}
	result := p.results[0]
	p.results = p.results[1:]
	return AppendBatchesResponse{Results: []AppendBatchRemoteResult{result}}, nil
}

func (p *sequencePlanePeerClient) channelEpochs() []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uint64(nil), p.epochs...)
}
