package plugin

import (
	"context"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

const (
	sendHookResultOK            = "ok"
	sendHookResultReject        = "reject"
	sendHookResultError         = "error"
	sendHookResultFailOpen      = "fail_open"
	sendHookResultInvalidReason = "invalid_reason"
)

// BeforeSend runs local Send plugins in hook order and returns the mutated command.
func (a *App) BeforeSend(ctx context.Context, cmd message.SendCommand) (message.SendCommand, message.Reason, error) {
	plugins, err := a.SendPluginCandidates(ctx)
	if err != nil {
		if a.failOpen {
			return cmd, message.ReasonSuccess, nil
		}
		return cmd, message.ReasonSystemError, err
	}
	if len(plugins) == 0 {
		return cmd, message.ReasonSuccess, nil
	}
	current := cmd
	for _, plugin := range plugins {
		start := time.Now()
		resp, err := a.InvokeSend(ctx, plugin.No, sendPacketFromCommand(current))
		if err != nil {
			if a.failOpen {
				a.observeSendInvoke(sendHookResultFailOpen, start)
				return cmd, message.ReasonSuccess, nil
			}
			a.observeSendInvoke(sendHookResultError, start)
			return current, message.ReasonSystemError, err
		}
		reason, valid := sendHookReason(resp.GetReason())
		if !valid {
			a.observeSendInvoke(sendHookResultInvalidReason, start)
			return current, message.ReasonSystemError, nil
		}
		if reason != 0 && reason != message.ReasonSuccess {
			a.observeSendInvoke(sendHookResultReject, start)
			return current, reason, nil
		}
		applySendPacketResponse(&current, resp)
		a.observeSendInvoke(sendHookResultOK, start)
	}
	return current, message.ReasonSuccess, nil
}

// SendPluginCandidates returns running local Send plugins in hook order.
func (a *App) SendPluginCandidates(ctx context.Context) ([]ObservedPlugin, error) {
	plugins, err := a.applyDesiredToPlugins(ctx, a.runtime.List())
	if err != nil {
		return nil, err
	}
	return runningPluginsByMethod(plugins, MethodSend), nil
}

func sendPacketFromCommand(cmd message.SendCommand) *pluginproto.SendPacket {
	return &pluginproto.SendPacket{
		FromUid:     cmd.FromUID,
		ChannelId:   cmd.ChannelID,
		ChannelType: uint32(cmd.ChannelType),
		Payload:     append([]byte(nil), cmd.Payload...),
		Reason:      uint32(message.ReasonSuccess),
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

func sendHookReason(reason uint32) (message.Reason, bool) {
	if reason > math.MaxUint8 {
		return 0, false
	}
	return message.Reason(reason), true
}

func (a *App) observeSendInvoke(result string, start time.Time) {
	if a == nil || a.observer == nil {
		return
	}
	a.observer.ObserveSendInvoke(result, time.Since(start))
}
