package handler

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 权限判断
func (h *Handler) permission(ctx *eventbus.ChannelContext) {

	events := ctx.Events
	channelId := ctx.ChannelId
	channelType := ctx.ChannelType
	// 记录消息轨迹
	for _, event := range events {
		event.Track.Record(track.PositionChannelPermission)
	}

	// --------------- 判断频道权限 ----------------
	reasonCode, err := h.hasPermissionForChannel(channelId, channelType)
	if err != nil {
		h.Error("hasPermissionForChannel error", zap.Error(err))
		reasonCode = wkproto.ReasonSystemError
	}
	if reasonCode != wkproto.ReasonSuccess {
		h.Info("hasPermissionForChannel failed", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.String("reasonCode", reasonCode.String()))
		for _, event := range events {
			event.ReasonCode = reasonCode
		}
		return
	}

	// --------------- 判断发送者权限 ----------------
	for _, event := range events {
		reasonCode, err = h.hasPermissionForSender(channelId, channelType, event)
		if err != nil {
			h.Error("hasPermissionForSender error", zap.Error(err), zap.String("fromUid", event.Conn.Uid), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int("events", len(events)))
			reasonCode = wkproto.ReasonSystemError
		} else if reasonCode != wkproto.ReasonSuccess {
			h.Info("hasPermissionForSender failed", zap.String("fromUid", event.Conn.Uid), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.String("reasonCode", reasonCode.String()))
		}
		event.ReasonCode = reasonCode
	}

}

func (h *Handler) hasPermissionForChannel(channelId string, channelType uint8) (wkproto.ReasonCode, error) {

	// 资讯频道是公开的，直接通过
	if channelType == wkproto.ChannelTypeInfo {
		return wkproto.ReasonSuccess, nil
	}
	// 客服频道，直接通过
	if channelType == wkproto.ChannelTypeCustomerService {
		return wkproto.ReasonSuccess, nil
	}

	// 查询频道基本信息
	channelInfo, err := service.Store.GetChannel(channelId, channelType)
	if err != nil {
		h.Error("hasPermission: GetChannel error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}

	// 频道被封禁
	if channelInfo.Ban {
		return wkproto.ReasonBan, nil
	}
	// 频道已解散
	if channelInfo.Disband {
		return wkproto.ReasonDisband, nil
	}
	return wkproto.ReasonSuccess, nil
}

// 判断发送者是否有权限
func (h *Handler) hasPermissionForSender(channelId string, channelType uint8, e *eventbus.Event) (wkproto.ReasonCode, error) {

	var (
		fromUid = e.Conn.Uid
	)

	// 资讯频道是公开的，直接通过
	if channelType == wkproto.ChannelTypeInfo {
		return wkproto.ReasonSuccess, nil
	}
	// 客服频道，直接通过
	if channelType == wkproto.ChannelTypeCustomerService {
		return wkproto.ReasonSuccess, nil
	}

	// 系统发的消息直接通过
	if options.G.IsSystemDevice(e.Conn.DeviceId) {
		return wkproto.ReasonSuccess, nil
	}

	// 系统账号，直接通过
	if service.SystemAccountManager.IsSystemAccount(fromUid) {
		return wkproto.ReasonSuccess, nil
	}

	// 个人频道,需要判断接收者是否允许
	if channelType == wkproto.ChannelTypePerson {
		return h.hasPermissionForPerson(channelId, channelType, e)
	}

	return h.hasPermissionForCommChannel(channelId, channelType, e)
}

// 通用频道权限判断
func (h *Handler) hasPermissionForCommChannel(channelId string, channelType uint8, e *eventbus.Event) (wkproto.ReasonCode, error) {
	var (
		realFakeChannelId = channelId
		fromUid           = e.Conn.Uid
	)
	// 如果是cmd频道则转换为真实频道的id，因为cmd频道的数据是跟对应的真实频道的数据共用的
	if options.G.IsCmdChannel(channelId) {
		realFakeChannelId = options.G.CmdChannelConvertOrginalChannel(channelId)
	}
	// 判断是否是黑名单内
	isDenylist, err := service.Store.ExistDenylist(realFakeChannelId, channelType, fromUid)
	if err != nil {
		h.Error("ExistDenylist error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}
	// 判断是否是订阅者
	isSubscriber, err := service.Store.ExistSubscriber(realFakeChannelId, channelType, fromUid)
	if err != nil {
		h.Error("ExistSubscriber error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if !isSubscriber {
		return wkproto.ReasonSubscriberNotExist, nil
	}

	// 判断是否在白名单内
	if !options.G.WhitelistOffOfPerson {
		hasAllowlist, err := service.Store.HasAllowlist(realFakeChannelId, channelType)
		if err != nil {
			h.Error("HasAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}

		if hasAllowlist { // 如果频道有白名单，则判断是否在白名单内
			isAllowlist, err := service.Store.ExistAllowlist(realFakeChannelId, channelType, fromUid)
			if err != nil {
				h.Error("ExistAllowlist error", zap.Error(err))
				return wkproto.ReasonSystemError, err
			}
			if !isAllowlist {
				return wkproto.ReasonNotInWhitelist, nil
			}
		}
	}
	return wkproto.ReasonSuccess, nil
}

// 个人频道权限判断
func (h *Handler) hasPermissionForPerson(channelId string, _ uint8, e *eventbus.Event) (wkproto.ReasonCode, error) {
	var (
		realFakeChannel = channelId
		fromUid         = e.Conn.Uid
	)
	// 如果是cmd频道则转换为真实频道的id，因为cmd频道的数据是跟对应的真实频道的数据共用的
	if options.G.IsCmdChannel(channelId) {
		realFakeChannel = options.G.CmdChannelConvertOrginalChannel(channelId)
	}
	uid1, uid2 := options.GetFromUIDAndToUIDWith(realFakeChannel)
	toUid := ""
	if uid1 == fromUid {
		toUid = uid2
	} else {
		toUid = uid1
	}
	// 如果接收者是系统账号，则直接通过
	systemAccount := service.SystemAccountManager.IsSystemAccount(toUid)
	if systemAccount {
		return wkproto.ReasonSuccess, nil
	}
	// 请求个人频道是否允许发送
	reasonCode, err := h.requestAllowSend(fromUid, toUid)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	return reasonCode, nil
}

func (h *Handler) requestAllowSend(from, to string) (wkproto.ReasonCode, error) {

	leaderNode, err := service.Cluster.SlotLeaderOfChannel(to, wkproto.ChannelTypePerson)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	if options.G.IsLocalNode(leaderNode.Id) {
		return h.allowSend(from, to)
	}

	resp, err := h.client.RequestAllowSendForPerson(leaderNode.Id, from, to)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	if resp.Status == proto.StatusOK {
		return wkproto.ReasonSuccess, nil
	}
	if resp.Status == proto.StatusError {
		return wkproto.ReasonSystemError, errors.New(string(resp.Body))
	}
	return wkproto.ReasonCode(resp.Status), nil
}

func (h *Handler) allowSend(from, to string) (wkproto.ReasonCode, error) {
	// 判断是否是黑名单内
	isDenylist, err := service.Store.ExistDenylist(to, wkproto.ChannelTypePerson, from)
	if err != nil {
		h.Error("ExistDenylist error", zap.String("from", from), zap.String("to", to), zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	if !options.G.WhitelistOffOfPerson {
		// 判断是否在白名单内
		isAllowlist, err := service.Store.ExistAllowlist(to, wkproto.ChannelTypePerson, from)
		if err != nil {
			h.Error("ExistAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}
		if !isAllowlist {
			return wkproto.ReasonNotInWhitelist, nil
		}
	}

	return wkproto.ReasonSuccess, nil
}
