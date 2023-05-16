package wkstore

type Store interface {
	Open() error
	Close() error
	// #################### user ####################
	// GetUserToken return token,device level and error
	GetUserToken(uid string, deviceFlag uint8) (string, uint8, error)
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

	// #################### messages ####################
	// StoreMsg return seqs and error, seqs len is msgs len
	AppendMessages(topic string, msgs []Message) (seqs []uint32, err error)
	LoadMsg(topic string, seq uint32) (Message, error)
	LoadNextMsgs(topic string, seq uint32, limit int) ([]Message, error)
	LoadPrevMsgs(topic string, seq uint32, limit int) ([]Message, error)
	LoadRangeMsgs(topic string, start, end uint32) ([]Message, error)

	AppendMessageOfNotifyQueue(m []Message) error
	GetMessagesOfNotifyQueue(count int) ([]Message, error)
	// RemoveMessagesOfNotifyQueue 从通知队列里移除消息
	RemoveMessagesOfNotifyQueue(messageIDs []int64) error

	// #################### conversations ####################
	AddOrUpdateConversations(uid string, conversations []*Conversation) error
	GetConversations(uid string) ([]*Conversation, error)
	GetConversation(uid string, channelID string, channelType uint8) (*Conversation, error)
	DeleteConversation(uid string, channelID string, channelType uint8) error // 删除最近会话

	// #################### system uids ####################
	AddSystemUIDs(uids []string) error    // 添加系统uid
	RemoveSystemUIDs(uids []string) error // 移除系统uid
	GetSystemUIDs() ([]string, error)
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
