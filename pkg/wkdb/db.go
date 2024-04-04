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
	// 会话
	ConversationDB
}

type MessageDB interface {

	// AppendMessages appends messages to the db.
	AppendMessages(channelId string, channelType uint8, msgs []Message) error
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
	LoadLastMsgsWithEnd(channelID string, channelType uint8, endMessageSeq uint64, limit int) ([]Message, error)
	// LoadLastMsgs 加载最后的消息
	LoadLastMsgs(channelID string, channelType uint8, limit int) ([]Message, error)
	// GetChannelLastMessageSeq 获取最后一条消息的seq
	GetChannelLastMessageSeq(channelId string, channelType uint8) (uint64, error)

	// SetChannelLastMessageSeq 设置最后一条消息的seq
	SetChannelLastMessageSeq(channelId string, channelType uint8, seq uint64) error
}

type UserDB interface {
	// GetUserToken 获取用户的token
	GetUser(uid string, deviceFlag uint8) (User, error)

	// UpdateUserToken 更新用户的token
	UpdateUser(u User) error
}

type ChannelDB interface {
	// AddSubscribers 添加订阅者
	AddSubscribers(channelID string, channelType uint8, subscribers []string) error

	// RemoveSubscribers 移除订阅者
	RemoveSubscribers(channelID string, channelType uint8, subscribers []string) error

	// RemoveAllSubscriber 移除所有订阅者
	RemoveAllSubscriber(channelId string, channelType uint8) error

	// GetSubscribers 获取订阅者
	GetSubscribers(channelID string, channelType uint8) ([]string, error)

	// AddOrUpdateChannel  添加或更新channel
	AddOrUpdateChannel(channelInfo ChannelInfo) error

	// GetChannel 获取channel
	GetChannel(channelID string, channelType uint8) (ChannelInfo, error)

	// ExistChannel 判断channel是否存在
	ExistChannel(channelID string, channelType uint8) (bool, error)

	// DeleteChannel 删除channel
	DeleteChannel(channelId string, channelType uint8) error

	// AddDenylist 添加黑名单
	AddDenylist(channelID string, channelType uint8, uids []string) error

	// GetDenylist 获取黑名单
	GetDenylist(channelID string, channelType uint8) ([]string, error)

	// RemoveDenylist 移除黑名单
	RemoveDenylist(channelID string, channelType uint8, uids []string) error

	// RemoveAllDenylist 移除所有黑名单
	RemoveAllDenylist(channelID string, channelType uint8) error

	// AddAllowlist 添加白名单
	AddAllowlist(channelID string, channelType uint8, uids []string) error

	// GetAllowlist 获取白名单
	GetAllowlist(channelID string, channelType uint8) ([]string, error)

	// RemoveAllowlist 移除白名单
	RemoveAllowlist(channelID string, channelType uint8, uids []string) error

	// RemoveAllAllowlist 移除所有白名单
	RemoveAllAllowlist(channelID string, channelType uint8) error
}

type ConversationDB interface {
	// AddOrUpdateConversations 添加或更新最近会话
	AddOrUpdateConversations(uid string, conversations []Conversation) error

	// DeleteConversation 删除最近会话
	DeleteConversation(uid string, channelID string, channelType uint8) error

	// GetConversations 获取指定用户的最近会话
	GetConversations(uid string) ([]Conversation, error)

	// GetConversation 获取指定用户的指定会话
	GetConversation(uid string, channelID string, channelType uint8) (Conversation, error)
}
