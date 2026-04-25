package message

import (
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type SendCommand struct {
	Framer               frame.Framer
	Setting              frame.Setting
	MsgKey               string
	Expire               uint32
	FromUID              string
	SenderSessionID      uint64
	ClientSeq            uint64
	ClientMsgNo          string
	StreamNo             string
	ChannelID            string
	ChannelType          uint8
	Topic                string
	Payload              []byte
	CommitMode           channel.CommitMode
	ProtocolVersion      uint8
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
}

type CommittedMessageEnvelope struct {
	ChannelID   string
	ChannelType uint8
	MessageID   uint64
	MessageSeq  uint64
	FromUID     string
	ClientMsgNo string
	Topic       string
	Payload     []byte
	Framer      frame.Framer
	Setting     frame.Setting
	MsgKey      string
	Expire      uint32
	StreamNo    string
	ClientSeq   uint64
}

type RecvAckCommand struct {
	UID        string
	SessionID  uint64
	Framer     frame.Framer
	MessageID  int64
	MessageSeq uint64
}

type RouteAckCommand struct {
	UID        string
	SessionID  uint64
	MessageID  uint64
	MessageSeq uint64
}

type SessionClosedCommand struct {
	UID       string
	SessionID uint64
}
