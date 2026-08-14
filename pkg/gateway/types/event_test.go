package types

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestContextSealOutboundAndWriteUsesSessionFinalWrite(t *testing.T) {
	var written frame.Frame
	sess := session.New(session.Config{
		ID: 1,
		WriteFrameFn: func(f frame.Frame, _ session.OutboundMeta) error {
			written = f
			return nil
		},
	})
	ctx := Context{Session: sess}
	ack := &frame.EventPacket{Type: "terminal-ack"}

	if err := ctx.SealOutboundAndWrite(ack); err != nil {
		t.Fatalf("SealOutboundAndWrite() error = %v", err)
	}
	if !ctx.OutboundSealed() {
		t.Fatal("OutboundSealed() = false after terminal frame admission")
	}
	if written != ack {
		t.Fatalf("written = %#v, want terminal ack", written)
	}
	if err := ctx.WriteFrame(&frame.PongPacket{}); !errors.Is(err, session.ErrOutboundSealed) {
		t.Fatalf("WriteFrame() after seal = %v, want %v", err, session.ErrOutboundSealed)
	}
}

func TestContextSealOutboundAndWriteFailsWhenCapabilityIsMissing(t *testing.T) {
	ctx := Context{Session: contextSessionWithoutOutboundSeal{}}
	if ctx.OutboundSealed() {
		t.Fatal("OutboundSealed() = true without optional capability")
	}
	if err := ctx.SealOutboundAndWrite(&frame.EventPacket{}); !errors.Is(err, session.ErrOutboundSealUnsupported) {
		t.Fatalf("SealOutboundAndWrite() error = %v, want %v", err, session.ErrOutboundSealUnsupported)
	}
}

type contextSessionWithoutOutboundSeal struct{}

func (contextSessionWithoutOutboundSeal) ID() uint64         { return 1 }
func (contextSessionWithoutOutboundSeal) Listener() string   { return "test" }
func (contextSessionWithoutOutboundSeal) RemoteAddr() string { return "remote" }
func (contextSessionWithoutOutboundSeal) LocalAddr() string  { return "local" }
func (contextSessionWithoutOutboundSeal) WriteFrame(frame.Frame, ...session.WriteOption) error {
	return nil
}
func (contextSessionWithoutOutboundSeal) Close() error         { return nil }
func (contextSessionWithoutOutboundSeal) SetValue(string, any) {}
func (contextSessionWithoutOutboundSeal) Value(string) any     { return nil }
