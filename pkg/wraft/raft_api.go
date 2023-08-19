package wraft

import (
	"context"
	"fmt"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
	"go.etcd.io/raft/v3/raftpb"
)

func (r *RaftNode) Tick() {
	r.tickMu.Lock()
	r.node.Tick()
	r.tickMu.Unlock()
}

func (r *RaftNode) Start() error {

	return r.start()
}

func (r *RaftNode) Stop() {
	r.stop()
}

func (r *RaftNode) Lead() uint64 {
	return r.leadID.Load()
}

func (r *RaftNode) Propose(ctx context.Context, cmd *CMDReq) (*CMDResp, error) {
	return r.propose(ctx, cmd)
}

// func (r *RaftNode) AddConfChange(cc raftpb.ConfChange) error {
// 	return r.addConfChange(cc)
// }

func (r *RaftNode) ProposeConfChange(ctx context.Context, peer *wpb.Peer) error {
	return r.proposePeer(ctx, peer)
}

func (r *RaftNode) ApplyConfChange(cc raftpb.ConfChange) *raftpb.ConfState {

	return r.applyConfChange(cc)
}

func (r *RaftNode) AppliedTo(applied uint64) error {
	return r.appliedTo(applied)
}

func (r *RaftNode) Addr() net.Addr {
	return r.grpcTransporter.transporterServer.Addr()
}

func (r *RaftNode) SendCMD(req *CMDReq) (*CMDResp, error) {
	return r.sendCMD(req)
}

// GetClusterConfig 从某个节点地址获取分布式配置
func (r *RaftNode) GetClusterConfigFrom(addr string) (*wpb.ClusterConfig, error) {
	req := &CMDReq{
		Id:   r.reqIDGen.Next(),
		Type: CMDGetClusterConfig.Uint32(),
	}
	resp, err := r.sendCMDTo(addr, req)
	if err != nil {
		return nil, err
	}
	clusterConfig := &wpb.ClusterConfig{}
	err = clusterConfig.Unmarshal(resp.Param)
	return clusterConfig, err
}

func (r *RaftNode) AddTransportPeer(id uint64, addr string) error {

	return r.grpcTransporter.AddPeer(id, addr)
}

func (r *RaftNode) JoinTo(peer *wpb.Peer) error {

	if r.cfg.ID == peer.Id {
		return nil
	}

	// add peer to transporter
	err := r.AddTransportPeer(peer.Id, peer.Addr)
	if err != nil {
		return err
	}
	selfPeer := wpb.NewPeer(r.cfg.ID, r.cfg.Addr)

	// add to peer to cluster config
	existPeer := r.ClusterConfigManager.GetPeer(peer.Id)
	if existPeer == nil {
		peer.Status = wpb.Status_WillJoin
		r.ClusterConfigManager.AddOrUpdatePeer(peer)
	} else if existPeer.Status == wpb.Status_Error {
		existPeer.Status = wpb.Status_WillJoin
		r.ClusterConfigManager.AddOrUpdatePeer(existPeer)
	}

	fmt.Println("join-->", peer.String())

	data, _ := selfPeer.Marshal()
	req := &CMDReq{
		Id:    r.reqIDGen.Next(),
		Type:  CMDJoinCluster.Uint32(),
		Param: data,
		To:    peer.Id,
	}
	resp, err := r.sendCMD(req)
	if err != nil {
		return err
	}
	if resp.Status != CMDRespStatusOK {
		return ErrCMDRespStatus
	}
	return nil
}
