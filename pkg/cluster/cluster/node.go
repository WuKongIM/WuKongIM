package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type node struct {
	id              uint64
	addr            string
	client          *client.Client
	activityTimeout time.Duration // 活动超时时间，如果这个时间内没有活动，就表示节点已下线
}

func newNode(id uint64, uid string, addr string) *node {
	cli := client.New(addr, client.WithUID(uid))

	return &node{
		id:              id,
		addr:            addr,
		client:          cli,
		activityTimeout: time.Second * 10, // TODO: 这个时间也不能太短，如果太短节点可能在启动中，这时可能认为下线了，导致触发领导的转移
	}
}

func (n *node) start() {
	n.client.Start()
}

func (n *node) stop() {
	n.client.Close()
}

func (n *node) online() bool {
	return time.Since(n.client.LastActivity()) < n.activityTimeout
}

func (n *node) send(msg *proto.Message) error {
	return n.client.Send(msg)
}

func (n *node) requestWithContext(ctx context.Context, path string, body []byte) (*proto.Response, error) {
	return n.client.RequestWithContext(ctx, path, body)
}

// requestChannelLastLogInfo 请求channel的最后一条日志信息
func (n *node) requestChannelLastLogInfo(ctx context.Context, req *ChannelLastLogInfoReq) (*ChannelLastLogInfoResponse, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/lastloginfo", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestChannelLastLogInfo is failed, status:%d", resp.Status)
	}
	channelLastLogInfoResp := &ChannelLastLogInfoResponse{}
	err = channelLastLogInfoResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return channelLastLogInfoResp, nil
}

// requestAppointLeader 任命频道领导者
func (n *node) requestChannelAppointLeader(ctx context.Context, req *AppointLeaderReq) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/appointleader", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("requestAppointLeader is failed, status:%d", resp.Status)
	}
	return nil
}

func (n *node) requestChannelClusterConfig(ctx context.Context, req *ChannelClusterConfigReq) (*ChannelClusterConfig, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/clusterconfig", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestChannelClusterConfig is failed, status:%d", resp.Status)
	}
	channelClusterConfigResp := &ChannelClusterConfig{}
	err = channelClusterConfigResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return channelClusterConfigResp, nil
}

func (n *node) requestChannelProposeMessage(ctx context.Context, req *ChannelProposeReq) (*ChannelProposeResp, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/proposeMessage", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestProposeMessage is failed, status:%d", resp.Status)
	}
	proposeMessageResp := &ChannelProposeResp{}
	err = proposeMessageResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return proposeMessageResp, nil
}

// 请求更新节点api地址
func (n *node) requestUpdateNodeApiServerAddr(ctx context.Context, req *UpdateNodeApiServerAddrReq) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := n.client.RequestWithContext(ctx, "/node/updateApiServerAddr", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("requestUpdateNodeApiServerAddr is failed, status:%d", resp.Status)
	}
	return nil
}

func (n *node) requestSlotLogInfo(ctx context.Context, req *SlotLogInfoReq) (*SlotLogInfoResp, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/logInfo", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestSlotLogInfo is failed, status:%d", resp.Status)
	}
	slotLogInfoResp := &SlotLogInfoResp{}
	err = slotLogInfoResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return slotLogInfoResp, nil

}

func (n *node) requestSlotPropose(ctx context.Context, req *SlotProposeReq) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/propose", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("requestSlotPropose is failed, status:%d", resp.Status)
	}

	return nil
}

func (n *node) requestClusterJoin(ctx context.Context, req *ClusterJoinReq) (*ClusterJoinResp, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/cluster/join", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestClusterJoin is failed, status:%d", resp.Status)
	}
	clusterJoinResp := &ClusterJoinResp{}
	err = clusterJoinResp.Unmarshal(resp.Body)
	return clusterJoinResp, err
}
