package message

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func (a *App) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	if cmd.FromUID == "" {
		fields := append([]wklog.Field{
			wklog.Event("message.send.unauthenticated.rejected"),
		}, messageLogFields(channel.ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType}, cmd.FromUID)...)
		fields = append(fields, wklog.Error(ErrUnauthenticatedSender))
		a.sendLogger().Warn("reject unauthenticated sender", fields...)
		return SendResult{}, ErrUnauthenticatedSender
	}

	if len(cmd.RequestSubscribers) > 0 {
		return a.sendRequestScoped(ctx, cmd)
	}

	if !isSupportedSendChannelType(cmd.ChannelType) {
		return SendResult{Reason: frame.ReasonNotSupportChannelType}, nil
	}

	sourceChannelID, alreadyCommandChannel := runtimechannelid.FromCommandChannel(cmd.ChannelID)
	cmd.ChannelID = sourceChannelID

	if cmd.ChannelType == frame.ChannelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return SendResult{}, err
		}
		cmd.ChannelID = channelID
	} else if cmd.ChannelType == frame.ChannelTypeAgent {
		channelID, err := normalizeAgentSendChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return SendResult{}, err
		}
		cmd.ChannelID = channelID
	}

	reason, err := a.checkSendPermission(ctx, cmd)
	if err != nil {
		if reason != 0 && reason != frame.ReasonSuccess {
			return SendResult{Reason: reason}, err
		}
		return SendResult{}, err
	}
	if reason != frame.ReasonSuccess {
		return SendResult{Reason: reason}, nil
	}

	cmd, hookResult, err := a.beforeSendHook(ctx, cmd)
	if err != nil || hookResult.Reason != frame.ReasonSuccess {
		return hookResult, err
	}

	if cmd.Framer.NoPersist {
		if cmd.Framer.SyncOnce || alreadyCommandChannel {
			realtimeCmd := cmd
			realtimeCmd.ChannelID = runtimechannelid.ToCommandChannel(cmd.ChannelID)
			return a.sendRealtime(ctx, realtimeCmd, nil)
		}
		return SendResult{Reason: frame.ReasonSuccess}, nil
	}

	appendCmd := cmd
	if cmd.Framer.SyncOnce || alreadyCommandChannel {
		appendCmd.ChannelID = runtimechannelid.ToCommandChannel(cmd.ChannelID)
	}

	if a.cluster == nil {
		fields := append([]wklog.Field{
			wklog.Event("message.send.cluster.required"),
		}, messageLogFields(channel.ChannelID{ID: appendCmd.ChannelID, Type: appendCmd.ChannelType}, appendCmd.FromUID)...)
		fields = append(fields, wklog.Error(ErrClusterRequired))
		a.sendLogger().Error("message cluster is required", fields...)
		return SendResult{}, ErrClusterRequired
	}

	return a.sendDurable(ctx, appendCmd)
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
	if scopedCmd.Framer.NoPersist {
		return a.sendRequestScopedRealtime(ctx, scopedCmd)
	}

	if a.dispatcher == nil {
		return SendResult{}, ErrCommittedDispatcherRequired
	}
	if a.cluster == nil {
		channelID := channel.ChannelID{ID: scopedCmd.ChannelID, Type: scopedCmd.ChannelType}
		fields := append([]wklog.Field{
			wklog.Event("message.send.cluster.required"),
		}, messageLogFields(channelID, scopedCmd.FromUID)...)
		fields = append(fields, wklog.Error(ErrClusterRequired))
		a.sendLogger().Error("message cluster is required", fields...)
		return SendResult{}, ErrClusterRequired
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
	draft := buildDurableMessage(cmd, a.now())
	channelID := channel.ChannelID{
		ID:   cmd.ChannelID,
		Type: cmd.ChannelType,
	}
	startedAt := a.now()
	result, err := sendWithEnsuredMeta(ctx, a.localNodeID, a.now, a.sendLogger(),
		a.cluster, a.remoteAppender, a.refresher, a.appendMetrics, channel.AppendRequest{
			ChannelID:             channelID,
			Message:               draft,
			SupportsMessageSeqU64: supportsMessageSeqU64(cmd.ProtocolVersion),
			CommitMode:            commitModeOrDefault(cmd.CommitMode),
			ExpectedChannelEpoch:  cmd.ExpectedChannelEpoch,
			ExpectedLeaderEpoch:   cmd.ExpectedLeaderEpoch,
			TraceID:               cmd.TraceID,
			Attempt:               0,
		})
	sendtrace.Record(sendtrace.Event{
		TraceID:     cmd.TraceID,
		Stage:       sendtrace.StageMessageSendDurable,
		At:          startedAt,
		Duration:    sendtrace.Elapsed(startedAt, a.now()),
		NodeID:      a.localNodeID,
		ChannelKey:  string(channelhandler.KeyFromChannelID(channelID)),
		ClientMsgNo: cmd.ClientMsgNo,
		MessageSeq:  result.MessageSeq,
		FromUID:     cmd.FromUID,
	})
	if err != nil {
		return SendResult{}, err
	}

	sendResult := SendResult{
		MessageID:  int64(result.MessageID),
		MessageSeq: result.MessageSeq,
		Reason:     frame.ReasonSuccess,
	}

	intentSubmitted := a.submitRequestScopedCMDConversationIntent(ctx, result.Message, cmd.RequestSubscribers)
	if a.dispatcher != nil {
		if err := a.dispatcher.SubmitCommitted(ctx, messageevents.MessageCommitted{
			Message:                        result.Message,
			SenderSessionID:                cmd.SenderSessionID,
			MessageScopedUIDs:              append([]string(nil), cmd.RequestSubscribers...),
			CMDConversationIntentSubmitted: intentSubmitted,
		}); err != nil {
			fields := append([]wklog.Field{
				wklog.Event("message.send.dispatch_submit.failed"),
			}, messageLogFields(channelID, cmd.FromUID)...)
			fields = append(fields, wklog.Error(err))
			a.sendLogger().Warn("submit committed message failed", fields...)
			if len(cmd.RequestSubscribers) > 0 {
				return SendResult{}, err
			}
		}
	}
	return sendResult, nil
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
