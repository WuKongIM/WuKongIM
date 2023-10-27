package cluster

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/eapache/queue"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type Transporter struct {
	server      *wkserver.Server
	currentPeer multiraft.Peer
	wklog.Log

	onSlotRaftMessage func(m *multiraft.RaftMessageReq)
	onNodeRaftMessage func(m raftpb.Message)
	nodeManager       *NodeManager
	writeDelayTime    time.Duration

	nodeMessageCacheMap     map[uint64]*queue.Queue
	nodeMessageCacheMapLock sync.RWMutex

	stopChan chan struct{}
}

func NewTransporter(peerID uint64, addr string) *Transporter {
	return &Transporter{
		currentPeer:         multiraft.NewPeer(peerID, addr),
		server:              wkserver.New(addr),
		Log:                 wklog.NewWKLog(fmt.Sprintf("Transporter[%d]", peerID)),
		nodeManager:         NewNodeManager(),
		writeDelayTime:      time.Millisecond * 10,
		nodeMessageCacheMap: make(map[uint64]*queue.Queue),
		stopChan:            make(chan struct{}),
	}
}

func (d *Transporter) Start() error {

	d.server.OnMessage(func(conn wknet.Conn, msg *proto.Message) {
		if msg.MsgType == MessageTypeRaftMessage {
			m := raftpb.Message{}
			err := m.Unmarshal(msg.Content)
			if err != nil {
				d.Error("unmarshal raft message is error", zap.Error(err), zap.ByteString("content", msg.Content))
				return
			}
			if d.onNodeRaftMessage != nil {
				go d.onNodeRaftMessage(m)
			}
		} else if msg.MsgType == MessageTypeRaftMessageReq {
			req := &multiraft.RaftMessageReq{}
			err := req.Unmarshal(msg.Content)
			if err != nil {
				d.Error("unmarshal raft message req is error", zap.Error(err))
				return
			}
			if d.onSlotRaftMessage != nil {
				go d.onSlotRaftMessage(req)
			}
		}
	})
	go d.loopWrite()

	return d.server.Start()
}

func (d *Transporter) Stop() {
	close(d.stopChan)
	d.server.Stop()
}

func (d *Transporter) loopWrite() {
	writeDelayTicker := time.NewTicker(d.writeDelayTime)
	for {
		select {
		case <-writeDelayTicker.C:

			// d.nodeMessageCacheMapLock.Lock()
			for nodeID, messageQueue := range d.nodeMessageCacheMap {
				nodeClient := d.nodeManager.Get(nodeID)
				if nodeClient != nil {
					if nodeClient.NeedConnect() {
						err := nodeClient.Connect()
						if err != nil {
							d.Error("node client connect is error", zap.Error(err))
							continue
						}
					}

				}
				for messageQueue.Length() > 0 {
					messageObj := messageQueue.Remove()
					err := nodeClient.SendNoFlush(messageObj.(*proto.Message))
					if err != nil {
						d.Error("node client send message is error", zap.Error(err))
						continue
					}
				}
				go nodeClient.Flush()
			}
			// d.nodeMessageCacheMapLock.Unlock()
		case <-d.stopChan:
			return

		}
	}
}

func (d *Transporter) Send(ctx context.Context, m *multiraft.RaftMessageReq) error {
	if d.currentPeer.ID == m.Message.To {
		return nil
	}
	data, err := m.Marshal()
	if err != nil {
		return err
	}
	raftpMsg := &proto.Message{
		MsgType: MessageTypeRaftMessageReq,
		Content: data,
	}
	d.nodeMessageCacheMapLock.Lock()
	messageQueue := d.nodeMessageCacheMap[m.Message.To]
	if messageQueue == nil {
		messageQueue = queue.New()
	}
	messageQueue.Add(raftpMsg)
	d.nodeMessageCacheMap[m.Message.To] = messageQueue
	d.nodeMessageCacheMapLock.Unlock()
	return nil
}

func (d *Transporter) SendToNode(ctx context.Context, m raftpb.Message) error {
	if d.currentPeer.ID == m.To {
		return nil
	}

	data, err := m.Marshal()
	if err != nil {
		return err
	}
	raftMsg := &proto.Message{
		MsgType: MessageTypeRaftMessage,
		Content: data,
	}
	d.nodeMessageCacheMapLock.Lock()
	messageQueue := d.nodeMessageCacheMap[m.To]
	if messageQueue == nil {
		messageQueue = queue.New()
	}
	messageQueue.Add(raftMsg)
	d.nodeMessageCacheMap[m.To] = messageQueue
	d.nodeMessageCacheMapLock.Unlock()
	return nil
}

func (d *Transporter) AddPeer(peer multiraft.Peer) error {
	if peer.ID == d.currentPeer.ID {
		return nil
	}
	nodecli := d.nodeManager.Get(peer.ID)
	if nodecli != nil {
		return nil
	}
	var err error
	nodecli = d.createNodeClient(peer)
	if err != nil {
		return err
	}
	d.nodeManager.Add(nodecli)
	return nil
}

func (d *Transporter) RemovePeer(id uint64) error {
	nodecli := d.nodeManager.Get(id)
	if nodecli == nil {
		return nil
	}
	err := nodecli.Close()
	if err != nil {
		return err
	}
	d.nodeManager.Remove(id)
	return nil

}

func (d *Transporter) TCPAddr() net.Addr {
	return d.server.Addr()
}

func (d *Transporter) OnRaftMessage(fnc func(m *multiraft.RaftMessageReq)) {
	d.onSlotRaftMessage = fnc
}

func (d *Transporter) OnNodeRaftMessage(fnc func(m raftpb.Message)) {
	d.onNodeRaftMessage = fnc
}

func (c *Transporter) createNodeClient(peer multiraft.Peer) *NodeClient {
	nodeClient := NewNodeClient(peer.ID, peer.Addr)
	return nodeClient
}
