package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type CMDType uint16

const (
	CMDUnknown CMDType = iota
	// 添加或更新设备
	CMDAddOrUpdateDevice
	// 添加或更新用户
	CMDAddOrUpdateUser
	// CMDUpdateMessageOfUserCursorIfNeed CMDUpdateMessageOfUserCursorIfNeed
	// 更新用户消息游标
	CMDUpdateMessageOfUserCursorIfNeed
	// 添加或更新频道
	CMDAddOrUpdateChannel
	// 添加订阅者
	CMDAddSubscribers
	// 移除订阅者
	CMDRemoveSubscribers
	// 移除所有订阅者
	CMDRemoveAllSubscriber
	// 删除频道
	CMDDeleteChannel
	// 添加黑名单
	CMDAddDenylist
	// 移除黑名单
	CMDRemoveDenylist
	// 移除所有黑名单
	CMDRemoveAllDenylist
	// 添加白名单
	CMDAddAllowlist
	// 移除白名单
	CMDRemoveAllowlist
	// 移除所有白名单
	CMDRemoveAllAllowlist
	// 追加消息
	CMDAppendMessages
	// 追加用户消息
	CMDAppendMessagesOfUser
	// 追加通知队列消息
	CMDAppendMessagesOfNotifyQueue
	// 移除通知队列消息
	CMDRemoveMessagesOfNotifyQueue
	// 删除频道并清空消息
	CMDDeleteChannelAndClearMessages
	// 添加或更新会话
	CMDAddOrUpdateConversations
	// 删除会话
	CMDDeleteConversation
	// 添加系统UID
	CMDSystemUIDsAdd
	// 移除系统UID
	CMDSystemUIDsRemove
	// 保存流元数据
	CMDSaveStreamMeta
	// 流结束
	CMDStreamEnd
	// 追加流元素
	CMDAppendStreamItem
	// 频道分布式配置保存
	CMDChannelClusterConfigSave
	// 频道分布式配置删除
	CMDChannelClusterConfigDelete
	// 添加或更新session
	CMDAddOrUpdateSession

	// 删除指定用户的session
	CMDDeleteSessionByUid
	// 通过id删除session
	CMDDeleteSession

	// 通过channel删除session
	CMDDeleteSessionByChannel
	// 通过channel删除session和会话
	CMDDeleteSessionAndConversationByChannel
	// 更新session的更新时间
	CMDUpdateSessionUpdatedAt
)

func (c CMDType) Uint16() uint16 {
	return uint16(c)
}

func (c CMDType) String() string {
	switch c {
	case CMDAddOrUpdateDevice:
		return "CMDAddOrUpdateDevice"
	case CMDUpdateMessageOfUserCursorIfNeed:
		return "CMDUpdateMessageOfUserCursorIfNeed"
	case CMDAddOrUpdateChannel:
		return "CMDAddOrUpdateChannel"
	case CMDAddSubscribers:
		return "CMDAddSubscribers"
	case CMDRemoveSubscribers:
		return "CMDRemoveSubscribers"
	case CMDRemoveAllSubscriber:
		return "CMDRemoveAllSubscriber"
	case CMDDeleteChannel:
		return "CMDDeleteChannel"
	case CMDAddDenylist:
		return "CMDAddDenylist"
	case CMDRemoveDenylist:
		return "CMDRemoveDenylist"
	case CMDRemoveAllDenylist:
		return "CMDRemoveAllDenylist"
	case CMDAddAllowlist:
		return "CMDAddAllowlist"
	case CMDRemoveAllowlist:
		return "CMDRemoveAllowlist"
	case CMDRemoveAllAllowlist:
		return "CMDRemoveAllAllowlist"
	case CMDAppendMessages:
		return "CMDAppendMessages"
	case CMDAppendMessagesOfUser:
		return "CMDAppendMessagesOfUser"
	case CMDAppendMessagesOfNotifyQueue:
		return "CMDAppendMessagesOfNotifyQueue"
	case CMDRemoveMessagesOfNotifyQueue:
		return "CMDRemoveMessagesOfNotifyQueue"
	case CMDDeleteChannelAndClearMessages:
		return "CMDDeleteChannelAndClearMessages"
	case CMDAddOrUpdateConversations:
		return "CMDAddOrUpdateConversations"
	case CMDDeleteConversation:
		return "CMDDeleteConversation"
	case CMDSystemUIDsAdd:
		return "CMDSystemUIDsAdd"
	case CMDSystemUIDsRemove:
		return "CMDSystemUIDsRemove"
	case CMDSaveStreamMeta:
		return "CMDSaveStreamMeta"
	case CMDStreamEnd:
		return "CMDStreamEnd"
	case CMDAppendStreamItem:
		return "CMDAppendStreamItem"
	case CMDChannelClusterConfigSave:
		return "CMDChannelClusterConfigSave"
	case CMDChannelClusterConfigDelete:
		return "CMDChannelClusterConfigDelete"
	case CMDAddOrUpdateSession:
		return "CMDAddOrUpdateSession"
	case CMDDeleteSessionByUid:
		return "CMDDeleteSessionByUid"
	case CMDDeleteSession:
		return "CMDDeleteSession"
	case CMDDeleteSessionByChannel:
		return "CMDDeleteSessionByChannel"
	case CMDDeleteSessionAndConversationByChannel:
		return "CMDDeleteSessionAndConversationByChannel"
	case CMDUpdateSessionUpdatedAt:
		return "CMDUpdateSessionUpdatedAt"
	default:
		return "CMDUnknown"
	}
}

type CMD struct {
	CmdType CMDType
	Data    []byte
	version uint16 // 数据协议版本

}

func NewCMD(cmdType CMDType, data []byte) *CMD {
	return &CMD{
		CmdType: cmdType,
		Data:    data,
	}
}

func (c *CMD) Marshal() ([]byte, error) {
	c.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(c.version)
	enc.WriteUint16(c.CmdType.Uint16())
	enc.WriteBytes(c.Data)
	return enc.Bytes(), nil

}

func (c *CMD) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.version, err = dec.Uint16(); err != nil {
		return err
	}
	var cmdType uint16
	if cmdType, err = dec.Uint16(); err != nil {
		return err
	}
	c.CmdType = CMDType(cmdType)
	if c.Data, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

func EncodeSubscribers(channelId string, channelType uint8, uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	encoder.WriteUint32(uint32(len(uids)))
	if len(uids) > 0 {
		for _, uid := range uids {
			encoder.WriteString(uid)
		}
	}
	return encoder.Bytes()
}

// DecodeCMDAddSubscribers DecodeCMDAddSubscribers
func (c *CMD) DecodeSubscribers() (channelId string, channelType uint8, uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	if channelId, err = decoder.String(); err != nil {
		return
	}

	if channelType, err = decoder.Uint8(); err != nil {
		return
	}

	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	if count > 0 {
		for i := uint32(0); i < count; i++ {
			var uid string
			if uid, err = decoder.String(); err != nil {
				return
			}
			uids = append(uids, uid)
		}
	}
	return
}

func (c *CMD) DecodeChannel() (channelId string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if channelId, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

func EncodeChannel(channelId string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

func EncodeCMDUser(u wkdb.User) []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(u.Uid)
	return enc.Bytes()
}

func (c *CMD) DecodeCMDUser() (u wkdb.User, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if u.Uid, err = decoder.String(); err != nil {
		return
	}
	return
}

// EncodeCMDDevice EncodeCMDDevice
func EncodeCMDDevice(d wkdb.Device) []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(d.Uid)
	enc.WriteUint64(d.DeviceFlag)
	enc.WriteUint8(d.DeviceLevel)
	enc.WriteString(d.Token)
	return enc.Bytes()
}

func (c *CMD) DecodeCMDDevice() (d wkdb.Device, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if d.Uid, err = decoder.String(); err != nil {
		return
	}
	if d.DeviceFlag, err = decoder.Uint64(); err != nil {
		return
	}

	if d.DeviceLevel, err = decoder.Uint8(); err != nil {
		return
	}
	if d.Token, err = decoder.String(); err != nil {
		return
	}
	return
}

// EncodeCMDUpdateMessageOfUserCursorIfNeed EncodeCMDUpdateMessageOfUserCursorIfNeed
func EncodeCMDUpdateMessageOfUserCursorIfNeed(uid string, messageSeq uint64) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteUint64(messageSeq)
	return encoder.Bytes()
}

// DecodeCMDUpdateMessageOfUserCursorIfNeed DecodeCMDUpdateMessageOfUserCursorIfNeed
func (c *CMD) DecodeCMDUpdateMessageOfUserCursorIfNeed() (uid string, messageSeq uint64, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if messageSeq, err = decoder.Uint64(); err != nil {
		return
	}
	return
}

// EncodeAddOrUpdateChannel EncodeAddOrUpdateChannel
func EncodeAddOrUpdateChannel(channelInfo wkdb.ChannelInfo) ([]byte, error) {

	return channelInfo.Marshal()
}

// DecodeAddOrUpdateChannel DecodeAddOrUpdateChannel
func (c *CMD) DecodeAddOrUpdateChannel() (wkdb.ChannelInfo, error) {
	channelInfo := &wkdb.ChannelInfo{}
	err := channelInfo.Unmarshal(c.Data)
	return *channelInfo, err
}

// EncodeCMDAddOrUpdateConversations EncodeCMDAddOrUpdateConversations
func EncodeCMDAddOrUpdateConversations(uid string, conversations []wkdb.Conversation) ([]byte, error) {

	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteUint32(uint32(len(conversations)))
	for _, conversation := range conversations {
		data, err := conversation.Marshal()
		if err != nil {
			return nil, err
		}
		encoder.WriteBinary(data)
	}
	return encoder.Bytes(), nil
}

// DecodeCMDAddOrUpdateConversations DecodeCMDAddOrUpdateConversations
func (c *CMD) DecodeCMDAddOrUpdateConversations() (uid string, conversations []wkdb.Conversation, err error) {
	if len(c.Data) == 0 {
		return
	}
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}

	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	for i := uint32(0); i < count; i++ {
		var conversationBytes []byte
		if conversationBytes, err = decoder.Binary(); err != nil {
			return
		}
		var conversation = &wkdb.Conversation{}
		err = conversation.Unmarshal(conversationBytes)
		if err != nil {
			return
		}
		conversations = append(conversations, *conversation)

	}

	return
}

func EncodeCMDDeleteConversation(uid string, channelID string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDDeleteConversation() (uid string, channelID string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

func EncodeCMDStreamEnd(channelID string, channelType uint8, streamNo string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	encoder.WriteString(streamNo)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDStreamEnd() (channelID string, channelType uint8, streamNo string, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	if streamNo, err = decoder.String(); err != nil {
		return
	}
	return
}

// func EncodeCMDAppendStreamItem(channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem) []byte {
// 	encoder := wkproto.NewEncoder()
// 	defer encoder.End()

// 	encoder.WriteString(channelID)
// 	encoder.WriteUint8(channelType)
// 	encoder.WriteString(streamNo)
// 	encoder.WriteBinary(wkstore.EncodeStreamItem(item))

// 	return encoder.Bytes()
// }
// func (c *CMD) DecodeCMDAppendStreamItem() (channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem, err error) {
// 	decoder := wkproto.NewDecoder(c.Data)
// 	if channelID, err = decoder.String(); err != nil {
// 		return
// 	}
// 	if channelType, err = decoder.Uint8(); err != nil {
// 		return
// 	}
// 	if streamNo, err = decoder.String(); err != nil {
// 		return
// 	}
// 	var itemBytes []byte
// 	itemBytes, err = decoder.Binary()
// 	if err != nil {
// 		return
// 	}
// 	item, err = wkstore.DecodeStreamItem(itemBytes)
// 	return
// }

func EncodeCMDChannelClusterConfigSave(channelID string, channelType uint8, data []byte) ([]byte, error) {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	encoder.WriteBytes(data)
	return encoder.Bytes(), nil
}

func (c *CMD) DecodeCMDChannelClusterConfigSave() (channelID string, channelType uint8, data []byte, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	data, err = decoder.BinaryAll()
	return
}

func EncodeCMDAppendMessagesOfUser(uid string, messages []wkdb.Message) ([]byte, error) {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteUint32(uint32(len(messages)))
	for _, message := range messages {
		msgData, err := message.Marshal()
		if err != nil {
			return nil, err
		}
		encoder.WriteBinary(msgData)
	}
	return encoder.Bytes(), nil
}

func (c *CMD) DecodeCMDAppendMessagesOfUser() (uid string, messages []wkdb.Message, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	for i := uint32(0); i < count; i++ {
		var messageBytes []byte
		if messageBytes, err = decoder.Binary(); err != nil {
			return
		}
		var msg = &wkdb.Message{}
		err = msg.Unmarshal(messageBytes)
		if err != nil {
			return
		}
		messages = append(messages, *msg)
	}
	return
}

func EncodeCMDDeleteSession(uid string, sessionId uint64) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteUint64(sessionId)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDDeleteSession() (uid string, sessionId uint64, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if sessionId, err = decoder.Uint64(); err != nil {
		return
	}
	return
}

func EncodeCMDDeleteSessionByChannel(uid string, channelId string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDDeleteSessionByChannel() (uid string, channelId string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if channelId, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

func EncodeCMDDeleteSessionAndConversationByChannel(uid string, channelId string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

func (c *CMD) DecodeCMDDeleteSessionAndConversationByChannel() (uid string, channelId string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if channelId, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

func EncodeCMDUpdateSessionUpdatedAt(models []*wkdb.UpdateSessionUpdatedAtModel) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()

	encoder.WriteUint32(uint32(len(models)))
	for _, model := range models {
		encoder.WriteUint16(uint16(len(model.Uids)))
		for uid, seq := range model.Uids {
			encoder.WriteString(uid)
			encoder.WriteUint64(seq)
		}
		encoder.WriteString(model.ChannelId)
		encoder.WriteUint8(model.ChannelType)

	}

	return encoder.Bytes()
}

func (c *CMD) DecodeCMDUpdateSessionUpdatedAt() (models []*wkdb.UpdateSessionUpdatedAtModel, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}

	for i := uint32(0); i < count; i++ {
		var model = &wkdb.UpdateSessionUpdatedAtModel{
			Uids: map[string]uint64{},
		}
		var uidCount uint16
		if uidCount, err = decoder.Uint16(); err != nil {
			return
		}
		for j := uint16(0); j < uidCount; j++ {
			var uid string
			if uid, err = decoder.String(); err != nil {
				return
			}

			var seq uint64
			if seq, err = decoder.Uint64(); err != nil {
				return
			}
			model.Uids[uid] = seq
		}
		if model.ChannelId, err = decoder.String(); err != nil {
			return
		}
		if model.ChannelType, err = decoder.Uint8(); err != nil {
			return
		}
		models = append(models, model)
	}

	return
}
