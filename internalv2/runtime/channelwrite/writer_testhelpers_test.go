package channelwrite

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// orderedAppender records the message seqs it appends, in call order.
type orderedAppender struct {
	mu    sync.Mutex
	calls []uint64
	next  uint64
}

func (a *orderedAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := AppendBatchResult{Items: make([]AppendBatchItemResult, len(req.Messages))}
	for i, msg := range req.Messages {
		a.next++
		a.calls = append(a.calls, a.next)
		out.Items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: a.next, Message: msg}
	}
	return out, nil
}

func (a *orderedAppender) seqs() []uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]uint64(nil), a.calls...)
}

func (a *orderedAppender) seqsMonotonic() bool {
	s := a.seqs()
	return sort.SliceIsSorted(s, func(i, j int) bool { return s[i] < s[j] })
}

// routingAppender dispatches AppendBatch to a per-channel orderedAppender.
type routingAppender struct {
	byChannel map[string]*orderedAppender
}

func (r *routingAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a := r.byChannel[req.ChannelID.ID]
	if a == nil {
		return AppendBatchResult{}, fmt.Errorf("no appender for channel %q", req.ChannelID.ID)
	}
	return a.AppendBatch(ctx, req)
}

type writerRuntimeConfig struct {
	appender Appender
}

// writerRuntime wraps a single writer + pool for advance-level tests, bypassing Group.
type writerRuntime struct {
	t      *testing.T
	pool   *workerPool
	writer *channelWriter
}

func newWriterRuntime(t *testing.T, cfg writerRuntimeConfig) *writerRuntime {
	t.Helper()
	limits := channelStateLimits{pendingItemHighWatermark: 4096, appendInflightLimit: 1}
	target := benchmarkAuthorityTarget("rt")
	w := newChannelWriter(target, limits)
	rt := &writerRuntime{t: t, pool: newWorkerPool(4), writer: w}
	w.ports = writerPorts{
		prepare:    preparePorts{messageID: newBenchmarkMessageIDs(1)},
		append:     appendPorts{appender: cfg.appender},
		commit:     commitPorts{},
		pool:       rt.pool,
		schedule:   rt.schedule,
		runtimeCtx: context.Background(),
	}
	return rt
}

func (rt *writerRuntime) schedule(w *channelWriter) {
	go w.advance()
}

func (rt *writerRuntime) submit(target AuthorityTarget, items []SendBatchItem) (*Future, error) {
	future := newFuture(len(items))
	batch := submittedBatch{target: target, items: cloneSendBatchItems(items), future: future}
	if rt.writer.enqueue(batch) {
		rt.schedule(rt.writer)
	}
	return future, nil
}

func (rt *writerRuntime) stop() {
	_ = rt.pool.stop(context.Background())
}
