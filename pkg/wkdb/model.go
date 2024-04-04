package wkdb

import (
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

var (
	proto = wkproto.New()
)

var EmptyMessage = Message{}

type Message struct {
	wkproto.RecvPacket
	Term uint64 // raft term
}

func (m *Message) Unmarshal(data []byte) error {
	f, size, err := proto.DecodeFrame(data, wkproto.LatestVersion)
	if err != nil {
		return err
	}
	rcv := f.(*wkproto.RecvPacket)
	m.RecvPacket = *rcv

	dec := wkproto.NewDecoder(data[size:])
	if m.Term, err = dec.Uint64(); err != nil {
		return err
	}

	return nil
}

func (m *Message) Marshal() ([]byte, error) {
	data, err := proto.EncodeFrame(&m.RecvPacket, wkproto.LatestVersion)
	if err != nil {
		return nil, err
	}

	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteBytes(data)
	enc.WriteUint64(m.Term)
	return enc.Bytes(), nil
}

var EmptyUser = User{}

type User struct {
	Id          uint64
	Uid         string
	Token       string
	DeviceFlag  uint8
	DeviceLevel uint8
}

var EmptyChannelInfo = ChannelInfo{}

type ChannelInfo struct {
	ChannelId   string // 频道ID
	ChannelType uint8  // 频道类型
	Ban         bool   // 是否被封
	Large       bool   // 是否是超大群
}

func NewChannelInfo(channelId string, channelType uint8) ChannelInfo {
	return ChannelInfo{
		ChannelId:   channelId,
		ChannelType: channelType,
	}

}

func IsEmptyChannelInfo(c ChannelInfo) bool {
	return strings.TrimSpace(c.ChannelId) == ""
}

func (c *ChannelInfo) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint8(wkutil.BoolToUint8(c.Ban))
	enc.WriteUint8(wkutil.BoolToUint8(c.Large))
	return enc.Bytes(), nil
}

func (c *ChannelInfo) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	var ban uint8
	if ban, err = dec.Uint8(); err != nil {
		return err
	}
	var large uint8
	if large, err = dec.Uint8(); err != nil {
		return err
	}
	c.Ban = wkutil.Uint8ToBool(ban)
	c.Large = wkutil.Uint8ToBool(large)
	return nil
}

var EmptyConversation = Conversation{}

func IsEmptyConversation(c Conversation) bool {
	return strings.TrimSpace(c.UID) == ""
}

// Conversation Conversation
type Conversation struct {
	Id              uint64
	UID             string // User UID (user who belongs to the most recent session)
	ChannelId       string // Conversation channel
	ChannelType     uint8
	UnreadCount     uint32 // Number of unread messages
	Timestamp       int64  // Last session timestamp (10 digits)
	LastMsgSeq      uint32 // Sequence number of the last message
	LastClientMsgNo string // Last message client number
	LastMsgID       int64  // Last message ID
	Version         int64  // Data version
}

func (c *Conversation) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.UID)
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint32(c.UnreadCount)
	enc.WriteInt64(c.Timestamp)
	enc.WriteUint32(c.LastMsgSeq)
	enc.WriteString(c.LastClientMsgNo)
	enc.WriteInt64(c.LastMsgID)
	enc.WriteInt64(c.Version)
	return enc.Bytes(), nil
}

func (c *Conversation) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.UID, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	if c.UnreadCount, err = dec.Uint32(); err != nil {
		return err
	}
	if c.Timestamp, err = dec.Int64(); err != nil {
		return err
	}
	if c.LastMsgSeq, err = dec.Uint32(); err != nil {
		return err
	}
	if c.LastClientMsgNo, err = dec.String(); err != nil {
		return err
	}
	if c.LastMsgID, err = dec.Int64(); err != nil {
		return err
	}
	if c.Version, err = dec.Int64(); err != nil {
		return err
	}
	return nil
}

type ConversationSet []Conversation

func (c ConversationSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(c)))
	for _, v := range c {
		data, err := v.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(data)
	}
	return enc.Bytes(), nil
}

func (c ConversationSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var size uint16
	if size, err = dec.Uint16(); err != nil {
		return err
	}

	for i := 0; i < int(size); i++ {
		v := Conversation{}
		data, err := dec.Binary()
		if err != nil {
			return err
		}
		if err = v.Unmarshal(data); err != nil {
			return err
		}
		c = append(c, v)
	}
	return nil
}
