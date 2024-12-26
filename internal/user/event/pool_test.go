package event

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

type mockUserEventHandler struct {
	connectC      chan struct{}
	connectPacket *wkproto.ConnectPacket
}

func (m *mockUserEventHandler) OnEvent(ctx *eventbus.UserContext) {

}

func (m *mockUserEventHandler) Connect(ctx *eventbus.UserContext) {
	m.connectPacket = ctx.Events[0].Frame.(*wkproto.ConnectPacket)
	m.connectC <- struct{}{}
}

func (m *mockUserEventHandler) OnMessage(ctx *eventbus.UserContext) {

}

func init() {
	options.G = options.New()
}

func TestNewUserEventPool(t *testing.T) {

	handler := &mockUserEventHandler{}
	pool := NewEventPool(handler)

	assert.NotNil(t, pool)
	assert.Equal(t, handler, pool.handler)
	assert.Equal(t, options.G.Poller.UserCount, len(pool.pollers))
}

func TestUserEventPool_Start(t *testing.T) {
	handler := &mockUserEventHandler{}
	pool := NewEventPool(handler)

	err := pool.Start()
	assert.Nil(t, err)
}

func TestUserEventPool_Stop(t *testing.T) {
	handler := &mockUserEventHandler{}
	pool := NewEventPool(handler)

	_ = pool.Start()
	pool.Stop()
}

func TestUserEventPool_AddConnectEvent(t *testing.T) {
	handler := &mockUserEventHandler{
		connectC: make(chan struct{}, 1),
	}
	pool := NewEventPool(handler)
	err := pool.Start()
	assert.NoError(t, err)
	defer pool.Stop()

	event := &eventbus.Event{
		Type: eventbus.EventConnect,
		Frame: &wkproto.ConnectPacket{
			UID: "test",
		},
	}
	pool.AddEvent("user1", event)

	<-handler.connectC

	assert.Equal(t, event.Frame, handler.connectPacket)
}
