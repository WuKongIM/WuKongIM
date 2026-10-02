package types

import "github.com/WuKongIM/WuKongIM/pkg/gateway/session"

// PacketHandler supplies an independent protocol's authentication and entry
// mapping. OnConnect runs on the auth pool; OnPacket runs in the session-ordered
// SEND mailbox. Neither callback runs on the transport event loop.
type PacketHandler interface {
	OnConnect(Context, any) (*PacketAuthResult, error)
	OnPacket(Context, any) error
	OnSessionOpen(Context) error
	OnSessionClose(Context) error
	OnSessionError(Context, error)
	OnListenerError(string, error)
}

// PacketAuthResult transfers one completed activation to the gateway. Accepted
// requires a non-nil Reply. Rollback releases resources when acceptance cannot
// reach OnSessionOpen; after that callback the handler owns close cleanup.
type PacketAuthResult struct {
	Accepted      bool
	Reply         any
	SessionValues map[string]any
	// CheckReply optionally revalidates accepted activation immediately before
	// reply enqueue. It must be bounded and retain resources until open/rollback.
	// An error or panic prevents the reply and invokes Rollback exactly once.
	CheckReply func() error
	Rollback   func(error)
}

// WritePacket uses the session's common encode/write/close serialization lock.
func (ctx *Context) WritePacket(packet any) error {
	if ctx == nil || ctx.Session == nil {
		return session.ErrSessionClosed
	}
	writer, ok := ctx.Session.(session.PacketWriter)
	if !ok {
		return session.ErrPacketWriteUnsupported
	}
	return writer.WritePacket(packet, session.WithReplyToken(ctx.ReplyToken))
}
