package core

import (
	"testing"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestAsyncAuthQueueSubmitRejectsWhenFull(t *testing.T) {
	queue := newAsyncAuthQueueWithCapacity(1)
	state := &sessionState{}

	if !queue.submit(asyncAuthTask{
		state:      state,
		replyToken: "r1",
		connect:    &frame.ConnectPacket{UID: "u1"},
	}) {
		t.Fatal("first auth submit rejected")
	}
	if queue.submit(asyncAuthTask{
		state:      state,
		replyToken: "r2",
		connect:    &frame.ConnectPacket{UID: "u2"},
	}) {
		t.Fatal("second auth submit accepted when queue is full")
	}
	queue.close()
}

func TestAsyncAuthQueueConsumeBalancesQueuedDepth(t *testing.T) {
	queue := newAsyncAuthQueueWithCapacity(1)
	if !queue.submit(asyncAuthTask{
		state:   &sessionState{},
		connect: &frame.ConnectPacket{UID: "u1"},
	}) {
		t.Fatal("auth submit rejected")
	}
	task := <-queue.tasks
	if task.connect == nil {
		t.Fatal("queued auth task has nil connect packet")
	}
	queue.consume(1)
	if got := queue.queued.Load(); got != 0 {
		t.Fatalf("queued depth = %d, want 0", got)
	}
	queue.close()
}

func TestAsyncAuthWorkerCountBounds(t *testing.T) {
	if got, want := adaptiveAsyncAuthWorkerCount(0), minAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with zero GOMAXPROCS = %d, want %d", got, want)
	}
	if got, want := adaptiveAsyncAuthWorkerCount(1), minAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with one GOMAXPROCS = %d, want %d", got, want)
	}
	if got, want := adaptiveAsyncAuthWorkerCount(128), maxAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with large GOMAXPROCS = %d, want %d", got, want)
	}
}

func TestCloseReasonForAsyncAuthQueueFull(t *testing.T) {
	if got := closeReasonForError(gatewaytypes.ErrAsyncAuthQueueFull, gatewaytypes.CloseReasonPolicyViolation); got != gatewaytypes.CloseReasonAsyncAuthQueueFull {
		t.Fatalf("close reason = %q, want %q", got, gatewaytypes.CloseReasonAsyncAuthQueueFull)
	}
}
