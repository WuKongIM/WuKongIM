package message

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
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
	for _, segment := range splitSegments(prepared) {
		a.appendSegment(segment, results)
	}
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

func splitSegments(items []preparedSend) []segment {
	if len(items) == 0 {
		return nil
	}
	out := make([]segment, 0, 1)
	start := 0
	current := ChannelID{ID: items[0].cmd.ChannelID, Type: items[0].cmd.ChannelType}
	for i := 1; i < len(items); i++ {
		channel := ChannelID{ID: items[i].cmd.ChannelID, Type: items[i].cmd.ChannelType}
		if channel == current {
			continue
		}
		out = append(out, segment{channel: current, items: items[start:i]})
		start = i
		current = channel
	}
	out = append(out, segment{channel: current, items: items[start:]})
	return out
}

func (a *App) appendSegment(segment segment, results []SendBatchItemResult) {
	active := activeSegmentItems(segment, results)
	if len(active) == 0 {
		return
	}
	req := AppendBatchRequest{
		ChannelID:         segment.channel,
		Messages:          make([]Message, 0, len(active)),
		CommitMode:        CommitModeQuorum,
		OmitResultPayload: a == nil || a.committed == nil,
	}
	for _, item := range active {
		req.Messages = append(req.Messages, Message{
			MessageID:   item.cmd.MessageID,
			ChannelID:   item.cmd.ChannelID,
			ChannelType: item.cmd.ChannelType,
			FromUID:     item.cmd.FromUID,
			ClientMsgNo: item.cmd.ClientMsgNo,
			Payload:     cloneBytes(item.cmd.Payload),
		})
	}
	ctx, cancel := segmentContext(active)
	defer cancel()
	res, err := a.appender.AppendBatch(ctx, req)
	if err != nil {
		for _, item := range active {
			results[item.index] = SendBatchItemResult{Err: err}
		}
		return
	}
	for i, item := range active {
		if i >= len(res.Items) {
			results[item.index] = SendBatchItemResult{Err: ErrAppendFailed}
			continue
		}
		appended := res.Items[i]
		if appended.Err != nil {
			results[item.index] = SendBatchItemResult{Result: SendResult{Reason: reasonForAppendError(appended.Err)}}
			continue
		}
		result := SendResult{MessageID: appended.MessageID, MessageSeq: appended.MessageSeq, Reason: ReasonSuccess}
		a.submitCommitted(item.ctx, item.cmd, appended)
		results[item.index] = SendBatchItemResult{Result: result}
	}
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
		MessageID:       appended.MessageID,
		MessageSeq:      appended.MessageSeq,
		ChannelID:       cmd.ChannelID,
		ChannelType:     cmd.ChannelType,
		FromUID:         cmd.FromUID,
		SenderSessionID: cmd.SenderSessionID,
		ClientMsgNo:     cmd.ClientMsgNo,
		Payload:         cloneBytes(appended.Message.Payload),
		RedDot:          cmd.RedDot,
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
