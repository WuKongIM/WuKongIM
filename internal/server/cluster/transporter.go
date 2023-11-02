package cluster

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/dragonboat/v4/config"
	"github.com/lni/dragonboat/v4/raftio"
	pb "github.com/lni/dragonboat/v4/raftpb"
)

type Transporter struct {
	server      *wkserver.Server
	currentPeer multiraft.Peer
	wklog.Log

	onMessage      func(conn wknet.Conn, msg *proto.Message)
	nodeManager    *NodeManager
	writeDelayTime time.Duration

	stopChan chan struct{}

	peerManager *PeerManager
}

func NewTransporter(peerID uint64, addr string, onMessage func(conn wknet.Conn, msg *proto.Message)) *Transporter {
	return &Transporter{
		currentPeer:    multiraft.NewPeer(peerID, addr),
		server:         wkserver.New(addr),
		Log:            wklog.NewWKLog(fmt.Sprintf("Transporter[%d]", peerID)),
		nodeManager:    GetNodeManager(),
		writeDelayTime: time.Millisecond * 50,
		stopChan:       make(chan struct{}),
		onMessage:      onMessage,
		peerManager:    GetPeerManager(),
	}
}

func (d *Transporter) Name() string {
	return "WuKongIMTransporter"
}

func (d *Transporter) Start() error {

	d.server.OnMessage(func(conn wknet.Conn, msg *proto.Message) {
		if d.onMessage != nil {
			d.onMessage(conn, msg)
		}
	})
	// go d.loopWrite()

	return d.server.Start()
}

func (d *Transporter) Close() error {
	close(d.stopChan)
	d.server.Stop()
	nodecliList := d.nodeManager.GetAll()
	if len(nodecliList) > 0 {
		for _, nodecli := range nodecliList {
			_ = d.RemovePeer(nodecli.NodeID)
		}
	}
	return nil
}

func (d *Transporter) GetConnection(ctx context.Context, target string) (raftio.IConnection, error) {
	peer := d.peerManager.GetPeerByServerAddr(target)
	if peer.ID == 0 {
		return nil, fmt.Errorf("peer is not found")
	}
	nodeClient := d.nodeManager.Get(peer.ID)
	if nodeClient == nil {
		nodeClient = d.createNodeClient(peer)
		d.nodeManager.Add(nodeClient)
	}
	if nodeClient.NeedConnect() {
		err := nodeClient.Connect()
		if err != nil {
			return nil, err
		}
	}
	return nodeClient, nil
}

func (d *Transporter) GetSnapshotConnection(ctx context.Context,
	target string) (raftio.ISnapshotConnection, error) {
	panic("GetSnapshotConnection not implemented")
}

// func (d *Transporter) loopWrite() {
// 	writeDelayTicker := time.NewTicker(d.writeDelayTime)
// 	for {
// 		select {
// 		case <-writeDelayTicker.C:

// 			d.nodeMessageCacheMapLock.Lock()
// 			for nodeID, messageQueue := range d.nodeMessageCacheMap {
// 				nodeClient := d.nodeManager.Get(nodeID)
// 				if nodeClient != nil {
// 					if nodeClient.NeedConnect() {
// 						err := nodeClient.Connect()
// 						if err != nil {
// 							d.Error("node client connect is error", zap.Error(err))
// 							continue
// 						}
// 					}

// 				}
// 				if nodeClient.IsConnected() {
// 					for messageQueue.Length() > 0 {
// 						messageObj := messageQueue.Remove()
// 						err := nodeClient.SendNoFlush(messageObj.(*proto.Message))
// 						if err != nil {
// 							d.Error("node client send message is error", zap.Error(err))
// 							continue
// 						}
// 					}
// 					go nodeClient.Flush()
// 				}
// 			}
// 			d.nodeMessageCacheMapLock.Unlock()
// 		case <-d.stopChan:
// 			return

// 		}
// 	}
// }

func (d *Transporter) AddPeer(peer Peer) error {
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
	nodecli.Close()
	d.nodeManager.Remove(id)
	return nil

}

func (d *Transporter) TCPAddr() net.Addr {
	return d.server.Addr()
}

func (c *Transporter) createNodeClient(peer Peer) *NodeClient {
	nodeClient := NewNodeClient(peer.ID, peer.ServerAddr)
	return nodeClient
}

type transportFactory struct {
	nodehostConfig config.NodeHostConfig
	messageHandler raftio.MessageHandler
	chunkHandler   raftio.ChunkHandler
}

func newTransportFactory() *transportFactory {
	return &transportFactory{}
}

func (t *transportFactory) Create(nodehostConfig config.NodeHostConfig,
	messageHandler raftio.MessageHandler, chunkHandler raftio.ChunkHandler) raftio.ITransport {
	t.nodehostConfig = nodehostConfig
	t.messageHandler = messageHandler
	t.chunkHandler = chunkHandler

	peerID, _ := strconv.ParseUint(nodehostConfig.NodeHostID, 10, 64)
	listenAddr := nodehostConfig.ListenAddress
	if !strings.HasPrefix(listenAddr, "tcp://") {
		listenAddr = "tcp://" + listenAddr
	}
	return NewTransporter(peerID, listenAddr, t.onMessage)
}

func (t *transportFactory) onMessage(conn wknet.Conn, msg *proto.Message) {
	switch msg.MsgType {
	case MessageTypeRaftMessage:
		req := pb.MessageBatch{}
		err := req.Unmarshal(msg.Content)
		if err != nil {
			return
		}
		if t.messageHandler != nil {
			t.messageHandler(req)
		}
	}
}

func (t *transportFactory) Validate(string) bool {
	return true
}
