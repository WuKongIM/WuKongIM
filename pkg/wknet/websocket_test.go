package wknet

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

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

	u := url.URL{Scheme: "ws", Host: e.WSRealListenAddr().String(), Path: "/"}

	c1, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	assert.NoError(t, err)
	defer c1.Close()

	err = c1.WriteMessage(websocket.BinaryMessage, []byte("hello"))
	assert.NoError(t, err)

	wg.Wait()

}

func TestBatchWSConn(t *testing.T) {
	e := NewEngine(WithWSAddr("ws://0.0.0.0:0"))
	e.Start()
	defer e.Stop()

	time.Sleep(time.Millisecond * 200)

	cliCount := 100 // 客户端数量
	msgCount := 200 // 每个客户端发送的消息数量

	finishChan := make(chan struct{})
	e.OnData(func(conn Conn) error {
		buff, err := conn.Peek(-1)
		if len(buff) == 0 {
			return nil
		}
		conn.Discard(len(buff))

		assert.NoError(t, err)
		reader := bufio.NewReader(bytes.NewReader(buff))

		for {
			line, _, err := reader.ReadLine()
			if err == io.EOF {
				break
			}
			assert.NoError(t, err)
			if len(line) > 0 && string(line) == "hello" {
				ctx := conn.Context()
				if ctx == nil {
					conn.SetContext(1)
				} else {
					conn.SetContext(ctx.(int) + 1)
				}
				helloCount := conn.Context().(int)
				if helloCount == msgCount {
					finishChan <- struct{}{}
				}
			}
		}
		return nil
	})

	done := make(chan struct{})
	go func() {
		finish := 0
		for {
			<-finishChan
			finish++
			if finish == cliCount {
				done <- struct{}{}
				break
			}
		}
	}()

	readyChan := make(chan struct{})
	for i := 0; i < cliCount; i++ {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		go testWSConnSend(msgCount, e.WSRealListenAddr().String(), t, wg, readyChan)
		wg.Wait()
	}

	close(readyChan)

	<-done

}

func testWSConnSend(msgNum int, addr string, t *testing.T, wg *sync.WaitGroup, readyChan chan struct{}) {
	u := url.URL{Scheme: "ws", Host: addr, Path: "/"}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	assert.NoError(t, err)

	wg.Done()
	assert.NoError(t, err)
	if conn == nil {
		return
	}
	<-readyChan

	for i := 0; i < msgNum; i++ {
		err = conn.WriteMessage(websocket.BinaryMessage, []byte("hello\n"))
		assert.NoError(t, err)
	}
}

func TestWebsocketWSS(t *testing.T) {
	cert, err := stls.X509KeyPair(rsaCertPEM, rsaKeyPEM)
	assert.NoError(t, err)
	tlsConfig := &stls.Config{
		Certificates: []stls.Certificate{cert},
	}

	e := NewEngine(WithWSSAddr("wss://0.0.0.0:0"), WithWSTLSConfig(tlsConfig))
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

	u := url.URL{Scheme: "wss", Host: e.WSSRealListenAddr().String(), Path: ""}
	dialer.NetDialTLSContext = func(ctx context.Context, network, addr string) (conn net.Conn, err error) {

		conn, err = tls.Dial(network, addr, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS13})
		return
	}

	c, _, err := dialer.Dial(u.String(), nil)
	assert.NoError(t, err)
	defer c.Close()

	err = c.WriteMessage(websocket.BinaryMessage, []byte("hello"))
	assert.NoError(t, err)

	wg.Wait()
}

func TestBatchWSSConn(t *testing.T) {
	cert, err := stls.X509KeyPair(rsaCertPEM, rsaKeyPEM)
	assert.NoError(t, err)
	tlsConfig := &stls.Config{
		Certificates: []stls.Certificate{cert},
	}

	e := NewEngine(WithWSSAddr("wss://0.0.0.0:0"), WithWSTLSConfig(tlsConfig))
	e.Start()
	defer e.Stop()

	time.Sleep(time.Millisecond * 200)

	cliCount := 100 // 客户端数量
	msgCount := 200 // 每个客户端发送的消息数量

	finishChan := make(chan struct{})
	e.OnData(func(conn Conn) error {
		buff, err := conn.Peek(-1)
		if len(buff) == 0 {
			return nil
		}
		conn.Discard(len(buff))

		assert.NoError(t, err)
		reader := bufio.NewReader(bytes.NewReader(buff))

		for {
			line, _, err := reader.ReadLine()
			if err == io.EOF {
				break
			}
			assert.NoError(t, err)
			if len(line) > 0 && string(line) == "hello" {
				ctx := conn.Context()
				if ctx == nil {
					conn.SetContext(1)
				} else {
					conn.SetContext(ctx.(int) + 1)
				}
				helloCount := conn.Context().(int)
				if helloCount == msgCount {
					finishChan <- struct{}{}
				}
			}
		}
		return nil
	})

	done := make(chan struct{})
	go func() {
		finish := 0
		for {
			<-finishChan
			finish++
			if finish == cliCount {
				done <- struct{}{}
				break
			}
		}
	}()

	readyChan := make(chan struct{})
	for i := 0; i < cliCount; i++ {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		go testWSSConnSend(msgCount, e.WSSRealListenAddr().String(), t, wg, readyChan)
		wg.Wait()
	}

	close(readyChan)

	<-done

}

func testWSSConnSend(msgNum int, addr string, t *testing.T, wg *sync.WaitGroup, readyChan chan struct{}) {
	u := url.URL{Scheme: "wss", Host: addr, Path: ""}

	dialer := websocket.DefaultDialer
	dialer.NetDialTLSContext = func(ctx context.Context, network, addr string) (conn net.Conn, err error) {

		conn, err = tls.Dial(network, addr, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS13})
		return
	}

	conn, _, err := dialer.Dial(u.String(), nil)
	assert.NoError(t, err)

	wg.Done()
	assert.NoError(t, err)
	if conn == nil {
		return
	}
	<-readyChan

	for i := 0; i < msgNum; i++ {
		err = conn.WriteMessage(websocket.BinaryMessage, []byte("hello\n"))
		assert.NoError(t, err)
	}
}
