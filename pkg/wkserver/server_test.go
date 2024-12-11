package wkserver_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/gnet/v2"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/raft/v3/raftpb"
)

func TestServerRoute(t *testing.T) {

	addr := "tcp://127.0.0.1:10000"
	s := wkserver.New(addr)
	s.Route("/test", func(c *wkserver.Context) {
		c.Write([]byte("test2"))
	})
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(addr, client.WithUid("uid"))
	err = cli.Start()
	assert.NoError(t, err)
	defer cli.Stop()

	time.Sleep(time.Millisecond * 200)

	resp, err := cli.Request("/test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("test2"), resp.Body)

	cli.Route("/hi", func(c *client.Context) {
		c.Write([]byte("world"))
	})

	resp, err = s.Request("uid", "/hi", []byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("world"), resp.Body)
}

func TestServerOnMessage(t *testing.T) {
	addr := "tcp://:10001"
	s := wkserver.New(addr)

	recvMsg := make(chan *proto.Message, 1)
	s.OnMessage(func(conn gnet.Conn, m *proto.Message) {
		recvMsg <- m
	})
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(addr, client.WithUid("uid"))
	err = cli.Start()
	assert.NoError(t, err)
	defer cli.Stop()

	time.Sleep(time.Millisecond * 200)

	rm := raftpb.Message{
		To:   1,
		From: 2,
		Type: raftpb.MsgApp,
		Entries: []raftpb.Entry{
			{
				Type: raftpb.EntryNormal,
				Data: []byte("hello"),
			},
		},
	}
	rmData, _ := rm.Marshal()
	err = cli.Send(&proto.Message{
		MsgType: 1,
		Content: rmData,
	})
	assert.NoError(t, err)

	m := <-recvMsg

	resultRM := raftpb.Message{}
	err = resultRM.Unmarshal(m.Content)
	assert.NoError(t, err)

	assert.Equal(t, rm.To, resultRM.To)
	assert.Equal(t, rm.From, resultRM.From)
	assert.Equal(t, rm.Type, resultRM.Type)
	assert.Equal(t, rm.Entries[0].Type, resultRM.Entries[0].Type)
	assert.Equal(t, rm.Entries[0].Data, resultRM.Entries[0].Data)
}

func TestReconnect(t *testing.T) {
	addr := "tcp://127.0.0.1:10000"
	s := wkserver.New(addr)
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(addr, client.WithUid("uid"))
	err = cli.Start()
	assert.NoError(t, err)
	defer cli.Stop()

	time.Sleep(time.Millisecond * 200)

	assert.Equal(t, true, cli.IsAuthed())

	cli.CloseConn()

	assert.Equal(t, false, cli.IsAuthed())

	time.Sleep(time.Millisecond * 200)

	assert.Equal(t, true, cli.IsAuthed())
}
