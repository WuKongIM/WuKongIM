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

func TestChannelPlaneUsesCompatibilityRemoteAppenderForRemoteLeader(t *testing.T) {
	remote := &recordingRemoteAppender{}
	p, err := New(Options{ReactorCount: 1, LocalNode: 1, Resolver: staticResolver{route: remoteRoute("remote", 2)}, LocalOwner: noopOwner{}, RemoteAppender: remote})
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer stopPlane(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = p.AppendBatch(ctx, appendReq("remote", 1))
	require.NoError(t, err)

	require.Equal(t, channel.NodeID(2), remote.nodeID)
	require.Equal(t, uint64(9), remote.req.ExpectedChannelEpoch)
	require.Equal(t, uint64(11), remote.req.ExpectedLeaderEpoch)
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

type noopOwner struct{}

func (noopOwner) AppendLocalBatch(context.Context, channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: 1}}}, nil
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

type recordingRemoteAppender struct {
	mu     sync.Mutex
	nodeID channel.NodeID
	req    channel.AppendBatchRequest
}

func (r *recordingRemoteAppender) AppendRemoteBatch(_ context.Context, nodeID channel.NodeID, req channel.AppendBatchRequest, _ ChannelRoute) (channel.AppendBatchResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodeID = nodeID
	r.req = req
	return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: 1}}}, nil
}
