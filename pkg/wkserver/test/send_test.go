package test

import (
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestSendAndRecv(t *testing.T) {
	s := wkserver.New("tcp://0.0.0.0:0")

	var wg sync.WaitGroup
	s.OnMessage(func(conn wknet.Conn, m *proto.Message) {
		wg.Done()
	})
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(s.Addr().String(), client.WithUID("uid"))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()

	msgCount := 100
	wg.Add(msgCount)

	for i := 0; i < msgCount; i++ {
		err = cli.SendNoFlush(&proto.Message{
			Id:      uint64(i),
			MsgType: 112349,
			Content: []byte("hello"),
		})
		assert.NoError(t, err)
	}
	cli.Flush()
	wg.Wait()
}

func TestRequestResp(t *testing.T) {

	wklog.Configure(&wklog.Options{
		Level: zap.DebugLevel,
	})
	s := wkserver.New("tcp://0.0.0.0:0")
	s.Route("/test", func(c *wkserver.Context) {
		c.Write([]byte("hello"))
	})
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(s.Addr().String(), client.WithUID("uid"))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()

	var count = 10

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
