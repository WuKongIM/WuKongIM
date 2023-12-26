package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type node struct {
	id     uint64
	addr   string
	client *client.Client
}

func newNode(id uint64, uid string, addr string) *node {
	cli := client.New(addr, client.WithUID(uid))
	return &node{
		id:     id,
		addr:   addr,
		client: cli,
	}
}

func (n *node) start() {
	n.client.Start()
}

func (n *node) stop() {
	n.client.Close()
}

func (n *node) send(msg *proto.Message) error {
	return n.client.Send(msg)
}

func (n *node) sendPing(req *PingRequest) error {

	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypePing.Uint32(),
		Content: data,
	})
}

func (n *node) sendVote(req *VoteRequest) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypeVoteRequest.Uint32(),
		Content: data,
	})
}

func (n *node) sendVoteResp(req *VoteRespose) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypeVoteResponse.Uint32(),
		Content: data,
	})
}
