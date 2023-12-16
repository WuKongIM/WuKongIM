package raftgroup

import (
	"context"
	"sync"
	"testing"

	sproto "github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/raft/v3/raftpb"
)

func TestTransportSendAndRecv(t *testing.T) {

	connManager := NewConnectionManager()
	connManager.AddNode(101, "127.0.0.1:11000")
	connManager.AddNode(102, "127.0.0.1:12000")

	// ---------- transport1 ----------
	trans1 := NewTransport(101, "127.0.0.1:11000", connManager)
	err := trans1.Start()
	assert.NoError(t, err)
	defer trans1.Stop()

	// ---------- transport2 ----------
	trans2 := NewTransport(102, "127.0.0.1:12000", connManager)
	err = trans2.Start()
	assert.NoError(t, err)
	defer trans2.Stop()

	// transport2 on recv
	var wg sync.WaitGroup
	wg.Add(1)
	trans2.OnRecv(func(msg *sproto.Message) {
		var req = &RaftMessageReq{}
		err = req.Unmarshal(msg.Content)
		assert.NoError(t, err)

		data := req.Message.Entries[0].Data
		assert.Equal(t, "hello", string(data))
		wg.Done()
	})

	// transport1 send
	err = trans1.Send(context.Background(), &RaftMessageReq{
		ShardID: 1,
		Message: raftpb.Message{
			Type: raftpb.MsgApp,
			To:   102,
			Entries: []raftpb.Entry{
				{
					Type: raftpb.EntryConfChange,
					Data: []byte("hello"),
				},
			},
		},
	})
	assert.NoError(t, err)
	wg.Wait()
}
