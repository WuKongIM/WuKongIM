package cluster

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
)

type spanRangeInfo struct {
	nodeId     uint64
	startIndex uint64
	endIndex   uint64
	span       trace.Span
	ctx        context.Context
}

type traceRecord struct {
	propseSpanInfos []spanRangeInfo
	commitSpanInfos []spanRangeInfo
	syncSpanInfos   []spanRangeInfo
	mu              sync.RWMutex
}

func newTraceRecord() *traceRecord {
	return &traceRecord{}
}

func (t *traceRecord) addProposeSpanRange(startIndex, endIndex uint64, span trace.Span, ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.propseSpanInfos = append(t.propseSpanInfos, spanRangeInfo{
		startIndex: startIndex,
		endIndex:   endIndex,
		span:       span,
		ctx:        ctx,
	})
}

func (t *traceRecord) getProposeContextsWithRange(startIndex, endIndex uint64) []context.Context {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var ctxs []context.Context
	for _, spanInfo := range t.propseSpanInfos {
		if (startIndex >= spanInfo.startIndex && startIndex <= spanInfo.endIndex) || (endIndex >= spanInfo.startIndex && startIndex <= spanInfo.endIndex) {
			ctxs = append(ctxs, spanInfo.ctx)
		}
	}
	return ctxs
}

func (t *traceRecord) removeProposeSpanWithRange(startIndex, endIndex uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var spans []spanRangeInfo
	for _, spanInfo := range t.propseSpanInfos {
		if startIndex == spanInfo.startIndex && endIndex == spanInfo.endIndex {
			continue
		}
		spans = append(spans, spanInfo)
	}
	t.propseSpanInfos = spans
}

func (t *traceRecord) addCommitSpanRange(startIndex, endIndex uint64, span trace.Span, ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.commitSpanInfos = append(t.commitSpanInfos, spanRangeInfo{
		startIndex: startIndex,
		endIndex:   endIndex,
		span:       span,
		ctx:        ctx,
	})
}

func (t *traceRecord) getCommitSpanWithRange(startIndex, endIndex uint64) []trace.Span {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var spans []trace.Span
	for _, spanInfo := range t.commitSpanInfos {
		if (startIndex >= spanInfo.startIndex && startIndex <= spanInfo.endIndex) || (endIndex >= spanInfo.startIndex && startIndex <= spanInfo.endIndex) {
			spans = append(spans, spanInfo.span)
		}
	}
	return spans
}

func (t *traceRecord) getCommitContextsWithRange(startIndex, endIndex uint64) []context.Context {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var ctxs []context.Context
	for _, spanInfo := range t.commitSpanInfos {
		if (startIndex >= spanInfo.startIndex && startIndex <= spanInfo.endIndex) || (endIndex >= spanInfo.startIndex && startIndex <= spanInfo.endIndex) {
			ctxs = append(ctxs, spanInfo.ctx)
		}
	}
	return ctxs
}

func (t *traceRecord) removeCommitSpanWithRange(startIndex, endIndex uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var spans []spanRangeInfo
	for _, spanInfo := range t.commitSpanInfos {
		if (startIndex >= spanInfo.startIndex && startIndex <= spanInfo.endIndex) || (endIndex >= spanInfo.startIndex && startIndex <= spanInfo.endIndex) {
			continue
		}
		spans = append(spans, spanInfo)
	}
	t.commitSpanInfos = spans
}

func (t *traceRecord) addSyncSpan(nodeId uint64, index uint64, span trace.Span, ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncSpanInfos = append(t.syncSpanInfos, spanRangeInfo{
		nodeId:     nodeId,
		startIndex: index,
		span:       span,
		ctx:        ctx,
	})
}

func (t *traceRecord) getSyncSpanWithIndex(nodeId uint64, index uint64) []spanRangeInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var spans []spanRangeInfo
	for _, spanInfo := range t.syncSpanInfos {
		if spanInfo.nodeId == nodeId && spanInfo.startIndex < index {
			spans = append(spans, spanInfo)
		}
	}
	return spans
}

func (t *traceRecord) removeSyncSpanWithIndex(nodeId uint64, index uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var spans []spanRangeInfo
	for _, spanInfo := range t.syncSpanInfos {
		if spanInfo.nodeId == nodeId && spanInfo.startIndex == index {
			continue
		}
		spans = append(spans, spanInfo)
	}
	t.syncSpanInfos = spans
}
