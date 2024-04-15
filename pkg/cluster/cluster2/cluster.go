package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type ICluster interface {
	Start() error
	Stop()
	// LeaderIdOfChannel 获取channel的leader节点ID
	LeaderIdOfChannel(ctx context.Context, channelId string, channelType uint8) (nodeId uint64, err error)
	// LeaderOfChannel 获取channel的leader节点信息
	LeaderOfChannel(ctx context.Context, channelId string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error)
	// SlotLeaderIdOfChannel 获取channel的leader节点信息(不激活频道)
	LeaderOfChannelForRead(channelId string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error)
	// SlotLeaderIdOfChannel 获取频道所属槽的领导
	SlotLeaderIdOfChannel(channelId string, channelType uint8) (nodeId uint64, err error)
	// SlotLeaderOfChannel 获取频道所属槽的领导
	SlotLeaderOfChannel(channelId string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error)
	// IsSlotLeaderOfChannel 当前节点是否是channel槽的leader节点
	IsSlotLeaderOfChannel(channelId string, channelType uint8) (isLeader bool, err error)
	// IsLeaderNodeOfChannel 当前节点是否是channel的leader节点
	IsLeaderOfChannel(ctx context.Context, channelId string, channelType uint8) (isLeader bool, err error)
	// NodeInfoByID 获取节点信息
	NodeInfoByID(nodeId uint64) (nodeInfo clusterconfig.NodeInfo, err error)
	// Route 设置接受请求的路由
	Route(path string, handler wkserver.Handler)
	// RequestWithContext 发送请求给指定的节点
	RequestWithContext(ctx context.Context, toNodeId uint64, path string, body []byte) (*proto.Response, error)
	// Send 发送消息给指定的节点, MsgType 使用 1000 - 2000之间的值
	Send(toNodeId uint64, msg *proto.Message) error
	// OnMessage 设置接收消息的回调
	OnMessage(f func(msg *proto.Message))
	// NodeIsOnline 节点是否在线
	NodeIsOnline(nodeId uint64) bool
	// Monitor 获取监控信息
	Monitor() IMonitor
}

type IMonitor interface {
	// RequestGoroutine 请求协程数
	RequestGoroutine() int64
	// MessageGoroutine 消息处理协程数
	MessageGoroutine() int64
	// InboundFlightMessageCount 入站飞行消息数
	InboundFlightMessageCount() int64
	// OutboundFlightMessageCount 出站飞行消息数
	OutboundFlightMessageCount() int64
	// InboundFlightMessageBytes 入站飞行消息字节数
	InboundFlightMessageBytes() int64
	// OutboundFlightMessageBytes 出站飞行消息字节数
	OutboundFlightMessageBytes() int64
}
