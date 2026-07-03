package message

import (
	"context"
	"errors"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func (a *App) checkSendPermission(ctx context.Context, cmd SendCommand) (SendCommand, Reason, error) {
	if cmd.RequestScoped || (len(cmd.MessageScopedUIDs) > 0 && cmd.ChannelID == "") {
		return cmd, ReasonSuccess, nil
	}

	sourceChannelID, alreadyCommandChannel := runtimechannelid.FromCommandChannel(cmd.ChannelID)
	cmd.ChannelID = sourceChannelID
	if cmd.ChannelType == channelTypePerson && cmd.NormalizePersonChannel {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return cmd, 0, err
		}
		cmd.ChannelID = channelID
	}
	reapplyCommandChannel := func(next SendCommand) SendCommand {
		if alreadyCommandChannel {
			next.ChannelID = runtimechannelid.ToCommandChannel(next.ChannelID)
		}
		return next
	}

	if a == nil || a.permissions == nil {
		return reapplyCommandChannel(cmd), ReasonSuccess, nil
	}
	if a.systemUIDs != nil && a.systemUIDs.IsSystemUID(cmd.FromUID) {
		return reapplyCommandChannel(cmd), ReasonSuccess, nil
	}

	if reason, err := a.checkSenderSendPermission(ctx, cmd.FromUID); reason != ReasonSuccess || err != nil {
		return cmd, reason, err
	}
	if a.systemDeviceID != "" && cmd.DeviceID == a.systemDeviceID {
		return reapplyCommandChannel(cmd), ReasonSuccess, nil
	}

	var (
		reason Reason
		err    error
	)
	switch cmd.ChannelType {
	case channelTypePerson:
		reason, err = a.checkPersonSendPermission(ctx, cmd)
	case channelTypeGroup:
		reason, err = a.checkGroupSendPermission(ctx, cmd)
	case channelTypeInfo, channelTypeCustomerService:
		reason = ReasonSuccess
	case channelTypeAgent:
		reason, err = a.checkAgentSendPermission(cmd)
	case channelTypeVisitors:
		reason, err = a.checkVisitorsSendPermission(ctx, cmd)
	default:
		reason = ReasonSuccess
	}
	if err != nil || reason != ReasonSuccess {
		return cmd, reason, err
	}
	return reapplyCommandChannel(cmd), ReasonSuccess, nil
}

func (a *App) checkSenderSendPermission(ctx context.Context, fromUID string) (Reason, error) {
	ch, err := a.permissions.GetChannelForPermission(ctx, fromUID, int64(channelTypePerson))
	if errors.Is(err, metadb.ErrNotFound) {
		return ReasonSuccess, nil
	}
	if err != nil {
		return ReasonSystemError, err
	}
	if ch.SendBan != 0 {
		return ReasonSendBan, nil
	}
	return ReasonSuccess, nil
}

func (a *App) checkGroupSendPermission(ctx context.Context, cmd SendCommand) (Reason, error) {
	ch, err := a.permissions.GetChannelForPermission(ctx, cmd.ChannelID, int64(cmd.ChannelType))
	if errors.Is(err, metadb.ErrNotFound) {
		return ReasonChannelNotExist, nil
	}
	if err != nil {
		return ReasonSystemError, err
	}
	if ch.Ban != 0 {
		return ReasonBan, nil
	}
	if ch.Disband != 0 {
		return ReasonDisband, nil
	}
	return a.checkCommonMemberPermission(ctx, channelmembers.ChannelKey{ChannelID: cmd.ChannelID, ChannelType: cmd.ChannelType}, cmd.FromUID)
}

func (a *App) checkCommonMemberPermission(ctx context.Context, key channelmembers.ChannelKey, fromUID string) (Reason, error) {
	channelType := int64(key.ChannelType)
	denied, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.DenylistChannelID(key), channelType, fromUID)
	if err != nil {
		return ReasonSystemError, err
	}
	if denied {
		return ReasonInBlacklist, nil
	}

	subscriber, err := a.permissions.ContainsChannelSubscriber(ctx, key.ChannelID, channelType, fromUID)
	if err != nil {
		return ReasonSystemError, err
	}
	if !subscriber {
		return ReasonSubscriberNotExist, nil
	}

	allowID := channelmembers.AllowlistChannelID(key)
	hasAllowlist, err := a.permissions.HasChannelSubscribers(ctx, allowID, channelType)
	if err != nil {
		return ReasonSystemError, err
	}
	if !hasAllowlist {
		return ReasonSuccess, nil
	}
	allowed, err := a.permissions.ContainsChannelSubscriber(ctx, allowID, channelType, fromUID)
	if err != nil {
		return ReasonSystemError, err
	}
	if !allowed {
		return ReasonNotInWhitelist, nil
	}
	return ReasonSuccess, nil
}

func (a *App) checkAgentSendPermission(cmd SendCommand) (Reason, error) {
	uid, agentUID, err := runtimechannelid.DecodeAgentChannel(cmd.ChannelID)
	if err != nil {
		return 0, err
	}
	if cmd.FromUID == uid || cmd.FromUID == agentUID {
		return ReasonSuccess, nil
	}
	return ReasonNotAllowSend, nil
}

func (a *App) checkVisitorsSendPermission(ctx context.Context, cmd SendCommand) (Reason, error) {
	if cmd.FromUID == cmd.ChannelID {
		return ReasonSuccess, nil
	}
	key := channelmembers.ChannelKey{ChannelID: cmd.ChannelID, ChannelType: channelTypeCustomerService}
	return a.checkCommonMemberPermission(ctx, key, cmd.FromUID)
}

func (a *App) checkPersonSendPermission(ctx context.Context, cmd SendCommand) (Reason, error) {
	left, right, err := runtimechannelid.DecodePersonChannel(cmd.ChannelID)
	if err != nil {
		return 0, err
	}
	receiver := right
	if cmd.FromUID == right {
		receiver = left
	}
	if a.systemUIDs != nil && a.systemUIDs.IsSystemUID(receiver) {
		return ReasonSuccess, nil
	}
	key := channelmembers.ChannelKey{ChannelID: receiver, ChannelType: channelTypePerson}
	denied, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.DenylistChannelID(key), int64(channelTypePerson), cmd.FromUID)
	if err != nil {
		return ReasonSystemError, err
	}
	if denied {
		return ReasonInBlacklist, nil
	}
	if !a.personWhitelistEnabled {
		return ReasonSuccess, nil
	}
	allowed, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.AllowlistChannelID(key), int64(channelTypePerson), cmd.FromUID)
	if err != nil {
		return ReasonSystemError, err
	}
	if allowed {
		return ReasonSuccess, nil
	}

	ch, err := a.permissions.GetChannelForPermission(ctx, receiver, int64(channelTypePerson))
	if errors.Is(err, metadb.ErrNotFound) {
		return ReasonNotInWhitelist, nil
	}
	if err != nil {
		return ReasonSystemError, err
	}
	if ch.AllowStranger != 0 {
		return ReasonSuccess, nil
	}
	return ReasonNotInWhitelist, nil
}
