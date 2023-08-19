package wraft

import (
	"context"
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

func (r *RaftNode) JoinTo(id uint64) error {
	peer := &wpb.Peer{
		Id:   uint64(r.cfg.ID),
		Addr: r.cfg.Addr,
	}
	data, _ := peer.Marshal()
	req := &CMDReq{
		Id:    r.reqIDGen.Next(),
		Type:  CMDJoinCluster.Uint32(),
		Param: data,
		To:    id,
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
