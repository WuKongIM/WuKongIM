package icluster

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type ICluster interface {
	// 节点相关接口
	IClusterNode
	// 槽相关接口
	IClusterSlot
	// 频道相关接口
	IClusterChannel
	// Send 向指定节点发送消息
	Send(nodeId uint64, msg *proto.Message) error

	// RequestWithContext 向指定节点发送请求
	RequestWithContext(ctx context.Context, toNodeId uint64, path string, body []byte) (*proto.Response, error)

	// Route 设置接受请求的路由
	Route(path string, handler wkserver.Handler)

	// MustWaitClusterReady 等待集群准备完成
	MustWaitClusterReady(timeout time.Duration) error
}

type IClusterSlot interface {
	// GetOrCreateChannelClusterConfigFromSlotLeader 从频道槽领导获取或创建频道分布式配置
	GetOrCreateChannelClusterConfigFromSlotLeader(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error)

	// SlotLeaderOfChannel 获取频道的槽领导
	SlotLeaderOfChannel(channelId string, channelType uint8) (*types.Node, error)

	// SlotLeaderIdOfChannel 获取频道的槽领导ID
	SlotLeaderIdOfChannel(channelId string, channelType uint8) (uint64, error)

	// LoadOnlyChannelClusterConfig 仅加载频道分布式配置
	LoadOnlyChannelClusterConfig(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error)

	// GetSlotId 获取字符串对应的槽ID
	GetSlotId(v string) uint32

	// SlotLeaderId 获取槽的领导节点ID
	SlotLeaderId(slotId uint32) uint64

	// SlotLeaderNodeInfo 获取槽的领导节点信息
	SlotLeaderNodeInfo(slotId uint32) *types.Node

	// MustWaitAllSlotsReady 等待所有槽准备完成
	MustWaitAllSlotsReady(timeout time.Duration)
}

type IClusterChannel interface {

	// LeaderOfChannel 获取频道的领导节点 (如果频道分布式配置不存在则会创建新的分布式配置并提案)
	LeaderOfChannel(channelId string, channelType uint8) (*types.Node, error)

	// LeaderOfChannelForRead 获取频道的领导节点（如果频道的分布式配置不存在，则返回ErrChannelClusterConfigNotFound错误）
	LeaderOfChannelForRead(channelId string, channelType uint8) (*types.Node, error)

	// LeaderIdOfChannel 获取频道的领导节点ID
	LeaderIdOfChannel(channelId string, channelType uint8) (uint64, error)
}

type IClusterNode interface {
	// NodeInfoById 根据节点ID获取节点信息
	NodeInfoById(nodeId uint64) *types.Node

	// NodeIsOnline 节点是否在线
	NodeIsOnline(nodeId uint64) bool

	// Nodes 获取所有节点信息
	Nodes() []*types.Node

	// NodeVersion 获取节点版本
	NodeVersion() uint64

	// LeaderId 获取领导节点ID
	LeaderId() uint64
}
