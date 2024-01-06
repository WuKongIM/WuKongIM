package cluster

import (
	"context"
	"fmt"

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
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestClusterConfig is failed, status:%d", resp.Status)
	}
	clusterCfg := &pb.Cluster{}
	err = clusterCfg.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return clusterCfg, nil
}

func (n *node) requestSlotLogInfo(ctx context.Context, req *SlotLogInfoReportRequest) (*SlotLogInfoReportResponse, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/loginfo", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestSlotLogInfo is failed, status:%d", resp.Status)
	}

	slotInfoResponse := &SlotLogInfoReportResponse{}
	err = slotInfoResponse.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return slotInfoResponse, nil
}

func (n *node) requestChannelLogInfo(ctx context.Context, req *ChannelLogInfoReportRequest) (*ChannelLogInfoReportResponse, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/loginfo", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestChannelLogInfo is failed, status:%d", resp.Status)
	}
	channelLogInfoResponse := &ChannelLogInfoReportResponse{}
	err = channelLogInfoResponse.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return channelLogInfoResponse, nil

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

func (n *node) sendChannelMetaLogSyncNotify(req *replica.SyncNotify) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypeChannelMetaLogSyncNotify.Uint32(),
		Content: data,
	})
}

func (n *node) sendChannelMessageLogSyncNotify(req *replica.SyncNotify) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	return n.client.Send(&proto.Message{
		MsgType: MessageTypeChannelMessageLogSyncNotify.Uint32(),
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
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestSlotSyncLog is failed, status:%d", resp.Status)
	}
	syncResp := &replica.SyncRsp{}
	err = syncResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return syncResp, nil

}

func (n *node) requestChannelMetaSyncLog(ctx context.Context, r *replica.SyncReq) (*replica.SyncRsp, error) {
	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/meta/syncLog", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestChannelMetaSyncLog is failed, status:%d", resp.Status)
	}
	syncResp := &replica.SyncRsp{}
	err = syncResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return syncResp, nil

}

func (n *node) requestChannelMessageSyncLog(ctx context.Context, r *replica.SyncReq) (*replica.SyncRsp, error) {
	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/message/syncLog", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestChannelMessageSyncLog is failed, status:%d", resp.Status)
	}

	syncResp := &replica.SyncRsp{}
	err = syncResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return syncResp, nil

}

func (n *node) requestSlotPropse(ctx context.Context, req *SlotProposeRequest) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/propose", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("requestSlotPropse is failed, status:%d", resp.Status)
	}
	return nil

}

func (n *node) requestChannelMetaPropse(ctx context.Context, req *ChannelProposeRequest) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/meta/propose", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("requestChannelMetaPropse is failed, status:%d", resp.Status)
	}
	return nil

}

func (n *node) requestChannelMessagePropse(ctx context.Context, req *ChannelProposeRequest) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/message/propose", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("requestChannelMessagePropse is failed, status:%d", resp.Status)
	}
	return nil

}

func (n *node) requestChannelClusterInfo(ctx context.Context, req *ChannelClusterInfoRequest) (*ChannelClusterInfo, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/getclusterinfo", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestChannelClusterInfo is failed, status:%d", resp.Status)
	}
	clusterInfo := &ChannelClusterInfo{}
	err = clusterInfo.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return clusterInfo, nil
}
