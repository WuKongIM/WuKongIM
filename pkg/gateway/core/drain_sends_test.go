package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestServerDrainSendsDeadlineDoesNotCancelAcceptedMailboxWork(t *testing.T) {
	handler := &blockingDrainSendHandler{started: make(chan struct{}), release: make(chan struct{})}
	server := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{Runtime: gatewaytypes.RuntimeOptions{
			AsyncSendWorkers:        1,
			AsyncSendQueueCapacity:  4,
			AsyncPoolReleaseTimeout: time.Second,
		}},
	}
	executor, err := newSendExecutor(server, server.options.Runtime)
	if err != nil {
		t.Fatalf("newSendExecutor() error = %v", err)
	}
	defer executor.stop()
	server.async.Store(&asyncRuntime{server: server, send: executor})

	state := &sessionState{server: server, closedCh: make(chan struct{}), requestContext: context.Background()}
	if !executor.submit(state, "reply", &frame.SendPacket{ChannelID: "drain", ClientMsgNo: "accepted"}) {
		t.Fatal("submit() = false, want accepted")
	}
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("accepted SEND did not start")
	}

	deadline, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := server.DrainSends(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DrainSends() error = %v, want context deadline exceeded", err)
	}
	if executor.submit(state, "reply", &frame.SendPacket{ChannelID: "drain", ClientMsgNo: "rejected"}) {
		t.Fatal("submit() after DrainSends admission close = true, want false")
	}

	close(handler.release)
	finished, finishedCancel := context.WithTimeout(context.Background(), time.Second)
	defer finishedCancel()
	if err := server.DrainSends(finished); err != nil {
		t.Fatalf("second DrainSends() error = %v, want nil after accepted work completes", err)
	}
}

func TestServerDrainSendsRejectsUnavailableRuntime(t *testing.T) {
	if err := (&Server{}).DrainSends(context.Background()); !errors.Is(err, gatewaytypes.ErrGatewayClosed) {
		t.Fatalf("DrainSends() error = %v, want gateway closed", err)
	}
}

type blockingDrainSendHandler struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockingDrainSendHandler) OnListenerError(string, error)            {}
func (h *blockingDrainSendHandler) OnSessionOpen(gatewaytypes.Context) error { return nil }
func (h *blockingDrainSendHandler) OnFrame(gatewaytypes.Context, frame.Frame) error {
	h.once.Do(func() { close(h.started) })
	<-h.release
	return nil
}
func (h *blockingDrainSendHandler) OnSessionClose(gatewaytypes.Context) error  { return nil }
func (h *blockingDrainSendHandler) OnSessionError(gatewaytypes.Context, error) {}
