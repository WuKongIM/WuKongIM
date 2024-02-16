package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type ICluster interface {
	// ProposeChannelMeta 提交元数据到指定的channel
	ProposeChannelMeta(channelID string, channelType uint8, data []byte) error
	// ProposeChannelMessage 提交消息到指定的channel
	ProposeChannelMessage(channelID string, channelType uint8, data []byte) (uint64, error)
	// ProposeChannelMessages 批量提交消息到指定的channel
	ProposeChannelMessages(channelID string, channelType uint8, data [][]byte) ([]uint64, error)
	// ProposeToSlots 提交数据到指定的槽
	ProposeToSlot(slotId uint32, data []byte) error
}

type CMDType uint16

const (
	CMDUnknown CMDType = iota
	// 更新用户token
	CMDUpdateUserToken
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
)

func (c CMDType) Uint16() uint16 {
	return uint16(c)
}

func (c CMDType) String() string {
	switch c {
	case CMDUpdateUserToken:
		return "CMDUpdateUserToken"
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

func (c *CMD) EncodeChannel(channelId string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelId)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

// EncodeUserToken EncodeUserToken
func EncodeCMDUserToken(uid string, deviceFlag uint8, deviceLevel uint8, token string) []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(uid)
	enc.WriteUint8(deviceFlag)
	enc.WriteUint8(deviceLevel)
	enc.WriteString(token)
	return enc.Bytes()
}

func (c *CMD) DecodeCMDUserToken() (uid string, deviceFlag uint8, deviceLevel uint8, token string, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if deviceFlag, err = decoder.Uint8(); err != nil {
		return
	}

	if deviceLevel, err = decoder.Uint8(); err != nil {
		return
	}
	if token, err = decoder.String(); err != nil {
		return
	}
	return
}

// EncodeCMDUpdateMessageOfUserCursorIfNeed EncodeCMDUpdateMessageOfUserCursorIfNeed
func EncodeCMDUpdateMessageOfUserCursorIfNeed(uid string, messageSeq uint32) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteUint32(messageSeq)
	return encoder.Bytes()
}

// DecodeCMDUpdateMessageOfUserCursorIfNeed DecodeCMDUpdateMessageOfUserCursorIfNeed
func (c *CMD) DecodeCMDUpdateMessageOfUserCursorIfNeed() (uid string, messageSeq uint32, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if messageSeq, err = decoder.Uint32(); err != nil {
		return
	}
	return
}

// EncodeAddOrUpdateChannel EncodeAddOrUpdateChannel
func EncodeAddOrUpdateChannel(channelInfo *wkstore.ChannelInfo) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelInfo.ChannelID)
	encoder.WriteUint8(channelInfo.ChannelType)
	encoder.WriteString(wkutil.ToJSON(channelInfo.ToMap()))
	return encoder.Bytes()
}

// DecodeAddOrUpdateChannel DecodeAddOrUpdateChannel
func (c *CMD) DecodeAddOrUpdateChannel() (*wkstore.ChannelInfo, error) {
	decoder := wkproto.NewDecoder(c.Data)
	channelInfo := &wkstore.ChannelInfo{}
	var err error
	if channelInfo.ChannelID, err = decoder.String(); err != nil {
		return nil, err
	}
	if channelInfo.ChannelType, err = decoder.Uint8(); err != nil {
		return nil, err
	}
	jsonStr, err := decoder.String()
	if err != nil {
		return nil, err
	}
	if len(jsonStr) > 0 {
		mp, err := wkutil.JSONToMap(jsonStr)
		if err != nil {
			return nil, err
		}
		channelInfo.From(mp)
	}
	return channelInfo, nil
}

// EncodeCMDAddOrUpdateConversations EncodeCMDAddOrUpdateConversations
func EncodeCMDAddOrUpdateConversations(uid string, conversations []*wkstore.Conversation) []byte {

	var conversationSet wkstore.ConversationSet
	if len(conversations) > 0 {
		conversationSet = wkstore.ConversationSet(conversations)

	}
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	if conversationSet != nil {
		encoder.WriteBytes(conversationSet.Encode())
	}
	return encoder.Bytes()
}

// DecodeCMDAddOrUpdateConversations DecodeCMDAddOrUpdateConversations
func (c *CMD) DecodeCMDAddOrUpdateConversations() (uid string, conversations []*wkstore.Conversation, err error) {
	if len(c.Data) == 0 {
		return "", nil, nil
	}
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}

	var data []byte
	data, _ = decoder.BinaryAll()
	if len(data) > 0 {
		conversations = wkstore.NewConversationSet(data)
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

func EncodeCMDAppendStreamItem(channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()

	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	encoder.WriteString(streamNo)
	encoder.WriteBinary(wkstore.EncodeStreamItem(item))

	return encoder.Bytes()
}
func (c *CMD) DecodeCMDAppendStreamItem() (channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem, err error) {
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
	var itemBytes []byte
	itemBytes, err = decoder.Binary()
	if err != nil {
		return
	}
	item, err = wkstore.DecodeStreamItem(itemBytes)
	return
}

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

func EncodeCMDAppendMessagesOfUser(uid string, messages []wkstore.Message) ([]byte, error) {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	encoder.WriteUint32(uint32(len(messages)))
	for _, message := range messages {
		encoder.WriteBinary(message.Encode())
	}
	return encoder.Bytes(), nil
}

func (c *CMD) DecodeCMDAppendMessagesOfUser(decodeFnc func(data []byte) (wkstore.Message, error)) (uid string, messages []wkstore.Message, err error) {
	decoder := wkproto.NewDecoder(c.Data)
	if uid, err = decoder.String(); err != nil {
		return
	}
	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return
	}
	var msg wkstore.Message
	for i := uint32(0); i < count; i++ {
		var messageBytes []byte
		if messageBytes, err = decoder.Binary(); err != nil {
			return
		}
		msg, err = decodeFnc(messageBytes)
		if err != nil {
			return
		}
		messages = append(messages, msg)
	}
	return
}
