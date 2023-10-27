package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.etcd.io/raft/v3/raftpb"
)

type NodeClient struct {
	NodeID uint64
	cli    *client.Client
}

func NewNodeClient(nodeID uint64, addr string) *NodeClient {

	cli := client.New(strings.ReplaceAll(addr, "tcp://", ""), client.WithUID(fmt.Sprintf("%d", nodeID)))
	return &NodeClient{
		NodeID: nodeID,
		cli:    cli,
	}
}

func (n *NodeClient) Request(path string, data []byte) (*proto.Response, error) {
	if n.cli.ConnectStatus() != client.CONNECTED {
		err := n.cli.Connect()
		if err != nil {
			return nil, err
		}
	}
	return n.cli.Request(path, data)
}

func (n *NodeClient) RequestWithContext(ctx context.Context, path string, data []byte) (*proto.Response, error) {
	if n.cli.ConnectStatus() != client.CONNECTED {
		err := n.cli.Connect()
		if err != nil {
			return nil, err
		}
	}
	return n.cli.RequestWithContext(ctx, path, data)
}

func (n *NodeClient) Connect() error {
	return n.cli.Connect()
}

func (n *NodeClient) Close() error {

	return n.cli.Close()
}

func (n *NodeClient) NeedConnect() bool {
	return n.cli.ConnectStatus() == client.DISCONNECTED || n.cli.ConnectStatus() == client.CLOSED
}

func (n *NodeClient) SendSlotRaftMessage(ctx context.Context, req *multiraft.RaftMessageReq) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	err = n.cli.Send(&proto.Message{
		MsgType: MessageTypeRaftMessageReq,
		Content: data,
	})
	if err != nil {
		return err
	}
	return nil
}

func (n *NodeClient) SendNodeRaftMessage(ctx context.Context, m raftpb.Message) error {
	data, err := m.Marshal()
	if err != nil {
		return err
	}
	err = n.cli.Send(&proto.Message{
		MsgType: MessageTypeRaftMessage,
		Content: data,
	})
	if err != nil {
		return err
	}
	return nil
}

func (n *NodeClient) SendNoFlush(m *proto.Message) error {

	return n.cli.SendNoFlush(m)
}

func (n *NodeClient) Flush() {
	n.cli.Flush()
}
