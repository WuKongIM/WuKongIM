// Copyright (c) 2022 Shanghai Xinbida Network Technology Co., Ltd. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package wraft

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/WuKongIMGoSDK/pkg/wksdk"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type Transporter interface {
	Stop() error

	Send(m []raftpb.Message)
}

type GRPCTransporter struct {
	peers     map[uint64]*GRPCPeer
	peersLock sync.RWMutex
	wklog.Log
	cfg               *RaftNodeConfig
	transporterServer *transporter.Transporter
	stopped           bool
	reqIDGen          *idutil.Generator
	onMessage         func(id uint64, msg *wksdk.Message)
}

func NewGRPCTransporter(recvChan chan transporter.Ready, cfg *RaftNodeConfig, onMessage func(id uint64, msg *wksdk.Message)) *GRPCTransporter {
	g := &GRPCTransporter{
		peers:     make(map[uint64]*GRPCPeer),
		Log:       wklog.NewWKLog(fmt.Sprintf("GRPCTransporter[%d]", cfg.ID)),
		cfg:       cfg,
		reqIDGen:  idutil.NewGenerator(uint16(cfg.ID), time.Now()),
		onMessage: onMessage,
	}
	g.transporterServer = transporter.New(uint64(cfg.ID), cfg.Addr, recvChan)

	return g
}

func (t *GRPCTransporter) Start() error {
	t.stopped = false
	return t.transporterServer.Start()
}

func (t *GRPCTransporter) Stop() error {
	t.stopped = true
	t.transporterServer.Stop()

	for _, peer := range t.peers {
		_ = peer.Disconnect()
	}

	return nil
}

func (t *GRPCTransporter) Send(ms []raftpb.Message) {
	if len(ms) == 0 {
		return
	}
	if len(ms) == 1 {
		m := ms[0]
		if m.To == 0 {
			return
		}
		req := NewCMDReq(t.reqIDGen.Next(), CMDRaftMessage.Uint32())
		data, _ := m.Marshal()
		req.Param = data
		req.To = uint64(m.To)
		err := t.SendCMD(req)
		if err != nil {
			t.Warn("failed to send", zap.Error(err))
		}
	}
	reqs := make([]*CMDReq, 0)
	for _, m := range ms {
		if m.To == 0 {
			continue
		}
		req := NewCMDReq(t.reqIDGen.Next(), CMDRaftMessage.Uint32())
		data, _ := m.Marshal()
		req.Param = data
		req.To = uint64(m.To)
		reqs = append(reqs, req)
	}
	err := t.SendCMD(reqs...)
	if err != nil {
		t.Warn("failed to send", zap.Error(err))
	}
}

func (t *GRPCTransporter) SendCMD(reqs ...*CMDReq) error {
	if t.stopped {
		t.Warn("transporter stopped")
		return nil
	}
	if len(reqs) == 0 {
		return nil
	}
	if len(reqs) == 1 {
		req := reqs[0]
		to := req.To
		peer := t.GetPeer(to)
		if peer != nil {
			if !peer.requestConnect {
				err := peer.Connect()
				if err != nil {
					t.Warn("failed to connect", zap.Error(err))
				}
			}
			err := peer.SendCMD(req)
			if err != nil {
				t.Warn("failed to send", zap.Uint64("to", uint64(to)))
			}
			return err
		} else {
			t.Warn("peer not found", zap.Uint64("to", uint64(to)))
		}
		return nil
	}
	reqsMap := make(map[uint64][]*CMDReq)
	for _, req := range reqs {
		to := req.To
		reqsMap[to] = append(reqsMap[to], req)
	}
	fmt.Println("reqsMap--------->", reqsMap)
	for to, reqs := range reqsMap {
		peer := t.GetPeer(to)
		if peer != nil {
			if !peer.requestConnect {
				err := peer.Connect()
				if err != nil {
					t.Warn("failed to connect", zap.Error(err))
				}
			}
			err := peer.SendCMD(reqs...)
			if err != nil {
				t.Warn("failed to send", zap.Uint64("to", uint64(to)))
			}
		} else {
			t.Warn("peer not found", zap.Uint64("to", uint64(to)))
		}
	}
	return nil
}

func (t *GRPCTransporter) SendCMDTo(addr string, req *CMDReq) (*wksdk.Client, error) {
	cli := wksdk.NewClient(addr, wksdk.WithUID(wkutil.GenUUID()), wksdk.WithToken(t.cfg.Token))
	err := cli.Connect()
	if err != nil {
		return nil, err
	}

	data, _ := req.Marshal()
	err = cli.SendMessageAsync(data, wkproto.Channel{
		ChannelID:   "to",
		ChannelType: wkproto.ChannelTypePerson,
	})
	if err != nil {
		return nil, err
	}

	return cli, nil
}

func (t *GRPCTransporter) AddPeer(id uint64, addr string) error {
	t.peersLock.Lock()
	defer t.peersLock.Unlock()
	peer := t.peers[id]
	if peer != nil {
		return nil
	}
	peer = NewGRPCPeer(id, addr, t.cfg.Token, t.onMessage)
	t.Debug("add peer", zap.Uint64("id", uint64(id)))
	t.peers[id] = peer
	return nil
}

func (t *GRPCTransporter) RemovePeer(id uint64) error {
	t.peersLock.Lock()
	defer t.peersLock.Unlock()
	peer := t.peers[id]
	delete(t.peers, id)
	return peer.Disconnect()
}

func (t *GRPCTransporter) UpdatePeer(id uint64, addr string) error {
	t.peersLock.Lock()
	defer t.peersLock.Unlock()
	peer := t.peers[id]
	if peer == nil {
		return nil
	}
	peer = NewGRPCPeer(id, addr, t.cfg.Token, t.onMessage)
	t.peers[id] = peer
	return nil
}

func (t *GRPCTransporter) GetPeer(id uint64) *GRPCPeer {
	t.peersLock.RLock()
	defer t.peersLock.RUnlock()
	return t.peers[id]
}

type GRPCPeer struct {
	id             uint64
	addr           string
	nodeClient     *transporter.NodeClient
	token          string
	requestConnect bool
	wklog.Log

	connectLock sync.Mutex
}

func NewGRPCPeer(id uint64, addr string, token string, onRecv func(id uint64, msg *wksdk.Message)) *GRPCPeer {
	return &GRPCPeer{
		id:    id,
		addr:  addr,
		token: token,
		nodeClient: transporter.NewNodeClient(uint64(id), addr, token, func(msg *wksdk.Message) {
			onRecv(id, msg)
		}),
		requestConnect: false,
		Log:            wklog.NewWKLog(fmt.Sprintf("GRPCPeer[%d]", id)),
		connectLock:    sync.Mutex{},
	}
}

// func (g *GRPCPeer) Send(ms []raftpb.Message) error {
// 	if len(ms) == 0 {
// 		return nil
// 	}
// 	dataList := make([][]byte, 0)
// 	for _, m := range ms {
// 		g.Debug("send msg", zap.String("msg", m.String()))
// 		data, _ := m.Marshal()

// 		dataList = append(dataList, data)
// 	}
// 	return g.nodeClient.Send(dataList...)
// }

func (g *GRPCPeer) SendCMD(cmds ...*CMDReq) error {
	if len(cmds) == 0 {
		return nil
	}
	dataList := make([][]byte, 0)
	for _, cmd := range cmds {
		data, _ := cmd.Marshal()
		dataList = append(dataList, data)
	}
	return g.nodeClient.Send(dataList...)
}

func (g *GRPCPeer) Connect() error {
	g.connectLock.Lock()
	defer g.connectLock.Unlock()
	if g.requestConnect {
		g.Debug("already connected", zap.Uint64("id", uint64(g.id)))
		return nil
	}
	g.Debug("connecting...", zap.Uint64("id", uint64(g.id)))
	g.requestConnect = true
	return g.nodeClient.Connect()
}

func (g *GRPCPeer) Disconnect() error {
	g.Debug("disconnect...", zap.Uint64("id", uint64(g.id)))
	g.requestConnect = false
	return g.nodeClient.Disconnect()
}
