package codec

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
)

func TestSendackEncodeAndDecode(t *testing.T) {

	packet := &frame.SendackPacket{
		ClientSeq:   234,
		ClientMsgNo: "client-msg-no",
		MessageSeq:  2,
		MessageID:   1234,
		ReasonCode:  frame.ReasonSuccess,
	}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)
	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultSendackPacket, ok := resultPacket.(*frame.SendackPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.ClientSeq, resultSendackPacket.ClientSeq)
	assert.Equal(t, packet.ClientMsgNo, resultSendackPacket.ClientMsgNo)
	assert.Equal(t, packet.MessageSeq, resultSendackPacket.MessageSeq)
	assert.Equal(t, packet.MessageID, resultSendackPacket.MessageID)
	assert.Equal(t, packet.ReasonCode, resultSendackPacket.ReasonCode)
}

func TestSendackEncodeAndDecodeSupportsUint64MessageSeqOnLatestVersion(t *testing.T) {
	packet := &frame.SendackPacket{
		ClientSeq:   234,
		ClientMsgNo: "client-msg-no",
		MessageSeq:  uint64(^uint32(0)) + 7,
		MessageID:   1234,
		ReasonCode:  frame.ReasonSuccess,
	}

	codec := New()
	packetBytes, err := codec.EncodeFrame(packet, frame.LatestVersion)
	assert.NoError(t, err)

	resultPacket, _, err := codec.DecodeFrame(packetBytes, frame.LatestVersion)
	assert.NoError(t, err)
	resultSendackPacket, ok := resultPacket.(*frame.SendackPacket)
	assert.Equal(t, true, ok)
	assert.Equal(t, packet.MessageSeq, resultSendackPacket.MessageSeq)
}

func TestSendackEncodeKeepsLegacyCoreFieldsBeforeOptionalClientMsgNo(t *testing.T) {
	packet := &frame.SendackPacket{
		ClientSeq:   234,
		ClientMsgNo: "cb123456-1234-1234-1234-123456789abc",
		MessageSeq:  9,
		MessageID:   1234,
		ReasonCode:  frame.ReasonSuccess,
	}

	codec := New()
	packetBytes, err := codec.EncodeFrame(packet, frame.LegacyMessageSeqVersion)
	assert.NoError(t, err)

	legacyAck, err := decodeLegacySendackCore(packetBytes, frame.LegacyMessageSeqVersion)
	assert.NoError(t, err)
	assert.Equal(t, packet.MessageID, legacyAck.MessageID)
	assert.Equal(t, packet.ClientSeq, legacyAck.ClientSeq)
	assert.Equal(t, packet.MessageSeq, legacyAck.MessageSeq)
	assert.Equal(t, packet.ReasonCode, legacyAck.ReasonCode)
}

func TestDecodeSendackAcceptsClientMsgNoBeforeMessageSeqForTransition(t *testing.T) {
	packet := &frame.SendackPacket{
		ClientSeq:   234,
		ClientMsgNo: "client-msg-no",
		MessageSeq:  9,
		MessageID:   1234,
		ReasonCode:  frame.ReasonSuccess,
	}

	packetBytes, err := encodeSendackWithClientMsgNoBeforeMessageSeq(packet, frame.LegacyMessageSeqVersion)
	assert.NoError(t, err)

	resultPacket, _, err := New().DecodeFrame(packetBytes, frame.LegacyMessageSeqVersion)
	assert.NoError(t, err)
	resultSendackPacket, ok := resultPacket.(*frame.SendackPacket)
	assert.Equal(t, true, ok)
	assert.Equal(t, packet.ClientSeq, resultSendackPacket.ClientSeq)
	assert.Equal(t, packet.ClientMsgNo, resultSendackPacket.ClientMsgNo)
	assert.Equal(t, packet.MessageSeq, resultSendackPacket.MessageSeq)
	assert.Equal(t, packet.MessageID, resultSendackPacket.MessageID)
	assert.Equal(t, packet.ReasonCode, resultSendackPacket.ReasonCode)
}

func decodeLegacySendackCore(packetBytes []byte, version uint8) (*frame.SendackPacket, error) {
	proto := New()
	framer, remainingLengthLength, err := proto.decodeFramer(packetBytes)
	if err != nil {
		return nil, err
	}
	body := packetBytes[1+remainingLengthLength:]
	dec := NewDecoder(body)

	ack := &frame.SendackPacket{Framer: framer}
	if ack.MessageID, err = dec.Int64(); err != nil {
		return nil, err
	}
	clientSeq, err := dec.Uint32()
	if err != nil {
		return nil, err
	}
	ack.ClientSeq = uint64(clientSeq)
	if ack.MessageSeq, err = decodeMessageSeq(dec, version); err != nil {
		return nil, err
	}
	reasonCode, err := dec.Uint8()
	if err != nil {
		return nil, err
	}
	ack.ReasonCode = frame.ReasonCode(reasonCode)
	return ack, nil
}

func encodeSendackWithClientMsgNoBeforeMessageSeq(packet *frame.SendackPacket, version uint8) ([]byte, error) {
	buffer := bytes.NewBuffer(nil)
	enc := NewEncoderBuffer(buffer)
	defer enc.End()

	New().encodeFrame(packet, enc, uint32(encodeSendackSize(packet, version)))
	enc.WriteInt64(packet.MessageID)
	enc.WriteUint32(uint32(packet.ClientSeq))
	enc.WriteString(packet.ClientMsgNo)
	if err := encodeMessageSeq(enc, version, packet.MessageSeq); err != nil {
		return nil, err
	}
	enc.WriteUint8(packet.ReasonCode.Byte())
	return buffer.Bytes(), nil
}
