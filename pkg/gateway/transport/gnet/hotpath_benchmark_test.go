package gnet

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
)

func BenchmarkGnetTCPOnTrafficEnqueue(b *testing.B) {
	payload := []byte("benchmark tcp payload")
	conn := &allocTestGnetConn{}
	runtime := &listenerRuntime{opts: transport.ListenerOptions{Network: "tcp"}}
	runtime.activate()
	state := &connState{
		raw:             conn,
		runtime:         runtime,
		mode:            connModeTCP,
		maxPendingBytes: 1 << 20,
	}
	conn.ctx = state
	group := &engineGroup{}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn.next = payload
		state.queue = state.queue[:0]
		state.pendingBytes = 0
		group.OnTraffic(conn)
	}
}

func BenchmarkGnetWSDecodeFrameWithLimit(b *testing.B) {
	payload := []byte("benchmark websocket payload")
	encoded := encodeMaskedTestWSFrame(b, true, wsOpcodeBinary, [4]byte{1, 2, 3, 4}, payload)
	inbound := make([]byte, len(encoded))
	state := &connState{
		mode:            connModeWSFrames,
		maxPendingBytes: 1 << 20,
	}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(inbound, encoded)
		state.wsInbound = inbound
		if result, ok := state.nextWSResult(); !ok || len(result.payload) != len(payload) {
			b.Fatalf("nextWSResult() = %+v, %v; want payload length %d", result, ok, len(payload))
		}
	}
}

func BenchmarkStateConnWrite(b *testing.B) {
	payloads := []struct {
		name    string
		payload []byte
	}{
		{name: "small", payload: []byte("benchmark write payload")},
		{name: "large", payload: bytes.Repeat([]byte("x"), 4<<10)},
	}
	networks := []struct {
		name    string
		network string
	}{
		{name: "tcp", network: "tcp"},
		{name: "websocket", network: "websocket"},
	}

	for _, pc := range payloads {
		for _, tc := range networks {
			b.Run(pc.name+"/"+tc.name, func(b *testing.B) {
				raw := &allocTestGnetConn{}
				conn := &stateConn{state: &connState{
					raw: raw,
					runtime: &listenerRuntime{opts: transport.ListenerOptions{
						Network: tc.network,
					}},
				}}

				b.SetBytes(int64(len(pc.payload)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := conn.Write(pc.payload); err != nil {
						b.Fatalf("Write() error = %v", err)
					}
				}
				b.StopTimer()
				if tc.network == "websocket" && len(pc.payload) < wsWritevPayloadThreshold && (len(raw.lastAsyncWrite) <= len(pc.payload) || raw.lastAsyncWritev != nil) {
					b.Fatalf("small websocket write used writev or missing header")
				}
				if tc.network == "websocket" && len(pc.payload) >= wsWritevPayloadThreshold && (len(raw.lastAsyncWritev) != 2 || len(raw.lastAsyncWritev[0]) == 0 || len(raw.lastAsyncWritev[1]) != len(pc.payload)) {
					b.Fatalf("large websocket writev buffers = %d, want header and payload", len(raw.lastAsyncWritev))
				}
			})
		}
	}
}

func BenchmarkStateConnWriteOutboundLimit(b *testing.B) {
	payload := []byte("benchmark write payload")
	for _, tc := range []struct {
		name    string
		network string
	}{
		{name: "tcp", network: "tcp"},
		{name: "websocket", network: "websocket"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			raw := &allocTestGnetConn{autoAsyncCallback: true}
			state := &connState{
				raw:              raw,
				maxOutboundBytes: 1 << 20,
				runtime: &listenerRuntime{opts: transport.ListenerOptions{
					Network: tc.network,
				}},
			}
			raw.ctx = state
			conn := &stateConn{state: state}

			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := conn.Write(payload); err != nil {
					b.Fatalf("Write() error = %v", err)
				}
			}
		})
	}
}
