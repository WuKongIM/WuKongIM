package multiraft

import (
	"context"
	"fmt"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

type Transporter interface {
	Send(ctx context.Context, m *RaftMessageReq) error

	AddPeer(peer Peer) error
	RemovePeer(id uint64) error

	OnRaftMessage(fnc func(m *RaftMessageReq))

	Start() error
	Stop()
}

type RaftMessageReq struct {
	ReplicaID uint32
	Message   raftpb.Message
	innerReq  pb.RaftMessageReq
}

func (r *RaftMessageReq) Marshal() ([]byte, error) {
	r.innerReq.ReplicaID = r.ReplicaID
	messageData, err := r.Message.Marshal()
	if err != nil {
		return nil, err
	}
	r.innerReq.Message = messageData

	return proto.Marshal(&r.innerReq)
}

func (r *RaftMessageReq) Unmarshal(data []byte) error {
	err := proto.Unmarshal(data, &r.innerReq)
	if err != nil {
		return err
	}
	r.ReplicaID = r.innerReq.ReplicaID
	err = r.Message.Unmarshal(r.innerReq.Message)
	if err != nil {
		return err
	}
	return nil
}

type DefaultTransporter struct {
	server            *wkserver.Server
	currentPeer       Peer
	peerClientManager *PeerClientManager
	wklog.Log

	onRaftMessage func(m *RaftMessageReq)
}

func NewDefaultTransporter(peerID uint64, addr string) *DefaultTransporter {
	return &DefaultTransporter{
		currentPeer:       NewPeer(peerID, addr),
		server:            wkserver.New(addr),
		peerClientManager: NewPeerClientManager(),
		Log:               wklog.NewWKLog(fmt.Sprintf("DefaultTransporter[%d]", peerID)),
	}
}

func (d *DefaultTransporter) Start() error {
	d.server.Route("/raftmessage", func(c *wkserver.Context) {
		req := &RaftMessageReq{}
		err := req.Unmarshal(c.Body())
		if err != nil {
			c.WriteErr(err)
			return
		}
		if d.onRaftMessage != nil {
			d.onRaftMessage(req)
		}
		c.WriteOk()
	})
	return d.server.Start()
}

func (d *DefaultTransporter) Stop() {
	d.server.Stop()
}

func (d *DefaultTransporter) Send(ctx context.Context, m *RaftMessageReq) error {
	if d.currentPeer.ID == m.Message.To {
		return nil
	}
	peerClient := d.peerClientManager.GetPeerClient(m.Message.To)
	if peerClient == nil {
		d.Warn("peer client not found", zap.Uint64("peerID", m.Message.To))
		return nil
	}
	if peerClient.NeedConnect() {
		err := peerClient.Connect()
		if err != nil {
			return err
		}
	}
	err := peerClient.SendRaftMessage(m)
	return err
}

func (d *DefaultTransporter) AddPeer(peer Peer) error {
	if peer.ID == d.currentPeer.ID {
		return nil
	}
	peercli := d.peerClientManager.GetPeerClient(peer.ID)
	if peercli != nil {
		return nil
	}
	var err error
	peercli = d.createPeerClient(peer)
	if err != nil {
		return err
	}
	d.peerClientManager.AddPeerClient(peer.ID, peercli)
	return nil
}

func (d *DefaultTransporter) RemovePeer(id uint64) error {
	peercli := d.peerClientManager.GetPeerClient(id)
	if peercli == nil {
		return nil
	}
	err := peercli.Close()
	if err != nil {
		return err
	}
	d.peerClientManager.RemovePeerClient(id)
	return nil

}

func (d *DefaultTransporter) TCPAddr() net.Addr {
	return d.server.Addr()
}

func (d *DefaultTransporter) OnRaftMessage(fnc func(m *RaftMessageReq)) {
	d.onRaftMessage = fnc
}

func (d *DefaultTransporter) createPeerClient(peer Peer) *PeerClient {
	peerClient := NewPeerClient(peer)

	return peerClient
}
