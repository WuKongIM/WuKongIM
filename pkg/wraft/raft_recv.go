package wraft

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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
				req  = &CMDReq{}
				err  error
				resp *CMDResp
			)
			err = req.Unmarshal(ready.Data)
			if err != nil {
				r.Warn("failed to unmarshal raft message", zap.Error(err))
				return
			}
			r.Debug("=========recv=======>", zap.Uint64("type", uint64(req.Type)))

			if req.Type == CMDRaftMessage.Uint32() { // raft message
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

			} else if req.Type == CMDGetClusterConfig.Uint32() { // get cluster config
				resp, err = r.handleGetClusterConfig(req)
				if err != nil {
					r.Error("failed to handle get cluster config", zap.Error(err))
				}

			} else if req.Type == CMDJoinCluster.Uint32() { // join cluster
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
				resp = NewCMDRespWithStatus(req.Id, CMDRespStatusError)
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

// recv message from client node
func (r *RaftNode) onNodeMessage(id uint64, msg *wksdk.Message) {
	cmdResp := &CMDResp{}
	err := cmdResp.Unmarshal(msg.Payload)
	if err != nil {
		r.Error("failed to unmarshal cmd resp", zap.Error(err))
		return
	}
	fmt.Println("cmdResp---->", cmdResp.Id)
	r.trigger(cmdResp)
}

func (r *RaftNode) handleGetClusterConfig(req *CMDReq) (*CMDResp, error) {
	fmt.Println("handleGetClusterConfig---->")
	clusterConfig := r.ClusterConfigManager.GetClusterConfig()
	data, err := clusterConfig.Marshal()
	if err != nil {
		return nil, err
	}
	resp := &CMDResp{
		Id:    req.Id,
		Param: data,
	}
	return resp, nil
}

func (r *RaftNode) handleClusterJoin(req *CMDReq) (*CMDResp, error) {
	fmt.Println("handleClusterJoin---->")
	peer := &wpb.Peer{}
	err := peer.Unmarshal(req.Param)
	if err != nil {
		return nil, err
	}
	err = r.AddTransportPeer(peer.Id, peer.Addr)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	return &CMDResp{
		Id:     req.Id,
		Status: CMDRespStatusOK,
	}, nil
}
