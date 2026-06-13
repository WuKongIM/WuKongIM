package core

import (
	"sync"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// asyncRuntime groups gateway async executors behind one package-local runtime.
type asyncRuntime struct {
	// server owns the gateway state used by async executor tasks.
	server *Server
	// auth admits CONNECT authentication work.
	auth *authExecutor
	// send admits SEND dispatch work.
	send *sendExecutor
	// releaseTimeout is the normalized graceful pool release timeout.
	releaseTimeout time.Duration
	// stopOnce makes runtime shutdown safe for repeated callers.
	stopOnce sync.Once
}

func newAsyncRuntime(s *Server) (*asyncRuntime, error) {
	opts := gatewaytypes.NormalizeRuntimeOptions(s.options.Runtime)

	auth, err := newAuthExecutor(s, opts)
	if err != nil {
		return nil, err
	}
	send, err := newSendExecutor(s, opts)
	if err != nil {
		auth.stop()
		return nil, err
	}

	return &asyncRuntime{
		server:         s,
		auth:           auth,
		send:           send,
		releaseTimeout: opts.AsyncPoolReleaseTimeout,
	}, nil
}

func (r *asyncRuntime) stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		if r.auth != nil {
			r.auth.stop()
		}
		if r.send != nil {
			r.send.stop()
		}
	})
}

func (r *asyncRuntime) submitAuth(task asyncAuthTask) bool {
	if r == nil || r.auth == nil {
		return false
	}
	return r.auth.submit(task)
}

func (r *asyncRuntime) submitSend(state *sessionState, replyToken string, sendFrame *frame.SendPacket) bool {
	if r == nil || r.send == nil {
		return false
	}
	return r.send.submit(state, replyToken, sendFrame)
}
