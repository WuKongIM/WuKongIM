package wknet

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"sync"
	"testing"

	stls "github.com/WuKongIM/WuKongIM/pkg/wknet/crypto/tls"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func TestWebsocket(t *testing.T) {
	e := NewEngine(WithWSAddr("ws://0.0.0.0:0"))
	e.Start()
	defer e.Stop()

	var wg sync.WaitGroup
	wg.Add(1) // 1 for upgrade, 1 for data
	e.OnData(func(conn Conn) error {
		data, err := conn.Peek(-1)
		assert.NoError(t, err)
		if len(data) == 0 {
			return nil
		}
		assert.Equal(t, "hello", string(data))
		wg.Done()
		return nil
	})

	u := url.URL{Scheme: "ws", Host: e.WSRealListenAddrt().String(), Path: "/"}

	c1, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	assert.NoError(t, err)
	defer c1.Close()

	err = c1.WriteMessage(websocket.BinaryMessage, []byte("hello"))
	assert.NoError(t, err)

	wg.Wait()

}

func TestWebsocketWSS(t *testing.T) {
	cert, err := stls.X509KeyPair(rsaCertPEM, rsaKeyPEM)
	if err != nil {
		fmt.Println("Error loading server certificate and key:", err)
		return
	}
	tlsConfig := &stls.Config{
		Certificates: []stls.Certificate{cert},
	}

	e := NewEngine(WithWSAddr("tcp://0.0.0.0:0"), WithWSTLSConfig(tlsConfig))
	err = e.Start()
	assert.NoError(t, err)
	defer e.Stop()

	var wg sync.WaitGroup
	wg.Add(1)
	e.OnData(func(conn Conn) error {
		data, err := conn.Peek(-1)
		assert.NoError(t, err)
		if string(data) == "hello" {
			wg.Done()
		}
		return nil
	})

	dialer := websocket.DefaultDialer

	u := url.URL{Scheme: "wss", Host: e.WSRealListenAddrt().String(), Path: ""}
	dialer.NetDialTLSContext = func(ctx context.Context, network, addr string) (conn net.Conn, err error) {

		conn, err = tls.Dial(network, addr, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12})
		return
	}

	c, _, err := dialer.Dial(u.String(), nil)
	assert.NoError(t, err)
	defer c.Close()

	err = c.WriteMessage(websocket.BinaryMessage, []byte("hello"))
	assert.NoError(t, err)

	wg.Wait()
}
