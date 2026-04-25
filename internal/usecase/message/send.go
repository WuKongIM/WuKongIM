package message

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
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
	if cmd.ChannelType == frame.ChannelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return SendResult{}, err
		}
		cmd.ChannelID = channelID
	}

	if a.cluster == nil {
		fields := append([]wklog.Field{
			wklog.Event("message.send.cluster.required"),
		}, messageLogFields(channel.ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType}, cmd.FromUID)...)
		fields = append(fields, wklog.Error(ErrClusterRequired))
		a.sendLogger().Error("message cluster is required", fields...)
		return SendResult{}, ErrClusterRequired
	}

	return a.sendDurable(ctx, cmd)
}

func (a *App) sendDurable(ctx context.Context, cmd SendCommand) (SendResult, error) {
	draft := buildDurableMessage(cmd, a.now())
	channelID := channel.ChannelID{
		ID:   cmd.ChannelID,
		Type: cmd.ChannelType,
	}
	startedAt := a.now()
	result, err := sendWithEnsuredMeta(ctx, a.localNodeID, a.now, a.sendLogger(),
		a.cluster, a.remoteAppender, a.refresher, channel.AppendRequest{
			ChannelID:             channelID,
			Message:               draft,
			SupportsMessageSeqU64: supportsMessageSeqU64(cmd.ProtocolVersion),
			CommitMode:            commitModeOrDefault(cmd.CommitMode),
			ExpectedChannelEpoch:  cmd.ExpectedChannelEpoch,
			ExpectedLeaderEpoch:   cmd.ExpectedLeaderEpoch,
		})
	sendtrace.Record(sendtrace.Event{
		Stage:       sendtrace.StageMessageSendDurable,
		At:          startedAt,
		Duration:    sendtrace.Elapsed(startedAt, a.now()),
		NodeID:      a.localNodeID,
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
		if err := a.dispatcher.SubmitCommitted(ctx, deliveryruntime.CommittedEnvelope{
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
