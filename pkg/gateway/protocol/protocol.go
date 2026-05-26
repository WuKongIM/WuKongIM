package protocol

import (
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type Adapter interface {
	Name() string
	Decode(session session.Session, in []byte) ([]frame.Frame, int, error)
	Encode(session session.Session, f frame.Frame, meta session.OutboundMeta) ([]byte, error)
	OnOpen(session session.Session) error
	OnClose(session session.Session) error
}

// DecodedFrameOwner marks adapters whose Decode results can be retained asynchronously.
// Adapters should not opt in when Decode reuses frame objects or mutable payload buffers.
type DecodedFrameOwner interface {
	// OwnsDecodedFrames reports whether decoded frames and payload bytes stay valid and immutable after Decode returns.
	OwnsDecodedFrames() bool
}

type ReplyTokenTracker interface {
	TakeReplyTokens(session session.Session, count int) []string
}
