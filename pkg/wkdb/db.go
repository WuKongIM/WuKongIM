package wkdb

type DB interface {
	Open() error
	Close() error
	// 消息
	MessageDB
	// 用户
	UserDB
	// channel
	ChannelDB
	// 最近会话
	ConversationDB
	// 频道分布式配置
	ChannelClusterConfigDB
	// 领导任期开始的第一条日志索引
	LeaderTermSequenceDB
	// 会话
	SessionDB
}

type MessageDB interface {

	// AppendMessages appends messages to the db.
	AppendMessages(channelId string, channelType uint8, msgs []Message) error
	// AppendMessagesBatch 批量添加消息
	AppendMessagesBatch(reqs []AppendMessagesReq) error
	// LoadPrevRangeMsgs 向上加载指定范围的消息 end=0表示不做限制 比如 start=100 end=0 limit=10 则返回的消息seq为91-100的消息, 比如 start=100 end=95 limit=10 则返回的消息seq为96-100的消息
	// 结果包含start,不包含end
	LoadPrevRangeMsgs(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]Message, error)

	// LoadNextRangeMsgs 向下加载指定范围的消息 end=0表示不做限制 比如 start=100 end=200 limit=10 则返回的消息seq为101-111的消息，
	// 比如start=100 end=105 limit=10 则返回的消息seq为101-104的消息
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
	GetChannelLastMessageSeq(channelId string, channelType uint8) (uint64, uint64, error)

	// SetChannelLastMessageSeq 设置最后一条消息的seq
	SetChannelLastMessageSeq(channelId string, channelType uint8, seq uint64) error
	// SetChannellastMessageSeqBatch 批量设置最后一条消息的seq
	SetChannellastMessageSeqBatch(reqs []SetChannelLastMessageSeqReq) error

	// AppendMessageOfNotifyQueue 添加消息到通知队列
	AppendMessageOfNotifyQueue(messages []Message) error

	// GetMessagesOfNotifyQueue 获取通知队列的消息
	GetMessagesOfNotifyQueue(count int) ([]Message, error)

	// RemoveMessagesOfNotifyQueue 移除通知队列的消息
	RemoveMessagesOfNotifyQueue(messageIDs []int64) error

	// AppendMessagesOfUserQueue 向用户队列里追加消息
	AppendMessagesOfUserQueue(uid string, messages []Message) error
	// UpdateMessageOfUserQueueCursorIfNeed 更新用户队列的游标
	UpdateMessageOfUserQueueCursorIfNeed(uid string, messageSeq uint64) error
}

type UserDB interface {
	// GetUserToken 获取用户的token
	GetUser(uid string, deviceFlag uint8) (User, error)

	// UpdateUserToken 更新用户的token
	UpdateUser(u User) error
}

type ChannelDB interface {
	// AddSubscribers 添加订阅者
	AddSubscribers(channelId string, channelType uint8, subscribers []string) error

	// RemoveSubscribers 移除订阅者
	RemoveSubscribers(channelId string, channelType uint8, subscribers []string) error

	// RemoveAllSubscriber 移除所有订阅者
	RemoveAllSubscriber(channelId string, channelType uint8) error

	// GetSubscribers 获取订阅者
	GetSubscribers(channelId string, channelType uint8) ([]string, error)

	// AddOrUpdateChannel  添加或更新channel
	AddOrUpdateChannel(channelInfo ChannelInfo) error

	// GetChannel 获取channel
	GetChannel(channelId string, channelType uint8) (ChannelInfo, error)

	// ExistChannel 判断channel是否存在
	ExistChannel(channelId string, channelType uint8) (bool, error)

	// DeleteChannel 删除channel
	DeleteChannel(channelId string, channelType uint8) error

	// AddDenylist 添加黑名单
	AddDenylist(channelId string, channelType uint8, uids []string) error
	// GetDenylist 获取黑名单
	GetDenylist(channelId string, channelType uint8) ([]string, error)

	// RemoveDenylist 移除黑名单
	RemoveDenylist(channelId string, channelType uint8, uids []string) error

	// RemoveAllDenylist 移除所有黑名单
	RemoveAllDenylist(channelId string, channelType uint8) error

	// AddAllowlist 添加白名单
	AddAllowlist(channelId string, channelType uint8, uids []string) error

	// GetAllowlist 获取白名单
	GetAllowlist(channelId string, channelType uint8) ([]string, error)

	// RemoveAllowlist 移除白名单
	RemoveAllowlist(channelId string, channelType uint8, uids []string) error

	// RemoveAllAllowlist 移除所有白名单
	RemoveAllAllowlist(channelId string, channelType uint8) error
	// 更新频道的应用索引
	UpdateChannelAppliedIndex(channelId string, channelType uint8, index uint64) error
	// 获取频道的应用索引
	GetChannelAppliedIndex(channelId string, channelType uint8) (uint64, error)
}

type ConversationDB interface {
	// AddOrUpdateConversations 添加或更新最近会话
	AddOrUpdateConversations(uid string, conversations []Conversation) error

	// DeleteConversation 删除最近会话
	DeleteConversation(uid string, sessionId uint64) error

	// GetConversations 获取指定用户的最近会话
	GetConversations(uid string) ([]Conversation, error)

	// GetConversation 获取指定用户的指定会话
	GetConversation(uid string, sessionId uint64) (Conversation, error)

	GetConversationBySessionIds(uid string, sessionIds []uint64) ([]Conversation, error)
}

type ChannelClusterConfigDB interface {

	// SaveChannelClusterConfig 保存频道的分布式配置
	SaveChannelClusterConfig(channelClusterConfig ChannelClusterConfig) error

	// GetChannelClusterConfig 获取频道的分布式配置
	GetChannelClusterConfig(channelId string, channelType uint8) (ChannelClusterConfig, error)

	// DeleteChannelClusterConfig 删除频道的分布式配置
	DeleteChannelClusterConfig(channelId string, channelType uint8) error

	// GetChannelClusterConfigs 获取频道的分布式配置
	GetChannelClusterConfigs(offsetId uint64, limit int) ([]ChannelClusterConfig, error)

	// GetChannelClusterConfigCountWithSlotId 获取某个槽的频道的分布式配置数量
	GetChannelClusterConfigCountWithSlotId(slotId uint32) (int, error)

	// GetChannelClusterConfigWithSlotId 获取某个槽的频道的分布式配置
	GetChannelClusterConfigWithSlotId(slotId uint32) ([]ChannelClusterConfig, error)
}

type LeaderTermSequenceDB interface {
	// SetLeaderTermStartIndex 设置领导任期开始的第一条日志索引
	SetLeaderTermStartIndex(shardNo string, term uint32, index uint64) error
	// LeaderLastTerm 获取最新的本地保存的领导任期
	LeaderLastTerm(shardNo string) (uint32, error)
	// LeaderTermStartIndex 获取领导任期开始的第一条日志索引
	LeaderTermStartIndex(shardNo string, term uint32) (uint64, error)
	// DeleteLeaderTermStartIndexGreaterThanTerm 删除比传入的term大的的LeaderTermStartIndex记录
	DeleteLeaderTermStartIndexGreaterThanTerm(shardNo string, term uint32) error
}

type SessionDB interface {
	// AddOrUpdateSession 添加或更新session
	AddOrUpdateSession(session Session) (Session, error)
	// GetSession 获取session
	GetSession(uid string, id uint64) (Session, error)
	// DeleteSession 删除session
	DeleteSession(uid string, id uint64) error
	DeleteSessionByChannel(uid string, channelId string, channelType uint8) error
	// DeleteSessionAndConversationByChannel 删除session和最近会话
	DeleteSessionAndConversationByChannel(uid string, channelId string, channelType uint8) error
	// GetSessions 获取用户的session
	GetSessions(uid string) ([]Session, error)
	// DeleteSessionByUid 删除用户的session
	DeleteSessionByUid(uid string) error
	// GetSessionByChannel 获取用户某个频道的session
	GetSessionByChannel(uid string, channelId string, channelType uint8) (Session, error)
	//	 UpdateSessionUpdatedAt 更新session的更新时间
	UpdateSessionUpdatedAt(models []*UpdateSessionUpdatedAtModel) error
	//	 GetLastSessionsByUid 获取用户最近的session
	// limit 为0表示不做限制
	GetLastSessionsByUid(uid string, limit int) ([]Session, error)
	// 获取大于指定更新时间的session(不包含updatedAt)
	// limit 为0表示不做限制
	GetSessionsGreaterThanUpdatedAtByUid(uid string, updatedAt int64, limit int) ([]Session, error)
}
