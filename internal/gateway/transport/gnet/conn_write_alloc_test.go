package gnet

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
)

func TestStateConnWebSocketWriteAllocatesOnlyFramedPayload(t *testing.T) {
	raw := &allocTestGnetConn{}
	conn := &stateConn{state: &connState{
		raw: raw,
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
		}},
	}}
	payload := []byte("hello websocket")

	if err := conn.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	frame, _, err := decodeWSFrame(raw.lastAsyncWrite)
	if err != nil {
		t.Fatalf("decodeWSFrame(write): %v", err)
	}
	if frame.opcode != wsOpcodeText {
		t.Fatalf("opcode = %d, want text", frame.opcode)
	}
	if !bytes.Equal(frame.payload, payload) {
		t.Fatalf("payload = %q, want %q", frame.payload, payload)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := conn.Write(payload); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	})
	if allocs > 1 {
		t.Fatalf("allocs = %.0f, want <= 1", allocs)
	}
}
