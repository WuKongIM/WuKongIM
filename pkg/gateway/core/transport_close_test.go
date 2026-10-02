package core

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type proofWriteConn struct {
	recordingWriteConn
	wait func(context.Context) error
}

func (c *proofWriteConn) CloseAndWait(ctx context.Context) error { return c.wait(ctx) }

func TestContextPhysicalCloseFencesWithoutJoiningBusinessCleanup(t *testing.T) {
	request, cancel := context.WithCancel(context.Background())
	state := &sessionState{server: &Server{}, listener: &listenerRuntime{}, session: session.New(session.Config{}),
		requestContext: request, cancelRequestContext: cancel, closedCh: make(chan struct{})}
	waited := 0
	state.conn = &proofWriteConn{wait: func(ctx context.Context) error {
		waited++
		if request.Err() == nil {
			t.Error("request work was not canceled")
		}
		if err := state.session.WriteFrame(&frame.PongPacket{}); !errors.Is(err, session.ErrSessionClosed) {
			t.Errorf("write not fenced: %v", err)
		}
		select {
		case <-state.closedCh:
			t.Error("logical cleanup was invoked by proof operation")
		default:
		}
		return ctx.Err()
	}}
	entry := (dispatcher{}).context(state, "", "", request)
	if err := entry.CloseTransportAndWait(context.Background(), gt.CloseReasonPolicyViolation); err != nil {
		t.Fatal(err)
	}
	if waited != 1 || !state.isClosed() || state.closeReason() != gt.CloseReasonPolicyViolation {
		t.Fatal("missing admission fence or close reason")
	}
	if err := entry.CloseTransportAndWait(context.Background(), gt.CloseReasonServerStop); err != nil {
		t.Fatal(err)
	}
	if state.closeReason() != gt.CloseReasonPolicyViolation {
		t.Fatal("retry changed original reason")
	}
}

func TestContextPhysicalCloseRejectsUnsupportedProof(t *testing.T) {
	state := &sessionState{server: &Server{}, listener: &listenerRuntime{}, session: session.New(session.Config{}), conn: &recordingWriteConn{}}
	entry := (dispatcher{}).context(state, "", "", nil)
	if err := entry.CloseTransportAndWait(context.Background(), gt.CloseReasonServerStop); !errors.Is(err, gt.ErrCloseProofUnsupported) {
		t.Fatal(err)
	}
	if state.isClosed() {
		t.Fatal("unsupported transport started incomplete core closure")
	}
	for _, entry := range []*gt.Context{nil, {}, {Session: state.session, CloseSessionFn: func(gt.CloseReason, error) { t.Error("fell back to logical close") }}} {
		if err := entry.CloseTransportAndWait(context.Background(), gt.CloseReasonServerStop); !errors.Is(err, gt.ErrCloseProofUnsupported) {
			t.Fatal(err)
		}
	}
}
