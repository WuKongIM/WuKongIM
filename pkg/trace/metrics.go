package trace

type ClusterKind int

const (
	// ClusterKindUnknown 未知
	ClusterKindUnknown ClusterKind = iota
	// ClusterKindSlot 槽
	ClusterKindSlot
	// ClusterKindChannel 频道
	ClusterKindChannel
)

type IMetrics interface {
	// System 系统监控
	System() ISystemMetrics
	// App  应用监控
	App() IAppMetrics
	// Cluster 分布式监控
	Cluster() IClusterMetrics
	// DB 数据库监控
	DB() IDBMetrics
}

// SystemMetrics 系统监控
type ISystemMetrics interface {
	// IntranetIncomingAdd 内网入口流量
	IntranetIncomingAdd(v int64)
	// IntranetOutgoingAdd 内网出口流量
	IntranetOutgoingAdd(v int64)

	// ExtranetIncomingAdd 外网入口流量
	ExtranetIncomingAdd(v int64)
	// ExtranetOutgoingAdd 外网出口流量
	ExtranetOutgoingAdd(v int64)

	// CPUUsageAdd CPU使用率
	CPUUsageAdd(v float64)
	// MemoryUsageAdd 内存使用率
	MemoryUsageAdd(v float64)
	// DiskIOReadCountAdd 磁盘读取次数
	DiskIOReadCountAdd(v int64)
	// DiskIOWriteCountAdd 磁盘写入次数
	DiskIOWriteCountAdd(v int64)
}

// IDBMetrics 数据库监控
type IDBMetrics interface {
}

// AppMetrics 应用监控
type IAppMetrics interface {
	// ConnCountAdd 连接数
	ConnCountAdd(v int64)
	// OnlineUserCount 在线人用户数
	OnlineUserCountAdd(v int64)
	// OnlineDeviceCount 在线设备数
	OnlineDeviceCountAdd(v int64)

	// MessageLatencyOb 消息延迟
	MessageLatencyOb(v float64)

	// PingBytesAdd ping流量
	PingBytesAdd(v int64)
	// PingCountAdd ping数量
	PingCountAdd(v int64)

	// PongBytesAdd pong流量
	PongBytesAdd(v int64)
	// PongCountAdd pong数量
	PongCountAdd(v int64)

	// SendPacketBytesAdd 发送包流量
	SendPacketBytesAdd(v int64)
	// SendPacketCountAdd 发送包数量
	SendPacketCountAdd(v int64)

	// SendackPacketBytesAdd 发送应答包流量
	SendackPacketBytesAdd(v int64)
	// SendackPacketCountAdd 发送应答包数量
	SendackPacketCountAdd(v int64)

	// RecvPacketBytesAdd 接收包流量
	RecvPacketBytesAdd(v int64)
	// RecvPacketCountAdd 接收包数量
	RecvPacketCountAdd(v int64)

	// RecvackPacketBytesAdd 接收应答包流量
	RecvackPacketBytesAdd(v int64)
	// RecvackPacketCountAdd 接收应答包数量
	RecvackPacketCountAdd(v int64)

	// ConnPacketBytesAdd 连接包流量
	ConnPacketBytesAdd(v int64)
	// ConnPacketCountAdd 连接包数量
	ConnPacketCountAdd(v int64)

	// ConnackPacketBytesAdd 连接应答包流量
	ConnackPacketBytesAdd(v int64)
	// ConnackPacketCountAdd 连接应答包数量
	ConnackPacketCountAdd(v int64)
}

// IClusterMetrics 分布式监控
type IClusterMetrics interface {
	// MessageIncomingBytesAdd 消息入口流量
	MessageIncomingBytesAdd(v int64)
	// MessageOutgoingBytesAdd 消息出口流量
	MessageOutgoingBytesAdd(v int64)

	// MessageIncomingCountAdd 消息入口数量
	MessageIncomingCountAdd(v int64)
	// MessageOutgoingCountAdd 消息出口数量
	MessageOutgoingCountAdd(v int64)

	// MessageConcurrencyAdd 消息并发数
	MessageConcurrencyAdd(v int64)

	// SendPacketIncomingBytesAdd 发送包入口流量
	SendPacketIncomingBytesAdd(v int64)
	// SendPacketOutgoingBytesAdd 发送包出口流量
	SendPacketOutgoingBytesAdd(v int64)

	// SendPacketIncomingCountAdd 发送包入口数量
	SendPacketIncomingCountAdd(v int64)
	// SendPacketOutgoingCountAdd 发送包出口数量
	SendPacketOutgoingCountAdd(v int64)

	// RecvPacketIncomingBytesAdd 接收包入口流量
	RecvPacketIncomingBytesAdd(v int64)
	// RecvPacketOutgoingBytesAdd 接收包出口流量
	RecvPacketOutgoingBytesAdd(v int64)

	// RecvPacketIncomingCountAdd 接受包入口数量
	RecvPacketIncomingCountAdd(v int64)
	// RecvPacketOutgoingCountAdd 接受包出口数量
	RecvPacketOutgoingCountAdd(v int64)

	// MsgSyncIncomingBytesAdd 消息同步入口流量
	MsgSyncIncomingBytesAdd(kind ClusterKind, v int64)
	// MsgSyncIncomingCountAdd 消息同步入口数量
	MsgSyncIncomingCountAdd(kind ClusterKind, v int64)

	// MsgSyncOutgoingBytesAdd 消息同步出口流量
	MsgSyncOutgoingBytesAdd(kind ClusterKind, v int64)
	// MsgSyncOutgoingCountAdd 消息同步出口数量
	MsgSyncOutgoingCountAdd(kind ClusterKind, v int64)

	// MsgSyncRespIncomingBytesAdd 消息同步响应入口流量
	MsgSyncRespIncomingBytesAdd(kind ClusterKind, v int64)
	// MsgSyncRespIncomingCountAdd 消息同步响应入口数量
	MsgSyncRespIncomingCountAdd(kind ClusterKind, v int64)

	// MsgSyncRespOutgoingBytesAdd 消息同步响应出口流量
	MsgSyncRespOutgoingBytesAdd(kind ClusterKind, v int64)
	// MsgSyncRespOutgoingCountAdd 消息同步响应出口数量
	MsgSyncRespOutgoingCountAdd(kind ClusterKind, v int64)

	// ClusterPingIncomingBytesAdd 分布式副本ping入口流量
	MsgClusterPingIncomingBytesAdd(kind ClusterKind, v int64)
	// ClusterPingIncomingCountAdd 分布式副本ping入口数量
	MsgClusterPingIncomingCountAdd(kind ClusterKind, v int64)

	// ClusterPingOutgoingBytesAdd 分布式副本ping出口流量
	MsgClusterPingOutgoingBytesAdd(kind ClusterKind, v int64)
	// ClusterPingOutgoingCountAdd 分布式副本ping出口数量
	MsgClusterPingOutgoingCountAdd(kind ClusterKind, v int64)

	// ClusterPongBytesAdd 分布式副本pong入口流量
	MsgClusterPongIncomingBytesAdd(kind ClusterKind, v int64)
	// ClusterPongCountAdd 分布式副本pong入口数量
	MsgClusterPongIncomingCountAdd(kind ClusterKind, v int64)

	// ClusterPongOutgoingBytesAdd 分布式副本pong出口流量
	MsgClusterPongOutgoingBytesAdd(kind ClusterKind, v int64)
	// ClusterPongOutgoingCountAdd 分布式副本pong出口数量
	MsgClusterPongOutgoingCountAdd(kind ClusterKind, v int64)

	// LogIncomingBytesAdd 日志入口流量
	LogIncomingBytesAdd(kind ClusterKind, v int64)
	// LogIncomingCountAdd 日志入口数量
	LogIncomingCountAdd(kind ClusterKind, v int64)

	// LogOutgoingBytesAdd 日志出口流量
	LogOutgoingBytesAdd(kind ClusterKind, v int64)
	// LogOutgoingCountAdd 日志出口数量
	LogOutgoingCountAdd(kind ClusterKind, v int64)

	// MsgLeaderTermStartIndexReqIncomingBytesAdd 领导者任期开始索引请求入口流量
	MsgLeaderTermStartIndexReqIncomingBytesAdd(kind ClusterKind, v int64)
	// MsgLeaderTermStartIndexReqIncomingCountAdd 领导者任期开始索引请求入口数量
	MsgLeaderTermStartIndexReqIncomingCountAdd(kind ClusterKind, v int64)

	// MsgLeaderTermStartIndexReqOutgoingBytesAdd 领导者任期开始索引请求出口流量
	MsgLeaderTermStartIndexReqOutgoingBytesAdd(kind ClusterKind, v int64)
	// MsgLeaderTermStartIndexReqOutgoingCountAdd 领导者任期开始索引请求出口数量
	MsgLeaderTermStartIndexReqOutgoingCountAdd(kind ClusterKind, v int64)

	// MsgLeaderTermStartIndexRespIncomingBytesAdd 领导者任期开始索引响应入口流量
	MsgLeaderTermStartIndexRespIncomingBytesAdd(kind ClusterKind, v int64)
	// MsgLeaderTermStartIndexRespIncomingCountAdd 领导者任期开始索引响应入口数量
	MsgLeaderTermStartIndexRespIncomingCountAdd(kind ClusterKind, v int64)

	// MsgLeaderTermStartIndexRespOutgoingBytesAdd 领导者任期开始索引响应出口流量
	MsgLeaderTermStartIndexRespOutgoingBytesAdd(kind ClusterKind, v int64)
	// MsgLeaderTermStartIndexRespOutgoingCountAdd 领导者任期开始索引响应出口数量
	MsgLeaderTermStartIndexRespOutgoingCountAdd(kind ClusterKind, v int64)

	// ForwardProposeBytesAdd 转发提议流量
	ForwardProposeBytesAdd(v int64)
	// ForwardProposeCountAdd 转发提议数量
	ForwardProposeCountAdd(v int64)

	// ForwardProposeRespBytesAdd 转发提议响应流量
	ForwardProposeRespBytesAdd(v int64)
	// ForwardProposeRespCountAdd 转发提议响应数量
	ForwardProposeRespCountAdd(v int64)

	// ForwardConnPingBytesAdd 转发连接ping流量（如果客户端没有连接到真正的逻辑节点，则代理节点会转发ping给真正的逻辑节点）
	ForwardConnPingBytesAdd(v int64)
	// ForwardConnPingCountAdd 转发连接ping数量（如果客户端没有连接到真正的逻辑节点，则代理节点会转发ping给真正的逻辑节点）
	ForwardConnPingCountAdd(v int64)

	// ForwardConnPongBytesAdd 转发连接pong流量（如果客户端没有连接到真正的逻辑节点，则代理节点会转发pong给真正的逻辑节点）
	ForwardConnPongBytesAdd(v int64)
	// ForwardConnPongCountAdd 转发连接pong数量（如果客户端没有连接到真正的逻辑节点，则代理节点会转发pong给真正的逻辑节点）
	ForwardConnPongCountAdd(v int64)

	// ChannelReplicaActiveCountAdd 频道副本激活数量
	ChannelReplicaActiveCountAdd(v int64)

	// ChannelElectionCountAdd 频道选举次数
	ChannelElectionCountAdd(v int64)
	// ChannelElectionSuccessCountAdd 频道选举成功次数
	ChannelElectionSuccessCountAdd(v int64)
	// ChannelElectionFailCountAdd 频道选举失败次数
	ChannelElectionFailCountAdd(v int64)

	// SlotElectionCountAdd  槽位选举次数
	SlotElectionCountAdd(v int64)
	// SlotElectionSuccessCountAdd  槽位选举成功次数
	SlotElectionSuccessCountAdd(v int64)
	// SlotElectionFailCountAdd  槽位选举失败次数
	SlotElectionFailCountAdd(v int64)

	// ProposeLatencyAdd 提案延迟统计
	ProposeLatencyAdd(kind ClusterKind, v int64)
}
