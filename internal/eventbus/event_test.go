package eventbus

import (
	"testing"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

type MockChannelEventHandler struct {
	handled bool
}

func (m *MockChannelEventHandler) OnEvent(ctx *ChannelContext) {
	m.handled = true
}

func TestChannelEventHandler(t *testing.T) {
	handler := &MockChannelEventHandler{}
	ctx := &ChannelContext{}

	handler.OnEvent(ctx)

	assert.True(t, handler.handled, "Expected the event to be handled")
}

type MockUserEventHandler struct {
	handled bool
}

func (m *MockUserEventHandler) OnEvent(ctx *UserContext) {
	m.handled = true
}

func TestUserEventHandler(t *testing.T) {
	handler := &MockUserEventHandler{}
	ctx := &UserContext{}

	handler.OnEvent(ctx)

	assert.True(t, handler.handled, "Expected the event to be handled")
}

func TestEventEncodeDecode(t *testing.T) {
	event := &Event{
		Type:      EventConnect,
		MessageId: 12345,
		Conn:      &Conn{Uid: "test"},
		Frame: &wkproto.ConnectPacket{
			UID: "test",
		},
	}

	enc := wkproto.NewEncoder()
	defer enc.End()
	err := event.encodeWithEcoder(enc)
	assert.NoError(t, err, "Expected no error during encoding")

	encoded := enc.Bytes()

	decodedEvent := &Event{}
	err = decodedEvent.decodeWithDecoder(wkproto.NewDecoder(encoded))
	assert.NoError(t, err, "Expected no error during decoding")

	assert.Equal(t, event.Type, decodedEvent.Type, "Expected event types to match")
	assert.Equal(t, event.MessageId, decodedEvent.MessageId, "Expected message IDs to match")
}

func TestEventBatchEncodeDecode(t *testing.T) {
	events := EventBatch{
		&Event{
			Type: EventConnect, MessageId: 12345,
			Conn:  &Conn{Uid: "test"},
			Frame: &wkproto.ConnectPacket{UID: "test"},
		},
		&Event{Type: EventConnack, MessageId: 67890,
			Conn:  &Conn{Uid: "test"},
			Frame: &wkproto.ConnectPacket{UID: "test"},
		},
	}

	encoded, err := events.Encode()
	assert.NoError(t, err, "Expected no error during batch encoding")

	var decodedEvents EventBatch
	err = decodedEvents.Decode(encoded)
	assert.NoError(t, err, "Expected no error during batch decoding")

	assert.Equal(t, len(events), len(decodedEvents), "Expected event batch lengths to match")
	for i, event := range events {
		assert.Equal(t, event.Type, decodedEvents[i].Type, "Expected event types to match")
		assert.Equal(t, event.MessageId, decodedEvents[i].MessageId, "Expected message IDs to match")
	}
}
