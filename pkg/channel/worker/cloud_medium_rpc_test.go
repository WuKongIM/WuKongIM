package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
)

func mixedRPCPullTask(opID ch.OpID, node ch.NodeID) Task {
	key := ch.ChannelKey("1:mixed-pull")
	return Task{
		Kind:  TaskRPCPull,
		Fence: ch.Fence{ChannelKey: key, OpID: opID},
		RPCPull: &RPCPullTask{
			Node:    node,
			Request: transport.PullRequest{ChannelKey: key, NextOffset: uint64(opID)},
		},
	}
}

func mixedRPCPullHintTask(opID ch.OpID, node ch.NodeID) Task {
	key := ch.ChannelKey("1:mixed-hint")
	return Task{
		Kind:  TaskRPCPullHint,
		Fence: ch.Fence{ChannelKey: key, OpID: opID},
		RPCPullHint: &RPCPullHintTask{
			Node: node,
			Request: transport.PullHintRequest{
				ChannelKey: key,
				LeaderLEO:  uint64(opID),
			},
		},
	}
}

func BenchmarkRPCBatchCloudMediumMixedReplicationEnvelope(b *testing.B) {
	const (
		taskCount   = 4000
		serviceTime = 5 * time.Millisecond
	)
	for _, workers := range []int{96, 128} {
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			b.ReportAllocs()
			totalCalls := 0
			observer := &rpcCapacityObserver{}
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				sink := &captureSink{}
				tr := &mixedBatchWorkerTransport{batchDelay: serviceTime}
				pool, err := NewPool(PoolConfig{
					Name:             "rpc",
					Workers:          workers,
					QueueSize:        taskCount,
					RPCBatchMaxItems: DefaultRPCBatchMaxItems,
					BatchMaxWait:     time.Millisecond,
				}, Deps{Transport: tr}, sink)
				if err != nil {
					b.Fatal(err)
				}
				pool.SetQueueObserver(observer)
				for i := 0; i < taskCount; i++ {
					opID := ch.OpID(i + 1)
					var task Task
					switch i % 4 {
					case 0:
						task = mixedRPCPullTask(opID, 2)
					case 1:
						task = mixedRPCPullTask(opID, 3)
					case 2:
						task = mixedRPCPullHintTask(opID, 2)
					default:
						task = mixedRPCPullHintTask(opID, 3)
					}
					if err := pool.Submit(context.Background(), task); err != nil {
						b.Fatal(err)
					}
				}
				deadline := time.Now().Add(3 * time.Second)
				for sink.Len() != taskCount && time.Now().Before(deadline) {
					time.Sleep(100 * time.Microsecond)
				}
				if sink.Len() != taskCount {
					b.Fatalf("completed %d/%d mixed RPC tasks", sink.Len(), taskCount)
				}
				totalCalls += tr.CallCount()
				if err := pool.Close(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(taskCount*b.N)/b.Elapsed().Seconds(), "items/s")
			b.ReportMetric(float64(totalCalls)/float64(b.N), "rpc-calls/op")
			b.ReportMetric(float64(observer.WaitP99())/float64(time.Microsecond), "queue-wait-p99-us")
		})
	}
}

type mixedBatchWorkerTransport struct {
	batchWorkerTransport
	batchDelay time.Duration
}

func (t *mixedBatchWorkerTransport) PullBatch(ctx context.Context, node ch.NodeID, req transport.PullBatchRequest) (transport.PullBatchResponse, error) {
	t.pullBatchDelay = t.batchDelay
	return t.batchWorkerTransport.PullBatch(ctx, node, req)
}

func (t *mixedBatchWorkerTransport) PullHintBatch(_ context.Context, _ ch.NodeID, req transport.PullHintBatchRequest) (transport.PullHintBatchResponse, error) {
	t.mu.Lock()
	t.pullHintBatchCalls++
	t.mu.Unlock()
	if t.batchDelay > 0 {
		time.Sleep(t.batchDelay)
	}
	return transport.PullHintBatchResponse{Items: make([]transport.PullHintBatchItemResult, len(req.Items))}, nil
}

func (t *mixedBatchWorkerTransport) CallCount() int {
	return t.PullCalls() + t.PullBatchCalls() + t.PullHintCalls() + t.PullHintBatchCalls()
}
