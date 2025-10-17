package service

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// RPCClient 定义RPC客户端接口，用于跨节点权限检查
type RPCClient interface {
	RequestAllowSendForPerson(toNodeId uint64, from, to string) (*proto.Response, error)
}

// PermissionService 权限服务，提供统一的权限检查功能
type PermissionService struct {
	wklog.Log
	rpcClient RPCClient // RPC客户端，用于跨节点权限检查
}

// NewPermissionService 创建权限服务实例
func NewPermissionService(client RPCClient) *PermissionService {
	return &PermissionService{
		Log:       wklog.NewWKLog("PermissionService"),
		rpcClient: client,
	}
}

// SenderInfo 发送者信息，用于权限检查
type SenderInfo struct {
	UID      string // 发送者UID
	DeviceID string // 设备ID
}

// IsPublicChannelType 检查是否为公开频道类型，这些频道类型允许直接通过权限检查
// 返回 true 表示是公开频道，可以直接通过；false 表示需要进一步权限检查
func (p *PermissionService) IsPublicChannelType(channelType uint8) bool {
	// 资讯频道是公开的，直接通过
	if channelType == wkproto.ChannelTypeInfo {
		return true
	}
	// 客服频道，直接通过
	if channelType == wkproto.ChannelTypeCustomerService ||
		channelType == wkproto.ChannelTypeVisitors ||
		channelType == wkproto.ChannelTypeAgent {
		return true
	}
	return false
}

// HasPermissionForChannel 检查频道级别的权限
// 主要检查频道是否被封禁或解散
func (p *PermissionService) HasPermissionForChannel(channelId string, channelType uint8) (wkproto.ReasonCode, error) {
	// 检查是否为公开频道类型
	if p.IsPublicChannelType(channelType) {
		return wkproto.ReasonSuccess, nil
	}

	// 查询频道基本信息
	channelInfo, err := Store.GetChannel(channelId, channelType)
	if err != nil {
		p.Error("HasPermissionForChannel: GetChannel error", zap.Error(err))
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

// HasPermissionForSender 检查发送者权限
// 检查发送者是否有权限向指定频道发送消息
func (p *PermissionService) HasPermissionForSender(channelId string, channelType uint8, sender SenderInfo) (wkproto.ReasonCode, error) {
	// 检查是否为公开频道类型的特殊处理
	if channelType == wkproto.ChannelTypeInfo {
		return wkproto.ReasonSuccess, nil
	}
	if channelType == wkproto.ChannelTypeCustomerService {
		return wkproto.ReasonSuccess, nil
	}

	// Agent频道特殊处理
	if channelType == wkproto.ChannelTypeAgent {
		uid, agentUID := options.GetUidAndAgentUIDWith(channelId)
		if sender.UID == uid || sender.UID == agentUID {
			return wkproto.ReasonSuccess, nil
		}
	}

	// 访客频道只有自己和客服可以发消息
	if channelType == wkproto.ChannelTypeVisitors {
		// 因为访客频道的id就是访客的uid，所以这里发送者是自己直接通过
		// 否则需要走普通频道的流程：判断是否是订阅者，白名单，黑名单这些
		if sender.UID == channelId {
			return wkproto.ReasonSuccess, nil
		}
	}

	// 系统发的消息直接通过
	if options.G.IsSystemDevice(sender.DeviceID) {
		return wkproto.ReasonSuccess, nil
	}

	// 系统账号，直接通过
	if SystemAccountManager.IsSystemAccount(sender.UID) {
		return wkproto.ReasonSuccess, nil
	}

	// 个人频道,需要判断接收者是否允许
	if channelType == wkproto.ChannelTypePerson {
		return p.hasPermissionForPerson(channelId, channelType, sender)
	}

	return p.hasPermissionForCommChannel(channelId, channelType, sender)
}

// hasPermissionForCommChannel 通用频道权限判断
func (p *PermissionService) hasPermissionForCommChannel(channelId string, channelType uint8, sender SenderInfo) (wkproto.ReasonCode, error) {
	realFakeChannelId := channelId
	fromUid := sender.UID

	// 如果是cmd频道则转换为真实频道的id，因为cmd频道的数据是跟对应的真实频道的数据共用的
	if options.G.IsCmdChannel(channelId) {
		realFakeChannelId = options.G.CmdChannelConvertOrginalChannel(channelId)
	}

	// 判断是否是黑名单内
	isDenylist, err := Store.ExistDenylist(realFakeChannelId, channelType, fromUid)
	if err != nil {
		p.Error("ExistDenylist error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	// 判断是否是订阅者
	isSubscriber, err := Store.ExistSubscriber(realFakeChannelId, channelType, fromUid)
	if err != nil {
		p.Error("ExistSubscriber error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if !isSubscriber {
		return wkproto.ReasonSubscriberNotExist, nil
	}

	if channelType != wkproto.ChannelTypePerson {
		hasAllowlist, err := Store.HasAllowlist(realFakeChannelId, channelType)
		if err != nil {
			p.Error("HasAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}

		if hasAllowlist { // 如果频道有白名单，则判断是否在白名单内
			isAllowlist, err := Store.ExistAllowlist(realFakeChannelId, channelType, fromUid)
			if err != nil {
				p.Error("ExistAllowlist error", zap.Error(err))
				return wkproto.ReasonSystemError, err
			}
			if !isAllowlist {
				return wkproto.ReasonNotInWhitelist, nil
			}
		}
	}
	return wkproto.ReasonSuccess, nil
}

// hasPermissionForPerson 个人频道权限判断
func (p *PermissionService) hasPermissionForPerson(channelId string, _ uint8, sender SenderInfo) (wkproto.ReasonCode, error) {
	realFakeChannel := channelId
	fromUid := sender.UID

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
	systemAccount := SystemAccountManager.IsSystemAccount(toUid)
	if systemAccount {
		return wkproto.ReasonSuccess, nil
	}

	// 请求个人频道是否允许发送
	reasonCode, err := p.requestAllowSend(fromUid, toUid)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	return reasonCode, nil
}

// HasPermissionForPersonLocal 检查个人频道权限（仅本地检查，适用于API层）
// 这个方法只进行本地权限检查，不涉及跨节点RPC调用
func (p *PermissionService) HasPermissionForPersonLocal(from, to string) (wkproto.ReasonCode, error) {
	return p.allowSend(from, to)
}

// requestAllowSend 请求是否允许发送个人消息（支持跨节点RPC调用）
func (p *PermissionService) requestAllowSend(from, to string) (wkproto.ReasonCode, error) {
	leaderNode, err := Cluster.SlotLeaderOfChannel(to, wkproto.ChannelTypePerson)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	if options.G.IsLocalNode(leaderNode.Id) {
		return p.allowSend(from, to)
	}

	// 对于非本地节点，使用RPC客户端进行跨节点调用
	resp, err := p.rpcClient.RequestAllowSendForPerson(leaderNode.Id, from, to)
	if err != nil {
		p.Error("RequestAllowSendForPerson RPC call failed", zap.Error(err), zap.String("from", from), zap.String("to", to), zap.Uint64("nodeId", leaderNode.Id))
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

// allowSend 检查是否允许发送个人消息（本地检查）
func (p *PermissionService) allowSend(from, to string) (wkproto.ReasonCode, error) {
	// 判断是否是黑名单内
	isDenylist, err := Store.ExistDenylist(to, wkproto.ChannelTypePerson, from)
	if err != nil {
		p.Error("ExistDenylist error", zap.String("from", from), zap.String("to", to), zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	if !options.G.WhitelistOffOfPerson {
		// 判断是否在白名单内
		isAllowlist, err := Store.ExistAllowlist(to, wkproto.ChannelTypePerson, from)
		if err != nil {
			p.Error("ExistAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}
		if !isAllowlist {
			return wkproto.ReasonNotInWhitelist, nil
		}
	}

	return wkproto.ReasonSuccess, nil
}

// AllowSendForPersonLocal 本地检查是否允许个人消息发送（替代原有的AllowSendForPerson）
func (p *PermissionService) AllowSendForPersonLocal(from, to string) (wkproto.ReasonCode, error) {
	return p.allowSend(from, to)
}

// 全局权限服务实例
var Permission *PermissionService
