package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestRecordingHandlerWaitForFrameCountWakesOnFrame(t *testing.T) {
	handler := NewRecordingHandler()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := make(chan bool, 1)
	go func() {
		result <- handler.WaitForFrameCount(ctx, 1)
	}()

	if err := handler.OnFrame(gateway.Context{}, &frame.PingPacket{}); err != nil {
		t.Fatalf("OnFrame: %v", err)
	}

	select {
	case ok := <-result:
		if !ok {
			t.Fatal("WaitForFrameCount returned false after the frame arrived")
		}
	case <-ctx.Done():
		t.Fatal("WaitForFrameCount did not wake after the frame arrived")
	}
}

func TestRecordingHandlerWaitForFrameCountHonorsCancellation(t *testing.T) {
	handler := NewRecordingHandler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if handler.WaitForFrameCount(ctx, 1) {
		t.Fatal("WaitForFrameCount returned true without a frame")
	}
}
