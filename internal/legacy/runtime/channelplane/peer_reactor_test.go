package channelplane

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestPeerReactorBatchesMultipleChannelsForSameNode(t *testing.T) {
	client := newRecordingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{
		Client:          client,
		LaneCount:       1,
		MaxBatchWait:    20 * time.Millisecond,
		MaxBatchRecords: 8,
		MaxPending:      8,
	})
	require.NoError(t, peer.Start())
	defer stopPeerReactor(t, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, id := range []string{"g1", "g2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := peer.AppendRemoteBatch(ctx, 2, appendReq(id, 1), remoteRoute(id, 2))
			require.NoError(t, err)
		}(id)
	}
	call := client.waitCall(t)
	require.Equal(t, channel.NodeID(2), call.nodeID)
	require.Len(t, call.req.Batches, 2)
	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{
		{Status: RemoteAppendStatusOK, Result: batchResult(10)},
		{Status: RemoteAppendStatusOK, Result: batchResult(20)},
	}})
	wg.Wait()
}

func TestPeerReactorSplitsResultsBackToOwningReactors(t *testing.T) {
	client := newRecordingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{
		Client:          client,
		LaneCount:       1,
		MaxBatchWait:    20 * time.Millisecond,
		MaxBatchRecords: 8,
		MaxPending:      8,
	})
	require.NoError(t, peer.Start())
	defer stopPeerReactor(t, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first := make(chan channel.AppendBatchResult, 1)
	second := make(chan channel.AppendBatchResult, 1)
	go func() {
		res, err := peer.AppendRemoteBatch(ctx, 2, appendReq("g1", 1), remoteRoute("g1", 2))
		require.NoError(t, err)
		first <- res
	}()
	require.Eventually(t, func() bool { return peer.pendingForTest(2, 0) == 1 }, time.Second, 10*time.Millisecond)
	go func() {
		res, err := peer.AppendRemoteBatch(ctx, 2, appendReq("g2", 1), remoteRoute("g2", 2))
		require.NoError(t, err)
		second <- res
	}()
	require.Eventually(t, func() bool { return peer.pendingForTest(2, 0) == 2 }, time.Second, 10*time.Millisecond)
	client.waitCall(t)
	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{
		{Status: RemoteAppendStatusOK, Result: batchResult(101)},
		{Status: RemoteAppendStatusOK, Result: batchResult(202)},
	}})

	require.Equal(t, uint64(101), (<-first).Items[0].MessageSeq)
	require.Equal(t, uint64(202), (<-second).Items[0].MessageSeq)
}

func TestPeerReactorReturnsTypedBackpressure(t *testing.T) {
	client := newRecordingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{
		Client:          client,
		LaneCount:       1,
		MaxBatchWait:    time.Minute,
		MaxBatchRecords: 1,
		MaxPending:      1,
	})
	require.NoError(t, peer.Start())
	defer stopPeerReactor(t, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := peer.AppendRemoteBatch(ctx, 2, appendReq("g1", 1), remoteRoute("g1", 2))
		firstDone <- err
	}()
	require.Eventually(t, func() bool { return peer.pendingForTest(2, 0) == 1 }, time.Second, 10*time.Millisecond)

	_, err := peer.AppendRemoteBatch(ctx, 2, appendReq("g2", 1), remoteRoute("g2", 2))
	require.ErrorIs(t, err, ErrPeerBackpressured)

	call := client.waitCall(t)
	require.Len(t, call.req.Batches, 1)
	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{Status: RemoteAppendStatusOK, Result: batchResult(1)}}})
	require.NoError(t, <-firstDone)
}

func TestPeerReactorReleasesCanceledQueuedTaskWhileRPCBlocked(t *testing.T) {
	client := newBlockingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{
		Client:          client,
		LaneCount:       1,
		MaxBatchWait:    time.Millisecond,
		MaxBatchRecords: 1,
		MaxPending:      4,
	})
	require.NoError(t, peer.Start())
	defer stopPeerReactor(t, peer)

	firstCtx, firstCancel := context.WithTimeout(context.Background(), time.Second)
	defer firstCancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := peer.AppendRemoteBatch(firstCtx, 2, appendReq("blocked-rpc", 1), remoteRoute("blocked-rpc", 2))
		firstDone <- err
	}()
	client.waitCall(t)
	require.Eventually(t, func() bool { return peer.pendingForTest(2, 0) == 1 }, time.Second, 10*time.Millisecond)

	secondCtx, secondCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer secondCancel()
	_, err := peer.AppendRemoteBatch(secondCtx, 2, appendReq("queued-cancel", 1), remoteRoute("queued-cancel", 2))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Eventually(t, func() bool { return peer.pendingForTest(2, 0) == 1 }, time.Second, 10*time.Millisecond)

	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{Status: RemoteAppendStatusOK, Result: batchResult(1)}}})
	require.NoError(t, <-firstDone)
}

func TestPeerReactorBoundsBlockedRPCWithTimeout(t *testing.T) {
	client := newBlockingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{
		Client:          client,
		LaneCount:       1,
		MaxBatchWait:    time.Millisecond,
		MaxBatchRecords: 1,
		MaxPending:      4,
		RPCTimeout:      20 * time.Millisecond,
	})
	require.NoError(t, peer.Start())
	defer stopPeerReactor(t, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := peer.AppendRemoteBatch(ctx, 2, appendReq("rpc-timeout", 1), remoteRoute("rpc-timeout", 2))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Eventually(t, func() bool { return peer.pendingForTest(2, 0) == 0 }, time.Second, 10*time.Millisecond)
}

func TestPeerReactorStopRejectsLaneSubmitWithoutEnqueue(t *testing.T) {
	client := newRecordingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{Client: client, LaneCount: 1, MaxBatchWait: time.Minute, MaxPending: 8})
	require.NoError(t, peer.Start())
	lane, err := peer.lane(2, channel.ChannelID{ID: "stop-submit", Type: 1})
	require.NoError(t, err)
	lane.stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 2000; i++ {
		task := &peerAppendTask{ctx: ctx, envelope: AppendBatchEnvelope{Request: appendReq("stop-submit", uint64(i+1))}, done: make(chan peerAppendResult, 1)}
		require.ErrorIs(t, lane.submit(ctx, task), ErrClosed)
		require.Empty(t, lane.inbox)
	}
}

func TestPeerReactorDoesNotStartUnboundedRPCFlushes(t *testing.T) {
	client := newBlockingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{
		Client:          client,
		LaneCount:       1,
		MaxBatchWait:    time.Millisecond,
		MaxBatchRecords: 1,
		MaxPending:      8,
		MaxInflightRPC:  1,
	})
	require.NoError(t, peer.Start())
	defer stopPeerReactor(t, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := peer.AppendRemoteBatch(ctx, 2, appendReq("rpc-bound-1", 1), remoteRoute("rpc-bound-1", 2))
		firstDone <- err
	}()
	client.waitCall(t)

	secondDone := make(chan error, 1)
	go func() {
		_, err := peer.AppendRemoteBatch(ctx, 2, appendReq("rpc-bound-2", 1), remoteRoute("rpc-bound-2", 2))
		secondDone <- err
	}()
	require.Never(t, func() bool { return client.callCount() > 1 }, 30*time.Millisecond, 5*time.Millisecond)

	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{Status: RemoteAppendStatusOK, Result: batchResult(1)}}})
	require.NoError(t, <-firstDone)
	client.waitCall(t)
	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{Status: RemoteAppendStatusOK, Result: batchResult(2)}}})
	require.NoError(t, <-secondDone)
}

func TestPeerReactorBoundsQueuedRPCBatchesWhenWorkerBlocked(t *testing.T) {
	client := newBlockingPeerClient()
	peer := NewPeerReactor(PeerReactorOptions{
		Client:          client,
		LaneCount:       1,
		MaxBatchWait:    time.Millisecond,
		MaxBatchRecords: 1,
		MaxPending:      3,
		MaxInflightRPC:  1,
	})
	require.NoError(t, peer.Start())
	defer stopPeerReactor(t, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done1 := startPeerAppendForTest(peer, ctx, "rpc-worker-1")
	client.waitCall(t)

	done2 := startPeerAppendForTest(peer, ctx, "rpc-worker-2")
	require.Never(t, func() bool { return client.callCount() > 1 }, 30*time.Millisecond, 5*time.Millisecond)

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	_, err := peer.AppendRemoteBatch(shortCtx, 2, appendReq("rpc-worker-3", 1), remoteRoute("rpc-worker-3", 2))
	require.Error(t, err)

	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{
		Status: RemoteAppendStatusOK,
		Result: batchResult(1),
	}}})
	require.NoError(t, <-done1)
	client.waitCall(t)
	client.completeNext(AppendBatchesResponse{Results: []AppendBatchRemoteResult{{
		Status: RemoteAppendStatusOK,
		Result: batchResult(2),
	}}})
	require.NoError(t, <-done2)
}

func stopPeerReactor(t *testing.T, peer *PeerReactor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, peer.Stop(ctx))
}

func startPeerAppendForTest(peer *PeerReactor, ctx context.Context, id string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := peer.AppendRemoteBatch(ctx, 2, appendReq(id, 1), remoteRoute(id, 2))
		done <- err
	}()
	return done
}

func batchResult(seq uint64) channel.AppendBatchResult {
	return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{MessageSeq: seq}}}
}

type recordingPeerClient struct {
	mu    sync.Mutex
	calls []recordingPeerCall
	wait  chan struct{}
}

type recordingPeerCall struct {
	nodeID channel.NodeID
	req    AppendBatchesRequest
	resp   chan recordingPeerResponse
}

type recordingPeerResponse struct {
	resp AppendBatchesResponse
	err  error
}

func newRecordingPeerClient() *recordingPeerClient {
	return &recordingPeerClient{wait: make(chan struct{}, 8)}
}

func (c *recordingPeerClient) AppendBatches(_ context.Context, nodeID channel.NodeID, req AppendBatchesRequest) (AppendBatchesResponse, error) {
	call := recordingPeerCall{nodeID: nodeID, req: req, resp: make(chan recordingPeerResponse, 1)}
	c.mu.Lock()
	c.calls = append(c.calls, call)
	c.mu.Unlock()
	c.wait <- struct{}{}
	reply := <-call.resp
	return reply.resp, reply.err
}

func (c *recordingPeerClient) waitCall(t *testing.T) recordingPeerCall {
	t.Helper()
	select {
	case <-c.wait:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for peer call")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[len(c.calls)-1]
}

func (c *recordingPeerClient) completeNext(resp AppendBatchesResponse) {
	c.mu.Lock()
	call := c.calls[0]
	c.calls = c.calls[1:]
	c.mu.Unlock()
	call.resp <- recordingPeerResponse{resp: resp}
}

type blockingPeerClient struct {
	mu    sync.Mutex
	calls []recordingPeerCall
	wait  chan struct{}
}

func newBlockingPeerClient() *blockingPeerClient {
	return &blockingPeerClient{wait: make(chan struct{}, 8)}
}

func (c *blockingPeerClient) AppendBatches(ctx context.Context, nodeID channel.NodeID, req AppendBatchesRequest) (AppendBatchesResponse, error) {
	call := recordingPeerCall{nodeID: nodeID, req: req, resp: make(chan recordingPeerResponse, 1)}
	c.mu.Lock()
	c.calls = append(c.calls, call)
	c.mu.Unlock()
	c.wait <- struct{}{}
	select {
	case reply := <-call.resp:
		return reply.resp, reply.err
	case <-ctx.Done():
		return AppendBatchesResponse{}, ctx.Err()
	}
}

func (c *blockingPeerClient) waitCall(t *testing.T) recordingPeerCall {
	t.Helper()
	select {
	case <-c.wait:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for peer call")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[len(c.calls)-1]
}

func (c *blockingPeerClient) completeNext(resp AppendBatchesResponse) {
	c.mu.Lock()
	call := c.calls[0]
	c.calls = c.calls[1:]
	c.mu.Unlock()
	call.resp <- recordingPeerResponse{resp: resp}
}

func (c *blockingPeerClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}
