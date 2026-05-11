package message

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
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

	if cmd.ChannelType != frame.ChannelTypePerson && cmd.ChannelType != frame.ChannelTypeGroup {
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
	}

	reason, err := a.checkSendPermission(ctx, cmd)
	if err != nil {
		return SendResult{}, err
	}
	if reason != frame.ReasonSuccess {
		return SendResult{Reason: reason}, nil
	}

	if cmd.Framer.NoPersist {
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
	})
	if err != nil {
		return SendResult{}, err
	}

	sendResult := SendResult{
		MessageID:  int64(result.MessageID),
		MessageSeq: result.MessageSeq,
		Reason:     frame.ReasonSuccess,
	}

	if a.dispatcher != nil {
		if err := a.dispatcher.SubmitCommitted(ctx, messageevents.MessageCommitted{
			Message:         result.Message,
			SenderSessionID: cmd.SenderSessionID,
		}); err != nil {
			fields := append([]wklog.Field{
				wklog.Event("message.send.dispatch_submit.failed"),
			}, messageLogFields(channelID, cmd.FromUID)...)
			fields = append(fields, wklog.Error(err))
			a.sendLogger().Warn("submit committed message failed", fields...)
		}
	}
	return sendResult, nil
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

func messageLogFields(channelID channel.ChannelID, uid string) []wklog.Field {
	return []wklog.Field{
		wklog.ChannelID(channelID.ID),
		wklog.ChannelType(int64(channelID.Type)),
		wklog.UID(uid),
	}
}
