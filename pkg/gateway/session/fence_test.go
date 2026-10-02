package session

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestOutboundFenceDoesNotWaitForEnteredEncoder(t *testing.T) {
	entered, release, written := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	s := New(Config{WritePacketFn: func(any, OutboundMeta) error {
		close(entered)
		<-release
		return nil
	}})
	go func() { written <- s.(PacketWriter).WritePacket("entered") }()
	<-entered
	s.(OutboundFencer).FenceOutbound()
	s.(OutboundFencer).FenceOutbound()
	if err := s.WriteFrame(&frame.PongPacket{}); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("frame after fence: %v", err)
	}
	if err := s.(PacketWriter).WritePacket("late"); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("packet after fence: %v", err)
	}
	close(release)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
