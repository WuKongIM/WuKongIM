package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type CMDType uint32

const (
	CMDUpdateUserToken CMDType = 201
	// CMDUpdateMessageOfUserCursorIfNeed CMDUpdateMessageOfUserCursorIfNeed
	CMDUpdateMessageOfUserCursorIfNeed CMDType = 202
	CMDAddOrUpdateChannel              CMDType = 203
	CMDAddSubscribers                  CMDType = 204
	CMDRemoveSubscribers               CMDType = 205
	CMDRemoveAllSubscriber             CMDType = 206
	CMDDeleteChannel                   CMDType = 207
	CMDAddDenylist                     CMDType = 208
	CMDRemoveDenylist                  CMDType = 209
	CMDRemoveAllDenylist               CMDType = 210
	CMDAddAllowlist                    CMDType = 211
	CMDRemoveAllowlist                 CMDType = 212
	CMDRemoveAllAllowlist              CMDType = 213
	CMDAppendMessages                  CMDType = 214
	CMDAppendMessagesOfUser            CMDType = 215
	CMDAppendMessagesOfNotifyQueue     CMDType = 216
	CMDRemoveMessagesOfNotifyQueue     CMDType = 217
	CMDDeleteChannelAndClearMessages   CMDType = 218
	CMDAddOrUpdateConversations        CMDType = 219
	CMDDeleteConversation              CMDType = 220
	CMDSystemUIDsAdd                   CMDType = 221
	CMDSystemUIDsRemove                CMDType = 222
	CMDSaveStreamMeta                  CMDType = 223
	CMDStreamEnd                       CMDType = 224
	CMDAppendStreamItem                CMDType = 225
)

func (c CMDType) Uint32() uint32 {
	return uint32(c)
}

type CMDReq transporter.CMDReq

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

func (c *CMDReq) DecodeCMDUserToken() (uid string, deviceFlag uint8, deviceLevel uint8, token string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
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
func (c *CMDReq) DecodeCMDUpdateMessageOfUserCursorIfNeed() (uid string, messageSeq uint32, err error) {
	decoder := wkproto.NewDecoder(c.Param)
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
func (c *CMDReq) DecodeAddOrUpdateChannel() (*wkstore.ChannelInfo, error) {
	decoder := wkproto.NewDecoder(c.Param)
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

// EncodeCMDAddSubscribers EncodeCMDAddSubscribers
func EncodeCMDAddSubscribers(channelID string, channelType uint8, uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	if len(uids) > 0 {
		encoder.WriteString(wkutil.ToJSON(uids))
	}
	return encoder.Bytes()
}

// DecodeCMDAddSubscribers DecodeCMDAddSubscribers
func (c *CMDReq) DecodeCMDAddSubscribers() (channelID string, channelType uint8, uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	var uidsBytes []byte
	uidsBytes, err = decoder.Binary()
	if err != nil {
		return
	}
	if len(uidsBytes) > 0 {
		err = wkutil.ReadJSONByByte(uidsBytes, &uids)
		if err != nil {
			return
		}
	}
	return
}

// EncodeCMDRemoveSubscribers EncodeCMDRemoveSubscribers
func EncodeCMDRemoveSubscribers(channelID string, channelType uint8, uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	if len(uids) > 0 {
		encoder.WriteString(wkutil.ToJSON(uids))
	}
	return encoder.Bytes()
}

// DecodeRemoveSubscribers DecodeRemoveSubscribers
func (c *CMDReq) DecodeCMDRemoveSubscribers() (channelID string, channelType uint8, uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	var uidsBytes []byte
	uidsBytes, err = decoder.Binary()
	if err != nil {
		return
	}
	if len(uidsBytes) > 0 {
		err = wkutil.ReadJSONByByte(uidsBytes, &uids)
		if err != nil {
			return
		}
	}
	return
}

// EncodeRemoveAllSubscriber EncodeRemoveAllSubscriber
func EncodeCMDRemoveAllSubscriber(channelID string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

// DecodeRemoveAllSubscriber DecodeRemoveAllSubscriber
func (c *CMDReq) DecodeCMDRemoveAllSubscriber() (channelID string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

// EncodeDeleteChannel EncodeDeleteChannel
func EncodeCMDDeleteChannel(channelID string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

// DecodeDeleteChannel DecodeDeleteChannel
func (c *CMDReq) DecodeCMDDeleteChannel() (channelID string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Param)

	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

// EncodeCMDAddDenylist EncodeCMDAddDenylist
func EncodeCMDAddDenylist(channelID string, channelType uint8, uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	if len(uids) > 0 {
		encoder.WriteString(wkutil.ToJSON(uids))
	}
	return encoder.Bytes()
}

// DecodeCMDAddDenylist DecodeCMDAddDenylist
func (c *CMDReq) DecodeCMDAddDenylist() (channelID string, channelType uint8, uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	var uidsBytes []byte
	uidsBytes, err = decoder.Binary()
	if err != nil {
		return
	}
	if len(uidsBytes) > 0 {
		err = wkutil.ReadJSONByByte(uidsBytes, &uids)
		if err != nil {
			return
		}
	}
	return
}

// EncodeCMDRemoveDenylist EncodeCMDRemoveDenylist
func EncodeCMDRemoveDenylist(channelID string, channelType uint8, uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	if len(uids) > 0 {
		encoder.WriteString(wkutil.ToJSON(uids))
	}
	return encoder.Bytes()
}

func EncodeCMDRemoveAllDenylist(channelID string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

func (c *CMDReq) DecodeCMDRemoveAllDenylist() (channelID string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

// DecodeCMDRemoveDenylist DecodeCMDRemoveDenylist
func (c *CMDReq) DecodeCMDRemoveDenylist() (channelID string, channelType uint8, uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	var uidsBytes []byte
	uidsBytes, err = decoder.Binary()
	if err != nil {
		return
	}
	if len(uidsBytes) > 0 {
		err = wkutil.ReadJSONByByte(uidsBytes, &uids)
		if err != nil {
			return
		}
	}
	return
}

// EncodeCMDAddAllowlist EncodeCMDAddAllowlist
func EncodeCMDAddAllowlist(channelID string, channelType uint8, uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	if len(uids) > 0 {
		encoder.WriteString(wkutil.ToJSON(uids))
	}
	return encoder.Bytes()
}

// DecodeCMDAddAllowlist DecodeCMDAddAllowlist
func (c *CMDReq) DecodeCMDAddAllowlist() (channelID string, channelType uint8, uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	var uidsBytes []byte
	uidsBytes, err = decoder.Binary()
	if err != nil {
		return
	}
	if len(uidsBytes) > 0 {
		err = wkutil.ReadJSONByByte(uidsBytes, &uids)
		if err != nil {
			return
		}
	}
	return
}

// EncodeCMDRemoveAllowlist EncodeCMDRemoveAllowlist
func EncodeCMDRemoveAllowlist(channelID string, channelType uint8, uids []string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()

	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	if len(uids) > 0 {
		encoder.WriteString(wkutil.ToJSON(uids))
	}
	return encoder.Bytes()
}

// DecodeCMDRemoveAllowlist DecodeCMDRemoveAllowlist
func (c *CMDReq) DecodeCMDRemoveAllowlist() (channelID string, channelType uint8, uids []string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	var uidsBytes []byte
	uidsBytes, err = decoder.Binary()
	if err != nil {
		return
	}
	if len(uidsBytes) > 0 {
		err = wkutil.ReadJSONByByte(uidsBytes, &uids)
		if err != nil {
			return
		}
	}
	return
}

// EncodeCMDRemoveAllAllowlist EncodeCMDRemoveAllAllowlist
func EncodeCMDRemoveAllAllowlist(channelID string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

// DecodeCMDRemoveAllAllowlist DecodeCMDRemoveAllAllowlist
func (c *CMDReq) DecodeCMDRemoveAllAllowlist() (channelID string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

// EncodeCMDAppendMessages EncodeCMDAppendMessages
func EncodeCMDAppendMessages(channelID string, channelType uint8, ms []wkstore.Message) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	msgSet := MessageSet{}
	for _, m := range ms {
		msgSet = append(msgSet, m.(*Message))
	}
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	encoder.WriteBytes(msgSet.Encode())
	return encoder.Bytes()
}

// DecodeCMDAppendMessages DecodeCMDAppendMessages
func (c *CMDReq) DecodeCMDAppendMessages() (channelID string, channelType uint8, messages []wkstore.Message, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	channelID, err = decoder.String()
	if err != nil {
		return
	}
	channelType, err = decoder.Uint8()
	if err != nil {
		return
	}
	data, err := decoder.BinaryAll()
	msgSet := MessageSet{}
	err = msgSet.Decode(data)
	if err != nil {
		return
	}
	for _, m := range msgSet {
		messages = append(messages, m)
	}

	return
}

// EncodeCMDAppendMessagesOfUser EncodeCMDAppendMessagesOfUser
func EncodeCMDAppendMessagesOfUser(uid string, ms []wkstore.Message) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)

	msgSet := MessageSet{}
	for _, m := range ms {
		msgSet = append(msgSet, m.(*Message))
	}
	encoder.WriteBytes(msgSet.Encode())
	return encoder.Bytes()
}

// DecodeCMDAppendMessagesOfUser DecodeCMDAppendMessagesOfUser
func (c *CMDReq) DecodeCMDAppendMessagesOfUser() (string, []wkstore.Message, error) {
	decoder := wkproto.NewDecoder(c.Param)
	uid, err := decoder.String()
	if err != nil {
		return "", nil, err
	}

	data, err := decoder.BinaryAll()
	if err != nil {
		return "", nil, err
	}

	messages := make([]wkstore.Message, 0)
	msgSet := MessageSet{}
	err = msgSet.Decode(data)
	if err != nil {
		return "", nil, err
	}
	for _, m := range msgSet {
		messages = append(messages, m)
	}

	return uid, messages, nil
}

func EncodeCMDAppendMessagesOfNotifyQueue(ms []wkstore.Message) []byte {
	msgSet := MessageSet{}
	for _, m := range ms {
		msgSet = append(msgSet, m.(*Message))
	}
	return msgSet.Encode()
}

func (c *CMDReq) DecodeCMDAppendMessagesOfNotifyQueue() ([]wkstore.Message, error) {
	messages := make([]wkstore.Message, 0)
	msgSet := MessageSet{}
	err := msgSet.Decode(c.Param)
	if err != nil {
		return nil, err
	}
	for _, m := range msgSet {
		messages = append(messages, m)
	}

	return messages, nil
}

// EncodeCMDDeleteChannelAndClearMessages EncodeCMDDeleteChannelAndClearMessages
func EncodeCMDDeleteChannelAndClearMessages(channelID string, channelType uint8) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	return encoder.Bytes()
}

// DecodeCMDDeleteChannelAndClearMessages DecodeCMDDeleteChannelAndClearMessages
func (c *CMDReq) DecodeCMDDeleteChannelAndClearMessages() (channelID string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if channelID, err = decoder.String(); err != nil {
		return
	}
	if channelType, err = decoder.Uint8(); err != nil {
		return
	}
	return
}

// EncodeCMDAddOrUpdateConversations EncodeCMDAddOrUpdateConversations
func EncodeCMDAddOrUpdateConversations(uid string, conversations []*wkstore.Conversation) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(uid)
	if len(conversations) > 0 {
		conversationSet := wkstore.ConversationSet(conversations)
		encoder.WriteBytes(conversationSet.Encode())
	}

	return encoder.Bytes()
}

// DecodeCMDAddOrUpdateConversations DecodeCMDAddOrUpdateConversations
func (c *CMDReq) DecodeCMDAddOrUpdateConversations() (uid string, conversations []*wkstore.Conversation, err error) {
	if len(c.Param) == 0 {
		return "", nil, nil
	}
	decoder := wkproto.NewDecoder(c.Param)
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

func (c *CMDReq) DecodeCMDDeleteConversation() (uid string, channelID string, channelType uint8, err error) {
	decoder := wkproto.NewDecoder(c.Param)
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

func EncodeSystemUIDsAdd(uids []string) []byte {
	if uids == nil {
		return []byte(wkutil.ToJSON(make([]string, 0)))
	} else {
		return []byte(wkutil.ToJSON(uids))
	}
}

func (c *CMDReq) DecodeSystemUIDsAdd() ([]string, error) {
	var uids []string
	err := wkutil.ReadJSONByByte(c.Param, &uids)
	return uids, err
}

func EncodeSystemUIDsRemove(uids []string) []byte {
	if uids == nil {
		return []byte(wkutil.ToJSON(make([]string, 0)))
	} else {
		return []byte(wkutil.ToJSON(uids))
	}
}

func (c *CMDReq) DecodeSystemUIDsRemove() ([]string, error) {
	var uids []string
	err := wkutil.ReadJSONByByte(c.Param, &uids)
	return uids, err
}

func EncodeCMDStreamEnd(channelID string, channelType uint8, streamNo string) []byte {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(channelID)
	encoder.WriteUint8(channelType)
	encoder.WriteString(streamNo)
	return encoder.Bytes()
}

func (c *CMDReq) DecodeCMDStreamEnd() (channelID string, channelType uint8, streamNo string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
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
func (c *CMDReq) DecodeCMDAppendStreamItem() (channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem, err error) {
	decoder := wkproto.NewDecoder(c.Param)
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
