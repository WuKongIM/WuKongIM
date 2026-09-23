package session

import "errors"

var ErrPacketWriteUnsupported = errors.New("gateway/session: independent packet writes unsupported")

// PacketWriter is an optional capability for protocols independent of WK frames.
// It shares the same closing, terminal-seal and serialization gates as WriteFrame.
type PacketWriter interface {
	WritePacket(any, ...WriteOption) error
}
type WritePacketFn func(any, OutboundMeta) error

func (s *session) WritePacket(packet any, opts ...WriteOption) error {
	if s == nil || s.closing.Load() || s.closed.Load() {
		return ErrSessionClosed
	}
	if s.outboundSealed.Load() {
		return ErrOutboundSealed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing.Load() || s.closed.Load() {
		return ErrSessionClosed
	}
	if s.outboundSealed.Load() {
		return ErrOutboundSealed
	}
	if s.writePacketFn == nil {
		return ErrPacketWriteUnsupported
	}
	meta := OutboundMeta{}
	for _, opt := range opts {
		if opt != nil {
			opt.apply(&meta)
		}
	}
	return s.writePacketFn(packet, meta)
}
