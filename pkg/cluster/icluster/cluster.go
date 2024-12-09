package icluster

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type Cluster interface {
	Propose

	Start() error
	Stop()
	// LeaderIdOfChannel 获取channel的leader节点ID
	LeaderIdOfChannel(ctx context.Context, channelId string, channelType uint8) (nodeId uint64, err error)
	// LeaderOfChannel 获取channel的leader节点信息
	LeaderOfChannel(ctx context.Context, channelId string, channelType uint8) (nodeInfo *pb.Node, err error)
	// LoadOrCreateChannel 加载或创建频道（此方法会激活频道）
	LoadOrCreateChannel(ctx context.Context, channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error)
	// SlotLeaderIdOfChannel 获取channel的leader节点信息(不激活频道)
	LeaderOfChannelForRead(channelId string, channelType uint8) (nodeInfo *pb.Node, err error)
	// SlotLeaderIdOfChannel 获取频道所属槽的领导
	SlotLeaderIdOfChannel(channelId string, channelType uint8) (nodeId uint64, err error)
	// SlotLeaderOfChannel 获取频道所属槽的领导
	SlotLeaderOfChannel(channelId string, channelType uint8) (nodeInfo *pb.Node, err error)
	// IsSlotLeaderOfChannel 当前节点是否是channel槽的leader节点
	IsSlotLeaderOfChannel(channelId string, channelType uint8) (isLeader bool, err error)
	// IsLeaderNodeOfChannel 当前节点是否是channel的leader节点
	IsLeaderOfChannel(ctx context.Context, channelId string, channelType uint8) (isLeader bool, err error)
	// NodeInfoById 获取节点信息
	NodeInfoById(nodeId uint64) (nodeInfo *pb.Node, err error)
	// Route 设置接受请求的路由
	Route(path string, handler wkserver.Handler)
	// RequestWithContext 发送请求给指定的节点
	RequestWithContext(ctx context.Context, toNodeId uint64, path string, body []byte) (*proto.Response, error)
	// Send 发送消息给指定的节点, MsgType 使用 1000 - 2000之间的值
	Send(toNodeId uint64, msg *proto.Message) error
	// OnMessage 设置接收消息的回调
	OnMessage(f func(fromNodeId uint64, msg *proto.Message))
	// NodeIsOnline 节点是否在线
	NodeIsOnline(nodeId uint64) bool
	//  GetSlotId 获取槽ID
	GetSlotId(v string) uint32

	// SlotLeaderNodeInfo 获取槽的节点信息
	SlotLeaderNodeInfo(slotId uint32) (nodeInfo *pb.Node, err error)

	// LoadOnlyChannelClusterConfig 加载频道配置，仅仅只加载配置，
	LoadOnlyChannelClusterConfig(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error)

	// 领导者Id
	LeaderId() uint64

	// 等待集群准备好
	MustWaitClusterReady(timeout time.Duration)

	// 等待所有槽准备好
	MustWaitAllSlotsReady(timeout time.Duration)

	// 测试分布式节点网络的ping
	TestPing() ([]PingResult, error)
}

type Propose interface {
	// ProposeChannelMessages 批量提交消息到指定的channel
	ProposeChannelMessages(ctx context.Context, channelId string, channelType uint8, logs []replica.Log) ([]ProposeResult, error)
	// ProposeToSlots 提案日志到指定的槽
	ProposeToSlot(ctx context.Context, slotId uint32, logs []replica.Log) ([]ProposeResult, error)
	// ProposeDataToSlot 提案数据到指定的槽
	ProposeDataToSlot(slotId uint32, data []byte) (ProposeResult, error)
}

type ProposeResult interface {
	LogId() uint64    // 日志Id
	LogIndex() uint64 // 日志下标
}

type PingResult struct {
	NodeId      uint64 // 响应的节点id
	Err         error  // 错误信息
	Millisecond int64  // 耗时
}
