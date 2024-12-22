package proto

import (
	"io"
	"testing"

	"github.com/panjf2000/gnet/v2"
	"github.com/stretchr/testify/assert"
)

type mockConn struct {
	gnet.Conn
	buffer []byte
}

func (m *mockConn) Peek(n int) ([]byte, error) {
	if len(m.buffer) < n {
		return nil, io.ErrShortBuffer
	}
	return m.buffer[:n], nil
}

func (m *mockConn) Discard(n int) (int, error) {
	if len(m.buffer) < n {
		return 0, io.ErrShortBuffer
	}
	m.buffer = m.buffer[n:]
	return n, nil
}

func (m *mockConn) InboundBuffered() int {
	return len(m.buffer)
}

func TestDefaultProto_Encode(t *testing.T) {
	proto := New()
	data := []byte("test data")
	msgType := MsgTypeMessage

	encoded, err := proto.Encode(data, msgType)
	assert.NoError(t, err)

	expectedLen := MagicNumberStartLength + MsgTypeLength + MsgContentLength + len(data)
	assert.Equal(t, expectedLen, len(encoded))
	assert.Equal(t, MagicNumberStart, encoded[:MagicNumberStartLength])
	assert.Equal(t, uint8(msgType), encoded[MagicNumberStartLength])
	assert.Equal(t, data, encoded[MagicNumberStartLength+MsgTypeLength+MsgContentLength:])
}

func TestDefaultProto_Decode(t *testing.T) {
	proto := New()
	data := []byte("test data")
	msgType := MsgTypeMessage

	encoded, err := proto.Encode(data, msgType)
	assert.NoError(t, err)

	conn := &mockConn{buffer: encoded}
	decodedData, decodedMsgType, msgLen, err := proto.Decode(conn)
	assert.NoError(t, err)

	assert.Equal(t, data, decodedData)
	assert.Equal(t, MsgType(msgType), decodedMsgType)
	assert.Equal(t, len(encoded), msgLen)

}

func TestDefaultProto_DecodeHeartbeat(t *testing.T) {
	proto := New()
	msgType := MsgTypeHeartbeat

	encoded, err := proto.Encode([]byte{byte(MsgTypeHeartbeat)}, msgType)
	assert.NoError(t, err)

	conn := &mockConn{buffer: encoded}
	decodedData, decodedMsgType, msgLen, err := proto.Decode(conn)
	assert.NoError(t, err)

	assert.Equal(t, []byte{msgType.Uint8()}, decodedData)
	assert.Equal(t, MsgType(msgType), decodedMsgType)
	assert.Equal(t, MagicNumberStartLength+MsgTypeLength, msgLen)
	assert.Equal(t, 0, conn.InboundBuffered())
}

func TestDefaultProto_DecodeInvalidMagicNumber(t *testing.T) {
	proto := New()
	data := []byte("test data")
	msgType := MsgTypeMessage

	encoded, err := proto.Encode(data, msgType)
	assert.NoError(t, err)

	encoded[0] = 'X' // Corrupt the magic number

	conn := &mockConn{buffer: encoded}
	_, _, _, err = proto.Decode(conn)
	assert.Error(t, err)
}

func TestDefaultProto_DecodeShortBuffer(t *testing.T) {
	proto := New()
	conn := &mockConn{buffer: []byte("short")}

	_, _, _, err := proto.Decode(conn)
	assert.Error(t, err)
}

func TestDefaultProto_DecodePartialData(t *testing.T) {
	proto := New()
	data := []byte("test data")
	msgType := MsgTypeMessage

	encoded, err := proto.Encode(data, msgType)
	assert.NoError(t, err)

	// Split the encoded data into two parts
	part1 := encoded[:len(encoded)/2]
	part2 := encoded[len(encoded)/2:]

	conn := &mockConn{buffer: part1}
	_, _, _, err = proto.Decode(conn)
	assert.Equal(t, io.ErrShortBuffer, err) // Expect an error due to incomplete data

	// Append the second part of the data
	conn.buffer = append(conn.buffer, part2...)
	decodedData, decodedMsgType, msgLen, err := proto.Decode(conn)
	assert.NoError(t, err)

	assert.Equal(t, data, decodedData)
	assert.Equal(t, MsgType(msgType), decodedMsgType)
	assert.Equal(t, len(encoded), msgLen)
	assert.Equal(t, 0, conn.InboundBuffered())
}

func TestDefaultProto_DecodeMixedHeartbeatAndMessage(t *testing.T) {
	proto := New()
	msgData := []byte("test message")
	msgType := MsgTypeMessage
	heartbeatType := MsgTypeHeartbeat

	encodedMsg, err := proto.Encode(msgData, msgType)
	assert.NoError(t, err)

	encodedHeartbeat, err := proto.Encode([]byte{byte(heartbeatType)}, heartbeatType)
	assert.NoError(t, err)

	// Combine encoded message and heartbeat
	combined := append(encodedMsg, encodedHeartbeat...)

	conn := &mockConn{buffer: combined}

	// Decode message
	decodedData, decodedMsgType, msgLen, err := proto.Decode(conn)
	assert.NoError(t, err)
	assert.Equal(t, msgData, decodedData)
	assert.Equal(t, msgType, decodedMsgType)
	assert.Equal(t, len(encodedMsg), msgLen)

	// Decode heartbeat
	decodedData, decodedMsgType, msgLen, err = proto.Decode(conn)
	assert.NoError(t, err)
	assert.Equal(t, []byte{heartbeatType.Uint8()}, decodedData)
	assert.Equal(t, heartbeatType, decodedMsgType)
	assert.Equal(t, MagicNumberStartLength+MsgTypeLength, msgLen)

	assert.Equal(t, 0, conn.InboundBuffered())
}
