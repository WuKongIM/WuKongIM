package codec

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
)

func TestEventEncodeAndDecode(t *testing.T) {
	packet := &frame.EventPacket{
		Id:        "123456",
		Type:      "test",
		Timestamp: 1234567890,
		Data:      []byte("test"),
	}
	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultEventPacket, ok := resultPacket.(*frame.EventPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.Id, resultEventPacket.Id)
	assert.Equal(t, packet.Type, resultEventPacket.Type)
	assert.Equal(t, packet.Timestamp, resultEventPacket.Timestamp)
	assert.Equal(t, packet.Data, resultEventPacket.Data)
}

func TestTerminalFenceEventCodecRoundTripPreservesExactCut(t *testing.T) {
	grant := frame.TerminalFenceGrant{Epoch: 0x1020304050607080, Capability: "codec-secret"}
	nonce := frame.TerminalFenceNonce{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	requestEvent, err := frame.NewTerminalFenceRequest(grant, nonce)
	if err != nil {
		t.Fatalf("NewTerminalFenceRequest() error = %v", err)
	}
	requestEncoded, err := New().EncodeFrame(requestEvent, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame(request) error = %v", err)
	}
	requestDecoded, consumed, err := New().DecodeFrame(requestEncoded, frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodeFrame(request) error = %v", err)
	}
	if consumed != len(requestEncoded) {
		t.Fatalf("DecodeFrame(request) consumed = %d, want %d", consumed, len(requestEncoded))
	}
	request, err := frame.ParseTerminalFenceRequest(requestDecoded.(*frame.EventPacket))
	if err != nil {
		t.Fatalf("ParseTerminalFenceRequest() error = %v", err)
	}
	if !request.AuthorizedBy(grant) {
		t.Fatal("decoded terminal request did not preserve exact grant")
	}
	pkt, err := request.AckEvent()
	if err != nil {
		t.Fatalf("AckEvent() error = %v", err)
	}
	encoded, err := New().EncodeFrame(pkt, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}
	decoded, consumed, err := New().DecodeFrame(encoded, frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodeFrame() error = %v", err)
	}
	if consumed != len(encoded) {
		t.Fatalf("DecodeFrame() consumed = %d, want %d", consumed, len(encoded))
	}
	event, ok := decoded.(*frame.EventPacket)
	if !ok {
		t.Fatalf("DecodeFrame() = %T, want *frame.EventPacket", decoded)
	}
	cut, err := frame.ParseTerminalFenceAck(event)
	if err != nil {
		t.Fatalf("ParseTerminalFenceEvent() error = %v", err)
	}
	if !cut.Matches(grant.Epoch, nonce) {
		t.Fatal("decoded terminal ACK did not preserve exact epoch and nonce")
	}
}
