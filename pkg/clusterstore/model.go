package clusterstore

import (
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
