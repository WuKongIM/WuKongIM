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

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/types"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type Transporter interface {
	Stop() error

	Send(m []raftpb.Message)
}

type GRPCTransporter struct {
	peers     map[types.ID]*GRPCPeer
	peersLock sync.RWMutex
	wklog.Log
	cfg               *RaftNodeConfig
	transporterServer *transporter.Transporter
	stopped           bool
}

func NewGRPCTransporter(recvChan chan []byte, cfg *RaftNodeConfig) *GRPCTransporter {
	g := &GRPCTransporter{
		peers: make(map[types.ID]*GRPCPeer),
		Log:   wklog.NewWKLog(fmt.Sprintf("GRPCTransporter[%s]", cfg.ID.String())),
		cfg:   cfg,
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
		peer.Disconnect()
	}

	return nil
}

func (t *GRPCTransporter) Send(ms []raftpb.Message) {
	if t.stopped {
		t.Warn("transporter stopped")
		return
	}
	var err error
	for _, m := range ms {
		if m.To == 0 {
			// ignore intentionally dropped message
			continue
		}
		if t.stopped {
			t.Warn("transporter stopped")
			return
		}

		to := types.ID(m.To)

		peer := t.GetPeer(to)
		if peer != nil {
			if !peer.requestConnect {
				err = peer.Connect()
				if err != nil {
					t.Warn("failed to connect", zap.Error(err))
				}
			}

			err = peer.Send(ms)
			if err != nil {
				t.Warn("failed to send", zap.Uint64("to", uint64(to)))
			}
		} else {
			t.Warn("peer not found", zap.Uint64("to", uint64(to)))
		}

	}
}

func (t *GRPCTransporter) AddPeer(id types.ID, addr string) error {
	t.peersLock.Lock()
	defer t.peersLock.Unlock()
	peer := NewGRPCPeer(id, addr, t.cfg.Token)
	t.peers[id] = peer
	return nil
}

func (t *GRPCTransporter) RemovePeer(id types.ID) error {
	t.peersLock.Lock()
	defer t.peersLock.Unlock()
	peer := t.peers[id]
	delete(t.peers, id)
	return peer.Disconnect()
}

func (t *GRPCTransporter) UpdatePeer(id types.ID, addr string) error {
	return nil
}

func (t *GRPCTransporter) GetPeer(id types.ID) *GRPCPeer {
	t.peersLock.RLock()
	defer t.peersLock.RUnlock()
	return t.peers[id]
}

type GRPCPeer struct {
	id             types.ID
	addr           string
	nodeClient     *transporter.NodeClient
	token          string
	requestConnect bool
	wklog.Log
}

func NewGRPCPeer(id types.ID, addr string, token string) *GRPCPeer {
	return &GRPCPeer{
		id:             id,
		addr:           addr,
		token:          token,
		nodeClient:     transporter.NewNodeClient(uint64(id), addr, token),
		requestConnect: false,
		Log:            wklog.NewWKLog(fmt.Sprintf("GRPCPeer[%s]", id.String())),
	}
}

func (g *GRPCPeer) Send(ms []raftpb.Message) error {
	if len(ms) == 0 {
		return nil
	}
	dataList := make([][]byte, 0)
	for _, m := range ms {
		g.Debug("send msg", zap.String("msg", m.String()))
		data, _ := m.Marshal()

		dataList = append(dataList, data)
	}
	return g.nodeClient.Send(dataList...)
}

func (g *GRPCPeer) Connect() error {
	g.requestConnect = true
	return g.nodeClient.Connect()
}

func (g *GRPCPeer) Disconnect() error {
	g.requestConnect = false
	return g.nodeClient.Disconnect()
}
