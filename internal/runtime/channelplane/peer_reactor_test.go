package channelplane

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
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

func stopPeerReactor(t *testing.T, peer *PeerReactor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, peer.Stop(ctx))
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
