package message

import (
	"context"
	"errors"
	"sync"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const channelTypePerson uint8 = 1

// maxConcurrentAppendSegments bounds channel-level append fanout inside one gateway batch.
const maxConcurrentAppendSegments = 64

const appendRetryInitialBackoff = 5 * time.Millisecond
const appendRetryMaxBackoff = 100 * time.Millisecond

const appendMetricPathChannelPlane = "channelplane"

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

// Send processes one send command.
func (a *App) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	results := a.SendBatch([]SendBatchItem{{Context: ctx, Command: cmd}})
	if len(results) == 0 {
		return SendResult{}, nil
	}
	return results[0].Result, results[0].Err
}

// SendBatch processes send commands and returns item-aligned results.
func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(items))
	prepared := make([]preparedSend, 0, len(items))
	for i, item := range items {
		ctx := item.Context
		if ctx == nil {
			ctx = context.Background()
		}
		next, done := a.prepare(ctx, item.Command)
		next.index = i
		next.ctx = ctx
		next.deadline = item.Deadline
		if done {
			results[i] = SendBatchItemResult{Result: next.result, Err: next.err}
			continue
		}
		prepared = append(prepared, next)
	}
	a.appendSegments(groupSegmentsByChannel(prepared), results)
	return results
}

type preparedSend struct {
	index    int
	ctx      context.Context
	deadline time.Time
	cmd      SendCommand
	result   SendResult
	err      error
}

func (a *App) prepare(ctx context.Context, cmd SendCommand) (preparedSend, bool) {
	if cmd.FromUID == "" {
		return preparedSend{result: SendResult{Reason: ReasonAuthFail}}, true
	}
	if cmd.ChannelID == "" || cmd.ChannelType == 0 || len(cmd.Payload) == 0 {
		return preparedSend{result: SendResult{Reason: ReasonInvalidRequest}}, true
	}
	if cmd.NoPersist {
		return preparedSend{result: SendResult{Reason: ReasonUnsupported}}, true
	}
	if err := ctx.Err(); err != nil {
		return preparedSend{err: err}, true
	}
	if a == nil || a.appender == nil {
		return preparedSend{err: ErrAppenderRequired}, true
	}
	decision, err := a.authorizer.AuthorizeSend(ctx, cmd)
	if err != nil {
		return preparedSend{err: err}, true
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == ReasonSuccess {
			reason = ReasonInvalidRequest
		}
		return preparedSend{result: SendResult{Reason: reason}}, true
	}
	if cmd.NormalizePersonChannel && cmd.ChannelType == channelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return preparedSend{err: err}, true
		}
		cmd.ChannelID = channelID
	}
	if cmd.MessageID == 0 {
		if a.messageID == nil {
			return preparedSend{err: ErrMessageIDAllocatorRequired}, true
		}
		cmd.MessageID = a.messageID.Next()
	}
	return preparedSend{cmd: cmd}, false
}

type segment struct {
	channel ChannelID
	items   []preparedSend
}

func groupSegmentsByChannel(items []preparedSend) []segment {
	if len(items) == 0 {
		return nil
	}
	out := make([]segment, 0, len(items))
	indexes := make(map[ChannelID]int, len(items))
	for _, item := range items {
		channel := ChannelID{ID: item.cmd.ChannelID, Type: item.cmd.ChannelType}
		if index, ok := indexes[channel]; ok {
			out[index].items = append(out[index].items, item)
			continue
		}
		indexes[channel] = len(out)
		out = append(out, segment{channel: channel, items: []preparedSend{item}})
	}
	return out
}

func (a *App) appendSegments(segments []segment, results []SendBatchItemResult) {
	switch len(segments) {
	case 0:
		return
	case 1:
		a.appendSegment(segments[0], results)
		return
	}
	workers := maxConcurrentAppendSegments
	if workers > len(segments) {
		workers = len(segments)
	}
	tasks := make(chan segment)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for segment := range tasks {
				a.appendSegment(segment, results)
			}
		}()
	}
	for _, segment := range segments {
		tasks <- segment
	}
	close(tasks)
	wg.Wait()
}

func (a *App) appendSegment(segment segment, results []SendBatchItemResult) {
	active := activeSegmentItems(segment, results)
	if len(active) == 0 {
		return
	}
	backoff := appendRetryInitialBackoff
	for {
		req := a.appendRequest(segment.channel, active)
		ctx, cancel := segmentContext(active)
		measureAppendDur := a.needsAppendDuration(active)
		var startedAt time.Time
		if measureAppendDur {
			startedAt = time.Now()
		}
		res, err := a.appender.AppendBatch(ctx, req)
		var appendDur time.Duration
		if measureAppendDur {
			appendDur = sendtrace.Elapsed(startedAt, time.Now())
		}
		cancel()
		if err != nil {
			if retryableAppendBatchError(err) {
				nextBackoff, ok := waitBeforeAppendRetry(active, backoff)
				if ok {
					backoff = nextBackoff
					continue
				}
			}
			for _, item := range active {
				a.observeAppend(appendMetricPathChannelPlane, err, appendDur)
				recordAppendDurableTrace(item, 0, err, appendDur)
				results[item.index] = SendBatchItemResult{Err: err}
			}
			return
		}
		for i, item := range active {
			if i >= len(res.Items) {
				a.observeAppend(appendMetricPathChannelPlane, ErrAppendResultMissing, appendDur)
				recordAppendDurableTrace(item, 0, ErrAppendResultMissing, appendDur)
				results[item.index] = SendBatchItemResult{Err: ErrAppendResultMissing}
				continue
			}
			appended := res.Items[i]
			a.observeAppend(appendMetricPathChannelPlane, appended.Err, appendDur)
			if appended.Err != nil {
				recordAppendDurableTrace(item, appended.MessageSeq, appended.Err, appendDur)
				results[item.index] = SendBatchItemResult{Result: SendResult{Reason: reasonForAppendError(appended.Err)}}
				continue
			}
			recordAppendDurableTrace(item, appended.MessageSeq, nil, appendDur)
			result := SendResult{MessageID: appended.MessageID, MessageSeq: appended.MessageSeq, Reason: ReasonSuccess}
			a.submitCommitted(item.ctx, item.cmd, appended)
			results[item.index] = SendBatchItemResult{Result: result}
		}
		return
	}
}

func (a *App) appendRequest(channel ChannelID, active []preparedSend) AppendBatchRequest {
	req := AppendBatchRequest{
		ChannelID:         channel,
		Messages:          make([]Message, 0, len(active)),
		CommitMode:        CommitModeQuorum,
		OmitResultPayload: a == nil || a.committed == nil,
	}
	for _, item := range active {
		if req.TraceID == "" && item.cmd.TraceID != "" {
			req.TraceID = item.cmd.TraceID
		}
		if req.ChannelKey == "" && item.cmd.ChannelKey != "" {
			req.ChannelKey = item.cmd.ChannelKey
		}
		req.Messages = append(req.Messages, Message{
			MessageID:   item.cmd.MessageID,
			ChannelID:   item.cmd.ChannelID,
			ChannelType: item.cmd.ChannelType,
			FromUID:     item.cmd.FromUID,
			ClientMsgNo: item.cmd.ClientMsgNo,
			TraceID:     item.cmd.TraceID,
			ChannelKey:  item.cmd.ChannelKey,
			Payload:     cloneBytes(item.cmd.Payload),
		})
	}
	return req
}

func retryableAppendBatchError(err error) bool {
	return errors.Is(err, ErrRouteNotReady) || errors.Is(err, ErrNotLeader) || errors.Is(err, ErrStaleRoute)
}

func (a *App) needsAppendDuration(items []preparedSend) bool {
	if a.hasAppendObserver() {
		return true
	}
	for _, item := range items {
		if item.cmd.TraceID != "" && sendtrace.Enabled() {
			return true
		}
	}
	return false
}

func (a *App) hasAppendObserver() bool {
	if a == nil || a.observer == nil {
		return false
	}
	_, ok := a.observer.(AppendObserver)
	return ok
}

func recordAppendDurableTrace(item preparedSend, messageSeq uint64, err error, duration time.Duration) {
	cmd := item.cmd
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

func waitBeforeAppendRetry(items []preparedSend, backoff time.Duration) (time.Duration, bool) {
	if !segmentHasDeadline(items) {
		return backoff, false
	}
	ctx, cancel := segmentContext(items)
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

func segmentHasDeadline(items []preparedSend) bool {
	for _, item := range items {
		if !item.deadline.IsZero() {
			return true
		}
		if item.ctx != nil {
			if _, ok := item.ctx.Deadline(); ok {
				return true
			}
		}
	}
	return false
}

func (a *App) observeAppend(path string, err error, dur time.Duration) {
	if a == nil || a.observer == nil {
		return
	}
	observer, ok := a.observer.(AppendObserver)
	if !ok {
		return
	}
	observer.AppendFinished(path, err, dur)
}

func activeSegmentItems(segment segment, results []SendBatchItemResult) []preparedSend {
	for i, item := range segment.items {
		if item.ctx == nil {
			continue
		}
		if err := item.ctx.Err(); err != nil {
			results[item.index] = SendBatchItemResult{Err: err}
			active := make([]preparedSend, 0, len(segment.items)-1)
			active = append(active, segment.items[:i]...)
			for _, candidate := range segment.items[i+1:] {
				if candidate.ctx != nil {
					if err := candidate.ctx.Err(); err != nil {
						results[candidate.index] = SendBatchItemResult{Err: err}
						continue
					}
				}
				active = append(active, candidate)
			}
			return active
		}
	}
	return segment.items
}

func segmentContext(items []preparedSend) (context.Context, context.CancelFunc) {
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
		recordDeadline(item.deadline, !item.deadline.IsZero())
		if item.ctx != nil {
			deadline, ok := item.ctx.Deadline()
			recordDeadline(deadline, ok)
		}
	}
	if hasDeadline {
		return context.WithDeadline(context.Background(), earliest)
	}
	return context.WithCancel(context.Background())
}

func (a *App) submitCommitted(ctx context.Context, cmd SendCommand, appended AppendBatchItemResult) {
	if a == nil || a.committed == nil {
		return
	}
	event := messageevents.MessageCommitted{
		MessageID:         appended.MessageID,
		MessageSeq:        appended.MessageSeq,
		ChannelID:         cmd.ChannelID,
		ChannelType:       cmd.ChannelType,
		FromUID:           cmd.FromUID,
		SenderNodeID:      cmd.SenderNodeID,
		SenderSessionID:   cmd.SenderSessionID,
		ClientMsgNo:       cmd.ClientMsgNo,
		Payload:           cloneBytes(appended.Message.Payload),
		RedDot:            cmd.RedDot,
		MessageScopedUIDs: append([]string(nil), cmd.MessageScopedUIDs...),
	}
	if err := a.committed.Submit(ctx, event); err != nil && a.observer != nil {
		a.observer.CommittedSinkError(cmd, err)
	}
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

func cloneAppendRequest(req AppendBatchRequest) AppendBatchRequest {
	req.Messages = append([]Message(nil), req.Messages...)
	for i := range req.Messages {
		req.Messages[i].Payload = cloneBytes(req.Messages[i].Payload)
	}
	return req
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
