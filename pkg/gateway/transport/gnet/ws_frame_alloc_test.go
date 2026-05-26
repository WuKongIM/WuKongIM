package gnet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNextWSResultSingleFrameAllocatesOnePayloadBuffer(t *testing.T) {
	encoded := encodeMaskedTestWSFrame(t, true, wsOpcodeText, [4]byte{1, 2, 3, 4}, []byte("hello websocket"))

	state := &connState{
		mode:      connModeWSFrames,
		wsInbound: make([]byte, 0, len(encoded)),
	}
	state.wsInbound = append(state.wsInbound[:0], encoded...)
	result, ok := state.nextWSResult()
	if !ok {
		t.Fatal("nextWSResult() did not return a complete frame")
	}
	if !bytes.Equal(result.payload, []byte("hello websocket")) {
		t.Fatalf("payload = %q, want %q", result.payload, "hello websocket")
	}

	var payloadLen int
	inbound := make([]byte, len(encoded))
	allocs := testing.AllocsPerRun(1000, func() {
		copy(inbound, encoded)
		state.wsInbound = inbound
		result, ok := state.nextWSResult()
		if !ok {
			t.Fatal("nextWSResult() did not return a complete frame")
		}
		payloadLen += len(result.payload)
	})
	if payloadLen == 0 {
		t.Fatal("nextWSResult() returned empty payloads during allocation check")
	}
	if allocs > 1 {
		t.Fatalf("allocs = %.0f, want <= 1", allocs)
	}
}

func encodeMaskedTestWSFrame(t testing.TB, final bool, opcode byte, maskKey [4]byte, payload []byte) []byte {
	t.Helper()

	var header bytes.Buffer
	first := opcode
	if final {
		first |= 0x80
	}
	header.WriteByte(first)

	payloadLen := len(payload)
	switch {
	case payloadLen < 126:
		header.WriteByte(0x80 | byte(payloadLen))
	case payloadLen <= 0xffff:
		header.WriteByte(0x80 | 126)
		if err := binary.Write(&header, binary.BigEndian, uint16(payloadLen)); err != nil {
			t.Fatalf("write websocket length: %v", err)
		}
	default:
		header.WriteByte(0x80 | 127)
		if err := binary.Write(&header, binary.BigEndian, uint64(payloadLen)); err != nil {
			t.Fatalf("write websocket length: %v", err)
		}
	}

	header.Write(maskKey[:])
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= maskKey[i%4]
	}
	header.Write(masked)
	return header.Bytes()
}
