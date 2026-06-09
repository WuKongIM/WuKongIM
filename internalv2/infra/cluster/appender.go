package cluster

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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

// ChannelAppender adapts clusterv2 channel append to the channelwrite append port.
type ChannelAppender struct {
	node   ChannelAppendNode
	logger wklog.Logger
}

// NewChannelAppender creates a ChannelAppender.
func NewChannelAppender(node ChannelAppendNode, logger ...wklog.Logger) *ChannelAppender {
	appender := &ChannelAppender{node: node}
	if len(logger) > 0 {
		appender.logger = logger[0]
	}
	return appender
}

// AppendBatch appends a channelwrite message batch through clusterv2.
func (a *ChannelAppender) AppendBatch(ctx context.Context, req channelwrite.AppendBatchRequest) (channelwrite.AppendBatchResult, error) {
	if a == nil || a.node == nil {
		return channelwrite.AppendBatchResult{}, channelwrite.ErrAppenderRequired
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
		ChannelID:            channelv2.ChannelID{ID: req.ChannelID.ID, Type: req.ChannelID.Type},
		ExpectedChannelEpoch: req.ExpectedEpoch,
		ExpectedLeaderEpoch:  req.ExpectedLeaderEpoch,
		Messages:             toChannelMessages(req.Messages),
		TraceID:              req.TraceID,
		ChannelKey:           req.ChannelKey,
		Attempt:              attempt,
		CommitMode:           toChannelCommitMode(req.CommitMode),
		OmitResultPayload:    req.OmitResultPayload,
	})
	if err != nil {
		mappedErr := mapAppendError(err)
		a.logAppendChannelBatchError(req, err, mappedErr)
		if traceEnabled {
			recordChannelAppendTrace(req, nil, mappedErr, sendtrace.Elapsed(startedAt, time.Now()))
		}
		return channelwrite.AppendBatchResult{}, mappedErr
	}
	if traceEnabled {
		recordChannelAppendTrace(req, res.Items, nil, sendtrace.Elapsed(startedAt, time.Now()))
	}
	return fromChannelAppendResult(res), nil
}

func (a *ChannelAppender) logAppendChannelBatchError(req channelwrite.AppendBatchRequest, rawErr error, mappedErr error) {
	logger := a.loggerOrNop()
	traceResult, errorCode := channelAppendTraceOutcome(mappedErr)
	logger.Error("channel append batch failed",
		wklog.Event("internalv2.infra.cluster.channel_append_batch_failed"),
		wklog.ChannelID(req.ChannelID.ID),
		wklog.ChannelType(int64(req.ChannelID.Type)),
		wklog.String("channelKey", req.ChannelKey),
		wklog.TraceID(req.TraceID),
		wklog.Uint64("expectedEpoch", req.ExpectedEpoch),
		wklog.Uint64("expectedLeaderEpoch", req.ExpectedLeaderEpoch),
		wklog.Attempt(normalizedChannelAppendAttempt(req.Attempt)),
		wklog.Int("records", len(req.Messages)),
		wklog.Result(errorCode),
		wklog.String("traceResult", string(traceResult)),
		wklog.String("errorCode", errorCode),
		wklog.Error(rawErr),
	)
}

func (a *ChannelAppender) loggerOrNop() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger
}

func normalizedChannelAppendAttempt(attempt int) int {
	if attempt <= 0 {
		return channelAppendDefaultAttempt
	}
	return attempt
}

func toChannelMessages(in []channelwrite.Message) []channelv2.Message {
	out := make([]channelv2.Message, 0, len(in))
	for _, msg := range in {
		out = append(out, channelv2.Message{
			MessageID:         msg.MessageID,
			MessageSeq:        msg.MessageSeq,
			ChannelID:         msg.ChannelID,
			ChannelType:       msg.ChannelType,
			FromUID:           msg.FromUID,
			ClientMsgNo:       msg.ClientMsgNo,
			TraceID:           msg.TraceID,
			ChannelKey:        msg.ChannelKey,
			Payload:           append([]byte(nil), msg.Payload...),
			ServerTimestampMS: msg.ServerTimestampMS,
		})
	}
	return out
}

func toChannelCommitMode(mode channelwrite.CommitMode) channelv2.CommitMode {
	if mode == channelwrite.CommitModeLocal {
		return channelv2.CommitModeLocal
	}
	return channelv2.CommitModeQuorum
}

func fromChannelAppendResult(res channelv2.AppendBatchResult) channelwrite.AppendBatchResult {
	items := make([]channelwrite.AppendBatchItemResult, 0, len(res.Items))
	for _, item := range res.Items {
		items = append(items, channelwrite.AppendBatchItemResult{
			MessageID:  item.MessageID,
			MessageSeq: item.MessageSeq,
			Message:    fromChannelMessage(item.Message),
			Err:        mapAppendError(item.Err),
		})
	}
	return channelwrite.AppendBatchResult{Items: items}
}

func fromChannelMessage(msg channelv2.Message) channelwrite.Message {
	return channelwrite.Message{
		MessageID:         msg.MessageID,
		MessageSeq:        msg.MessageSeq,
		ChannelID:         msg.ChannelID,
		ChannelType:       msg.ChannelType,
		FromUID:           msg.FromUID,
		ClientMsgNo:       msg.ClientMsgNo,
		TraceID:           msg.TraceID,
		ChannelKey:        msg.ChannelKey,
		Payload:           append([]byte(nil), msg.Payload...),
		ServerTimestampMS: msg.ServerTimestampMS,
	}
}

func recordChannelAppendTrace(req channelwrite.AppendBatchRequest, items []channelv2.AppendBatchItemResult, batchErr error, duration time.Duration) {
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
	var first channelwrite.Message
	if len(req.Messages) > 0 {
		first = req.Messages[0]
	}
	err := batchErr
	if len(req.Messages) > 0 {
		err = itemAppendError(items, 0, batchErr)
	}
	recordChannelAppendTraceForMessage(req, first, itemMessageSeq(items, 0), err, duration)
}

func recordChannelAppendTraceForMessage(req channelwrite.AppendBatchRequest, msg channelwrite.Message, messageSeq uint64, err error, duration time.Duration) {
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
	attempt = normalizedChannelAppendAttempt(attempt)
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

func appendRequestHasTrace(req channelwrite.AppendBatchRequest) bool {
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
	case errors.Is(err, channelwrite.ErrRouteNotReady):
		return sendtrace.ResultError, channelAppendTraceErrorCodeRouteNotReady
	case errors.Is(err, channelwrite.ErrStaleRoute):
		return sendtrace.ResultError, channelAppendTraceErrorCodeStaleRoute
	case errors.Is(err, channelwrite.ErrNotLeader):
		return sendtrace.ResultError, channelAppendTraceErrorCodeNotLeader
	case errors.Is(err, channelwrite.ErrChannelNotFound):
		return sendtrace.ResultError, channelAppendTraceErrorCodeChannelNotFound
	case errors.Is(err, channelwrite.ErrBackpressured):
		return sendtrace.ResultError, channelAppendTraceErrorCodeBackpressured
	case errors.Is(err, channelwrite.ErrAppendFailed):
		return sendtrace.ResultError, channelAppendTraceErrorCodeAppendFailed
	default:
		return sendtrace.ResultError, channelAppendTraceErrorCodeOther
	}
}
