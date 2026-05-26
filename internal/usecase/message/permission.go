package message

import (
	"context"
	"errors"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func (a *App) checkSendPermission(ctx context.Context, cmd SendCommand) (frame.ReasonCode, error) {
	if a == nil || a.permissions == nil {
		return frame.ReasonSuccess, nil
	}
	if a.systemUIDs != nil && a.systemUIDs.IsSystemUID(cmd.FromUID) {
		return frame.ReasonSuccess, nil
	}

	if reason, err := a.checkSenderSendPermission(ctx, cmd.FromUID); reason != frame.ReasonSuccess || err != nil {
		return reason, err
	}
	if a.systemDeviceID != "" && cmd.DeviceID == a.systemDeviceID {
		return frame.ReasonSuccess, nil
	}

	switch cmd.ChannelType {
	case frame.ChannelTypePerson:
		return a.checkPersonSendPermission(ctx, cmd)
	case frame.ChannelTypeGroup:
		return a.checkGroupSendPermission(ctx, cmd)
	case frame.ChannelTypeInfo, frame.ChannelTypeCustomerService:
		return frame.ReasonSuccess, nil
	case frame.ChannelTypeAgent:
		return a.checkAgentSendPermission(cmd)
	case frame.ChannelTypeVisitors:
		return a.checkVisitorsSendPermission(ctx, cmd)
	default:
		return frame.ReasonSuccess, nil
	}
}

func (a *App) checkSenderSendPermission(ctx context.Context, fromUID string) (frame.ReasonCode, error) {
	ch, err := a.permissions.GetChannelForPermission(ctx, fromUID, int64(frame.ChannelTypePerson))
	if errors.Is(err, metadb.ErrNotFound) {
		return frame.ReasonSuccess, nil
	}
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if ch.SendBan != 0 {
		return frame.ReasonSendBan, nil
	}
	return frame.ReasonSuccess, nil
}

func (a *App) checkGroupSendPermission(ctx context.Context, cmd SendCommand) (frame.ReasonCode, error) {
	ch, err := a.permissions.GetChannelForPermission(ctx, cmd.ChannelID, int64(cmd.ChannelType))
	if errors.Is(err, metadb.ErrNotFound) {
		return frame.ReasonChannelNotExist, nil
	}
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if ch.Ban != 0 {
		return frame.ReasonBan, nil
	}
	if ch.Disband != 0 {
		return frame.ReasonDisband, nil
	}

	return a.checkCommonMemberPermission(ctx, channelmembers.ChannelKey{ChannelID: cmd.ChannelID, ChannelType: cmd.ChannelType}, cmd.FromUID)
}

func (a *App) checkCommonMemberPermission(ctx context.Context, key channelmembers.ChannelKey, fromUID string) (frame.ReasonCode, error) {
	channelType := int64(key.ChannelType)
	denied, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.DenylistChannelID(key), channelType, fromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if denied {
		return frame.ReasonInBlacklist, nil
	}

	subscriber, err := a.permissions.ContainsChannelSubscriber(ctx, key.ChannelID, channelType, fromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if !subscriber {
		return frame.ReasonSubscriberNotExist, nil
	}

	allowID := channelmembers.AllowlistChannelID(key)
	hasAllowlist, err := a.permissions.HasChannelSubscribers(ctx, allowID, channelType)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if !hasAllowlist {
		return frame.ReasonSuccess, nil
	}
	allowed, err := a.permissions.ContainsChannelSubscriber(ctx, allowID, channelType, fromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if !allowed {
		return frame.ReasonNotInWhitelist, nil
	}
	return frame.ReasonSuccess, nil
}

func (a *App) checkAgentSendPermission(cmd SendCommand) (frame.ReasonCode, error) {
	uid, agentUID, err := runtimechannelid.DecodeAgentChannel(cmd.ChannelID)
	if err != nil {
		return 0, err
	}
	if cmd.FromUID == uid || cmd.FromUID == agentUID {
		return frame.ReasonSuccess, nil
	}
	return frame.ReasonNotAllowSend, nil
}

func (a *App) checkVisitorsSendPermission(ctx context.Context, cmd SendCommand) (frame.ReasonCode, error) {
	if cmd.FromUID == cmd.ChannelID {
		return frame.ReasonSuccess, nil
	}
	key := channelmembers.ChannelKey{ChannelID: cmd.ChannelID, ChannelType: frame.ChannelTypeCustomerService}
	return a.checkCommonMemberPermission(ctx, key, cmd.FromUID)
}

func (a *App) checkPersonSendPermission(ctx context.Context, cmd SendCommand) (frame.ReasonCode, error) {
	left, right, err := runtimechannelid.DecodePersonChannel(cmd.ChannelID)
	if err != nil {
		return 0, err
	}
	receiver := right
	if cmd.FromUID == right {
		receiver = left
	}
	if a.systemUIDs != nil && a.systemUIDs.IsSystemUID(receiver) {
		return frame.ReasonSuccess, nil
	}
	key := channelmembers.ChannelKey{ChannelID: receiver, ChannelType: frame.ChannelTypePerson}
	denied, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.DenylistChannelID(key), int64(frame.ChannelTypePerson), cmd.FromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if denied {
		return frame.ReasonInBlacklist, nil
	}
	if !a.personWhitelistEnabled {
		return frame.ReasonSuccess, nil
	}
	allowed, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.AllowlistChannelID(key), int64(frame.ChannelTypePerson), cmd.FromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if allowed {
		return frame.ReasonSuccess, nil
	}

	ch, err := a.permissions.GetChannelForPermission(ctx, receiver, int64(frame.ChannelTypePerson))
	if errors.Is(err, metadb.ErrNotFound) {
		return frame.ReasonNotInWhitelist, nil
	}
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if ch.AllowStranger != 0 {
		return frame.ReasonSuccess, nil
	}
	return frame.ReasonNotInWhitelist, nil
}
