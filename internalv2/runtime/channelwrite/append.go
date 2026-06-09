package channelwrite

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const appendMetricPathChannelPlane = "channelplane"

const (
	appendRetryInitialBackoff = 5 * time.Millisecond
	appendRetryMaxBackoff     = 100 * time.Millisecond
	appendInitialAttempt      = 1
)

const (
	sendTraceErrorCodeRouteNotReady       = "route_not_ready"
	sendTraceErrorCodeStaleRoute          = "stale_route"
	sendTraceErrorCodeNotLeader           = "not_leader"
	sendTraceErrorCodeChannelNotFound     = "channel_not_found"
	sendTraceErrorCodeBackpressured       = "backpressured"
	sendTraceErrorCodeAppendResultMissing = "append_result_missing"
	sendTraceErrorCodeAppendFailed        = "append_failed"
	sendTraceErrorCodeCanceled            = "canceled"
	sendTraceErrorCodeTimeout             = "timeout"
	sendTraceErrorCodeOther               = "other"
)

type appendPorts struct {
	appender Appender
	observer AppendObserver
}

type appendEffect struct {
	target AuthorityTarget
	key    string
	seq    uint64
	items  []preparedSend
}

type appendCompletedEvent struct {
	key      string
	seq      uint64
	duration time.Duration
	items    []appendItemCompletion
}

type appendItemCompletion struct {
	item     preparedSend
	result   SendBatchItemResult
	appended AppendBatchItemResult
	traceErr error
}

func (e appendEffect) run(runtimeCtx context.Context, ports appendPorts) appendCompletedEvent {
	completion := appendCompletedEvent{
		key: e.key,
		seq: e.seq,
	}
	if len(e.items) == 0 {
		return completion
	}
	if ports.appender == nil {
		completion.items = appendBatchErrorCompletions(e.items, ErrAppenderRequired)
		return completion
	}

	active, inactive := activeAppendItems(e.items)
	completion.items = append(completion.items, inactive...)
	if len(active) == 0 {
		return completion
	}

	backoff := appendRetryInitialBackoff
	attempt := appendInitialAttempt
	for {
		req := appendRequest(e.target, active, attempt)
		ctx, cancel := appendItemsContext(runtimeCtx, active)
		startedAt := time.Now()
		res, err := ports.appender.AppendBatch(ctx, req)
		appendDur := sendtrace.Elapsed(startedAt, time.Now())
		cancel()
		completion.duration = appendDur
		if err != nil {
			if retryableAppendBatchError(err) {
				nextBackoff, ok := waitBeforeAppendRetry(runtimeCtx, active, backoff)
				if ok {
					backoff = nextBackoff
					attempt++
					continue
				}
			}
			completion.items = append(completion.items, appendBatchErrorCompletions(active, err)...)
			return completion
		}
		completion.items = append(completion.items, appendResultCompletions(active, res)...)
		return completion
	}
}

func appendRequest(target AuthorityTarget, active []preparedSend, attempt int) AppendBatchRequest {
	if attempt <= 0 {
		attempt = appendInitialAttempt
	}
	req := AppendBatchRequest{
		ChannelID:           target.ChannelID,
		ExpectedEpoch:       target.Epoch,
		ExpectedLeaderEpoch: target.LeaderEpoch,
		Messages:            make([]Message, 0, len(active)),
		Attempt:             attempt,
		CommitMode:          CommitModeQuorum,
		OmitResultPayload:   false,
	}
	for _, item := range active {
		cmd := item.Command
		if req.TraceID == "" && cmd.TraceID != "" {
			req.TraceID = cmd.TraceID
		}
		if req.ChannelKey == "" && cmd.ChannelKey != "" {
			req.ChannelKey = cmd.ChannelKey
		}
		serverTimestampMS := item.ServerTimestampMS
		if serverTimestampMS == 0 {
			serverTimestampMS = time.Now().UnixMilli()
		}
		req.Messages = append(req.Messages, Message{
			MessageID:         cmd.MessageID,
			ChannelID:         cmd.ChannelID,
			ChannelType:       cmd.ChannelType,
			FromUID:           cmd.FromUID,
			ClientMsgNo:       cmd.ClientMsgNo,
			TraceID:           cmd.TraceID,
			ChannelKey:        cmd.ChannelKey,
			Payload:           cloneBytes(cmd.Payload),
			ServerTimestampMS: serverTimestampMS,
		})
	}
	return req
}

func activeAppendItems(items []preparedSend) ([]preparedSend, []appendItemCompletion) {
	active := make([]preparedSend, 0, len(items))
	inactive := make([]appendItemCompletion, 0)
	for _, item := range items {
		if item.Context != nil {
			if err := item.Context.Err(); err != nil {
				inactive = append(inactive, appendItemCompletion{
					item:     item,
					result:   SendBatchItemResult{Err: err},
					traceErr: err,
				})
				continue
			}
		}
		active = append(active, item)
	}
	return active, inactive
}

func appendBatchErrorCompletions(items []preparedSend, err error) []appendItemCompletion {
	out := make([]appendItemCompletion, 0, len(items))
	for _, item := range items {
		out = append(out, appendItemCompletion{
			item:     item,
			result:   SendBatchItemResult{Err: err},
			traceErr: err,
		})
	}
	return out
}

func appendResultCompletions(items []preparedSend, res AppendBatchResult) []appendItemCompletion {
	out := make([]appendItemCompletion, 0, len(items))
	for i, item := range items {
		if i >= len(res.Items) {
			out = append(out, appendItemCompletion{
				item:     item,
				result:   SendBatchItemResult{Err: ErrAppendResultMissing},
				traceErr: ErrAppendResultMissing,
			})
			continue
		}
		appended := res.Items[i].Clone()
		if appended.Err != nil {
			out = append(out, appendItemCompletion{
				item:     item,
				result:   SendBatchItemResult{Result: SendResult{Reason: reasonForAppendError(appended.Err)}},
				appended: appended,
				traceErr: appended.Err,
			})
			continue
		}
		out = append(out, appendItemCompletion{
			item: item,
			result: SendBatchItemResult{Result: SendResult{
				MessageID:  appended.MessageID,
				MessageSeq: appended.MessageSeq,
				Reason:     ReasonSuccess,
			}},
			appended: appended,
		})
	}
	return out
}

func appendItemsContext(runtimeCtx context.Context, items []preparedSend) (context.Context, context.CancelFunc) {
	base := context.Background()
	if runtimeCtx != nil {
		base = runtimeCtx
	}
	var earliest time.Time
	hasDeadline := false
	recordDeadline := func(deadline time.Time, ok bool) {
		if !ok || deadline.IsZero() {
			return
		}
		if !hasDeadline || deadline.Before(earliest) {
			earliest = deadline
			hasDeadline = true
		}
	}
	for _, item := range items {
		recordDeadline(item.Deadline, !item.Deadline.IsZero())
		if item.Context != nil {
			deadline, ok := item.Context.Deadline()
			recordDeadline(deadline, ok)
		}
	}
	if hasDeadline {
		return context.WithDeadline(base, earliest)
	}
	return context.WithCancel(base)
}

func waitBeforeAppendRetry(runtimeCtx context.Context, items []preparedSend, backoff time.Duration) (time.Duration, bool) {
	if !appendItemsHaveDeadline(items) {
		return backoff, false
	}
	ctx, cancel := appendItemsContext(runtimeCtx, items)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return backoff, false
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nextAppendRetryBackoff(backoff), true
	case <-ctx.Done():
		return backoff, false
	}
}

func nextAppendRetryBackoff(backoff time.Duration) time.Duration {
	next := backoff * 2
	if next < appendRetryInitialBackoff {
		return appendRetryInitialBackoff
	}
	if next > appendRetryMaxBackoff {
		return appendRetryMaxBackoff
	}
	return next
}

func appendItemsHaveDeadline(items []preparedSend) bool {
	for _, item := range items {
		if !item.Deadline.IsZero() {
			return true
		}
		if item.Context != nil {
			if _, ok := item.Context.Deadline(); ok {
				return true
			}
		}
	}
	return false
}

func retryableAppendBatchError(err error) bool {
	return errors.Is(err, ErrRouteNotReady) || errors.Is(err, ErrNotLeader) || errors.Is(err, ErrStaleRoute)
}

func reasonForAppendError(err error) Reason {
	switch {
	case errors.Is(err, ErrChannelNotFound):
		return ReasonChannelNotExist
	case errors.Is(err, ErrNotLeader), errors.Is(err, ErrStaleRoute), errors.Is(err, ErrRouteNotReady):
		return ReasonNodeNotMatch
	default:
		return ReasonSystemError
	}
}

func committedEnvelopeForAppend(item preparedSend, appended AppendBatchItemResult) CommittedEnvelope {
	cmd := item.Command
	serverTimestampMS := item.ServerTimestampMS
	if appended.Message.ServerTimestampMS != 0 {
		serverTimestampMS = appended.Message.ServerTimestampMS
	}
	payload := appended.Message.Payload
	if len(payload) == 0 {
		payload = cmd.Payload
	}
	return CommittedEnvelope{
		MessageID:         appended.MessageID,
		MessageSeq:        appended.MessageSeq,
		ChannelID:         cmd.ChannelID,
		ChannelType:       cmd.ChannelType,
		FromUID:           cmd.FromUID,
		SenderNodeID:      cmd.SenderNodeID,
		SenderSessionID:   cmd.SenderSessionID,
		ClientMsgNo:       cmd.ClientMsgNo,
		ServerTimestampMS: serverTimestampMS,
		Payload:           cloneBytes(payload),
		RedDot:            cmd.RedDot,
		MessageScopedUIDs: append([]string(nil), cmd.MessageScopedUIDs...),
	}
}

func observeAppendCompletion(observer AppendObserver, completion appendItemCompletion, dur time.Duration) {
	if observer == nil {
		return
	}
	observer.AppendFinished(appendMetricPathChannelPlane, completion.traceErr, dur)
}

func recordAppendDurableTrace(item preparedSend, messageSeq uint64, err error, duration time.Duration) {
	cmd := item.Command
	if cmd.TraceID == "" || !sendtrace.Enabled() {
		return
	}
	result, errorCode := sendTraceResultForError(err)
	event := sendtrace.Event{
		Stage:       sendtrace.StageMessageSendDurable,
		TraceID:     cmd.TraceID,
		Duration:    duration,
		NodeID:      cmd.SenderNodeID,
		ChannelKey:  cmd.ChannelKey,
		ClientMsgNo: cmd.ClientMsgNo,
		MessageSeq:  messageSeq,
		FromUID:     cmd.FromUID,
		Result:      result,
		ErrorCode:   errorCode,
	}
	if err != nil {
		event.Error = err.Error()
	}
	sendtrace.Record(event)
}

func sendTraceResultForError(err error) (sendtrace.Result, string) {
	switch {
	case err == nil:
		return sendtrace.ResultOK, ""
	case errors.Is(err, context.Canceled):
		return sendtrace.ResultCanceled, sendTraceErrorCodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return sendtrace.ResultTimeout, sendTraceErrorCodeTimeout
	default:
		return sendtrace.ResultError, sendTraceErrorCodeForAppendError(err)
	}
}

func sendTraceErrorCodeForAppendError(err error) string {
	switch {
	case errors.Is(err, ErrRouteNotReady):
		return sendTraceErrorCodeRouteNotReady
	case errors.Is(err, ErrStaleRoute):
		return sendTraceErrorCodeStaleRoute
	case errors.Is(err, ErrNotLeader):
		return sendTraceErrorCodeNotLeader
	case errors.Is(err, ErrChannelNotFound):
		return sendTraceErrorCodeChannelNotFound
	case errors.Is(err, ErrBackpressured):
		return sendTraceErrorCodeBackpressured
	case errors.Is(err, ErrAppendResultMissing):
		return sendTraceErrorCodeAppendResultMissing
	case errors.Is(err, ErrAppendFailed):
		return sendTraceErrorCodeAppendFailed
	default:
		return sendTraceErrorCodeOther
	}
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
