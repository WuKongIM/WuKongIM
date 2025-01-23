package trace

import "github.com/WuKongIM/WuKongIM/pkg/wkhttp"

type ClusterKind int

const (
	// ClusterKindUnknown 未知
	ClusterKindUnknown ClusterKind = iota
	// ClusterKindSlot 槽
	ClusterKindSlot
	// ClusterKindChannel 频道
	ClusterKindChannel
	// ClusterKindConfig 配置
	ClusterKindConfig
	// ClusterKindOther 其他
	ClusterKindOther
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

	Route(r *wkhttp.WKHttp)
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
	// ========== compact 压缩相关 ==========
	// CompactTotalCountSet 压缩总次数
	CompactTotalCountSet(shardId uint32, v int64)
	// CompactDefaultCountSet （还没搞清楚😂）
	CompactDefaultCountSet(shardId uint32, v int64)
	// CompactDeleteOnlyCountSet 删除次数
	CompactDeleteOnlyCountSet(shardId uint32, v int64)
	// CompactElisionOnlyCountSet 移除了多余数据次数
	CompactElisionOnlyCountSet(shardId uint32, v int64)
	// CompactMoveCountSet 移动次数
	CompactMoveCountSet(shardId uint32, v int64)
	// CompactReadCountSet 读取次数
	CompactReadCountSet(shardId uint32, v int64)
	// CompactRewriteCountSet 重写次数
	CompactRewriteCountSet(shardId uint32, v int64)
	// CompactMultiLevelCount 多级压缩次数
	CompactMultiLevelCount(shardId uint32, v int64)
	// CompactEstimatedDebtSet 预计还要压缩多少个字节才能到达稳定状态，这个值越大，说明需要压缩的数据越多
	CompactEstimatedDebtSet(shardId uint32, v int64)
	// CompactInProgressBytesSet 正在压缩的字节数量
	CompactInProgressBytesSet(shardId uint32, v int64)
	// CompactInProgressSet 正在压缩的操作数量
	CompactNumInProgressSet(shardId uint32, v int64)
	// CompactMarkedFilesSet 被标记需要压缩的文件数量
	CompactMarkedFilesSet(shardId uint32, v int64)

	// ========== flush 相关 ==========
	// FlushCountSet flush次数
	FlushCountAdd(shardId uint32, v int64)
	// FlushBytesSet flush字节数量
	FlushBytesAdd(shardId uint32, v int64)
	// FlushNumInProgressSet 进行中的flush数量
	FlushNumInProgressAdd(shardId uint32, v int64)
	// FlushAsIngestCountSet flush作为ingest的次数
	FlushAsIngestCountAdd(shardId uint32, v int64)
	// FlushAsIngestTableCountSet flush作为ingest的表数量
	FlushAsIngestTableCountAdd(shardId uint32, v int64)
	// FlushAsIngestBytesSet flush作为ingest的字节数量
	FlushAsIngestBytesAdd(shardId uint32, v int64)

	// ========== memtable 内存表相关 ==========
	MemTableSizeSet(shardId uint32, v int64)
	MemTableCountSet(shardId uint32, v int64)
	// MemTableZombieSizeSet 那些已经被标记为删除但尚未从物理存储中清除的数据大小。
	MemTableZombieSizeSet(shardId uint32, v int64)
	// MemTableZombieCountSet 那些已经被标记为删除但尚未从物理存储中清除的数据数量。
	MemTableZombieCountSet(shardId uint32, v int64)

	// ========== Snapshots 镜像相关 ==========
	// SnapshotsCountSet 当前打开的镜像数量
	SnapshotsCountSet(shardId uint32, v int64)

	// ========== TableCache 相关 ==========
	// TableCacheSizeSet 表缓存大小
	TableCacheSizeSet(shardId uint32, v int64)
	// TableCacheCountSet 表缓存数量
	TableCacheCountSet(shardId uint32, v int64)

	// ========== TableIters 相关 ==========
	// TableItersCountSet sstable iterators数量
	TableItersCountSet(shardId uint32, v int64)

	// ========== WAL 相关 ==========

	// WALFilesCountSet WAL文件数量
	WALFilesCountSet(shardId uint32, v int64)
	// WALSizeSet WAL数据大小
	WALSizeSet(shardId uint32, v int64)
	// WALPhysicalSizeSet WAL物理硬盘上的大小
	WALPhysicalSizeSet(shardId uint32, v int64)
	// WALObsoleteFilesCountSet WAL过时文件数量
	WALObsoleteFilesCountSet(shardId uint32, v int64)
	// WALObsoletePhysicalSizeSet WAL过时物理大小
	WALObsoletePhysicalSizeSet(shardId uint32, v int64)
	// WALBytesInSet 写入WAL的逻辑字节数
	WALBytesInSet(shardId uint32, v int64)
	// WALBytesWrittenSet 写入WAL的字节数
	WALBytesWrittenSet(shardId uint32, v int64)

	// ========== Log Writer 相关 ==========
	LogWriterBytesSet(shardId uint32, v int64)

	// ========== DB 相关 ==========
	DiskSpaceUsageSet(shardId uint32, v int64)

	// ========== level 相关 ==========
	LevelNumFilesSet(shardId uint32, v int64)
	LevelFileSizeSet(shardId uint32, v int64)
	LevelCompactScoreSet(shardId uint32, v int64)
	LevelBytesInSet(shardId uint32, v int64)
	LevelBytesIngestedSet(shardId uint32, v int64)
	LevelBytesMovedSet(shardId uint32, v int64)
	LevelBytesReadSet(shardId uint32, v int64)
	LevelBytesCompactedSet(shardId uint32, v int64)
	LevelBytesFlushedSet(shardId uint32, v int64)
	LevelTablesCompactedSet(shardId uint32, v int64)
	LevelTablesFlushedSet(shardId uint32, v int64)
	LevelTablesIngestedSet(shardId uint32, v int64)
	LevelTablesMovedSet(shardId uint32, v int64)

	// ========== message 相关 ==========

	// 消息批量追加次数
	MessageAppendBatchCountAdd(v int64)

	// ========== 基础操作 ==========
	SetAdd(v int64)         // set操作
	DeleteAdd(v int64)      // delete操作
	DeleteRangeAdd(v int64) // deleteRange操作
	CommitAdd(v int64)      // commit次数

	// ========== 数据操作 ==========
	// 白名单
	AddAllowlistAdd(v int64)       // 添加白名单
	GetAllowlistAdd(v int64)       // 获取白名单
	HasAllowlistAdd(v int64)       // 是否有白名单
	ExistAllowlistAdd(v int64)     // 是否存在白名单
	RemoveAllowlistAdd(v int64)    // 移除白名单
	RemoveAllAllowlistAdd(v int64) // 移除所有白名单

	// 分布式配置
	SaveChannelClusterConfigAdd(v int64)               // 保存频道配置
	SaveChannelClusterConfigsAdd(v int64)              // 保存频道配置
	GetChannelClusterConfigAdd(v int64)                // 获取频道配置
	GetChannelClusterConfigVersionAdd(v int64)         // 获取频道配置版本
	GetChannelClusterConfigsAdd(v int64)               // 获取频道配置列表
	SearchChannelClusterConfigAdd(v int64)             // 搜索频道配置
	GetChannelClusterConfigCountWithSlotIdAdd(v int64) // 获取槽位下的频道配置数量
	GetChannelClusterConfigWithSlotIdAdd(v int64)      // 获取槽位下的频道配置

	// 频道
	AddChannelAdd(v int64)                // 添加频道
	UpdateChannelAdd(v int64)             // 更新频道
	GetChannelAdd(v int64)                // 获取频道
	SearchChannelsAdd(v int64)            // 搜索频道
	ExistChannelAdd(v int64)              // 是否存在频道
	UpdateChannelAppliedIndexAdd(v int64) // 更新频道应用索引
	GetChannelAppliedIndexAdd(v int64)    // 获取频道应用索引
	DeleteChannelAdd(v int64)             // 删除频道

	// 最近会话
	AddOrUpdateConversationsAddWithUser(v int64) // 添加或更新最近会话
	AddOrUpdateConversationsAdd(v int64)         // 添加或更新最近会话
	GetConversationsAdd(v int64)                 // 获取最近会话
	GetConversationsByTypeAdd(v int64)           // 获取最近会话
	GetLastConversationsAdd(v int64)             // 获取最近会话
	GetConversationAdd(v int64)                  // 获取最近会话
	ExistConversationAdd(v int64)                // 是否存在最近会话
	DeleteConversationAdd(v int64)               // 删除最近会话
	DeleteConversationsAdd(v int64)              // 删除最近会话
	SearchConversationAdd(v int64)               // 搜索最近会话
	AddDenylistAdd(v int64)                      // 添加黑名单
	GetDenylistAdd(v int64)                      // 获取黑名单
	ExistDenylistAdd(v int64)                    // 是否存在黑名单
	RemoveDenylistAdd(v int64)                   // 移除黑名单
	RemoveAllDenylistAdd(v int64)                // 移除所有黑名单

	// 设备
	GetDeviceAdd(v int64)      // 获得设备
	GetDevicesAdd(v int64)     // 获得设备
	GetDeviceCountAdd(v int64) // 获得设备数量
	AddDeviceAdd(v int64)      // 添加设备
	UpdateDeviceAdd(v int64)   // 更新设备
	SearchDeviceAdd(v int64)   // 搜索设备

	// 消息队列
	AppendMessageOfNotifyQueueAdd(v int64)  // 添加消息到通知队列
	GetMessagesOfNotifyQueueAdd(v int64)    // 获取通知队列的消息
	RemoveMessagesOfNotifyQueueAdd(v int64) // 移除通知队列的消息

	// 消息
	AppendMessagesAdd(v int64)           // 添加消息
	AppendMessagesBatchAdd(v int64)      // 批量添加消息
	GetMessageAdd(v int64)               // 获取消息
	LoadPrevRangeMsgsAdd(v int64)        // 加载前面的消息
	LoadNextRangeMsgsAdd(v int64)        // 加载后面的消息
	LoadMsgAdd(v int64)                  // 加载消息
	LoadLastMsgsAdd(v int64)             // 加载最后的消息
	LoadLastMsgsWithEndAdd(v int64)      // 加载最后的消息
	LoadNextRangeMsgsForSizeAdd(v int64) // 加载下一个范围的消息
	TruncateLogToAdd(v int64)            // 截断日志
	GetChannelLastMessageSeqAdd(v int64) // 获取频道最后的消息序号
	SetChannelLastMessageSeqAdd(v int64) // 设置频道最后的消息序号
	SearchMessagesAdd(v int64)           // 搜索消息

	// 订阅者
	AddSubscribersAdd(v int64)      // 添加订阅者
	GetSubscribersAdd(v int64)      // 获取订阅者
	RemoveSubscribersAdd(v int64)   // 移除订阅者
	ExistSubscriberAdd(v int64)     // 是否存在订阅者
	RemoveAllSubscriberAdd(v int64) // 移除所有订阅者

	// 系统账号
	AddSystemUidsAdd(v int64)    // 添加系统UID
	RemoveSystemUidsAdd(v int64) // 移除系统UID
	GetSystemUidsAdd(v int64)    // 获取系统UID

	// 用户
	GetUserAdd(v int64)    // 获取用户
	ExistUserAdd(v int64)  // 是否存在用户
	SearchUserAdd(v int64) // 搜索用户
	AddUserAdd(v int64)    // 添加用户
	UpdateUserAdd(v int64) // 更新用户

	// leader_term_sequence
	SetLeaderTermStartIndexAdd(v int64)                   // 设置领导者任期开始索引
	LeaderLastTermAdd(v int64)                            // 领导者最后任期
	LeaderTermStartIndexAdd(v int64)                      // 领导者任期开始索引
	LeaderLastTermGreaterThanAdd(v int64)                 // 领导者最后任期大于
	DeleteLeaderTermStartIndexGreaterThanTermAdd(v int64) // 删除领导者任期开始索引大于
}

// AppMetrics 应用监控
type IAppMetrics interface {
	// ConnCountAdd 连接数
	ConnCountAdd(v int64)
	// OnlineUserCount 在线人用户数
	OnlineUserCountAdd(v int64)
	// OnlineUserCountSet 在线人用户数
	OnlineUserCountSet(v int64)
	// OnlineDeviceCount 在线设备数
	OnlineDeviceCountAdd(v int64)
	// OnlineDeviceCountSet 在线设备数
	OnlineDeviceCountSet(v int64)

	// MessageLatencyOb 消息延迟
	MessageLatencyOb(v int64)

	// PingBytesAdd ping流量
	PingBytesAdd(v int64)
	PingBytes() int64

	// PingCountAdd ping数量
	PingCountAdd(v int64)
	PingCount() int64

	// PongBytesAdd pong流量
	PongBytesAdd(v int64)
	PongBytes() int64
	// PongCountAdd pong数量
	PongCountAdd(v int64)
	PongCount() int64

	// SendPacketBytesAdd 发送包流量
	SendPacketBytesAdd(v int64)
	SendPacketBytes() int64
	// SendPacketCountAdd 发送包数量
	SendPacketCountAdd(v int64)
	SendPacketCount() int64

	// SendackPacketBytesAdd 发送应答包流量
	SendackPacketBytesAdd(v int64)
	SendackPacketBytes() int64
	// SendackPacketCountAdd 发送应答包数量
	SendackPacketCountAdd(v int64)
	SendackPacketCount() int64

	// RecvPacketBytesAdd 接收包流量
	RecvPacketBytesAdd(v int64)
	RecvPacketBytes() int64
	// RecvPacketCountAdd 接收包数量
	RecvPacketCountAdd(v int64)
	RecvPacketCount() int64

	// RecvackPacketBytesAdd 接收应答包流量
	RecvackPacketBytesAdd(v int64)
	RecvackPacketBytes() int64
	// RecvackPacketCountAdd 接收应答包数量
	RecvackPacketCountAdd(v int64)
	RecvackPacketCount() int64

	// ConnPacketBytesAdd 连接包流量
	ConnPacketBytesAdd(v int64)
	ConnPacketBytes() int64
	// ConnPacketCountAdd 连接包数量
	ConnPacketCountAdd(v int64)
	ConnPacketCount() int64

	// ConnackPacketBytesAdd 连接应答包流量
	ConnackPacketBytesAdd(v int64)
	ConnackPacketBytes() int64
	// ConnackPacketCountAdd 连接应答包数量
	ConnackPacketCountAdd(v int64)
	ConnackPacketCount() int64
}

// IClusterMetrics 分布式监控
type IClusterMetrics interface {
	// MessageIncomingBytesAdd 消息入口流量
	MessageIncomingBytesAdd(kind ClusterKind, v int64)
	// MessageOutgoingBytesAdd 消息出口流量
	MessageOutgoingBytesAdd(kind ClusterKind, v int64)

	// MessageIncomingCountAdd 消息入口数量
	MessageIncomingCountAdd(kind ClusterKind, v int64)
	// MessageOutgoingCountAdd 消息出口数量
	MessageOutgoingCountAdd(kind ClusterKind, v int64)

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

	// ChannelActiveCountAdd 频道激活数量
	ChannelActiveCountAdd(v int64)

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

	// ProposeFailedCountAdd 提案失败的次数
	ProposeFailedCountAdd(kind ClusterKind, v int64)

	// ObserverNodeRequesting 节点请求中的数量
	ObserverNodeRequesting(f func() int64)

	// ObserverNodeSending 节点消息发送中的数量
	ObserverNodeSending(f func() int64)
}
