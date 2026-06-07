package message

import (
	"context"
	"errors"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
)

const channelTypePerson uint8 = 1

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
	results, prepared := a.prepareBatch(items)
	a.appendChannelSegments(groupPreparedByChannel(prepared), results)
	return results
}

func (a *App) prepareBatch(items []SendBatchItem) ([]SendBatchItemResult, []preparedSend) {
	results := make([]SendBatchItemResult, len(items))
	prepared := make([]preparedSend, 0, len(items))

	for i, item := range items {
		ctx := item.Context
		if ctx == nil {
			ctx = context.Background()
		}
		next, done := a.prepareSend(ctx, item.Command)
		next.index = i
		next.ctx = ctx
		next.deadline = item.Deadline
		if done {
			results[i] = SendBatchItemResult{Result: next.result, Err: next.err}
			continue
		}
		prepared = append(prepared, next)
	}
	return results, prepared
}

// preparedSend is one validated send item ready to enter durable append.
type preparedSend struct {
	index    int
	ctx      context.Context
	deadline time.Time
	cmd      SendCommand
	result   SendResult
	err      error
}

func (a *App) prepareSend(ctx context.Context, cmd SendCommand) (preparedSend, bool) {
	if cmd.FromUID == "" {
		return preparedSend{result: SendResult{Reason: ReasonAuthFail}}, true
	}
	if cmd.RequestScoped || (len(cmd.MessageScopedUIDs) > 0 && cmd.ChannelID == "") {
		return a.prepareRequestScopedSend(ctx, cmd)
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

func (a *App) prepareRequestScopedSend(ctx context.Context, cmd SendCommand) (preparedSend, bool) {
	if len(cmd.Payload) == 0 {
		return preparedSend{result: SendResult{Reason: ReasonInvalidRequest}}, true
	}
	if !cmd.SyncOnce {
		return preparedSend{err: ErrRequestSubscribersRequireSyncOnce}, true
	}
	if cmd.ChannelID != "" {
		return preparedSend{err: ErrRequestSubscribersConflictChannel}, true
	}
	scoped, err := runtimechannelid.RequestSubscriberChannelFor(cmd.MessageScopedUIDs)
	if err != nil {
		if errors.Is(err, runtimechannelid.ErrRequestSubscribersRequired) {
			return preparedSend{err: ErrRequestSubscribersRequired}, true
		}
		return preparedSend{err: err}, true
	}
	cmd.ChannelID = scoped.CommandChannelID
	cmd.ChannelType = scoped.ChannelType
	cmd.MessageScopedUIDs = scoped.Subscribers
	cmd.NormalizePersonChannel = false
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

// channelAppendSegment preserves original item order for one canonical channel.
type channelAppendSegment struct {
	channel ChannelID
	items   []preparedSend
}

func groupPreparedByChannel(items []preparedSend) []channelAppendSegment {
	if len(items) == 0 {
		return nil
	}
	segments := make([]channelAppendSegment, 0, len(items))
	indexes := make(map[ChannelID]int, len(items))
	for _, item := range items {
		channel := ChannelID{ID: item.cmd.ChannelID, Type: item.cmd.ChannelType}
		if index, ok := indexes[channel]; ok {
			segments[index].items = append(segments[index].items, item)
			continue
		}
		indexes[channel] = len(segments)
		segments = append(segments, channelAppendSegment{channel: channel, items: []preparedSend{item}})
	}
	return segments
}

func (a *App) appendChannelSegments(segments []channelAppendSegment, results []SendBatchItemResult) {
	for _, segment := range segments {
		a.appendSegment(segment, results)
	}
}
