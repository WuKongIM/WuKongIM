package test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
)

func TestSendAndRecv(t *testing.T) {
	s := wkserver.New("tcp://0.0.0.0:0")

	var wg sync.WaitGroup
	s.OnMessage(func(conn wknet.Conn, m *proto.Message) {
		fmt.Println("id---->", m.Id)
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
	fmt.Println("flush-----")

	wg.Wait()
}
