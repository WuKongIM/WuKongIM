package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type ICluster interface {
	// ProposeMetaToChannel 提交元数据到指定的channel
	ProposeMetaToChannel(channelID string, channelType uint8, data []byte) error
	// ProposeMessageToChannel 提交消息到指定的channel
	ProposeMessageToChannel(channelID string, channelType uint8, data []byte) error
	// ProposeMessagesToChannel 批量提交消息到指定的channel
	ProposeMessagesToChannel(channelID string, channelType uint8, data [][]byte) error
}

type CMDType uint16

const (
	CMDUnknown CMDType = iota
	CMDUpdateUserToken
	// CMDUpdateMessageOfUserCursorIfNeed CMDUpdateMessageOfUserCursorIfNeed
	CMDUpdateMessageOfUserCursorIfNeed
	CMDAddOrUpdateChannel
	CMDAddSubscribers
	CMDRemoveSubscribers
	CMDRemoveAllSubscriber
	CMDDeleteChannel
	CMDAddDenylist
	CMDRemoveDenylist
	CMDRemoveAllDenylist
	CMDAddAllowlist
	CMDRemoveAllowlist
	CMDRemoveAllAllowlist
	CMDAppendMessages
	CMDAppendMessagesOfUser
	CMDAppendMessagesOfNotifyQueue
	CMDRemoveMessagesOfNotifyQueue
	CMDDeleteChannelAndClearMessages
	CMDAddOrUpdateConversations
	CMDDeleteConversation
	CMDSystemUIDsAdd
	CMDSystemUIDsRemove
	CMDSaveStreamMeta
	CMDStreamEnd
	CMDAppendStreamItem
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

func EncodeSubscribers(uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteUint32(uint32(len(uids)))
	if len(uids) > 0 {
		for _, uid := range uids {
			encoder.WriteString(uid)
		}
	}
	return encoder.Bytes()
}

// DecodeCMDAddSubscribers DecodeCMDAddSubscribers
func (c *CMD) DecodeSubscribers() (uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Data)

	var count uint32
	if count, err = decoder.Uint32(); err != nil {
		return nil, err
	}
	if count > 0 {
		for i := uint32(0); i < count; i++ {
			var uid string
			if uid, err = decoder.String(); err != nil {
				return nil, err
			}
			uids = append(uids, uid)
		}
	}
	return uids, nil
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
