package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const (
	channelAppendTraceErrorCodeChannelNotFound = "channel_not_found"
	channelAppendTraceErrorCodeBackpressured   = "backpressured"
	channelAppendTraceErrorCodeAppendFailed    = "append_failed"
	channelAppendTraceErrorCodeCanceled        = "canceled"
	channelAppendTraceErrorCodeTimeout         = "timeout"
	channelAppendTraceErrorCodeNotLeader       = "not_leader"
	channelAppendTraceErrorCodeOther           = "other"
)

// appendTraceItem carries transient trace metadata for one selected append record.
type appendTraceItem struct {
	traceID        string
	channelKey     string
	clientMsgNo    string
	fromUID        string
	attempt        int
	requestIdx     int
	recordIdx      int
	localRecordIdx int
}

// appendTraceBatch carries transient trace sidecars for one append batch.
type appendTraceBatch struct {
	items     []appendTraceItem
	evaluated bool
}

func selectAppendTraceBatch(batch appendBatch) appendTraceBatch {
	var items []appendTraceItem
	hasUnevaluatedTrace := false
	for reqIdx, req := range batch.requests {
		if req.traceEvaluated {
			items = append(items, rebaseAppendTraceItems(req.traceItems, batch.requests, reqIdx)...)
			continue
		}
		if appendRequestHasTrace(req) {
			hasUnevaluatedTrace = true
		}
	}
	if !hasUnevaluatedTrace {
		return appendTraceBatch{items: items, evaluated: len(items) > 0 || appendBatchHasEvaluatedTrace(batch)}
	}
	limits, ok := sendtrace.ActiveDetailLimits()
	if !ok || limits.MaxItemsPerBatch <= 0 {
		return appendTraceBatch{items: items}
	}
	if items == nil {
		items = make([]appendTraceItem, 0, minInt(limits.MaxItemsPerBatch, requestsRecordCount(batch.requests)))
	}
	for reqIdx, req := range batch.requests {
		if req.traceEvaluated {
			continue
		}
		for msgIdx, msg := range req.req.Messages {
			if len(items) >= limits.MaxItemsPerBatch {
				return appendTraceBatch{items: items, evaluated: true}
			}
			key := appendMessageDetailKey(req.req, msg)
			if key.TraceID == "" {
				continue
			}
			decision, _, ok := sendtrace.DetailDecisionFor(key)
			if !ok || !decision.Keep {
				continue
			}
			items = append(items, appendTraceItem{
				traceID:        key.TraceID,
				channelKey:     key.ChannelKey,
				clientMsgNo:    key.ClientMsgNo,
				fromUID:        key.FromUID,
				attempt:        normalizedAppendAttempt(req.req.Attempt),
				requestIdx:     reqIdx,
				recordIdx:      appendRequestRecordIndex(batch.requests, reqIdx, msgIdx),
				localRecordIdx: msgIdx,
			})
		}
	}
	return appendTraceBatch{items: items, evaluated: true}
}

func traceItemsForRequest(items []appendTraceItem, reqIdx int) []appendTraceItem {
	var out []appendTraceItem
	for _, item := range items {
		if item.requestIdx != reqIdx {
			continue
		}
		out = append(out, item)
	}
	return out
}

func rebaseAppendTraceItems(items []appendTraceItem, requests []appendRequest, reqIdx int) []appendTraceItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]appendTraceItem, 0, len(items))
	for _, item := range items {
		item.requestIdx = reqIdx
		item.recordIdx = appendRequestRecordIndex(requests, reqIdx, item.localRecordIdx)
		out = append(out, item)
	}
	return out
}

func (r *Reactor) recordLeaderQueueAndLocalDurableTrace(req appendRequest, timing appendTiming, batch appendBatch, completedAt time.Time, baseOffset uint64, err error) {
	items := timing.traceItems
	if len(items) == 0 {
		items = lazyTraceItemsForStoreStage(req, batch, timing, completedAt, err)
	}
	if len(items) == 0 {
		return
	}
	queueDur := sendtrace.Elapsed(req.enqueuedAt, timing.storeSubmittedAt)
	localDur := sendtrace.Elapsed(timing.storeSubmittedAt, completedAt)
	recordCount := timing.recordCount
	if recordCount == 0 {
		recordCount = len(req.records)
	}
	for _, item := range items {
		var seq uint64
		if baseOffset > 0 {
			seq = baseOffset + uint64(item.localRecordIdx)
		}
		recordLeaderTraceEvent(sendtrace.StageReplicaLeaderQueueWait, completedAt, item, seq, queueDur, recordCount, nil)
		recordLeaderTraceEvent(sendtrace.StageReplicaLeaderLocalDurable, completedAt, item, seq, localDur, recordCount, err)
	}
}

// recordLeaderQuorumTrace emits quorum wait trace events for selected append sidecars.
func (r *Reactor) recordLeaderQuorumTrace(timing appendTiming, completedAt time.Time, err error) {
	if timing.mode != ch.CommitModeQuorum || timing.storeCompletedAt.IsZero() || len(timing.traceItems) == 0 {
		return
	}
	duration := sendtrace.Elapsed(timing.storeCompletedAt, completedAt)
	for _, item := range timing.traceItems {
		var messageSeq uint64
		if timing.targetOffset > 0 && timing.recordCount > 0 && item.localRecordIdx >= 0 && item.localRecordIdx < timing.recordCount {
			deltaFromTarget := timing.recordCount - 1 - item.localRecordIdx
			if timing.targetOffset > uint64(deltaFromTarget) {
				messageSeq = timing.targetOffset - uint64(deltaFromTarget)
			}
		}
		recordLeaderTraceEvent(sendtrace.StageReplicaLeaderQuorumWait, completedAt, item, messageSeq, duration, timing.recordCount, err)
	}
}

func recordLeaderTraceEvent(stage sendtrace.Stage, at time.Time, item appendTraceItem, messageSeq uint64, duration time.Duration, recordCount int, err error) {
	result, errorCode := leaderTraceOutcome(err)
	event := sendtrace.Event{
		Stage:       stage,
		TraceID:     item.traceID,
		At:          at,
		Duration:    duration,
		ChannelKey:  item.channelKey,
		ClientMsgNo: item.clientMsgNo,
		MessageSeq:  messageSeq,
		FromUID:     item.fromUID,
		Result:      result,
		ErrorCode:   errorCode,
		Attempt:     item.attempt,
		RecordCount: recordCount,
	}
	if err != nil {
		event.Error = err.Error()
	}
	sendtrace.Record(event)
}

func leaderTraceOutcome(err error) (sendtrace.Result, string) {
	switch {
	case err == nil:
		return sendtrace.ResultOK, ""
	case errors.Is(err, context.Canceled):
		return sendtrace.ResultCanceled, channelAppendTraceErrorCodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return sendtrace.ResultTimeout, channelAppendTraceErrorCodeTimeout
	case errors.Is(err, ch.ErrNotLeader):
		return sendtrace.ResultError, channelAppendTraceErrorCodeNotLeader
	case errors.Is(err, ch.ErrChannelNotFound):
		return sendtrace.ResultError, channelAppendTraceErrorCodeChannelNotFound
	case errors.Is(err, ch.ErrBackpressured):
		return sendtrace.ResultError, channelAppendTraceErrorCodeBackpressured
	default:
		return sendtrace.ResultError, channelAppendTraceErrorCodeOther
	}
}

func lazyTraceItemsForStoreStage(req appendRequest, batch appendBatch, timing appendTiming, completedAt time.Time, err error) []appendTraceItem {
	limits, ok := sendtrace.ActiveDetailLimits()
	if !ok || limits.MaxItemsPerBatch <= 0 {
		return nil
	}
	queueDur := sendtrace.Elapsed(req.enqueuedAt, timing.storeSubmittedAt)
	localDur := sendtrace.Elapsed(timing.storeSubmittedAt, completedAt)
	if err == nil && (limits.SlowThreshold <= 0 || (queueDur < limits.SlowThreshold && localDur < limits.SlowThreshold)) {
		return nil
	}
	return lazyTraceItemsForRequest(req, batch, limits.MaxItemsPerBatch)
}

func lazyTraceItemsForRequest(req appendRequest, batch appendBatch, limit int) []appendTraceItem {
	if limit <= 0 || len(req.req.Messages) == 0 {
		return nil
	}
	reqIdx := appendRequestIndex(batch.requests, req.opID)
	if reqIdx < 0 {
		return nil
	}
	items := make([]appendTraceItem, 0, minInt(limit, len(req.req.Messages)))
	for msgIdx, msg := range req.req.Messages {
		if len(items) >= limit {
			return items
		}
		key := appendMessageDetailKey(req.req, msg)
		if key.TraceID == "" {
			continue
		}
		items = append(items, appendTraceItem{
			traceID:        key.TraceID,
			channelKey:     key.ChannelKey,
			clientMsgNo:    key.ClientMsgNo,
			fromUID:        key.FromUID,
			attempt:        normalizedAppendAttempt(req.req.Attempt),
			requestIdx:     reqIdx,
			recordIdx:      appendRequestRecordIndex(batch.requests, reqIdx, msgIdx),
			localRecordIdx: msgIdx,
		})
	}
	return items
}

func appendRequestIndex(requests []appendRequest, opID ch.OpID) int {
	for idx, req := range requests {
		if req.opID == opID {
			return idx
		}
	}
	return -1
}

func appendBatchHasTrace(batch appendBatch) bool {
	for _, req := range batch.requests {
		if appendRequestHasTrace(req) {
			return true
		}
	}
	return false
}

func appendBatchHasEvaluatedTrace(batch appendBatch) bool {
	for _, req := range batch.requests {
		if req.traceEvaluated {
			return true
		}
	}
	return false
}

func appendRequestHasTrace(req appendRequest) bool {
	if req.req.TraceID != "" {
		return true
	}
	for _, msg := range req.req.Messages {
		if msg.TraceID != "" {
			return true
		}
	}
	return false
}

func appendMessageDetailKey(req ch.AppendBatchRequest, msg ch.Message) sendtrace.DetailKey {
	traceID := msg.TraceID
	if traceID == "" {
		traceID = req.TraceID
	}
	if traceID == "" {
		return sendtrace.DetailKey{}
	}
	channelKey := msg.ChannelKey
	if channelKey == "" {
		channelKey = req.ChannelKey
	}
	if channelKey == "" {
		channelKey = sendtrace.ChannelKeyFromID(req.ChannelID.ID, req.ChannelID.Type)
	}
	return sendtrace.DetailKey{TraceID: traceID, ChannelKey: channelKey, ClientMsgNo: msg.ClientMsgNo, FromUID: msg.FromUID}
}

func normalizedAppendAttempt(attempt int) int {
	if attempt <= 0 {
		return 1
	}
	return attempt
}

func appendRequestRecordIndex(requests []appendRequest, reqIdx int, msgIdx int) int {
	index := msgIdx
	for i := 0; i < reqIdx && i < len(requests); i++ {
		index += len(requests[i].records)
	}
	return index
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
