package message

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const appendRetryInitialBackoff = 5 * time.Millisecond
const appendRetryMaxBackoff = 100 * time.Millisecond
const appendInitialAttempt = 1

const appendMetricPathChannelPlane = "channelplane"

func (a *App) appendSegment(segment channelAppendSegment, results []SendBatchItemResult) {
	active := activeSegmentItems(segment, results)
	if len(active) == 0 {
		return
	}

	backoff := appendRetryInitialBackoff
	attempt := appendInitialAttempt
	for {
		req := a.appendRequest(segment.channel, active, attempt)
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
					attempt++
					continue
				}
			}
			a.finishBatchAppendError(active, results, err, appendDur)
			return
		}

		a.finishBatchAppendResult(active, results, res, appendDur)
		return
	}
}

func (a *App) finishBatchAppendError(items []preparedSend, results []SendBatchItemResult, err error, appendDur time.Duration) {
	for _, item := range items {
		a.observeAppend(appendMetricPathChannelPlane, err, appendDur)
		recordAppendDurableTrace(item, 0, err, appendDur)
		results[item.index] = SendBatchItemResult{Err: err}
	}
}

func (a *App) finishBatchAppendResult(items []preparedSend, results []SendBatchItemResult, res AppendBatchResult, appendDur time.Duration) {
	for i, item := range items {
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
}

func (a *App) appendRequest(channel ChannelID, active []preparedSend, attempt int) AppendBatchRequest {
	if attempt <= 0 {
		attempt = appendInitialAttempt
	}
	req := AppendBatchRequest{
		ChannelID:         channel,
		Messages:          make([]Message, 0, len(active)),
		Attempt:           attempt,
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

func activeSegmentItems(segment channelAppendSegment, results []SendBatchItemResult) []preparedSend {
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
