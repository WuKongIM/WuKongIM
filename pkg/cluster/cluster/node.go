package cluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type node struct {
	id     uint64
	addr   string
	client *client.Client
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
