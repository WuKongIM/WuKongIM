package protocol

import (
	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type Adapter interface {
	Name() string
	Decode(session session.Session, in []byte) ([]frame.Frame, int, error)
	Encode(session session.Session, f frame.Frame, meta session.OutboundMeta) ([]byte, error)
	OnOpen(session session.Session) error
	OnClose(session session.Session) error
}

type ReplyTokenTracker interface {
	TakeReplyTokens(session session.Session, count int) []string
}
