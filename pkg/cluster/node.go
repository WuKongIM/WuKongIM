package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type node struct {
	id        uint64
	addr      string
	client    *client.Client
	allowVote bool // 是否是允许投票的节点
	online    bool // 是否在线
}

func newNode(id uint64, uid string, addr string) *node {
	cli := client.New(addr, client.WithUID(uid))
	return &node{
		id:     id,
		addr:   addr,
		client: cli,
	}
}

func (n *node) start() {
	n.client.Start()
}

func (n *node) stop() {
	n.client.Close()
}

func (n *node) send(msg *proto.Message) error {
	return n.client.Send(msg)
}

func (n *node) sendPing(req *PingRequest) error {

	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypePing.Uint32(),
		Content: data,
	})
}

func (n *node) sendVote(req *VoteRequest) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypeVoteRequest.Uint32(),
		Content: data,
	})
}

func (n *node) sendVoteResp(req *VoteResponse) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypeVoteResponse.Uint32(),
		Content: data,
	})
}

func (n *node) sendPong(req *PongResponse) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypePong.Uint32(),
		Content: data,
	})
}

// 请求集群配置
func (n *node) requestClusterConfig(ctx context.Context) (*pb.Cluster, error) {
	resp, err := n.client.RequestWithContext(ctx, "/syncClusterConfig", nil)
	if err != nil {
		return nil, err
	}
	clusterCfg := &pb.Cluster{}
	err = clusterCfg.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return clusterCfg, nil
}

func (n *node) requestSlotInfo(ctx context.Context, req *SlotInfoReportRequest) (*SlotInfoReportResponse, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/infos", data)
	if err != nil {
		return nil, err
	}

	slotInfoResponse := &SlotInfoReportResponse{}
	err = slotInfoResponse.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return slotInfoResponse, nil
}

func (n *node) sendSlotSyncNotify(req *replica.SyncNotify) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypeSlotLogSyncNotify.Uint32(),
		Content: data,
	})
}

func (n *node) sendChannelSyncNotify(req *replica.SyncNotify) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypeChannelLogSyncNotify.Uint32(),
		Content: data,
	})
}

func (n *node) requestSlotSyncLog(ctx context.Context, r *replica.SyncReq) (*replica.SyncRsp, error) {
	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/syncLog", data)
	if err != nil {
		return nil, err
	}
	syncResp := &replica.SyncRsp{}
	err = syncResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return syncResp, nil

}

func (n *node) requestChannelSyncLog(ctx context.Context, r *replica.SyncReq) (*replica.SyncRsp, error) {
	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/syncLog", data)
	if err != nil {
		return nil, err
	}
	syncResp := &replica.SyncRsp{}
	err = syncResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return syncResp, nil

}

func (n *node) requestSlotPropse(ctx context.Context, req *ProposeRequest) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	_, err = n.client.RequestWithContext(ctx, "/slot/propose", data)
	if err != nil {
		return err
	}
	return nil

}
