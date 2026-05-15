package plugin

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// BeforeSend runs local Send plugins in hook order and returns the mutated command.
func (a *App) BeforeSend(ctx context.Context, cmd message.SendCommand) (message.SendCommand, frame.ReasonCode, error) {
	plugins, err := a.SendPluginCandidates(ctx)
	if err != nil {
		return message.SendCommand{}, 0, err
	}
	if len(plugins) == 0 {
		return cmd, frame.ReasonSuccess, nil
	}
	current := cmd
	for _, plugin := range plugins {
		packet := sendPacketFromCommand(current)
		resp, err := a.InvokeSend(ctx, plugin.No, packet)
		if err != nil {
			if a.failOpen {
				return cmd, frame.ReasonSuccess, nil
			}
			return message.SendCommand{}, 0, err
		}
		if reason := frame.ReasonCode(resp.GetReason()); reason != 0 && reason != frame.ReasonSuccess {
			return current, reason, nil
		}
		applySendPacketResponse(&current, resp)
	}
	return current, frame.ReasonSuccess, nil
}

func sendPacketFromCommand(cmd message.SendCommand) *pluginproto.SendPacket {
	return &pluginproto.SendPacket{
		FromUid:     cmd.FromUID,
		ChannelId:   cmd.ChannelID,
		ChannelType: uint32(cmd.ChannelType),
		Payload:     append([]byte(nil), cmd.Payload...),
		Reason:      uint32(frame.ReasonSuccess),
		Conn: &pluginproto.Conn{
			Uid:        cmd.FromUID,
			ConnId:     int64(cmd.SenderSessionID),
			DeviceId:   cmd.DeviceID,
			DeviceFlag: uint32(cmd.DeviceFlag),
		},
	}
}

func applySendPacketResponse(cmd *message.SendCommand, resp *pluginproto.SendPacket) {
	if resp == nil {
		return
	}
	cmd.FromUID = resp.GetFromUid()
	cmd.ChannelID = resp.GetChannelId()
	cmd.ChannelType = uint8(resp.GetChannelType())
	cmd.Payload = append([]byte(nil), resp.GetPayload()...)
}
