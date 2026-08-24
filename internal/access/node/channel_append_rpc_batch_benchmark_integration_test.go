//go:build integration

package node

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const (
	channelAppendRPCBenchmarkRate    = 1000
	channelAppendRPCBenchmarkWorkers = 256
	channelAppendRPCBenchmarkNodeID  = 2
	channelAppendRPCBenchmarkBatchID = 249
)

// BenchmarkChannelAppendRPCBatchingPaced1000QPS compares the current
// one-channel-per-RPC path with an optimistic cross-call envelope. Both paths
// use the production TCP transport and replay the retained three-node
// authority-handler latency distribution. The burst shape is deliberately
// favorable to batching so that response head-of-line amplification remains
// visible even when the RPC reduction is near its practical upper bound.
func BenchmarkChannelAppendRPCBatchingPaced1000QPS(b *testing.B) {
	if b.N < channelAppendRPCBenchmarkRate {
		return
	}
	for _, shape := range []struct {
		name      string
		burstSize int
	}{
		{name: "uniform", burstSize: 1},
		{name: "burst_10", burstSize: 10},
	} {
		b.Run(shape.name+"/singular", func(b *testing.B) {
			benchmarkChannelAppendRPCShape(b, shape.burstSize, 0, 1)
		})
		b.Run(shape.name+"/batch_8_wait_1ms", func(b *testing.B) {
			benchmarkChannelAppendRPCShape(b, shape.burstSize, time.Millisecond, 8)
		})
	}
}

func benchmarkChannelAppendRPCShape(b *testing.B, burstSize int, batchWait time.Duration, batchMax int) {
	authority := &channelAppendRPCBenchmarkAuthority{}
	adapter := NewChannelAppendAdapter(ChannelAppendOptions{ChannelAppend: authority})
	server := clusternet.NewTransportServer(clusternet.TransportServerConfig{NodeID: channelAppendRPCBenchmarkNodeID})
	if batchWait <= 0 {
		server.Register(ChannelAppendRPCServiceID, clusternet.HandlerFunc(adapter.HandleChannelAppendRPC))
	} else {
		server.Register(channelAppendRPCBenchmarkBatchID, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			requests, err := decodeChannelAppendRPCBenchmarkEnvelope(payload)
			if err != nil {
				return nil, err
			}
			responses := make([][]byte, len(requests))
			errs := make([]error, len(requests))
			var wg sync.WaitGroup
			wg.Add(len(requests))
			for index := range requests {
				index := index
				go func() {
					defer wg.Done()
					responses[index], errs[index] = adapter.HandleChannelAppendRPC(ctx, requests[index])
				}()
			}
			wg.Wait()
			for _, requestErr := range errs {
				if requestErr != nil {
					return nil, requestErr
				}
			}
			return encodeChannelAppendRPCBenchmarkEnvelope(responses), nil
		}))
	}
	if err := server.Start("127.0.0.1:0"); err != nil {
		b.Fatalf("transport server Start(): %v", err)
	}
	defer server.Stop()

	discovery := clusternet.NewDiscovery()
	discovery.Update([]clusternet.NodeAddress{{NodeID: channelAppendRPCBenchmarkNodeID, Addr: server.Addr()}})
	transportClient := clusternet.NewTransportClient(clusternet.TransportClientConfig{
		Discovery: discovery,
		NodeID:    1,
		PoolSize:  16,
	})
	defer transportClient.Stop()

	var submit func(context.Context, channelappend.AuthorityTarget, channelappend.SendBatchItem) channelappend.SendBatchItemResult
	var batcher *channelAppendRPCBenchmarkBatcher
	if batchWait <= 0 {
		client := NewClient(channelAppendRPCBenchmarkNode{client: transportClient})
		submit = func(ctx context.Context, target channelappend.AuthorityTarget, item channelappend.SendBatchItem) channelappend.SendBatchItemResult {
			results := client.ForwardSendBatch(ctx, target, []channelappend.SendBatchItem{item})
			if len(results) != 1 {
				return channelappend.SendBatchItemResult{Err: channelappend.ErrAppendResultMissing}
			}
			return results[0]
		}
	} else {
		batcher = newChannelAppendRPCBenchmarkBatcher(transportClient, batchWait, batchMax)
		defer batcher.Stop()
		submit = batcher.Submit
	}

	latencies := make([]time.Duration, b.N)
	jobs := make(chan int, channelAppendRPCBenchmarkWorkers)
	var workers sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	workers.Add(channelAppendRPCBenchmarkWorkers)
	for range channelAppendRPCBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				target, item := channelAppendRPCBenchmarkInput(index)
				started := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				result := submit(ctx, target, item)
				cancel()
				latencies[index] = time.Since(started)
				if result.Err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("submit %d: %w", index, result.Err)
					}
					errMu.Unlock()
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for index := range b.N {
		channelAppendRPCBenchmarkWaitUntil(channelAppendRPCBenchmarkDueAt(started, index, burstSize))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()
	if firstErr != nil {
		b.Fatal(firstErr)
	}

	rpcCalls := uint64(b.N)
	if batcher != nil {
		rpcCalls = batcher.rpcCalls.Load()
	}
	reportChannelAppendRPCBenchmark(b, latencies, rpcCalls)
}

type channelAppendRPCBenchmarkNode struct {
	client *clusternet.TransportClient
}

func (n channelAppendRPCBenchmarkNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	return n.client.Call(ctx, nodeID, serviceID, payload)
}

type channelAppendRPCBenchmarkAuthority struct{}

func (a *channelAppendRPCBenchmarkAuthority) SubmitForAuthority(_ context.Context, _ channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	results := make([]channelappend.SendBatchItemResult, len(items))
	for index, item := range items {
		time.Sleep(channelAppendRPCBenchmarkAuthorityLatency(int(item.Command.ClientSeq) - 1))
		results[index].Result = channelappend.SendResult{
			MessageID:  item.Command.ClientSeq,
			MessageSeq: item.Command.ClientSeq,
			Reason:     channelappend.ReasonSuccess,
		}
	}
	return results
}

type channelAppendRPCBenchmarkRequest struct {
	ctx      context.Context
	target   channelappend.AuthorityTarget
	item     channelappend.SendBatchItem
	response chan channelappend.SendBatchItemResult
}

type channelAppendRPCBenchmarkBatcher struct {
	client   *clusternet.TransportClient
	wait     time.Duration
	max      int
	requests chan channelAppendRPCBenchmarkRequest
	stop     chan struct{}
	done     chan struct{}
	flights  sync.WaitGroup
	rpcCalls atomic.Uint64
}

func newChannelAppendRPCBenchmarkBatcher(client *clusternet.TransportClient, wait time.Duration, max int) *channelAppendRPCBenchmarkBatcher {
	b := &channelAppendRPCBenchmarkBatcher{
		client:   client,
		wait:     wait,
		max:      max,
		requests: make(chan channelAppendRPCBenchmarkRequest, 4096),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go b.run()
	return b
}

func (b *channelAppendRPCBenchmarkBatcher) Submit(ctx context.Context, target channelappend.AuthorityTarget, item channelappend.SendBatchItem) channelappend.SendBatchItemResult {
	req := channelAppendRPCBenchmarkRequest{
		ctx:      ctx,
		target:   target,
		item:     item,
		response: make(chan channelappend.SendBatchItemResult, 1),
	}
	select {
	case b.requests <- req:
	case <-ctx.Done():
		return channelappend.SendBatchItemResult{Err: ctx.Err()}
	case <-b.stop:
		return channelappend.SendBatchItemResult{Err: errors.New("channel append RPC benchmark batcher stopped")}
	}
	select {
	case result := <-req.response:
		return result
	case <-ctx.Done():
		return channelappend.SendBatchItemResult{Err: ctx.Err()}
	}
}

func (b *channelAppendRPCBenchmarkBatcher) Stop() {
	select {
	case <-b.stop:
		return
	default:
		close(b.stop)
	}
	<-b.done
	b.flights.Wait()
}

func (b *channelAppendRPCBenchmarkBatcher) run() {
	defer close(b.done)
	for {
		var first channelAppendRPCBenchmarkRequest
		select {
		case first = <-b.requests:
		case <-b.stop:
			return
		}
		batch := []channelAppendRPCBenchmarkRequest{first}
		timer := time.NewTimer(b.wait)
	collect:
		for len(batch) < b.max {
			select {
			case request := <-b.requests:
				batch = append(batch, request)
			case <-timer.C:
				break collect
			case <-b.stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				b.dispatch(batch)
				return
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		b.dispatch(batch)
	}
}

func (b *channelAppendRPCBenchmarkBatcher) dispatch(batch []channelAppendRPCBenchmarkRequest) {
	b.flights.Add(1)
	go func() {
		defer b.flights.Done()
		requests := make([][]byte, len(batch))
		for index, request := range batch {
			body, err := encodeChannelAppendRequest(channelAppendRequest{
				Target: request.target,
				Items: []channelAppendItem{{
					Command: request.item.Command.Clone(),
					Timeout: channelAppendRelativeTimeout(request.item, time.Now()),
				}},
			})
			if err != nil {
				request.response <- channelappend.SendBatchItemResult{Err: err}
				return
			}
			requests[index] = body
		}
		b.rpcCalls.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		responseBody, err := b.client.Call(ctx, channelAppendRPCBenchmarkNodeID, channelAppendRPCBenchmarkBatchID, encodeChannelAppendRPCBenchmarkEnvelope(requests))
		cancel()
		if err != nil {
			for _, request := range batch {
				request.response <- channelappend.SendBatchItemResult{Err: err}
			}
			return
		}
		responses, err := decodeChannelAppendRPCBenchmarkEnvelope(responseBody)
		if err != nil || len(responses) != len(batch) {
			if err == nil {
				err = channelappend.ErrAppendResultMissing
			}
			for _, request := range batch {
				request.response <- channelappend.SendBatchItemResult{Err: err}
			}
			return
		}
		for index, responseBody := range responses {
			response, decodeErr := decodeChannelAppendResponse(responseBody)
			if decodeErr != nil || response.Status != rpcStatusOK || len(response.Results) != 1 {
				if decodeErr == nil {
					decodeErr = channelappend.ErrAppendResultMissing
				}
				batch[index].response <- channelappend.SendBatchItemResult{Err: decodeErr}
				continue
			}
			batch[index].response <- response.Results[0]
		}
	}()
}

func encodeChannelAppendRPCBenchmarkEnvelope(messages [][]byte) []byte {
	dst := appendUvarint(nil, uint64(len(messages)))
	for _, message := range messages {
		dst = appendUvarint(dst, uint64(len(message)))
		dst = append(dst, message...)
	}
	return dst
}

func decodeChannelAppendRPCBenchmarkEnvelope(body []byte) ([][]byte, error) {
	count, offset, err := readUvarint(body, 0)
	if err != nil || count == 0 || count > 64 {
		return nil, errors.New("invalid channel append RPC benchmark envelope count")
	}
	messages := make([][]byte, 0, count)
	for range count {
		length, next, readErr := readUvarint(body, offset)
		if readErr != nil || length > uint64(len(body)-next) {
			return nil, errors.New("invalid channel append RPC benchmark envelope item")
		}
		offset = next
		messages = append(messages, body[offset:offset+int(length)])
		offset += int(length)
	}
	if offset != len(body) {
		return nil, errors.New("trailing channel append RPC benchmark envelope bytes")
	}
	return messages, nil
}

func channelAppendRPCBenchmarkInput(index int) (channelappend.AuthorityTarget, channelappend.SendBatchItem) {
	target := channelAppendTestTarget()
	target.LeaderNodeID = channelAppendRPCBenchmarkNodeID
	target.ChannelID.ID = fmt.Sprintf("rpc-benchmark-%04d", index%1000)
	target.ChannelKey = target.ChannelID.ID
	command := channelAppendTestCommand()
	command.ClientSeq = uint64(index + 1)
	command.ClientMsgNo = fmt.Sprintf("rpc-benchmark-%d", index+1)
	command.ChannelID = target.ChannelID.ID
	return target, channelappend.SendBatchItem{Context: context.Background(), Command: command}
}

func channelAppendRPCBenchmarkAuthorityLatency(index int) time.Duration {
	// Cumulative counts are the measured before-to-sample-1 delta from all
	// three nodes: 39,592 authority calls, mean 76.94ms, and 0.993% >200ms.
	rank := (uint64(index+1) * 11400714819323198485) % 39592
	switch {
	case rank < 1009:
		return 17500 * time.Microsecond
	case rank < 9491:
		return 37500 * time.Microsecond
	case rank < 30468:
		return 75 * time.Millisecond
	case rank < 37661:
		return 125 * time.Millisecond
	case rank < 39199:
		return 175 * time.Millisecond
	case rank < 39562:
		return 225 * time.Millisecond
	default:
		return 375 * time.Millisecond
	}
}

func channelAppendRPCBenchmarkDueAt(started time.Time, index int, burstSize int) time.Time {
	burst := index / burstSize
	return started.Add(time.Duration(burst*burstSize) * time.Second / channelAppendRPCBenchmarkRate)
}

func channelAppendRPCBenchmarkWaitUntil(deadline time.Time) {
	if remaining := time.Until(deadline); remaining > 0 {
		timer := time.NewTimer(remaining)
		<-timer.C
	}
}

func reportChannelAppendRPCBenchmark(b *testing.B, latencies []time.Duration, rpcCalls uint64) {
	b.Helper()
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[(len(sorted)*99-1)/100]
	aboveBudget := 0
	for _, latency := range latencies {
		if latency > 200*time.Millisecond {
			aboveBudget++
		}
	}
	ratio := 100 * float64(aboveBudget) / float64(len(latencies))
	itemsPerRPC := float64(len(latencies)) / float64(rpcCalls)
	b.Logf("channel append RPC: p99=%s over_200ms=%.3f%% calls=%d items/rpc=%.3f", p99, ratio, rpcCalls, itemsPerRPC)
	b.ReportMetric(float64(p99)/float64(time.Millisecond), "logical-p99-ms")
	b.ReportMetric(ratio, "logical-over-200ms-pct")
	b.ReportMetric(itemsPerRPC, "items/rpc")
}

var _ ChannelAppend = (*channelAppendRPCBenchmarkAuthority)(nil)
