package plugin

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// BeforeSend runs local Send plugins in hook order and returns the mutated command.
func (a *App) BeforeSend(ctx context.Context, cmd message.SendCommand) (message.SendCommand, frame.ReasonCode, error) {
	plugins, err := a.SendPluginCandidates(ctx)
	if err != nil {
		if a.failOpen {
			return cmd, frame.ReasonSuccess, nil
		}
		return cmd, frame.ReasonSystemError, err
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
			return current, frame.ReasonSystemError, err
		}
		reason, valid := sendHookReason(resp.GetReason())
		if !valid {
			return current, frame.ReasonSystemError, nil
		}
		if reason != 0 && reason != frame.ReasonSuccess {
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
	if resp.Payload != nil {
		cmd.Payload = append([]byte(nil), resp.GetPayload()...)
	}
}

func sendHookReason(reason uint32) (frame.ReasonCode, bool) {
	if reason > math.MaxUint8 {
		return 0, false
	}
	return frame.ReasonCode(reason), true
}
