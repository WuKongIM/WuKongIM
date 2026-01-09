package wkdb

type DB interface {
	Open() error
	Close() error
	// 获取下一个主键
	NextPrimaryKey() uint64
	// 消息
	MessageDB
	// 用户
	UserDB
	// 设备
	DeviceDB
	// channel
	ChannelDB
	// 最近会话
	ConversationDB
	// 频道分布式配置
	ChannelClusterConfigDB
	// 领导任期开始的第一条日志索引
	LeaderTermSequenceDB
	// 会话
	// SessionDB
	// 数据统计
	TotalDB
	//	系统账号
	SystemUidDB
	// 流
	StreamDB
	// 测试机
	TesterDB
	// 插件
	PluginDB

	GetPerformanceMonitor() *PerformanceMonitor

	GetCacheManager() *CacheManager

	// GetShardNum 获取数据库分片数量
	GetShardNum() int
	// GetChannelShardIndex 获取频道所在的分片索引
	GetChannelShardIndex(channelId string, channelType uint8) uint32
}

type MessageDB interface {

	// GetMessage 获取指定消息id的消息 TODO: 如果消息不在此节点上，是查询不到的，需要通过频道id判断消息是否在此节点上
	GetMessage(messageId uint64) (Message, error)

	// AppendMessages appends messages to the db.
	AppendMessages(channelId string, channelType uint8, msgs []Message) error

	// LoadPrevRangeMsgs 向上加载指定范围的消息 end=0表示不做限制 比如 start=100 end=0 limit=10 则返回的消息seq为91-100的消息, 比如 start=100 end=95 limit=10 则返回的消息seq为96-100的消息
	// 结果包含start,不包含end
	LoadPrevRangeMsgs(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]Message, error)

	// LoadNextRangeMsgs 向下加载指定范围的消息 end=0表示不做限制 比如 start=100 end=200 limit=10 则返回的消息seq为100-109的消息，
	// 比如start=100 end=105 limit=10 则返回的消息seq为100-104的消息
	// 结果包含start,不包含end
	LoadNextRangeMsgs(channelId string, channelType uint8, start, end uint64, limit int) ([]Message, error)

	LoadNextRangeMsgsForSize(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limitSize uint64) ([]Message, error)
	// LoadMsg 加载指定seq的消息
	LoadMsg(channelId string, channelType uint8, seq uint64) (Message, error)
	// // TruncateLogTo 截断消息, 从messageSeq开始截断,messageSeq=0 表示清空所有日志 （保留下来的内容包含messageSeq）
	TruncateLogTo(channelId string, channelType uint8, messageSeq uint64) error

	// LoadLastMsgsWithEnd 加载最新的消息 endMessageSeq表示加载到endMessageSeq的位置结束加载 endMessageSeq=0表示不做限制 结果不包含endMessageSeq
	LoadLastMsgsWithEnd(channelId string, channelType uint8, endMessageSeq uint64, limit int) ([]Message, error)
	// LoadLastMsgs 加载最后的消息
	LoadLastMsgs(channelID string, channelType uint8, limit int) ([]Message, error)
	// GetChannelLastMessageSeq 获取最后一条消息的seq
	GetChannelLastMessageSeq(channelId string, channelType uint8) (seq uint64, lastTime uint64, err error)

	// SetChannelLastMessageSeq 设置最后一条消息的seq
	SetChannelLastMessageSeq(channelId string, channelType uint8, seq uint64) error
	// SetChannellastMessageSeqBatch 批量设置最后一条消息的seq
	// SetChannellastMessageSeqBatch(reqs []SetChannelLastMessageSeqReq) error

	// AppendMessageOfNotifyQueue 添加消息到通知队列
	AppendMessageOfNotifyQueue(messages []Message) error

	// GetMessagesOfNotifyQueue 获取通知队列的消息
	GetMessagesOfNotifyQueue(count int) ([]Message, error)

	// RemoveMessagesOfNotifyQueue 移除通知队列的消息
	RemoveMessagesOfNotifyQueue(messageIDs []int64) error

	// RemoveMessagesOfNotifyQueueCount 移除指定数量的通知队列的消息
	RemoveMessagesOfNotifyQueueCount(count int) error

	// 搜索消息
	SearchMessages(req MessageSearchReq) ([]Message, error)

	// GetLastMsg 获取最后一条消息
	GetLastMsg(channelId string, channelType uint8) (Message, error)

	// LoadMsgByClientMsgNo 通过 clientMsgNo 加载消息
	LoadMsgByClientMsgNo(channelId string, channelType uint8, clientMsgNo string) (Message, error)

	// GetUserLastMsgSeq 获取用户在指定频道内发送的最新一条消息的seq
	GetUserLastMsgSeq(fromUid string, channelId string, channelType uint8) (uint64, error)

	// LoadMsgsBatch 批量获取多个频道的消息
	LoadMsgsBatch(requests []BatchMsgRequest) ([]BatchMsgResponse, error)

	// GetUserLastMsgSeqBatch 批量获取用户在多个频道的最后消息序号
	GetUserLastMsgSeqBatch(fromUid string, channels []Channel) (map[string]uint64, error)
}

// BatchMsgRequest 批量消息查询请求
type BatchMsgRequest struct {
	ChannelId   string // 频道ID
	ChannelType uint8  // 频道类型
	MsgSeq      uint64 // 消息序号（OrderByLast=true时为EndSeq，否则为StartSeq）
	Limit       int    // 限制数量
	OrderByLast bool   // true: 从最新往前查（LoadLastMsgsWithEnd）, false: 从指定位置往后查（LoadNextRangeMsgs）
}

// BatchMsgResponse 批量消息查询响应
type BatchMsgResponse struct {
	ChannelId   string    // 频道ID
	ChannelType uint8     // 频道类型
	Messages    []Message // 消息列表
}

type DeviceDB interface {
	// GetDevice 获取设备
	GetDevice(uid string, deviceFlag uint64) (Device, error)

	// GetDevices 获取用户的所有设备
	GetDevices(uid string) ([]Device, error)

	// GetDeviceCount 获取用户的设备数量
	GetDeviceCount(uid string) (int, error)

	// AddDevice 添加设备
	AddDevice(device Device) error

	// UpdateDevice 更新设备
	UpdateDevice(device Device) error
}

type UserDB interface {
	// GetUserToken 获取用户信息
	GetUser(uid string) (User, error)

	// ExistUser 判断用户是否存在
	ExistUser(uid string) (bool, error)

	// SearchUser 搜索用户
	SearchUser(req UserSearchReq) ([]User, error)

	// SearchDevice 搜索设备
	SearchDevice(req DeviceSearchReq) ([]Device, error)

	// AddUser 添加用户
	AddUser(u User) error

	// UpdateUser 更新用户
	UpdateUser(u User) error
}

type ChannelDB interface {
	// AddSubscribers 添加订阅者
	AddSubscribers(channelId string, channelType uint8, members []Member) error

	// RemoveSubscribers 移除订阅者
	RemoveSubscribers(channelId string, channelType uint8, uids []string) error

	// ExistSubscriber 判断订阅者是否存在
	ExistSubscriber(channelId string, channelType uint8, uid string) (bool, error)

	// RemoveAllSubscriber 移除所有订阅者
	RemoveAllSubscriber(channelId string, channelType uint8) error

	// GetSubscribers 获取订阅者
	GetSubscribers(channelId string, channelType uint8) ([]Member, error)

	// GetSubscriberCount 获取订阅者数量
	GetSubscriberCount(channelId string, channelType uint8) (int, error)

	// AddOrUpdateChannel  添加或更新channel
	AddChannel(channelInfo ChannelInfo) (uint64, error)
	// UpdateChannel 更新channel
	UpdateChannel(channelInfo ChannelInfo) error

	// GetChannel 获取channel
	GetChannel(channelId string, channelType uint8) (ChannelInfo, error)

	// ExistChannel 判断channel是否存在
	ExistChannel(channelId string, channelType uint8) (bool, error)

	// DeleteChannel 删除channel
	DeleteChannel(channelId string, channelType uint8) error

	// AddDenylist 添加黑名单
	AddDenylist(channelId string, channelType uint8, members []Member) error
	// GetDenylist 获取黑名单
	GetDenylist(channelId string, channelType uint8) ([]Member, error)

	// RemoveDenylist 移除黑名单
	RemoveDenylist(channelId string, channelType uint8, uids []string) error

	// RemoveAllDenylist 移除所有黑名单
	RemoveAllDenylist(channelId string, channelType uint8) error

	// ExistDenylist 判断黑名单是否存在
	ExistDenylist(channelId string, channelType uint8, uid string) (bool, error)

	// AddAllowlist 添加白名单
	AddAllowlist(channelId string, channelType uint8, members []Member) error

	// GetAllowlist 获取白名单
	GetAllowlist(channelId string, channelType uint8) ([]Member, error)

	// RemoveAllowlist 移除白名单
	RemoveAllowlist(channelId string, channelType uint8, uids []string) error

	// RemoveAllAllowlist 移除所有白名单
	RemoveAllAllowlist(channelId string, channelType uint8) error

	// ExistAllowlist 判断白名单是否存在
	ExistAllowlist(channelId string, channelType uint8, uid string) (bool, error)

	// HasAllowlist  判断是否有白名单
	HasAllowlist(channelId string, channelType uint8) (bool, error)

	// 更新频道的应用索引
	UpdateChannelAppliedIndex(channelId string, channelType uint8, index uint64) error
	// 获取频道的应用索引
	GetChannelAppliedIndex(channelId string, channelType uint8) (uint64, error)

	// SearchChannels 搜索频道
	SearchChannels(req ChannelSearchReq) ([]ChannelInfo, error)
}

type ConversationDB interface {
	// AddOrUpdateConversations 添加或更新最近会话
	AddOrUpdateConversations(conversations []Conversation) error

	// AddOrUpdateConversationsBatchIfNotExist 批量添加或更新最近会话，如果存在则不添加
	AddOrUpdateConversationsBatchIfNotExist(conversations []Conversation) error

	// AddOrUpdateConversationsWithUser 添加或更新最近会话
	AddOrUpdateConversationsWithUser(uid string, conversations []Conversation) error

	// UpdateConversationIfSeqGreaterAsync 如果readToMsgSeq大于当前最近会话的readToMsgSeq则更新当前最近会话 (异步操作)
	UpdateConversationIfSeqGreaterAsync(uid, channelId string, channelType uint8, readToMsgSeq uint64) error

	// DeleteConversation 删除最近会话
	DeleteConversation(uid string, channelId string, channelType uint8) error

	// DeleteConversations 批量删除最近会话
	DeleteConversations(uid string, channels []Channel) error

	// GetConversations 获取指定用户的最近会话
	GetConversations(uid string) ([]Conversation, error)

	// GetConversationsByType 获取指定用户的指定类型的最近会话
	GetConversationsByType(uid string, tp ConversationType) ([]Conversation, error)

	// GetLastConversations 获取指定用户的最近会话
	GetLastConversations(uid string, tp ConversationType, updatedAt uint64, excludeChannelTypes []uint8, limit int) ([]Conversation, error)

	// GetConversation 获取指定用户的指定会话
	GetConversation(uid string, channelId string, channelType uint8) (Conversation, error)

	// GetChannelConversationLocalUsers 获取频道的在本节点的最近会话的用户uid集合
	GetChannelConversationLocalUsers(channelId string, channelType uint8) ([]string, error)

	// ExistConversation 是否存在会话
	ExistConversation(uid string, channelId string, channelType uint8) (bool, error)

	// GetConversationBySessionIds(uid string, sessionIds []uint64) ([]Conversation, error)

	// SearchConversation 搜索最近会话
	SearchConversation(req ConversationSearchReq) ([]Conversation, error)

	// UpdateConversationDeletedAtMsgSeq 更新最近会话的已删除的消息序号位置
	UpdateConversationDeletedAtMsgSeq(uid string, channelId string, channelType uint8, deletedAtMsgSeq uint64) error

	// GetLastConversationIds 获取最近会话ID列表（用于测试重复ID问题）
	GetLastConversationIds(uid string, updatedAt uint64, limit int) ([]uint64, error)
}

type ChannelClusterConfigDB interface {

	// SaveChannelClusterConfig 保存频道的分布式配置
	SaveChannelClusterConfig(channelClusterConfig ChannelClusterConfig) error

	// SaveChannelClusterConfigs 批量保存频道的分布式配置
	SaveChannelClusterConfigs(channelClusterConfigs []ChannelClusterConfig) error

	// GetChannelClusterConfig 获取频道的分布式配置
	GetChannelClusterConfig(channelId string, channelType uint8) (ChannelClusterConfig, error)

	// DeleteChannelClusterConfig 删除频道的分布式配置
	// DeleteChannelClusterConfig(channelId string, channelType uint8) error

	// GetChannelClusterConfigs 获取频道的分布式配置
	GetChannelClusterConfigs(offsetId uint64, limit int) ([]ChannelClusterConfig, error)

	// GetChannelClusterConfigCountWithSlotId 获取某个槽的频道的分布式配置数量
	GetChannelClusterConfigCountWithSlotId(slotId uint32) (int, error)

	// GetChannelClusterConfigWithSlotId 获取某个槽的频道的分布式配置
	GetChannelClusterConfigWithSlotId(slotId uint32) ([]ChannelClusterConfig, error)

	// GetChannelClusterConfigVersion 获取频道的分布式配置版本
	GetChannelClusterConfigVersion(channelId string, channelType uint8) (uint64, error)

	// SearchChannelClusterConfig 搜索频道的分布式配置
	SearchChannelClusterConfig(req ChannelClusterConfigSearchReq, filter ...func(cfg ChannelClusterConfig) bool) ([]ChannelClusterConfig, error)
}

type LeaderTermSequenceDB interface {
	// SetLeaderTermStartIndex 设置领导任期开始的第一条日志索引
	SetLeaderTermStartIndex(shardNo string, term uint32, index uint64) error
	// LeaderLastTerm 获取最新的本地保存的领导任期
	LeaderLastTerm(shardNo string) (uint32, error)
	// LeaderTermStartIndex 获取领导任期开始的第一条日志索引
	LeaderTermStartIndex(shardNo string, term uint32) (uint64, error)
	// LeaderLastTermGreaterEqThan 获取大于或等于传入的term的最新的本地保存的领导任期
	LeaderLastTermGreaterEqThan(shardNo string, term uint32) (uint32, error)
	// DeleteLeaderTermStartIndexGreaterThanTerm 删除比传入的term大的的LeaderTermStartIndex记录
	DeleteLeaderTermStartIndexGreaterThanTerm(shardNo string, term uint32) error
}

// type SessionDB interface {
// 	// AddOrUpdateSession 添加或更新session
// 	AddOrUpdateSession(session Session) (Session, error)
// 	// GetSession 获取session
// 	GetSession(uid string, id uint64) (Session, error)
// 	// SearchSession 搜索session
// 	SearchSession(req SessionSearchReq) ([]Session, error)
// 	// DeleteSession 删除session
// 	DeleteSession(uid string, id uint64) error
// 	DeleteSessionByChannel(uid string, channelId string, channelType uint8) error
// 	// DeleteSessionAndConversationByChannel 删除session和最近会话
// 	DeleteSessionAndConversationByChannel(uid string, channelId string, channelType uint8) error
// 	// GetSessions 获取用户的session
// 	GetSessions(uid string) ([]Session, error)

// 	// GetSessionsByType 获取某个用户某个类型的会话
// 	GetSessionsByType(uid string, sessionType SessionType) ([]Session, error)

// 	// DeleteSessionByUid 删除用户的session
// 	DeleteSessionByUid(uid string) error
// 	// GetSessionByChannel 获取用户某个频道的session
// 	GetSessionByChannel(uid string, channelId string, channelType uint8) (Session, error)
// 	//	 UpdateSessionUpdatedAt 更新session的更新时间
// 	UpdateSessionUpdatedAt(models []*BatchUpdateConversationModel) error
// 	//	 GetLastSessionsByUid 获取用户最近的session
// 	// limit 为0表示不做限制
// 	GetLastSessionsByUid(uid string, sessionType SessionType, limit int) ([]Session, error)
// 	// 获取大于指定更新时间的session(不包含updatedAt)
// 	// limit 为0表示不做限制
// 	GetSessionsGreaterThanUpdatedAtByUid(uid string, sessionType SessionType, updatedAt int64, limit int) ([]Session, error)
// }

// 数据统计表
type TotalDB interface {
	// IncMessageCount 递增消息数量
	IncMessageCount(v int) error

	// IncUserCount 递增用户数量
	IncUserCount(v int) error

	// IncDeviceCount 递增设备数量
	IncDeviceCount(v int) error

	// IncChannelCount 递增频道数量
	IncChannelCount(v int) error

	// IncSessionCount 递增会话数量
	IncSessionCount(v int) error

	// IncConversationCount 递增最近会话数量
	IncConversationCount(v int) error

	// GetTotalMessageCount 获取总个消息数量
	GetTotalMessageCount() (int, error)

	// GetTotalUserCount 获取总个用户数量
	GetTotalUserCount() (int, error)

	// GetTotalDeviceCount 获取总个设备数量
	GetTotalDeviceCount() (int, error)

	// GetTotalSessionCount 获取总个会话数量
	GetTotalSessionCount() (int, error)

	// GetTotalChannelCount 获取总个频道数量
	GetTotalChannelCount() (int, error)

	// GetTotalConversationCount 获取总个最近会话数量
	GetTotalConversationCount() (int, error)

	// GetTotalChannelClusterConfigCount 获取总个频道分布式配置数量
	GetTotalChannelClusterConfigCount() (int, error)
}

type SystemUidDB interface {
	// AddSystemUids  添加系统账号的uid
	AddSystemUids(uids []string) error
	// RemoveSystemUids 移除系统账号的uid
	RemoveSystemUids(uids []string) error
	// GetSystemUids 获取系统账号的uid
	GetSystemUids() ([]string, error)
}

type StreamDB interface {
	// AddStreamMeta 添加流元数据
	AddStreamMeta(streamMeta *StreamMeta) error

	GetStreamMeta(streamNo string) (*StreamMeta, error)

	// AddStream 添加流
	AddStream(stream *Stream) error

	// AddStreams 批量添加流
	AddStreams(streams []*Stream) error

	// GetStreams 获取流
	GetStreams(streamNo string) ([]*Stream, error)

	// SaveStreamV2 保存流V2
	SaveStreamV2(stream *StreamV2) error
	// GetStreamV2 获取流V2
	GetStreamV2(clientMsgNo string) (*StreamV2, error)

	// GetStreamV2s 获取流V2
	GetStreamV2s(clientMsgNos []string) ([]*StreamV2, error)
}

type TesterDB interface {

	// AddOrUpdateTester 添加或更新测试机
	AddOrUpdateTester(tester Tester) error

	// GetTester 获取测试机
	GetTester(no string) (Tester, error)

	// GetTesters 获取测试机列表
	GetTesters() ([]Tester, error)

	// RemoveTester 移除测试机
	RemoveTester(no string) error
}

type PluginDB interface {
	// AddOrUpdatePlugin 添加或更新插件
	AddOrUpdatePlugin(plugin Plugin) error
	// DeletePlugin 删除插件
	DeletePlugin(no string) error
	// GetPlugins 获取插件列表
	GetPlugins() ([]Plugin, error)
	// GetPlugin 获取插件
	GetPlugin(no string) (Plugin, error)
	// AddOrUpdatePluginUsers 添加或更新插件用户，如果用户已经存在则更新
	AddOrUpdatePluginUsers(pluginUsers []PluginUser) error
	// RemovePluginUser 移除插件用户
	RemovePluginUser(pluginNo string, uid string) error
	// GetPluginUsers 获取插件绑定的用户列表
	GetPluginUsers(pluginNo string) ([]PluginUser, error)
	// GetHighestPriorityPluginByUid 获取用户最高优先级的插件
	GetHighestPriorityPluginByUid(uid string) (string, error)
	// ExistPluginByUid 判断用户是否存在插件
	ExistPluginByUid(uid string) (bool, error)
	// UpdatePluginConfig 更新插件配置
	UpdatePluginConfig(no string, config map[string]interface{}) error
	// GetPluginConfig 获取插件配置
	GetPluginConfig(no string) (map[string]interface{}, error)
	// SearchPluginUsers 搜索插件用户
	SearchPluginUsers(req SearchPluginUserReq) ([]PluginUser, error)
}

type MessageSearchReq struct {
	MessageId        int64
	FromUid          string // 发送者uid
	ChannelId        string // 频道id
	ChannelType      uint8  // 频道类型
	Limit            int    // 消息限制
	Payload          []byte // payload内容
	OffsetMessageId  int64  // 偏移的消息id
	OffsetMessageSeq uint64 // 偏移的消息seq(如果按频道查询，则分页需要传入这个值)
	Pre              bool   // 是否向前搜索

	ClientMsgNo string // 客户端消息编号
}

type ChannelSearchReq struct {
	ChannelId          string // 频道id
	ChannelType        uint8  // 频道类型
	Ban                *bool  // 是否被禁用
	Disband            *bool  // 是否解散
	SubscriberCountGte *int   // 大于等于的订阅者数量
	SubscriberCountLte *int   // 小于等于的订阅者数量
	DenylistCountGte   *int   // 大于等于的黑名单数量
	DenylistCountLte   *int   // 小于等于的黑名单数量
	AllowlistCountGte  *int   // 大于等于的白名单数量
	AllowlistCountLte  *int   // 小于等于的白名单数量
	Limit              int    // 限制查询数量
	OffsetCreatedAt    int64  // 偏移的创建时间
	Pre                bool   // 是否向前搜索
}

type UserSearchReq struct {
	Uid             string // 用户id
	Limit           int    // 限制查询数量
	OffsetCreatedAt int64  // 偏移id
	Pre             bool   // 是否向前搜索
}

type DeviceSearchReq struct {
	Uid             string // 用户id
	DeviceFlag      uint64 // 设备标识
	Limit           int    // 限制查询数量
	OffsetCreatedAt int64  // 偏移的创建时间
	Pre             bool   // 是否向前搜索
}

type ConversationSearchReq struct {
	Uid         string // 用户id
	Limit       int    // 限制查询数量
	CurrentPage int    // 当前页码

}

type SessionSearchReq struct {
	Uid         string // 用户id
	Limit       int    // 限制查询数量
	CurrentPage int    // 当前页码
}

type ChannelClusterConfigSearchReq struct {
	ChannelId       string // 频道id
	ChannelType     uint8  // 频道类型
	Limit           int    // 限制查询数量
	SlotLeaderId    uint64 // 槽领导者id
	OffsetCreatedAt int64  // 偏移的创建时间
	Pre             bool   // 是否向前搜索

}
