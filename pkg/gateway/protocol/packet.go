package protocol

import (
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"time"
)

// InboundPacket is an owned independent-protocol value. DecodePackets must detach
// every retained byte from transport input and decoder scratch before returning.
// Bytes is the complete wire size; Connect classifies the initial handshake.
type InboundPacket struct {
	Value   any
	Bytes   int
	Connect bool
	// ReadIdleTimeout, only on CONNECT, overrides the default until close.
	// Zero disables it. Independent packets refresh activity only when complete.
	ReadIdleTimeout *time.Duration
}

// PacketAdapter is the independent-packet extension beside the existing WK
// Adapter. It performs no authentication, business orchestration or socket I/O.
// Non-handshake packets enter the same bounded ordered gateway mailbox as SEND.
type PacketAdapter interface {
	Name() string
	DecodePackets(session.Session, []byte) ([]InboundPacket, int, error)
	EncodePacket(session.Session, any, session.OutboundMeta) ([]byte, error)
	// PacketName returns a fixed protocol kind for observations, never peer data.
	PacketName(any) string
	OnOpen(session.Session) error
	OnClose(session.Session) error
}
