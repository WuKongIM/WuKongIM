package wknet

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEngine(t *testing.T) {
	e := NewEngine()

	e.Start()
	defer e.Stop()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	finishChan := make(chan struct{})
	clientCount := 100
	clientMsgCount := 5

	var wg sync.WaitGroup

	wg.Add(clientCount * clientMsgCount)

	go func() {
		<-timeoutCtx.Done()
		assert.NoError(t, errors.New("timeout"))
		finishChan <- struct{}{}

	}()

	go func() {
		wg.Wait()
		finishChan <- struct{}{}
	}()

	e.OnData(func(conn Conn) error {
		buffs, err := conn.Peek(-1)
		conn.Discard(len(buffs))

		buffStrs := strings.Split(string(buffs), "\n")
		for i := 0; i < len(buffStrs); i++ {
			buff := buffStrs[i]
			assert.NoError(t, err)
			value := buff
			values := strings.Split(value, " ")
			cmd := values[0]
			param := values[1:]
			if !conn.IsAuthed() {
				if cmd == "auth" {
					conn.SetAuthed(true)
					conn.SetUID(param[0])
					conn.WriteToOutboundBuffer([]byte("auth ok\n"))
					conn.WakeWrite()
				} else {
					panic("need auth")
				}
			} else if cmd == "send" {
				go func(c Conn, p []string) {
					assert.Equal(t, param[0], c.UID())
					wg.Done()

				}(conn, param)

			}
		}

		return nil
	})

	time.Sleep(time.Millisecond * 100)
	for i := 0; i < clientCount; i++ {
		uid := fmt.Sprintf("uid%d", i)
		cli, err := net.Dial("tcp", e.TCPRealListenAddr().String())
		assert.NoError(t, err)
		_, err = cli.Write([]byte(fmt.Sprintf("auth %s\n", uid)))
		assert.NoError(t, err)

		_, _, err = bufio.NewReader(cli).ReadLine()
		assert.NoError(t, err)
		defer cli.Close()
		go func(c net.Conn) {
			for j := 0; j < clientMsgCount; j++ {
				c.Write([]byte(fmt.Sprintf("send %s\n", uid)))
			}
		}(cli)
	}
	// fmt.Println("finishChan wait")
	<-finishChan
}
