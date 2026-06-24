package channelappend

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const appendMetricPathChannelPlane = "channelplane"

const appendInitialAttempt = 1

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
	effectStartedAt := time.Now()
	effectResult := channelAppendResultOK
	completion := appendCompletedEvent{
		key: e.key,
		seq: e.seq,
	}
	defer func() {
		observeEffect(ports.observer, EffectObservation{Stage: "append", Result: effectResult, Items: len(e.items), Duration: elapsedSince(effectStartedAt)})
	}()
	if len(e.items) == 0 {
		return completion
	}
	if ports.appender == nil {
		effectResult = channelAppendResultAppendFailed
		completion.items = appendBatchErrorCompletions(e.items, ErrAppenderRequired)
		return completion
	}

	active, inactive := activeAppendItems(e.items)
	completion.items = append(completion.items, inactive...)
	if len(active) == 0 {
		return completion
	}

	req := appendRequest(e.target, active, appendInitialAttempt)
	ctx, cancel := appendBatchContext(runtimeCtx)
	startedAt := time.Now()
	res, err := ports.appender.AppendBatch(ctx, req)
	appendDur := sendtrace.Elapsed(startedAt, time.Now())
	cancel()
	completion.duration = appendDur
	if err != nil {
		effectResult = errorClass(err)
		completion.items = append(completion.items, appendBatchErrorCompletions(active, err)...)
		return completion
	}
	completion.items = append(completion.items, appendResultCompletions(active, res)...)
	effectResult = appendCompletionsResultClass(completion.items)
	return completion
}

func appendPanicCompletion(effect appendEffect, recovered any) appendCompletedEvent {
	return appendErrorCompletion(effect, effectPanicError(effectStageAppend, recovered))
}

func appendScheduleErrorCompletion(effect appendEffect, scheduleErr error) appendCompletedEvent {
	return appendErrorCompletion(effect, effectScheduleError(effectStageAppend, scheduleErr))
}

func appendErrorCompletion(effect appendEffect, err error) appendCompletedEvent {
	return appendCompletedEvent{
		key:   effect.key,
		seq:   effect.seq,
		items: appendBatchErrorCompletions(effect.items, err),
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
		OmitResultPayload:   true,
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
			Setting:           cmd.Setting,
			Topic:             cmd.Topic,
			Expire:            cmd.Expire,
			FromUID:           cmd.FromUID,
			ClientMsgNo:       cmd.ClientMsgNo,
			TraceID:           cmd.TraceID,
			ChannelKey:        cmd.ChannelKey,
			Payload:           cmd.Payload,
			SyncOnce:          cmd.SyncOnce,
			ServerTimestampMS: serverTimestampMS,
		})
	}
	return req
}

func activeAppendItems(items []preparedSend) ([]preparedSend, []appendItemCompletion) {
	var active []preparedSend
	var inactive []appendItemCompletion
	filtered := false
	for i, item := range items {
		if err := appendItemError(item); err != nil {
			filtered = true
			if active == nil {
				active = append([]preparedSend(nil), items[:i]...)
			}
			inactive = append(inactive, appendItemErrorCompletion(item, err))
			continue
		}
		if filtered {
			active = append(active, item)
		}
	}
	if !filtered {
		return items, inactive
	}
	return active, inactive
}

func appendItemErrorCompletion(item preparedSend, err error) appendItemCompletion {
	return appendItemCompletion{
		item:     item,
		result:   SendBatchItemResult{Err: err},
		traceErr: err,
	}
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
		appended := res.Items[i]
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

func appendCompletionsResultClass(items []appendItemCompletion) string {
	if len(items) == 0 {
		return channelAppendResultOK
	}
	class := ""
	for _, item := range items {
		next := resultClass(item.result)
		if class == "" {
			class = next
			continue
		}
		if class != next {
			return channelAppendResultMixed
		}
	}
	return class
}

func appendBatchContext(runtimeCtx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if runtimeCtx != nil {
		base = runtimeCtx
	}
	return context.WithCancel(base)
}

func appendItemError(item preparedSend) error {
	if item.Context != nil {
		if err := item.Context.Err(); err != nil {
			return err
		}
	}
	if !item.Deadline.IsZero() && !item.Deadline.After(time.Now()) {
		return context.DeadlineExceeded
	}
	return nil
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
	setting := cmd.Setting
	topic := cmd.Topic
	expire := cmd.Expire
	if appendMessageReturned(appended.Message) {
		setting = appended.Message.Setting
		topic = appended.Message.Topic
		expire = appended.Message.Expire
	}
	return CommittedEnvelope{
		MessageID:         appended.MessageID,
		MessageSeq:        appended.MessageSeq,
		ChannelID:         cmd.ChannelID,
		ChannelType:       cmd.ChannelType,
		FromUID:           cmd.FromUID,
		SenderNodeID:      cmd.SenderNodeID,
		SenderSessionID:   cmd.SenderSessionID,
		Setting:           setting,
		Topic:             topic,
		Expire:            expire,
		ClientMsgNo:       cmd.ClientMsgNo,
		ServerTimestampMS: serverTimestampMS,
		Payload:           cloneBytes(payload),
		RedDot:            cmd.RedDot,
		SyncOnce:          cmd.SyncOnce,
		MessageScopedUIDs: append([]string(nil), cmd.MessageScopedUIDs...),
	}
}

func appendMessageReturned(msg Message) bool {
	return msg.MessageID != 0 ||
		msg.MessageSeq != 0 ||
		msg.ChannelID != "" ||
		msg.ChannelType != 0 ||
		msg.Setting != 0 ||
		msg.Topic != "" ||
		msg.Expire != 0 ||
		msg.FromUID != "" ||
		msg.ClientMsgNo != "" ||
		msg.TraceID != "" ||
		msg.ChannelKey != "" ||
		len(msg.Payload) != 0 ||
		msg.SyncOnce ||
		msg.ServerTimestampMS != 0
}

func observeAppendCompletion(observer AppendObserver, completion appendItemCompletion, dur time.Duration) {
	if observer == nil {
		return
	}
	observer.AppendFinished(appendMetricPathChannelPlane, completion.traceErr, dur)
}

func appendTraceMessageSeq(completion appendItemCompletion) uint64 {
	if completion.appended.MessageSeq != 0 {
		return completion.appended.MessageSeq
	}
	return completion.result.Result.MessageSeq
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
