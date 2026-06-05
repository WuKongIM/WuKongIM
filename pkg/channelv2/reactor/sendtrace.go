package reactor

import (
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
	items []appendTraceItem
}

func selectAppendTraceBatch(batch appendBatch) appendTraceBatch {
	limits, ok := sendtrace.ActiveDetailLimits()
	if !ok || limits.MaxItemsPerBatch <= 0 || !appendBatchHasTrace(batch) {
		return appendTraceBatch{}
	}
	items := make([]appendTraceItem, 0, minInt(limits.MaxItemsPerBatch, requestsRecordCount(batch.requests)))
	for reqIdx, req := range batch.requests {
		for msgIdx, msg := range req.req.Messages {
			if len(items) >= limits.MaxItemsPerBatch {
				return appendTraceBatch{items: items}
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
	return appendTraceBatch{items: items}
}

func appendBatchHasTrace(batch appendBatch) bool {
	for _, req := range batch.requests {
		if req.req.TraceID != "" {
			return true
		}
		for _, msg := range req.req.Messages {
			if msg.TraceID != "" {
				return true
			}
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
