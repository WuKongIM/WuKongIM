package cluster

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/pb"
	"github.com/WuKongIM/WuKongIM/internal/server/cluster/rpc"
)

// CMDEvent cmd事件
type CMDEvent interface {
	// 收到发送包
	OnSendPacket(req *rpc.ForwardSendPacketReq) (*rpc.ForwardSendPacketResp, error)
	// 收到接受包
	OnRecvPacket(req *rpc.ForwardRecvPacketReq) error
	// 获取频道订阅者
	OnGetSubscribers(channelID string, channelType uint8) ([]string, error)
	// 连接请求
	OnConnectReq(req *rpc.ConnectReq) (*rpc.ConnectResp, error)
	// 连接写入
	OnConnectWriteReq(req *rpc.ConnectWriteReq) (rpc.Status, error)
	// 连接ping
	OnConnPingReq(req *rpc.ConnPingReq) (rpc.Status, error)
	// 发送同步提议请求
	OnSendSyncProposeReq(req *rpc.SendSyncProposeReq) (*rpc.SendSyncProposeResp, error)
	// 收到接受ack包
	OnRecvackPacket(req *rpc.RecvacksReq) error
}

type defaultCMDEvent struct {
	cmdEvent CMDEvent
	cluster  *Cluster
}

func newDefaultCMDEvent(cmdEvent CMDEvent, cluster *Cluster) *defaultCMDEvent {

	return &defaultCMDEvent{
		cmdEvent: cmdEvent,
		cluster:  cluster,
	}
}

// 收到发送包
func (d *defaultCMDEvent) OnSendPacket(req *rpc.ForwardSendPacketReq) (*rpc.ForwardSendPacketResp, error) {
	return d.cmdEvent.OnSendPacket(req)

}

// 收到接受包
func (d *defaultCMDEvent) OnRecvPacket(req *rpc.ForwardRecvPacketReq) error {
	return d.cmdEvent.OnRecvPacket(req)
}

// 获取频道订阅者
func (d *defaultCMDEvent) OnGetSubscribers(channelID string, channelType uint8) ([]string, error) {
	return d.cmdEvent.OnGetSubscribers(channelID, channelType)
}

// 连接请求
func (d *defaultCMDEvent) OnConnectReq(req *rpc.ConnectReq) (*rpc.ConnectResp, error) {
	return d.cmdEvent.OnConnectReq(req)
}

// 连接写入
func (d *defaultCMDEvent) OnConnectWriteReq(req *rpc.ConnectWriteReq) (rpc.Status, error) {
	return d.cmdEvent.OnConnectWriteReq(req)
}

// 连接ping
func (d *defaultCMDEvent) OnConnPingReq(req *rpc.ConnPingReq) (rpc.Status, error) {
	return d.cmdEvent.OnConnPingReq(req)
}

// 发送同步提议请求
func (d *defaultCMDEvent) OnSendSyncProposeReq(req *rpc.SendSyncProposeReq) (*rpc.SendSyncProposeResp, error) {
	return d.cmdEvent.OnSendSyncProposeReq(req)
}

// 收到接受ack包
func (d *defaultCMDEvent) OnRecvackPacket(req *rpc.RecvacksReq) error {
	return d.cmdEvent.OnRecvackPacket(req)
}

// 获取集群配置
func (d *defaultCMDEvent) OnGetClusterConfig() ([]byte, error) {
	leaderID := d.cluster.clusterManager.GetLeaderPeerID()
	if leaderID == 0 {
		return nil, errors.New("no leader")
	}
	var clusterConfig *pb.Cluster
	var err error

	if leaderID != d.cluster.opts.PeerID {
		clusterConfig, err = d.cluster.RequestClusterConfig(leaderID)
		if err != nil {
			return nil, err
		}
	} else {
		clusterConfig = d.cluster.GetClusterConfig()
	}

	clusterConfigData, _ := clusterConfig.Marshal()
	return clusterConfigData, nil
}

func (d *defaultCMDEvent) OnJoinReq(req *pb.JoinReq) error {
	leaderID := d.cluster.clusterManager.GetLeaderPeerID()
	if leaderID == 0 {
		return errors.New("no leader")
	}
	if leaderID != d.cluster.opts.PeerID {
		req.ToPeerID = leaderID
		return d.cluster.requestJoinReq(req)
	}
	return d.cluster.SyncRequestAddReplica(req.JoinPeer)
}
