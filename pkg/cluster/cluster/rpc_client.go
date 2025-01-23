package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type rpcClient struct {
	s *Server
}

func newRpcClient(s *Server) *rpcClient {
	return &rpcClient{
		s: s,
	}
}

// RequestChannelProposeBatchUntilApplied 向指定节点请求频道提案
func (r *rpcClient) RequestChannelProposeBatchUntilApplied(nodeId uint64, channelId string, channelType uint8, reqs types.ProposeReqSet) (types.ProposeRespSet, error) {

	req := &channelProposeReq{
		channelId:   channelId,
		channelType: channelType,
		reqs:        reqs,
	}
	data, err := req.encode()
	if err != nil {
		return nil, err
	}
	body, err := r.request(nodeId, "/rpc/channel/propose", data)
	if err != nil {
		return nil, err
	}

	resps := types.ProposeRespSet{}
	if err := resps.Unmarshal(body); err != nil {
		return nil, err
	}
	return resps, nil
}

func (r *rpcClient) RequestGetOrCreateChannelClusterConfig(nodeId uint64, channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {

	req := &channelReq{
		channelId:   channelId,
		channelType: channelType,
	}
	data, err := req.encode()
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}
	body, err := r.request(nodeId, "/rpc/channel/getOrCreateConfig", data)
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}

	config := wkdb.ChannelClusterConfig{}
	if err := config.Unmarshal(body); err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}
	return config, nil
}

func (r *rpcClient) RequestGetChannelClusterConfig(nodeId uint64, channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {

	req := &channelReq{
		channelId:   channelId,
		channelType: channelType,
	}
	data, err := req.encode()
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}
	body, err := r.request(nodeId, "/rpc/channel/getConfig", data)
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}

	config := wkdb.ChannelClusterConfig{}
	if err := config.Unmarshal(body); err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}
	return config, nil
}

func (r *rpcClient) RequestSlotLastLogInfo(nodeId uint64, req *SlotLogInfoReq) (*SlotLogInfoResp, error) {

	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	body, err := r.request(nodeId, "/rpc/slot/lastLogInfo", data)
	if err != nil {
		return nil, err
	}

	resp := &SlotLogInfoResp{}
	if err := resp.Unmarshal(body); err != nil {
		return nil, err
	}
	return resp, nil

}

// RequestWakeLeaderIfNeed 向指定节点请求唤醒领导
func (r *rpcClient) RequestWakeLeaderIfNeed(nodeId uint64, config wkdb.ChannelClusterConfig) error {

	data, err := config.Marshal()
	if err != nil {
		return err
	}
	_, err = r.request(nodeId, "/rpc/channel/wakeLeaderIfNeed", data)
	return err
}

// RequestChannelSwitchConfig 请求切换频道配置
func (r *rpcClient) RequestChannelSwitchConfig(nodeId uint64, config wkdb.ChannelClusterConfig) error {
	data, err := config.Marshal()
	if err != nil {
		return err
	}
	_, err = r.request(nodeId, "/rpc/channel/switchConfig", data)
	return err
}

// RequestChannelLastLogInfo 请求频道最后日志信息
func (r *rpcClient) RequestChannelLastLogInfo(nodeId uint64, channelId string, channelType uint8) (*ChannelLastLogInfoResponse, error) {
	req := &channelReq{
		channelId:   channelId,
		channelType: channelType,
	}
	data, err := req.encode()
	if err != nil {
		return nil, err
	}
	body, err := r.request(nodeId, "/rpc/channel/lastLogInfo", data)
	if err != nil {
		return nil, err
	}

	resp := &ChannelLastLogInfoResponse{}
	if err := resp.Unmarshal(body); err != nil {
		return nil, err
	}
	return resp, nil
}

// 节点请求加入
func (r *rpcClient) RequestClusterJoin(nodeId uint64, req *ClusterJoinReq) (*ClusterJoinResp, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	body, err := r.request(nodeId, "/rpc/cluster/join", data)
	if err != nil {
		return nil, err
	}
	resp := &ClusterJoinResp{}
	if err := resp.Unmarshal(body); err != nil {
		return nil, err
	}
	return resp, nil
}

func (r *rpcClient) request(nodeId uint64, path string, body []byte) ([]byte, error) {

	node := r.s.nodeManager.node(nodeId)
	if node == nil {
		return nil, fmt.Errorf("node[%d] not exist", nodeId)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	resp, err := node.requestWithContext(timeoutCtx, path, body)
	if err != nil {
		return nil, err
	}

	if resp.Status == proto.StatusOK {
		return resp.Body, nil
	}

	return nil, fmt.Errorf("rpc request failed, status: %d", resp.Status)

}

type channelReq struct {
	channelId   string
	channelType uint8
}

func (ch *channelReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteString(ch.channelId)
	enc.WriteUint8(ch.channelType)

	return enc.Bytes(), nil
}

func (ch *channelReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if ch.channelId, err = dec.String(); err != nil {
		return err
	}
	if ch.channelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}
