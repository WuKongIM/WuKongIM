package wknet

import (
	"net/url"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func TestWebsocket(t *testing.T) {
	e := NewWSEngine(WithAddr("tcp://0.0.0.0:9080"))
	e.Start()
	defer e.Stop()

	var wg sync.WaitGroup
	wg.Add(1)
	e.OnData(func(conn Conn) error {
		data, err := conn.Peek(-1)
		assert.NoError(t, err)
		assert.Equal(t, "hello", string(data))
		wg.Done()
		return nil
	})

	u := url.URL{Scheme: "ws", Host: "localhost:9080", Path: "/"}

	c1, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	assert.NoError(t, err)
	defer c1.Close()

	err = c1.WriteMessage(websocket.BinaryMessage, []byte("hello"))
	assert.NoError(t, err)

	wg.Wait()

	// var wg sync.WaitGroup
	// wg.Add(1)
	// go func() {
	// 	for {
	// 		_, message, err := c1.ReadMessage()
	// 		assert.NoError(t, err)
	// 		assert.Equal(t, "hello", string(message))
	// 		wg.Done()
	// 		break
	// 	}
	// }()

	// wg.Wait()

}
