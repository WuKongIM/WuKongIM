package wraft

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
	"github.com/WuKongIM/WuKongIMGoSDK/pkg/wksdk"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

func (r *RaftNode) loopRecv() {

	defer r.Debug("loopRecv stop")
	for {
		select {
		case ready, ok := <-r.recvChan:
			if !ok {
				return
			}
			var (
				req  = ready.Req
				err  error
				resp *transporter.CMDResp
			)
			r.Debug("=========recv=======>", zap.Uint64("type", uint64(req.Type)))

			if req.Type == transporter.CMDRaftMessage.Uint32() { // raft message
				close(ready.Result)

				var msg raftpb.Message
				err = msg.Unmarshal(req.Param)
				if err != nil {
					r.Warn("failed to unmarshal raft message", zap.Error(err))
					return
				}

				r.Debug("=========step=======>", zap.String("msg", msg.String()))
				err = r.node.Step(context.Background(), msg)
				if err != nil {
					r.Warn("failed to step raft node", zap.Error(err))
				}

			} else if req.Type == transporter.CMDGetClusterConfig.Uint32() { // get cluster config
				resp, err = r.handleGetClusterConfig(req)
				if err != nil {
					r.Error("failed to handle get cluster config", zap.Error(err))
				}

			} else if req.Type == transporter.CMDJoinCluster.Uint32() { // join cluster
				resp, err = r.handleClusterJoin(req)
				if err != nil {
					r.Error("failed to handle cluster join", zap.Error(err))
				}
			} else {
				close(ready.Result)
				return
			}
			if err != nil {
				r.Error("failed to handle cmd", zap.Error(err))
				resp = transporter.NewCMDRespWithStatus(req.Id, transporter.CMDRespStatusError)
			}
			if resp != nil {
				data, err := resp.Marshal()
				if err != nil {
					r.Error("failed to marshal cmd resp", zap.Error(err))
					return
				}
				ready.Result <- data
			}

		case <-r.stopped:
			fmt.Println("loopRecv done")
			return
		}
	}

}

// func (r *RaftNode) requestGetClusterConfigIfNeed(peerID uint64, term uint64) {
// 	peer := r.ClusterConfigManager.GetPeer(peerID)
// 	if peer != nil && peer.Term < term {
// 		r.requestGetClusterConfig(peerID)
// 	}
// }

func (r *RaftNode) requestGetClusterConfig(peerID uint64) {
	if r.startRequestGetClusterConfig.Load() {
		return
	}
	r.startRequestGetClusterConfig.Store(true)
	defer r.startRequestGetClusterConfig.Store(false)

	clusterConfig, err := r.GetClusterConfig(peerID)
	if err != nil {
		r.Error("failed to get cluster config", zap.Error(err), zap.Uint64("peerID", peerID))
		return
	}
	if clusterConfig != nil && len(clusterConfig.Peers) > 0 {
		for _, peer := range clusterConfig.Peers {
			if peer.Id == peerID {
				r.ClusterConfigManager.AddOrUpdatePeer(peer)
				break
			}

		}
	}

}

func (r *RaftNode) listenClusterConfigChange() {
	defer r.Debug("listenClusterConfigChange stop")
	for {
		select {
		case ready := <-r.clusterFSMManager.ReadyChan():
			r.Debug("cluster config ready")
			fmt.Println(ready)
			if ready.State == ClusterStatePeerStatusChange {
				r.applyPeerStateChange(ready.Peer)
			} else if ready.State == ClusterStatePeerConfigUpdate {
				r.applyPeerConfigUpdate(ready.Peer)
			}

		case <-r.stopped:
			return
		}
	}
}

func (r *RaftNode) applyPeerStateChange(peer *wpb.Peer) {
	if peer == nil || peer.Id == 0 {
		return
	}
	if peer.Status == wpb.Status_WillJoin {
		r.applyPeerWillJoin(peer)
		return
	}
}

func (r *RaftNode) applyPeerConfigUpdate(peer *wpb.Peer) {
	if peer == nil || peer.Id == 0 {
		return
	}
	r.requestGetClusterConfig(peer.Id)
}

func (r *RaftNode) applyPeerWillJoin(peer *wpb.Peer) {
	if peer == nil || peer.Id == 0 {
		return
	}
	err := r.AddTransportPeer(peer.Id, peer.Addr)
	if err != nil {
		r.Error("failed to add transport peer", zap.Error(err))
		return
	}
	cc := raftpb.ConfChange{
		ID:     r.reqIDGen.Next(),
		NodeID: uint64(peer.Id),
		Type:   raftpb.ConfChangeAddNode,
		Context: []byte(wkutil.ToJSON(map[string]interface{}{
			"addr": peer.Addr,
		})),
	}
	err = r.proposeConfChange(cc)
	if err != nil {
		r.Error("failed to propose conf change", zap.Error(err))
		return
	}
}

// recv message from client node
func (r *RaftNode) onNodeMessage(id uint64, msg *wksdk.Message) {
	cmdResp := &transporter.CMDResp{}
	err := cmdResp.Unmarshal(msg.Payload)
	if err != nil {
		r.Error("failed to unmarshal cmd resp", zap.Error(err))
		return
	}
	fmt.Println("cmdResp---->", cmdResp.Id)
	r.trigger(cmdResp)
}

func (r *RaftNode) handleGetClusterConfig(req *transporter.CMDReq) (*transporter.CMDResp, error) {
	fmt.Println("handleGetClusterConfig---->")
	clusterConfig := r.ClusterConfigManager.GetClusterConfig()
	data, err := clusterConfig.Marshal()
	if err != nil {
		return nil, err
	}
	resp := &transporter.CMDResp{
		Id:    req.Id,
		Param: data,
	}
	return resp, nil
}

func (r *RaftNode) handleClusterJoin(req *transporter.CMDReq) (*transporter.CMDResp, error) {
	fmt.Println("handleClusterJoin---->", req.Id, req.Param)
	peer := &wpb.Peer{}
	err := peer.Unmarshal(req.Param)
	if err != nil {
		return nil, err
	}
	fmt.Println("peer---->", peer.String())
	err = r.AddTransportPeer(peer.Id, peer.Addr)
	if err != nil {
		return nil, err
	}
	existPeer := r.ClusterConfigManager.GetPeer(peer.Id)
	if existPeer == nil {
		peer.Status = wpb.Status_WillJoin
		r.ClusterConfigManager.AddOrUpdatePeer(peer)
	}
	return &transporter.CMDResp{
		Id:     req.Id,
		Status: transporter.CMDRespStatusOK,
	}, nil
}
