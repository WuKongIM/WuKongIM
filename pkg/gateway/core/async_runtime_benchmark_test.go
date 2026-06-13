package core

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	benchmarkAsyncRuntimeBurst       = 512
	benchmarkAsyncRuntimePayloadSize = 128
)

func BenchmarkSendExecutorSubmitAndDispatch(b *testing.B) {
	for _, tc := range []struct {
		name     string
		sessions int
	}{
		{name: "single_session", sessions: 1},
		{name: "many_sessions", sessions: 64},
	} {
		b.Run(tc.name, func(b *testing.B) {
			handler := &benchmarkCoreSendHandler{}
			srv := benchmarkCoreServer(handler, gatewaytypes.SessionOptions{
				AsyncSendBatchMaxWait:    -time.Nanosecond,
				AsyncSendBatchMaxRecords: 128,
				AsyncSendBatchMaxBytes:   1 << 20,
			}, gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        16,
				AsyncSendQueueCapacity:  64 * 1024,
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  1,
				AsyncPoolReleaseTimeout: time.Second,
			})
			executor, err := newSendExecutor(srv, srv.options.Runtime)
			if err != nil {
				b.Fatalf("new send executor: %v", err)
			}
			defer executor.stop()

			states := benchmarkCoreSessionStates(srv, tc.sessions)
			send := &frame.SendPacket{
				ClientSeq:   1,
				ClientMsgNo: "benchmark-send",
				ChannelID:   "benchmark-channel",
				ChannelType: 2,
				Payload:     make([]byte, benchmarkAsyncRuntimePayloadSize),
			}

			b.ReportAllocs()
			b.SetBytes(benchmarkAsyncRuntimePayloadSize)
			b.ResetTimer()
			for submitted := 0; submitted < b.N; {
				burst := benchmarkCoreBurst(b.N - submitted)
				handler.expect(burst)
				for i := 0; i < burst; i++ {
					state := states[(submitted+i)%len(states)]
					if !executor.submit(state, "", send) {
						b.Fatalf("send submit rejected at iteration %d", submitted+i)
					}
				}
				handler.wait()
				submitted += burst
			}
			b.StopTimer()
		})
	}
}

func BenchmarkAuthExecutorSubmitAndRun(b *testing.B) {
	var writes atomic.Uint64
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Authenticator: gatewaytypes.AuthenticatorFunc(func(*gatewaytypes.Context, *frame.ConnectPacket) (*gatewaytypes.AuthResult, error) {
				return &gatewaytypes.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			Handler: handler,
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  1,
				AsyncAuthWorkers:        16,
				AsyncAuthQueueCapacity:  8 * 1024,
				AsyncPoolReleaseTimeout: time.Second,
			},
		},
		dispatcher: newDispatcher(handler),
	}
	executor, err := newAuthExecutor(srv, srv.options.Runtime)
	if err != nil {
		b.Fatalf("new auth executor: %v", err)
	}
	defer executor.stop()

	states := benchmarkCoreAuthStates(srv, benchmarkAsyncRuntimeBurst, &writes)
	connect := &frame.ConnectPacket{UID: "benchmark-user", DeviceID: "benchmark-device", DeviceFlag: frame.APP}

	b.ReportAllocs()
	b.ResetTimer()
	for submitted := 0; submitted < b.N; {
		burst := benchmarkCoreBurst(b.N - submitted)
		targetWrites := writes.Load() + uint64(burst)
		for i := 0; i < burst; i++ {
			state := states[i%len(states)]
			state.setAuthPending(true)
			if !executor.submit(asyncAuthTask{state: state, connect: connect}) {
				b.Fatalf("auth submit rejected at iteration %d", submitted+i)
			}
		}
		benchmarkCoreWait(b, func() bool {
			return writes.Load() >= targetWrites
		})
		submitted += burst
	}
	b.StopTimer()
}

func benchmarkCoreServer(handler gatewaytypes.Handler, sessionOpts gatewaytypes.SessionOptions, runtimeOpts gatewaytypes.RuntimeOptions) *Server {
	return &Server{
		options: gatewaytypes.Options{
			Handler:        handler,
			DefaultSession: sessionOpts,
			Runtime:        runtimeOpts,
		},
		dispatcher: newDispatcher(handler),
	}
}

func benchmarkCoreSessionStates(srv *Server, count int) []*sessionState {
	states := make([]*sessionState, count)
	for i := range states {
		states[i] = &sessionState{
			server: srv,
			listener: &listenerRuntime{options: gatewaytypes.ListenerOptions{
				Name:      "benchmark",
				Network:   "tcp",
				Transport: "benchmark",
				Protocol:  "benchmark",
			}},
			session:  session.New(session.Config{ID: uint64(i + 1)}),
			closedCh: make(chan struct{}),
		}
		states[i].requestContext, states[i].cancelRequestContext = context.WithCancel(context.Background())
	}
	return states
}

func benchmarkCoreAuthStates(srv *Server, count int, writes *atomic.Uint64) []*sessionState {
	states := benchmarkCoreSessionStates(srv, count)
	for i, state := range states {
		state.listener.adapter = asyncAuthEncodeOnlyProtocol{}
		state.session = session.New(session.Config{ID: uint64(i + 1)})
		state.conn = benchmarkCoreAuthConn{id: uint64(i + 1), writes: writes}
		state.setAuthRequired(true)
		state.setAuthPending(true)
		state.session.SetValue(gatewaytypes.SessionValueProtocolName, "wkproto")
	}
	return states
}

func benchmarkCoreBurst(remaining int) int {
	if remaining < benchmarkAsyncRuntimeBurst {
		return remaining
	}
	return benchmarkAsyncRuntimeBurst
}

func benchmarkCoreWait(b *testing.B, condition func() bool) {
	b.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Microsecond)
	}
	b.Fatal("benchmark condition not met before timeout")
}

type benchmarkCoreSendHandler struct {
	wg     sync.WaitGroup
	frames atomic.Uint64
}

func (h *benchmarkCoreSendHandler) expect(count int) {
	h.wg.Add(count)
}

func (h *benchmarkCoreSendHandler) wait() {
	h.wg.Wait()
}

func (h *benchmarkCoreSendHandler) OnListenerError(string, error) {}

func (h *benchmarkCoreSendHandler) OnSessionOpen(gatewaytypes.Context) error {
	return nil
}

func (h *benchmarkCoreSendHandler) OnFrame(gatewaytypes.Context, frame.Frame) error {
	h.frames.Add(1)
	h.wg.Done()
	return nil
}

func (h *benchmarkCoreSendHandler) OnSendBatch(items []gatewaytypes.SendBatchItem) error {
	h.frames.Add(uint64(len(items)))
	for range items {
		h.wg.Done()
	}
	return nil
}

func (h *benchmarkCoreSendHandler) OnSessionClose(gatewaytypes.Context) error {
	return nil
}

func (h *benchmarkCoreSendHandler) OnSessionError(gatewaytypes.Context, error) {}

type benchmarkCoreAuthConn struct {
	id     uint64
	writes *atomic.Uint64
}

func (c benchmarkCoreAuthConn) ID() uint64 { return c.id }

func (c benchmarkCoreAuthConn) Write([]byte) error {
	c.writes.Add(1)
	return nil
}

func (c benchmarkCoreAuthConn) Close() error { return nil }

func (c benchmarkCoreAuthConn) LocalAddr() string {
	return "local-" + strconv.FormatUint(c.id, 10)
}

func (c benchmarkCoreAuthConn) RemoteAddr() string {
	return "remote-" + strconv.FormatUint(c.id, 10)
}
