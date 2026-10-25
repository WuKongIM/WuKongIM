//go:build integration

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/stretchr/testify/require"
)

func TestChannelAppendRPCPhasesReachRealSingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	id := ch.ChannelID{ID: "trace-room", Type: 2}
	node := newSendackSmokeSingleNodeCluster(t, cfg.Cluster, id)
	a, err := New(cfg, WithCluster(node))
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	waitSingleNodeClusterRouteLeader(t, node, id.ID, cfg.NodeID)

	t.Run("shared first effect retains both original traces", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		authorizer := appendRPCPhaseAuthorizer(func(_ context.Context, cmd channelappend.SendCommand) (channelappend.Decision, error) {
			if cmd.ChannelID == "block-prepare" {
				close(entered)
				<-release
				return channelappend.Decision{Allowed: false, Reason: channelappend.ReasonSystemError}, nil
			}
			return channelappend.Decision{Allowed: true, Reason: channelappend.ReasonSuccess}, nil
		})
		port := &appendRPCPhaseNode{node: node, calls: make(chan int, 2)}
		group := newAppendRPCPhaseClusterGroup(t, port, authorizer, 100)
		t.Cleanup(unblock)
		blockTarget, blockItems := channelAppendRPCTraceBatch()
		blockTarget.ChannelID.ID, blockTarget.ChannelKey = "block-prepare", "2:block-prepare"
		blockItems = blockItems[:1]
		blockItems[0].Command.ChannelID, blockItems[0].Command.ChannelKey = "block-prepare", "2:block-prepare"
		blocker, err := group.SubmitLocal(ctx, blockTarget, blockItems)
		require.NoError(t, err)
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		var futures []*channelappend.Future
		var traces []*rpctrace.Trace
		for _, sender := range []string{"alice", "bob"} {
			_, trace, caller := newChannelAppendRPCServerTrace(t, ctx)
			target, items := channelAppendRPCTraceBatch()
			for i := range items {
				items[i].Command.FromUID = sender
			}
			future, err := group.SubmitLocal(caller, target, items)
			require.NoError(t, err)
			futures, traces = append(futures, future), append(traces, trace)
		}
		unblock()
		_, err = blocker.Wait(ctx)
		require.NoError(t, err)
		for i, future := range futures {
			results, err := future.Wait(ctx)
			require.NoError(t, err)
			require.Len(t, results, 2)
			for _, result := range results {
				require.NoError(t, result.Err)
				require.Equal(t, channelappend.ReasonSuccess, result.Result.Reason)
				require.Positive(t, result.Result.MessageSeq)
			}
			requireAppendRPCPhaseNesting(t, traces[i].Finish(rpctrace.ResultOK))
		}
		select {
		case count := <-port.calls:
			require.Equal(t, 4, count, "fixture must execute one physical first effect for both Futures")
		default:
			t.Fatal("the real Node append was not reached")
		}
		require.Empty(t, port.calls, "the fixture must not silently split the shared effect")
	})

	t.Run("finished original sample stays frozen while durable work continues", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		port := &appendRPCPhaseNode{node: node, calls: make(chan int, 1), entered: entered, release: release, completed: make(chan error, 1), results: make(chan ch.AppendBatchResult, 1)}
		group := newAppendRPCPhaseClusterGroup(t, port, nil, 200)
		t.Cleanup(unblock)
		caller, cancelCaller := context.WithCancel(ctx)
		defer cancelCaller()
		_, trace, caller := newChannelAppendRPCServerTrace(t, caller)
		target, items := channelAppendRPCTraceBatch()
		for i := range items {
			items[i].Command.ClientMsgNo += "-canceled"
		}
		results := make(chan []channelappend.SendBatchItemResult, 1)
		go func() {
			results <- (channelAppendAuthorityLocal{group: group}).SubmitForAuthority(caller, target, items)
		}()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		cancelCaller()
		for _, result := range awaitChannelAppendRPCResults(t, results) {
			require.ErrorIs(t, result.Err, context.Canceled)
		}
		sample := trace.Finish(rpctrace.ResultCanceled)
		require.NotNil(t, sample)
		frozen := sample.Clone()
		unblock()
		require.NoError(t, group.WaitIdle(ctx))
		require.Equal(t, frozen, *sample)
		require.Nil(t, trace.Finish(rpctrace.ResultOK))
		require.Equal(t, 2, <-port.calls, "accepted work must reach the real Node after caller cancellation")
		require.NoError(t, <-port.completed, "accepted append must durably finish, not merely reach the backend")
		committed := <-port.results
		require.Len(t, committed.Items, 2)
		for _, item := range committed.Items {
			require.NoError(t, item.Err)
			require.Positive(t, item.MessageSeq, "nil batch error alone does not prove each item committed")
		}
	})
}

type appendRPCPhaseAuthorizer func(context.Context, channelappend.SendCommand) (channelappend.Decision, error)

func (f appendRPCPhaseAuthorizer) AuthorizeSend(ctx context.Context, cmd channelappend.SendCommand) (channelappend.Decision, error) {
	return f(ctx, cmd)
}

// appendRPCPhaseNode gates only the test's accepted invocation, then delegates
// to the real Node and its real Channel runtime. It does not manufacture spans.
type appendRPCPhaseNode struct {
	node      *cluster.Node
	calls     chan int
	entered   chan struct{}
	release   chan struct{}
	completed chan error
	results   chan ch.AppendBatchResult
}

func (p *appendRPCPhaseNode) AppendChannelBatch(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	if rpctrace.From(ctx) != nil {
		return ch.AppendBatchResult{}, channelappend.ErrAppendFailed
	}
	if p.entered != nil {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return ch.AppendBatchResult{}, ctx.Err()
		}
	}
	result, err := p.node.AppendChannelBatch(ctx, req)
	p.calls <- len(req.Messages)
	if p.completed != nil {
		p.completed <- err
	}
	if p.results != nil {
		p.results <- result
	}
	return result, err
}

func newAppendRPCPhaseClusterGroup(t *testing.T, node *appendRPCPhaseNode, authorizer channelappend.Authorizer, firstID uint64) *channelappend.Group {
	t.Helper()
	group := channelappend.New(channelappend.Options{
		LocalNodeID: 1, Appender: clusterinfra.NewChannelAppender(node), Authorizer: authorizer,
		MessageID: &lifecycleMessageIDAllocator{next: firstID}, AuthorityShardCount: 1,
		AdvancePoolSize: 1, EffectPoolSize: 1, AdmissionCapacityPerShard: 16,
		ChannelBacklogHighWatermark: 16, PostCommitHandoffCapacity: 16,
		InboxCoalesceWindow: -1, InboxCoalesceMaxItems: -1,
	})
	require.NoError(t, group.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, group.Stop(ctx))
	})
	return group
}

func requireAppendRPCPhaseNesting(t *testing.T, sample *rpctrace.Sample) {
	t.Helper()
	require.NotNil(t, sample)
	first := requireChannelAppendRPCSpan(t, sample, "channel_first_append", rpctrace.ResultOK)
	runtime := requireChannelAppendRPCSpan(t, sample, "channel_runtime_append", rpctrace.ResultOK)
	for _, stage := range []rpctrace.Stage{"channel_meta_resolve", "channel_meta_apply", "channel_runtime_append", "channel_runtime_reserve", "channel_runtime_submit", "channel_runtime_future_wait"} {
		span := requireChannelAppendRPCSpan(t, sample, stage, rpctrace.ResultOK)
		require.GreaterOrEqual(t, span.Offset, first.Offset)
		require.LessOrEqual(t, span.Offset+span.Duration, first.Offset+first.Duration)
		if stage == "channel_runtime_reserve" || stage == "channel_runtime_submit" || stage == "channel_runtime_future_wait" {
			require.GreaterOrEqual(t, span.Offset, runtime.Offset)
			require.LessOrEqual(t, span.Offset+span.Duration, runtime.Offset+runtime.Duration)
		}
	}
	require.Zero(t, sample.DroppedSpans)
}
