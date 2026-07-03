package message

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/runtime/userlimit"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// Send processes one message send command and returns the client-facing result.
func (a *App) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	results := a.SendBatch([]SendBatchItem{{Context: ctx, Command: cmd}})
	if len(results) == 0 {
		return SendResult{}, nil
	}
	return results[0].Result, results[0].Err
}

// SendBatch processes multiple send commands while preserving item-aligned results.
func (a *App) SendBatch(items []SendBatchItem) []SendBatchItemResult {
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
		if done {
			results[i] = SendBatchItemResult{Result: next.result, Err: next.err}
			continue
		}
		prepared = append(prepared, next)
	}
	for _, segment := range splitDurableSendSegments(prepared) {
		a.sendDurableSegment(segment, results)
	}
	return results
}

type preparedSend struct {
	index int
	ctx   context.Context
	cmd   SendCommand

	result SendResult
	err    error
}

type durableSendSegmentKey struct {
	channelID             string
	channelType           uint8
	commitMode            channel.CommitMode
	expectedChannelEpoch  uint64
	expectedLeaderEpoch   uint64
	supportsMessageSeqU64 bool
}

type durableSendSegment struct {
	key   durableSendSegmentKey
	items []preparedSend
}

func (a *App) prepareSend(ctx context.Context, cmd SendCommand) (preparedSend, bool) {
	if cmd.FromUID == "" {
		fields := append([]wklog.Field{
			wklog.Event("message.send.unauthenticated.rejected"),
		}, messageLogFields(channel.ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType}, cmd.FromUID)...)
		fields = append(fields, wklog.Error(ErrUnauthenticatedSender))
		a.sendLogger().Warn("reject unauthenticated sender", fields...)
		return preparedSend{result: SendResult{}, err: ErrUnauthenticatedSender}, true
	}
	if result, limited := a.checkUserSendLimit(cmd); limited {
		return preparedSend{result: result}, true
	}

	if len(cmd.RequestSubscribers) > 0 {
		result, err := a.sendRequestScoped(ctx, cmd)
		return preparedSend{result: result, err: err}, true
	}

	if !isSupportedSendChannelType(cmd.ChannelType) {
		return preparedSend{result: SendResult{Reason: frame.ReasonNotSupportChannelType}}, true
	}

	sourceChannelID, alreadyCommandChannel := runtimechannelid.FromCommandChannel(cmd.ChannelID)
	cmd.ChannelID = sourceChannelID

	if cmd.ChannelType == frame.ChannelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return preparedSend{err: err}, true
		}
		cmd.ChannelID = channelID
	} else if cmd.ChannelType == frame.ChannelTypeAgent {
		channelID, err := normalizeAgentSendChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return preparedSend{err: err}, true
		}
		cmd.ChannelID = channelID
	}

	reason, err := a.checkSendPermission(ctx, cmd)
	if err != nil {
		if reason != 0 && reason != frame.ReasonSuccess {
			return preparedSend{result: SendResult{Reason: reason}, err: err}, true
		}
		return preparedSend{err: err}, true
	}
	if reason != frame.ReasonSuccess {
		return preparedSend{result: SendResult{Reason: reason}}, true
	}

	cmd, hookResult, err := a.beforeSendHook(ctx, cmd)
	if err != nil || hookResult.Reason != frame.ReasonSuccess {
		return preparedSend{result: hookResult, err: err}, true
	}

	if cmd.Framer.NoPersist {
		if cmd.Framer.SyncOnce || alreadyCommandChannel {
			realtimeCmd := cmd
			realtimeCmd.ChannelID = runtimechannelid.ToCommandChannel(cmd.ChannelID)
			result, err := a.sendRealtime(ctx, realtimeCmd, nil)
			return preparedSend{result: result, err: err}, true
		}
		return preparedSend{result: SendResult{Reason: frame.ReasonSuccess}}, true
	}

	appendCmd := cmd
	if cmd.Framer.SyncOnce || alreadyCommandChannel {
		appendCmd.ChannelID = runtimechannelid.ToCommandChannel(cmd.ChannelID)
	}

	if a.appender == nil {
		fields := append([]wklog.Field{
			wklog.Event("message.send.channel_appender.required"),
		}, messageLogFields(channel.ChannelID{ID: appendCmd.ChannelID, Type: appendCmd.ChannelType}, appendCmd.FromUID)...)
		fields = append(fields, wklog.Error(ErrChannelAppenderRequired))
		a.sendLogger().Error("message channel appender is required", fields...)
		return preparedSend{err: ErrChannelAppenderRequired}, true
	}

	return preparedSend{ctx: ctx, cmd: appendCmd}, false
}

func splitDurableSendSegments(items []preparedSend) []durableSendSegment {
	segments := make([]durableSendSegment, 0, len(items))
	for _, item := range items {
		key := durableSegmentKey(item.cmd)
		if len(segments) > 0 && segments[len(segments)-1].key == key {
			segments[len(segments)-1].items = append(segments[len(segments)-1].items, item)
			continue
		}
		segments = append(segments, durableSendSegment{key: key, items: []preparedSend{item}})
	}
	return segments
}

func durableSegmentKey(cmd SendCommand) durableSendSegmentKey {
	return durableSendSegmentKey{
		channelID:             cmd.ChannelID,
		channelType:           cmd.ChannelType,
		commitMode:            commitModeOrDefault(cmd.CommitMode),
		expectedChannelEpoch:  cmd.ExpectedChannelEpoch,
		expectedLeaderEpoch:   cmd.ExpectedLeaderEpoch,
		supportsMessageSeqU64: supportsMessageSeqU64(cmd.ProtocolVersion),
	}
}

func (a *App) checkUserSendLimit(cmd SendCommand) (SendResult, bool) {
	if a == nil || a.userSendLimiter == nil {
		return SendResult{}, false
	}
	origin := cmd.Origin
	if origin == "" {
		origin = SendOriginClient
	}
	decision := a.userSendLimiter.AllowSend(a.now(), userlimit.Request{
		UID:         cmd.FromUID,
		ChannelID:   cmd.ChannelID,
		ChannelType: cmd.ChannelType,
		Origin:      string(origin),
		IsSystemUID: a.isSystemUID(cmd.FromUID),
	})
	if decision.Allowed {
		return SendResult{}, false
	}
	return SendResult{Reason: frame.ReasonRateLimit}, true
}

func (a *App) isSystemUID(uid string) bool {
	return a != nil && a.systemUIDs != nil && a.systemUIDs.IsSystemUID(uid)
}

func (a *App) beforeSendHook(ctx context.Context, cmd SendCommand) (SendCommand, SendResult, error) {
	if cmd.Origin == "" {
		cmd.Origin = SendOriginClient
	}
	if cmd.SkipPluginHooks || a.sendHook == nil {
		return cmd, SendResult{Reason: frame.ReasonSuccess}, nil
	}
	if cmd.Origin == SendOriginPlugin {
		if cmd.HookDepth >= DefaultPluginSendMaxHookDepth {
			return cmd, SendResult{}, ErrSendHookDepthExceeded
		}
		cmd.HookDepth++
	}
	mutated, reason, err := a.sendHook.BeforeSend(ctx, cmd)
	if err != nil {
		if reason != 0 && reason != frame.ReasonSuccess {
			return mutated, SendResult{Reason: reason}, err
		}
		return mutated, SendResult{}, err
	}
	if reason != 0 && reason != frame.ReasonSuccess {
		return mutated, SendResult{Reason: reason}, nil
	}
	return mutated, SendResult{Reason: frame.ReasonSuccess}, nil
}

// sendRequestScoped routes a one-shot subscriber list through an internal temp command channel.
func (a *App) sendRequestScoped(ctx context.Context, cmd SendCommand) (SendResult, error) {
	if !cmd.Framer.SyncOnce {
		return SendResult{}, ErrRequestSubscribersRequireSyncOnce
	}
	if cmd.ChannelID != "" {
		return SendResult{}, ErrRequestSubscribersConflictChannel
	}
	scoped, err := runtimechannelid.RequestSubscriberChannelFor(cmd.RequestSubscribers)
	if err != nil {
		if errors.Is(err, runtimechannelid.ErrRequestSubscribersRequired) {
			return SendResult{}, ErrRequestSubscribersRequired
		}
		return SendResult{}, err
	}

	scopedCmd := cmd
	scopedCmd.ChannelID = scoped.CommandChannelID
	scopedCmd.ChannelType = scoped.ChannelType
	scopedCmd.RequestSubscribers = scoped.Subscribers
	scopedCmd, hookResult, err := a.beforeSendHook(ctx, scopedCmd)
	if err != nil || hookResult.Reason != frame.ReasonSuccess {
		return hookResult, err
	}
	scopedCmd.ChannelID = scoped.CommandChannelID
	scopedCmd.ChannelType = scoped.ChannelType
	scopedCmd.RequestSubscribers = scoped.Subscribers
	if scopedCmd.Framer.NoPersist {
		return a.sendRequestScopedRealtime(ctx, scopedCmd)
	}

	if a.dispatcher == nil {
		return SendResult{}, ErrCommittedDispatcherRequired
	}
	if a.appender == nil {
		channelID := channel.ChannelID{ID: scopedCmd.ChannelID, Type: scopedCmd.ChannelType}
		fields := append([]wklog.Field{
			wklog.Event("message.send.channel_appender.required"),
		}, messageLogFields(channelID, scopedCmd.FromUID)...)
		fields = append(fields, wklog.Error(ErrChannelAppenderRequired))
		a.sendLogger().Error("message channel appender is required", fields...)
		return SendResult{}, ErrChannelAppenderRequired
	}

	return a.sendDurable(ctx, scopedCmd)
}

func (a *App) sendRequestScopedRealtime(ctx context.Context, cmd SendCommand) (SendResult, error) {
	return a.sendRealtime(ctx, cmd, cmd.RequestSubscribers)
}

// sendRealtime dispatches a transient command message without writing the channel log.
func (a *App) sendRealtime(ctx context.Context, cmd SendCommand, messageScopedUIDs []string) (SendResult, error) {
	if a.messageIDs == nil {
		return SendResult{}, ErrMessageIDGeneratorRequired
	}
	if a.realtime == nil {
		return SendResult{}, ErrRealtimeDispatcherRequired
	}
	msg := buildDurableMessage(cmd, a.now())
	msg.MessageID = a.messageIDs.Next()
	msg.MessageSeq = 0
	if err := a.realtime.SubmitRealtime(ctx, messageevents.MessageRealtime{
		Message:           msg,
		SenderSessionID:   cmd.SenderSessionID,
		MessageScopedUIDs: append([]string(nil), messageScopedUIDs...),
	}); err != nil {
		return SendResult{}, err
	}
	return SendResult{
		MessageID:  int64(msg.MessageID),
		MessageSeq: 0,
		Reason:     frame.ReasonSuccess,
	}, nil
}

func (a *App) sendDurable(ctx context.Context, cmd SendCommand) (SendResult, error) {
	results := make([]SendBatchItemResult, 1)
	a.sendDurableSegment(durableSendSegment{
		key: durableSegmentKey(cmd),
		items: []preparedSend{{
			index: 0,
			ctx:   ctx,
			cmd:   cmd,
		}},
	}, results)
	return results[0].Result, results[0].Err
}

func (a *App) sendDurableSegment(segment durableSendSegment, results []SendBatchItemResult) {
	if len(segment.items) == 0 {
		return
	}
	active := make([]preparedSend, 0, len(segment.items))
	for _, item := range segment.items {
		if err := item.ctx.Err(); err != nil {
			results[item.index] = SendBatchItemResult{Err: err}
			continue
		}
		active = append(active, item)
	}
	if len(active) == 0 {
		return
	}

	channelID := channel.ChannelID{ID: segment.key.channelID, Type: segment.key.channelType}
	messages := make([]channel.Message, 0, len(active))
	for _, item := range active {
		messages = append(messages, buildDurableMessage(item.cmd, a.now()))
	}
	startedAt := a.now()
	segmentCtx, cancel := segmentContext(active)
	defer cancel()
	appendReq := channel.AppendBatchRequest{
		ChannelID:             channelID,
		Messages:              messages,
		SupportsMessageSeqU64: segment.key.supportsMessageSeqU64,
		CommitMode:            segment.key.commitMode,
		ExpectedChannelEpoch:  segment.key.expectedChannelEpoch,
		ExpectedLeaderEpoch:   segment.key.expectedLeaderEpoch,
		TraceID:               active[0].cmd.TraceID,
		Attempt:               0,
	}
	appendResult, err := a.appendDurableBatch(segmentCtx, appendReq)
	duration := sendtrace.Elapsed(startedAt, a.now())
	if err != nil {
		for _, item := range active {
			sendtrace.Record(sendtrace.Event{
				TraceID:     item.cmd.TraceID,
				Stage:       sendtrace.StageMessageSendDurable,
				At:          startedAt,
				Duration:    duration,
				NodeID:      a.localNodeID,
				ChannelKey:  string(channelhandler.KeyFromChannelID(channelID)),
				ClientMsgNo: item.cmd.ClientMsgNo,
				FromUID:     item.cmd.FromUID,
			})
			results[item.index] = SendBatchItemResult{Err: err}
		}
		return
	}
	for i, item := range active {
		if i >= len(appendResult.Items) {
			results[item.index] = SendBatchItemResult{Err: errors.New("message: append batch result count mismatch")}
			continue
		}
		itemResult := appendResult.Items[i]
		sendtrace.Record(sendtrace.Event{
			TraceID:     item.cmd.TraceID,
			Stage:       sendtrace.StageMessageSendDurable,
			At:          startedAt,
			Duration:    duration,
			NodeID:      a.localNodeID,
			ChannelKey:  string(channelhandler.KeyFromChannelID(channelID)),
			ClientMsgNo: item.cmd.ClientMsgNo,
			MessageSeq:  itemResult.MessageSeq,
			FromUID:     item.cmd.FromUID,
		})
		if itemResult.Err != nil {
			results[item.index] = SendBatchItemResult{Err: itemResult.Err}
			continue
		}
		sendResult := SendResult{
			MessageID:  int64(itemResult.MessageID),
			MessageSeq: itemResult.MessageSeq,
			Reason:     frame.ReasonSuccess,
		}
		intentSubmitted := a.submitRequestScopedCMDConversationIntent(item.ctx, itemResult.Message, item.cmd.RequestSubscribers)
		if a.dispatcher != nil {
			if err := a.dispatcher.SubmitCommitted(item.ctx, messageevents.MessageCommitted{
				Message:                        itemResult.Message,
				SenderSessionID:                item.cmd.SenderSessionID,
				MessageScopedUIDs:              append([]string(nil), item.cmd.RequestSubscribers...),
				CMDConversationIntentSubmitted: intentSubmitted,
			}); err != nil {
				fields := append([]wklog.Field{
					wklog.Event("message.send.dispatch_submit.failed"),
				}, messageLogFields(channelID, item.cmd.FromUID)...)
				fields = append(fields, wklog.Error(err))
				a.sendLogger().Warn("submit committed message failed", fields...)
				if len(item.cmd.RequestSubscribers) > 0 {
					results[item.index] = SendBatchItemResult{Err: err}
					continue
				}
			}
		}
		results[item.index] = SendBatchItemResult{Result: sendResult}
	}
}

func (a *App) appendDurableBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	if a == nil || a.appender == nil {
		return channel.AppendBatchResult{}, ErrChannelAppenderRequired
	}
	startedAt := time.Now()
	result, err := a.appender.AppendBatch(ctx, req)
	if err != nil {
		fields := append([]wklog.Field{
			wklog.Event("message.send.append_batch.failed"),
		}, messageLogFields(req.ChannelID, appendBatchFromUID(req))...)
		fields = append(fields, wklog.Error(err))
		a.sendLogger().Error("append batch failed", fields...)
	}
	observeMessageAppend(a.appendMetrics, "channelplane", appendMetricResult(err), time.Since(startedAt))
	return result, err
}

func appendBatchFromUID(req channel.AppendBatchRequest) string {
	if len(req.Messages) == 0 {
		return ""
	}
	return req.Messages[0].FromUID
}

type messageAppendMetrics interface {
	ObserveAppend(path, result string, dur time.Duration)
}

func observeMessageAppend(metrics messageAppendMetrics, path, result string, dur time.Duration) {
	if metrics == nil {
		return
	}
	metrics.ObserveAppend(path, result, dur)
}

func appendMetricResult(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func segmentContext(items []preparedSend) (context.Context, context.CancelFunc) {
	if len(items) == 1 && items[0].ctx != nil {
		return items[0].ctx, func() {}
	}
	var earliest time.Time
	for _, item := range items {
		if deadline, ok := item.ctx.Deadline(); ok && (earliest.IsZero() || deadline.Before(earliest)) {
			earliest = deadline
		}
	}
	if earliest.IsZero() {
		return context.Background(), func() {}
	}
	return context.WithDeadline(context.Background(), earliest)
}

func (a *App) submitRequestScopedCMDConversationIntent(ctx context.Context, msg channel.Message, subscribers []string) bool {
	if a == nil || a.cmdConversationIntents == nil || len(subscribers) == 0 {
		return false
	}
	intent, ok := cmdsync.BuildConversationIntent(msg, subscribers, a.now)
	if !ok {
		return false
	}
	accepted, err := a.cmdConversationIntents.PushIntent(ctx, intent)
	if err != nil {
		fields := append([]wklog.Field{
			wklog.Event("message.send.cmd_conversation_intent.failed"),
		}, messageLogFields(channel.ChannelID{ID: msg.ChannelID, Type: msg.ChannelType}, msg.FromUID)...)
		fields = append(fields, wklog.Error(err))
		a.sendLogger().Warn("submit cmd conversation intent failed", fields...)
		return false
	}
	return accepted
}

func buildDurableMessage(cmd SendCommand, now time.Time) channel.Message {
	return channel.Message{
		Framer:      cmd.Framer,
		Setting:     cmd.Setting,
		MsgKey:      cmd.MsgKey,
		Expire:      cmd.Expire,
		ClientSeq:   cmd.ClientSeq,
		ClientMsgNo: cmd.ClientMsgNo,
		StreamNo:    cmd.StreamNo,
		Timestamp:   int32(now.Unix()),
		ChannelID:   cmd.ChannelID,
		ChannelType: cmd.ChannelType,
		Topic:       cmd.Topic,
		FromUID:     cmd.FromUID,
		Payload:     append([]byte(nil), cmd.Payload...),
	}
}

func supportsMessageSeqU64(version uint8) bool {
	return version == 0 || version > frame.LegacyMessageSeqVersion
}

func commitModeOrDefault(mode channel.CommitMode) channel.CommitMode {
	if mode == 0 {
		return channel.CommitModeQuorum
	}
	return mode
}

func isSupportedSendChannelType(channelType uint8) bool {
	switch channelType {
	case frame.ChannelTypePerson,
		frame.ChannelTypeGroup,
		frame.ChannelTypeCustomerService,
		frame.ChannelTypeInfo,
		frame.ChannelTypeVisitors,
		frame.ChannelTypeAgent:
		return true
	default:
		return false
	}
}

func normalizeAgentSendChannel(senderUID, channelID string) (string, error) {
	if senderUID == "" || channelID == "" {
		return "", runtimechannelid.ErrInvalidAgentChannel
	}
	if !strings.Contains(channelID, "@") {
		return runtimechannelid.EncodeAgentChannel(senderUID, channelID), nil
	}
	uid, agentUID, err := runtimechannelid.DecodeAgentChannel(channelID)
	if err != nil {
		return "", err
	}
	return runtimechannelid.EncodeAgentChannel(uid, agentUID), nil
}

func messageLogFields(channelID channel.ChannelID, uid string) []wklog.Field {
	return []wklog.Field{
		wklog.ChannelID(channelID.ID),
		wklog.ChannelType(int64(channelID.Type)),
		wklog.UID(uid),
	}
}
