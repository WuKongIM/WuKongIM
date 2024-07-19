package wkserver_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/raft/v3/raftpb"
)

func TestServerRoute(t *testing.T) {
	s := wkserver.New("tcp://0.0.0.0:0")
	s.Route("/test", func(c *wkserver.Context) {
		c.Write([]byte("test2"))
	})

	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(s.Addr().String(), client.WithUID("uid"))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()

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
	s := wkserver.New("tcp://0.0.0.0:0")

	recvMsg := make(chan *proto.Message, 1)
	s.OnMessage(func(conn wknet.Conn, m *proto.Message) {
		recvMsg <- m
	})
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(s.Addr().String(), client.WithUID("uid"))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()

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
