package process

import (
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (c *Channel) processPermission(channelInfo wkdb.ChannelInfo, m *reactor.ChannelMessage) {
	fmt.Println("processPermission--->", m.MessageId, string(m.SendPacket.Payload))
	reasonCode, err := c.hasPermission(channelInfo, m)
	if err != nil {
		c.Error("hasPermission error", zap.Error(err))
		reasonCode = wkproto.ReasonSystemError
	}
	m.ReasonCode = reasonCode

	// 如果是成功并且消息是否需要存储的，则存储消息，否则是发送回执
	if reasonCode == wkproto.ReasonSuccess {
		if !m.SendPacket.NoPersist {
			m.MsgType = reactor.ChannelMsgStorage
		} else {
			// 如果是非存储消息，则跳到通知队列存储
			m.MsgType = reactor.ChannelMsgStorageNotifyQueue
		}
	} else {
		m.MsgType = reactor.ChannelMsgSendack
	}
	reactor.Channel.AddMessage(m)
}

// 判断是否有权限
func (c *Channel) hasPermission(channelInfo wkdb.ChannelInfo, m *reactor.ChannelMessage) (wkproto.ReasonCode, error) {

	var (
		channelType = m.ChannelType
		fromUid     = m.Conn.Uid
	)
	// 资讯频道是公开的，直接通过
	if channelType == wkproto.ChannelTypeInfo {
		return wkproto.ReasonSuccess, nil
	}
	// 客服频道，直接通过
	if channelType == wkproto.ChannelTypeCustomerService {
		return wkproto.ReasonSuccess, nil
	}
	// 频道被封禁
	if channelInfo.Ban {
		return wkproto.ReasonBan, nil
	}
	// 频道已解散
	if channelInfo.Disband {
		return wkproto.ReasonDisband, nil
	}
	// 系统账号，直接通过
	if service.SystemAccountManager.IsSystemAccount(fromUid) {
		return wkproto.ReasonSuccess, nil
	}

	// 个人频道,需要判断接收者是否允许
	if channelType == wkproto.ChannelTypePerson {
		return c.hasPermissionForPerson(m)
	}

	return c.hasPermissionForCommChannel(m)
}

// 通用频道权限判断
func (c *Channel) hasPermissionForCommChannel(m *reactor.ChannelMessage) (wkproto.ReasonCode, error) {
	var (
		realFakeChannelId = m.FakeChannelId
		fromUid           = m.Conn.Uid
		channelType       = m.ChannelType
	)
	// 如果是cmd频道则转换为真实频道的id，因为cmd频道的数据是跟对应的真实频道的数据共用的
	if options.G.IsCmdChannel(m.FakeChannelId) {
		realFakeChannelId = options.G.CmdChannelConvertOrginalChannel(m.FakeChannelId)
	}
	// 判断是否是黑名单内
	isDenylist, err := service.Store.ExistDenylist(realFakeChannelId, channelType, fromUid)
	if err != nil {
		c.Error("ExistDenylist error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}
	// 判断是否是订阅者
	isSubscriber, err := service.Store.ExistSubscriber(realFakeChannelId, channelType, fromUid)
	if err != nil {
		c.Error("ExistSubscriber error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if !isSubscriber {
		return wkproto.ReasonSubscriberNotExist, nil
	}

	// 判断是否在白名单内
	if !options.G.WhitelistOffOfPerson {
		hasAllowlist, err := service.Store.HasAllowlist(realFakeChannelId, channelType)
		if err != nil {
			c.Error("HasAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}

		if hasAllowlist { // 如果频道有白名单，则判断是否在白名单内
			isAllowlist, err := service.Store.ExistAllowlist(realFakeChannelId, channelType, fromUid)
			if err != nil {
				c.Error("ExistAllowlist error", zap.Error(err))
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
func (c *Channel) hasPermissionForPerson(m *reactor.ChannelMessage) (wkproto.ReasonCode, error) {
	var (
		realFakeChannel = m.FakeChannelId
		fromUid         = m.Conn.Uid
	)
	// 如果是cmd频道则转换为真实频道的id，因为cmd频道的数据是跟对应的真实频道的数据共用的
	if options.G.IsCmdChannel(m.FakeChannelId) {
		realFakeChannel = options.G.CmdChannelConvertOrginalChannel(m.FakeChannelId)
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
	reasonCode, err := c.requestAllowSend(fromUid, toUid)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	return reasonCode, nil
}

func (c *Channel) requestAllowSend(from, to string) (wkproto.ReasonCode, error) {

	leaderNode, err := service.Cluster.SlotLeaderOfChannel(to, wkproto.ChannelTypePerson)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	if options.G.IsLocalNode(leaderNode.Id) {
		return c.allowSend(from, to)
	}

	timeoutCtx, cancel := c.WithTimeout()
	defer cancel()

	req := &allowSendReq{
		From: from,
		To:   to,
	}
	bodyBytes, err := req.encode()
	if err != nil {
		return wkproto.ReasonSystemError, err
	}

	resp, err := service.Cluster.RequestWithContext(timeoutCtx, leaderNode.Id, "/wk/allowSend", bodyBytes)
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

func (c *Channel) allowSend(from, to string) (wkproto.ReasonCode, error) {
	// 判断是否是黑名单内
	isDenylist, err := service.Store.ExistDenylist(to, wkproto.ChannelTypePerson, from)
	if err != nil {
		c.Error("ExistDenylist error", zap.String("from", from), zap.String("to", to), zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	if !options.G.WhitelistOffOfPerson {
		// 判断是否在白名单内
		isAllowlist, err := service.Store.ExistAllowlist(to, wkproto.ChannelTypePerson, from)
		if err != nil {
			c.Error("ExistAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}
		if !isAllowlist {
			return wkproto.ReasonNotInWhitelist, nil
		}
	}

	return wkproto.ReasonSuccess, nil
}
