package reactor

import (
	"testing"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

type MockFrame struct{}

func TestDefaultUserMessage_EncodeDecode(t *testing.T) {
	// Initialize the message with mock values
	msg := &UserMessage{
		Conn:      &Conn{},
		Frame:     &wkproto.PingPacket{},
		WriteData: []byte("some-data"),
		Index:     12345,
		ToNode:    67890,
	}

	// Test Encode
	encoded, err := msg.Encode()
	assert.NoError(t, err)
	assert.NotNil(t, encoded)
	t.Logf("Encoded message: %v", encoded)

	// Test Decode
	decodedMsg := &UserMessage{}
	err = decodedMsg.Decode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, msg.Index, decodedMsg.Index)
	assert.Equal(t, msg.ToNode, decodedMsg.ToNode)
	assert.Equal(t, string(msg.WriteData), string(decodedMsg.WriteData))
}

func TestDefaultUserMessage_EncodeNoConn(t *testing.T) {
	// Test the case when Conn is nil
	msg := &UserMessage{
		Frame:     &wkproto.PingPacket{},
		WriteData: []byte("some-data"),
		Index:     12345,
		ToNode:    67890,
	}

	encoded, err := msg.Encode()
	assert.NoError(t, err)
	assert.NotNil(t, encoded)

	// Decode it back
	decodedMsg := &UserMessage{}
	err = decodedMsg.Decode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, msg.Index, decodedMsg.Index)
	assert.Equal(t, msg.ToNode, decodedMsg.ToNode)
	assert.Equal(t, string(msg.WriteData), string(decodedMsg.WriteData))
}

func TestDefaultUserMessage_EncodeNoFrame(t *testing.T) {
	// Test the case when Frame is nil
	msg := &UserMessage{
		Conn: &Conn{
			Uid: "u1",
		},
		WriteData: []byte("some-data"),
		Index:     12345,
		ToNode:    67890,
	}

	encoded, err := msg.Encode()
	assert.NoError(t, err)
	assert.NotNil(t, encoded)

	// Decode it back
	decodedMsg := &UserMessage{}
	err = decodedMsg.Decode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, msg.Index, decodedMsg.Index)
	assert.Equal(t, msg.ToNode, decodedMsg.ToNode)
	assert.Equal(t, string(msg.WriteData), string(decodedMsg.WriteData))
	assert.Equal(t, msg.Conn.Uid, decodedMsg.Conn.Uid)
}

func TestDefaultUserMessage_EncodeNoWriteData(t *testing.T) {
	// Test the case when WriteData is empty
	msg := &UserMessage{
		Conn: &Conn{
			Uid: "u1",
		},
		Frame:  &wkproto.PingPacket{},
		Index:  12345,
		ToNode: 67890,
	}

	encoded, err := msg.Encode()
	assert.NoError(t, err)
	assert.NotNil(t, encoded)

	// Decode it back
	decodedMsg := &UserMessage{}
	err = decodedMsg.Decode(encoded)
	assert.NoError(t, err)
	assert.Equal(t, msg.Index, decodedMsg.Index)
	assert.Equal(t, msg.ToNode, decodedMsg.ToNode)
	assert.Equal(t, msg.Conn.Uid, decodedMsg.Conn.Uid)
}
