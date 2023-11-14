package wkstore

type Store interface {
	Open() error
	Close() error
	// #################### user ####################
	// GetUserToken return token,device level and error
	GetUserToken(uid string, deviceFlag uint8) (string, uint8, error)
	UpdateUserToken(uid string, deviceFlag uint8, deviceLevel uint8, token string) error
	// UpdateMessageOfUserCursorIfNeed 更新用户消息队列的游标，用户读到的位置
	UpdateMessageOfUserCursorIfNeed(uid string, messageSeq uint32) error

	// #################### channel ####################
	GetChannel(channelID string, channelType uint8) (*ChannelInfo, error)
	// AddOrUpdateChannel add or update channel
	AddOrUpdateChannel(channelInfo *ChannelInfo) error
	// ExistChannel return true if channel exist
	ExistChannel(channelID string, channelType uint8) (bool, error)
	// AddSubscribers 添加订阅者
	AddSubscribers(channelID string, channelType uint8, uids []string) error
	// RemoveSubscribers 移除指定频道内指定uid的订阅者
	RemoveSubscribers(channelID string, channelType uint8, uids []string) error
	// GetSubscribers 获取订阅者列表
	GetSubscribers(channelID string, channelType uint8) ([]string, error)
	RemoveAllSubscriber(channelID string, channelType uint8) error
	GetAllowlist(channelID string, channelType uint8) ([]string, error)
	GetDenylist(channelID string, channelType uint8) ([]string, error)
	// DeleteChannel 删除频道
	DeleteChannel(channelID string, channelType uint8) error
	// AddDenylist 添加频道黑名单
	AddDenylist(channelID string, channelType uint8, uids []string) error
	// RemoveAllDenylist 移除指定频道的所有黑名单
	RemoveAllDenylist(channelID string, channelType uint8) error
	// RemoveDenylist 移除频道内指定用户的黑名单
	RemoveDenylist(channelID string, channelType uint8, uids []string) error
	// AddAllowlist 添加白名单
	AddAllowlist(channelID string, channelType uint8, uids []string) error
	// RemoveAllAllowlist 移除指定频道的所有白名单
	RemoveAllAllowlist(channelID string, channelType uint8) error
	// RemoveAllowlist 移除白名单
	RemoveAllowlist(channelID string, channelType uint8, uids []string) error

	// #################### messages ####################
	// StoreMsg return seqs and error, seqs len is msgs len
	AppendMessages(channelID string, channelType uint8, msgs []Message) (seqs []uint32, err error)
	// 追加消息到用户的消息队列
	AppendMessagesOfUser(uid string, msgs []Message) (seqs []uint32, err error)
	LoadMsg(channelID string, channelType uint8, seq uint32) (Message, error)
	LoadLastMsgs(channelID string, channelType uint8, limit int) ([]Message, error)
	// LoadLastMsgsWithEnd 加载最新的消息 end表示加载到end的位置结束加载 end=0表示不做限制 结果不包含end
	LoadLastMsgsWithEnd(channelID string, channelType uint8, end uint32, limit int) ([]Message, error)
	// LoadPrevRangeMsgs 向上加载指定范围的消息 end=0表示不做限制 比如 start=100 end=0 limit=10 则返回的消息seq为99-90的消息
	// 结果包含start,不包含end
	LoadPrevRangeMsgs(channelID string, channelType uint8, start, end uint32, limit int) ([]Message, error)
	// LoadNextRangeMsgs 向下加载指定范围的消息 end=0表示不做限制 比如 start=100 end=200 limit=10 则返回的消息seq为101-111的消息，
	// 比如start=100 end=105 limit=10 则返回的消息seq为101-104的消息
	// 结果包含start,不包含end
	LoadNextRangeMsgs(channelID string, channelType uint8, start, end uint32, limit int) ([]Message, error)
	// GetLastMsgSeq 获取最新的消息seq
	GetLastMsgSeq(channelID string, channelType uint8) (uint32, error)

	// GetMessageOfUserCursor 获取用户消息队列的游标，用户读到的位置
	GetMessageOfUserCursor(uid string) (uint32, error)
	// SyncMessageOfUser 同步用户队列里的消息（写扩散）
	SyncMessageOfUser(uid string, startMessageSeq uint32, limit int) ([]Message, error)

	AppendMessageOfNotifyQueue(m []Message) error
	GetMessagesOfNotifyQueue(count int) ([]Message, error)
	// RemoveMessagesOfNotifyQueue 从通知队列里移除消息
	RemoveMessagesOfNotifyQueue(messageIDs []int64) error

	DeleteChannelAndClearMessages(channelID string, channelType uint8) error

	// #################### conversations ####################
	AddOrUpdateConversations(uid string, conversations []*Conversation) error
	GetConversations(uid string) ([]*Conversation, error)
	GetConversation(uid string, channelID string, channelType uint8) (*Conversation, error)
	DeleteConversation(uid string, channelID string, channelType uint8) error // 删除最近会话

	// #################### system uids ####################
	AddSystemUIDs(uids []string) error    // 添加系统uid
	RemoveSystemUIDs(uids []string) error // 移除系统uid
	GetSystemUIDs() ([]string, error)

	// #################### message stream ####################
	// SaveStreamMeta 保存消息流元数据
	SaveStreamMeta(meta *StreamMeta) error
	// StreamEnd 结束流
	StreamEnd(channelID string, channelType uint8, streamNo string) error
	// GetStreamMeta 获取消息流元数据
	GetStreamMeta(channelID string, channelType uint8, streamNo string) (*StreamMeta, error)
	// AppendStreamItem 追加消息流
	AppendStreamItem(channelID string, channelType uint8, streamNo string, item *StreamItem) (uint32, error)
	// GetStreamItems 获取消息流
	GetStreamItems(channelID string, channelType uint8, streamNo string) ([]*StreamItem, error)

	// AddIPBlacklist 添加ip黑名单
	AddIPBlacklist(ips []string) error
	// RemoveIPBlacklist 移除ip黑名单
	RemoveIPBlacklist(ips []string) error
	// GetIPBlacklist 获取ip黑名单
	GetIPBlacklist() ([]string, error)
}

type ChannelInfo struct {
	ChannelID   string `json:"-"`
	ChannelType uint8  `json:"-"`
	Ban         bool   `json:"ban"`   // 是否被封
	Large       bool   `json:"large"` // 是否是超大群
}

// ToMap ToMap
func (c *ChannelInfo) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"ban":   c.Ban,
		"large": c.Large,
	}
}

// NewChannelInfo NewChannelInfo
func NewChannelInfo(channelID string, channelType uint8) *ChannelInfo {
	return &ChannelInfo{
		ChannelID:   channelID,
		ChannelType: channelType,
	}
}
func (c *ChannelInfo) from(mp map[string]interface{}) {
	if mp["ban"] != nil {
		c.Ban = mp["ban"].(bool)
	}
	if mp["large"] != nil {
		c.Large = mp["large"].(bool)
	}

}

// 订阅信息
type SubscribeInfo struct {
	UID   string
	Param map[string]interface{}
}
