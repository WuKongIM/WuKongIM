package cluster

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const (
	channelAppendDefaultAttempt = 1

	channelAppendTraceErrorCodeRouteNotReady   = "route_not_ready"
	channelAppendTraceErrorCodeStaleRoute      = "stale_route"
	channelAppendTraceErrorCodeNotLeader       = "not_leader"
	channelAppendTraceErrorCodeChannelNotFound = "channel_not_found"
	channelAppendTraceErrorCodeBackpressured   = "backpressured"
	channelAppendTraceErrorCodeAppendFailed    = "append_failed"
	channelAppendTraceErrorCodeCanceled        = "canceled"
	channelAppendTraceErrorCodeTimeout         = "timeout"
	channelAppendTraceErrorCodeOther           = "other"
)

var errChannelAppendResultMissing = errors.New("internalv2/infra/cluster: append result missing")

// ChannelAppendNode is the clusterv2 append surface used by internalv2.
type ChannelAppendNode interface {
	AppendChannelBatch(context.Context, channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error)
}

// ChannelAppender adapts clusterv2 channel append to the message usecase port.
type ChannelAppender struct {
	node ChannelAppendNode
}

// NewChannelAppender creates a ChannelAppender.
func NewChannelAppender(node ChannelAppendNode) *ChannelAppender {
	return &ChannelAppender{node: node}
}

// AppendBatch appends a message batch through clusterv2.
func (a *ChannelAppender) AppendBatch(ctx context.Context, req message.AppendBatchRequest) (message.AppendBatchResult, error) {
	if a == nil || a.node == nil {
		return message.AppendBatchResult{}, message.ErrAppenderRequired
	}
	traceEnabled := appendRequestHasTrace(req) && sendtrace.Enabled()
	var startedAt time.Time
	if traceEnabled {
		startedAt = time.Now()
	}
	attempt := req.Attempt
	if attempt <= 0 {
		attempt = channelAppendDefaultAttempt
	}
	res, err := a.node.AppendChannelBatch(ctx, channelv2.AppendBatchRequest{
		ChannelID:         channelv2.ChannelID{ID: req.ChannelID.ID, Type: req.ChannelID.Type},
		Messages:          toChannelMessages(req.Messages),
		TraceID:           req.TraceID,
		ChannelKey:        req.ChannelKey,
		Attempt:           attempt,
		CommitMode:        toChannelCommitMode(req.CommitMode),
		OmitResultPayload: req.OmitResultPayload,
	})
	if err != nil {
		mappedErr := mapAppendError(err)
		if traceEnabled {
			recordChannelAppendTrace(req, nil, mappedErr, sendtrace.Elapsed(startedAt, time.Now()))
		}
		return message.AppendBatchResult{}, mappedErr
	}
	if traceEnabled {
		recordChannelAppendTrace(req, res.Items, nil, sendtrace.Elapsed(startedAt, time.Now()))
	}
	return fromChannelAppendResult(res), nil
}

func toChannelMessages(in []message.Message) []channelv2.Message {
	out := make([]channelv2.Message, 0, len(in))
	for _, msg := range in {
		out = append(out, channelv2.Message{
			MessageID:   msg.MessageID,
			MessageSeq:  msg.MessageSeq,
			ChannelID:   msg.ChannelID,
			ChannelType: msg.ChannelType,
			FromUID:     msg.FromUID,
			ClientMsgNo: msg.ClientMsgNo,
			TraceID:     msg.TraceID,
			ChannelKey:  msg.ChannelKey,
			Payload:     append([]byte(nil), msg.Payload...),
		})
	}
	return out
}

func toChannelCommitMode(mode message.CommitMode) channelv2.CommitMode {
	if mode == message.CommitModeLocal {
		return channelv2.CommitModeLocal
	}
	return channelv2.CommitModeQuorum
}

func fromChannelAppendResult(res channelv2.AppendBatchResult) message.AppendBatchResult {
	items := make([]message.AppendBatchItemResult, 0, len(res.Items))
	for _, item := range res.Items {
		items = append(items, message.AppendBatchItemResult{
			MessageID:  item.MessageID,
			MessageSeq: item.MessageSeq,
			Message:    fromChannelMessage(item.Message),
			Err:        mapAppendError(item.Err),
		})
	}
	return message.AppendBatchResult{Items: items}
}

func fromChannelMessage(msg channelv2.Message) message.Message {
	return message.Message{
		MessageID:   msg.MessageID,
		MessageSeq:  msg.MessageSeq,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		FromUID:     msg.FromUID,
		ClientMsgNo: msg.ClientMsgNo,
		TraceID:     msg.TraceID,
		ChannelKey:  msg.ChannelKey,
		Payload:     append([]byte(nil), msg.Payload...),
	}
}

func recordChannelAppendTrace(req message.AppendBatchRequest, items []channelv2.AppendBatchItemResult, batchErr error, duration time.Duration) {
	if !appendRequestHasTrace(req) || !sendtrace.Enabled() {
		return
	}
	recorded := false
	for i, msg := range req.Messages {
		if msg.TraceID == "" {
			continue
		}
		recordChannelAppendTraceForMessage(req, msg, itemMessageSeq(items, i), itemAppendError(items, i, batchErr), duration)
		recorded = true
	}
	if recorded {
		return
	}
	var first message.Message
	if len(req.Messages) > 0 {
		first = req.Messages[0]
	}
	err := batchErr
	if len(req.Messages) > 0 {
		err = itemAppendError(items, 0, batchErr)
	}
	recordChannelAppendTraceForMessage(req, first, itemMessageSeq(items, 0), err, duration)
}

func recordChannelAppendTraceForMessage(req message.AppendBatchRequest, msg message.Message, messageSeq uint64, err error, duration time.Duration) {
	traceID := msg.TraceID
	if traceID == "" {
		traceID = req.TraceID
	}
	if traceID == "" {
		return
	}
	channelKey := msg.ChannelKey
	if channelKey == "" {
		channelKey = req.ChannelKey
	}
	if channelKey == "" {
		channelKey = sendtrace.ChannelKeyFromID(req.ChannelID.ID, req.ChannelID.Type)
	}
	attempt := req.Attempt
	if attempt <= 0 {
		attempt = channelAppendDefaultAttempt
	}
	result, errorCode := channelAppendTraceOutcome(err)
	event := sendtrace.Event{
		Stage:       sendtrace.StageChannelAppendLocal,
		TraceID:     traceID,
		Duration:    duration,
		ChannelKey:  channelKey,
		ClientMsgNo: msg.ClientMsgNo,
		MessageSeq:  messageSeq,
		FromUID:     msg.FromUID,
		Result:      result,
		ErrorCode:   errorCode,
		Attempt:     attempt,
		RecordCount: len(req.Messages),
	}
	if err != nil {
		event.Error = err.Error()
	}
	sendtrace.Record(event)
}

func appendRequestHasTrace(req message.AppendBatchRequest) bool {
	if req.TraceID != "" {
		return true
	}
	for _, msg := range req.Messages {
		if msg.TraceID != "" {
			return true
		}
	}
	return false
}

func itemMessageSeq(items []channelv2.AppendBatchItemResult, index int) uint64 {
	if index < 0 || index >= len(items) {
		return 0
	}
	if items[index].MessageSeq != 0 {
		return items[index].MessageSeq
	}
	return items[index].Message.MessageSeq
}

func itemAppendError(items []channelv2.AppendBatchItemResult, index int, batchErr error) error {
	if batchErr != nil {
		return batchErr
	}
	if index < 0 || index >= len(items) {
		return mapAppendError(errChannelAppendResultMissing)
	}
	return mapAppendError(items[index].Err)
}

func channelAppendTraceOutcome(err error) (sendtrace.Result, string) {
	switch {
	case err == nil:
		return sendtrace.ResultOK, ""
	case errors.Is(err, context.Canceled):
		return sendtrace.ResultCanceled, channelAppendTraceErrorCodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return sendtrace.ResultTimeout, channelAppendTraceErrorCodeTimeout
	case errors.Is(err, message.ErrRouteNotReady):
		return sendtrace.ResultError, channelAppendTraceErrorCodeRouteNotReady
	case errors.Is(err, message.ErrStaleRoute):
		return sendtrace.ResultError, channelAppendTraceErrorCodeStaleRoute
	case errors.Is(err, message.ErrNotLeader):
		return sendtrace.ResultError, channelAppendTraceErrorCodeNotLeader
	case errors.Is(err, message.ErrChannelNotFound):
		return sendtrace.ResultError, channelAppendTraceErrorCodeChannelNotFound
	case errors.Is(err, message.ErrBackpressured):
		return sendtrace.ResultError, channelAppendTraceErrorCodeBackpressured
	case errors.Is(err, message.ErrAppendFailed):
		return sendtrace.ResultError, channelAppendTraceErrorCodeAppendFailed
	default:
		return sendtrace.ResultError, channelAppendTraceErrorCodeOther
	}
}
