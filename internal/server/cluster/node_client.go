package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	pb "github.com/lni/dragonboat/v4/raftpb"
	"go.uber.org/atomic"
)

type NodeClient struct {
	NodeID     uint64
	cli        *client.Client
	connecting atomic.Bool
	connected  atomic.Bool

	msgChan chan *proto.Message

	stopChan chan struct{}
}

func NewNodeClient(nodeID uint64, addr string) *NodeClient {

	cli := client.New(strings.ReplaceAll(addr, "tcp://", ""), client.WithUID(fmt.Sprintf("%d", nodeID)))
	return &NodeClient{
		NodeID:   nodeID,
		cli:      cli,
		msgChan:  make(chan *proto.Message, 10000),
		stopChan: make(chan struct{}),
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
	if n.connecting.Load() {
		return nil
	}
	n.connecting.Store(true)
	err := n.cli.Connect()
	if err != nil {
		n.connecting.Store(false)
		return err
	}
	n.connected.Store(true)
	n.connecting.Store(false)

	go n.loopMsgs()
	return nil
}

func (n *NodeClient) loopMsgs() {
	batch := make([]*proto.Message, 0, 100)
	t := time.NewTicker(time.Millisecond * 100)
	for {
		select {
		case msg := <-n.msgChan:
			batch = append(batch, msg)
			for {
				select {
				case msg = <-n.msgChan:
					batch = append(batch, msg)
				default:
					goto send
				}
			}
		send:
			if len(batch) > 0 {
				for _, msg := range batch {
					err := n.cli.SendNoFlush(msg)
					if err != nil {
						continue
					}
				}
				n.cli.Flush()
				batch = batch[:0]
			}
		case <-t.C:
			if len(batch) > 0 {
				for _, msg := range batch {
					err := n.cli.SendNoFlush(msg)
					if err != nil {
						continue
					}
				}
				n.cli.Flush()
				batch = batch[:0]
			}
		case <-n.stopChan:
			return
		}
	}
}

func (n *NodeClient) Close() {
	// 不需要关闭，这里的close主要为了实现dragonboat的接口
}

func (n *NodeClient) Stop() {
	n.connected.Store(false)
	n.connecting.Store(false)
	close(n.stopChan)
	err := n.cli.Close()
	if err != nil {
		return
	}
}

func (n *NodeClient) IsConnecting() bool {
	return n.connecting.Load()
}

func (n *NodeClient) IsConnected() bool {
	return n.connected.Load()
}

func (n *NodeClient) NeedConnect() bool {
	return n.cli.ConnectStatus() == client.DISCONNECTED || n.cli.ConnectStatus() == client.CLOSED || n.cli.ConnectStatus() == client.UNKNOWN
}

func (n *NodeClient) SendMessageBatch(batch pb.MessageBatch) error {
	data, err := batch.Marshal()
	if err != nil {
		return err
	}
	msg := &proto.Message{
		MsgType: MessageTypeRaftMessage,
		Content: data,
	}
	select {
	case n.msgChan <- msg:
	default:
		return fmt.Errorf("msg chan is full")
	}
	return nil
}

func (n *NodeClient) SendNoFlush(m *proto.Message) error {

	return n.cli.SendNoFlush(m)
}

func (n *NodeClient) Flush() {
	n.cli.Flush()
}
