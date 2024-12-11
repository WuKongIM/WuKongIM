package test

import (
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/gnet/v2"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestSendAndRecv(t *testing.T) {

	addr := "tcp://:10001"

	s := wkserver.New(addr, wkserver.WithMessagePoolOn(false))

	var wg sync.WaitGroup
	s.OnMessage(func(conn gnet.Conn, m *proto.Message) {
		wg.Done()
	})
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(addr, client.WithUid("uid"))
	err = cli.Start()
	assert.NoError(t, err)
	defer cli.Stop()

	time.Sleep(time.Millisecond * 200)

	msgCount := 100000
	wg.Add(msgCount)

	for i := 0; i < msgCount; i++ {
		err = cli.Send(&proto.Message{
			Id:      uint64(i),
			MsgType: 112349,
			Content: []byte("hellohellohellohellohellohellohellohellohellohellohellohellohellohellohello"),
		})
		assert.NoError(t, err)
	}

	wg.Wait()
}

func TestRequestResp(t *testing.T) {
	addr := "tcp://127.0.0.1:10001"
	wklog.Configure(&wklog.Options{
		Level: zap.InfoLevel,
	})
	s := wkserver.New(addr)
	s.Route("/test", func(c *wkserver.Context) {
		c.Write([]byte("hello"))
	})
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	time.Sleep(time.Millisecond * 200)

	cli := client.New(addr, client.WithUid("uid"))
	err = cli.Start()
	assert.NoError(t, err)
	defer cli.Stop()

	time.Sleep(time.Millisecond * 200)

	var count = 10000

	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			resp, err := cli.Request("/test", []byte("hi"))
			assert.NoError(t, err)
			assert.Equal(t, []byte("hello"), resp.Body)
			wg.Done()

		}()
	}

	wg.Wait()

}
