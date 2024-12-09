package cluster

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/sendgrid/rest"
	"go.uber.org/zap"
)

var (
	errCircuitBreakerNotReady error = fmt.Errorf("circuit breaker not ready")
	errRateLimited                  = fmt.Errorf("rate limited")
	errChanIsFull                   = fmt.Errorf("channel is full")
)

func SlotIdToKey(slotId uint32) string {
	return strconv.FormatUint(uint64(slotId), 10)
}

// 分区类型
type ShardType uint8

const (
	ShardTypeUnknown ShardType = iota // 未知
	ShardTypeSlot                     // slot分区
	ShardTypeChannel                  // channel分区
	ShardTypeConfig                   // 配置
)

const (
	MsgTypeUnknown                    uint32 = iota
	MsgTypeSlot                              // slot
	MsgTypeChannel                           // channel
	MsgTypeConfig                            // 配置
	MsgTypeChannelClusterConfigUpdate        // 通知更新channel的配置
	MsgTypePing                              // ping
	MsgTypePong                              // pong
)

func myUptime(d time.Duration) string {
	// Just use total seconds for uptime, and display days / years
	tsecs := d / time.Second
	tmins := tsecs / 60
	thrs := tmins / 60
	tdays := thrs / 24
	tyrs := tdays / 365

	if tyrs > 0 {
		return fmt.Sprintf("%dy%dd%dh%dm%ds", tyrs, tdays%365, thrs%24, tmins%60, tsecs%60)
	}
	if tdays > 0 {
		return fmt.Sprintf("%dd%dh%dm%ds", tdays, thrs%24, tmins%60, tsecs%60)
	}
	if thrs > 0 {
		return fmt.Sprintf("%dh%dm%ds", thrs, tmins%60, tsecs%60)
	}
	if tmins > 0 {
		return fmt.Sprintf("%dm%ds", tmins, tsecs%60)
	}
	return fmt.Sprintf("%ds", tsecs)
}

func traceOutgoingMessage(kind trace.ClusterKind, msgType replica.MsgType, size int64) {

	switch msgType {
	case replica.MsgSyncReq:
		trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingBytesAdd(kind, size)
	case replica.MsgPing:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingOutgoingBytesAdd(kind, size)
	case replica.MsgPong:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongOutgoingBytesAdd(kind, size)
	case replica.MsgLeaderTermStartIndexReq:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqOutgoingBytesAdd(kind, size)
	case replica.MsgLeaderTermStartIndexResp:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespOutgoingBytesAdd(kind, size)

	}

}

func traceIncomingMessage(kind trace.ClusterKind, msgType replica.MsgType, size int64) {
	switch msgType {
	case replica.MsgSyncReq:
		trace.GlobalTrace.Metrics.Cluster().MsgSyncIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgSyncIncomingBytesAdd(kind, size)

	case replica.MsgPing:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingIncomingBytesAdd(kind, size)
	case replica.MsgPong:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongIncomingBytesAdd(kind, size)
	case replica.MsgLeaderTermStartIndexReq:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqIncomingBytesAdd(kind, size)
	case replica.MsgLeaderTermStartIndexResp:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespIncomingBytesAdd(kind, size)

	}
}

func handlerIMError(resp *rest.Response) error {
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest {
			resultMap, err := wkutil.JSONToMap(resp.Body)
			if err != nil {
				return err
			}
			if resultMap != nil && resultMap["msg"] != nil {
				return fmt.Errorf("IM服务失败！ -> %s", resultMap["msg"])
			}
		}
		return fmt.Errorf("IM服务返回状态[%d]失败！", resp.StatusCode)
	}
	return nil
}

func (s *Server) formatPath(path string) string {
	var prefix = s.apiPrefix
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimSuffix(prefix, "/")
	path = strings.TrimPrefix(path, "/")

	return fmt.Sprintf("%s/%s", prefix, path)
}

// 请求指定节点的配置信息
func (s *Server) requestNodeInfo(nodeId uint64, headers map[string]string) (*NodeConfig, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	resp, err := network.Get(fmt.Sprintf("%s%s", node.ApiServerAddr, s.formatPath("/node")), nil, headers)
	if err != nil {
		return nil, err
	}
	nodeCfg := &NodeConfig{}
	err = wkutil.ReadJSONByByte([]byte(resp.Body), nodeCfg)
	if err != nil {
		return nil, err
	}
	return nodeCfg, nil
}
func (s *Server) getLocalNodeInfo() *NodeConfig {
	leaderId := s.clusterEventServer.LeaderId()
	node := s.clusterEventServer.Node(s.opts.NodeId)

	cfg := s.clusterEventServer.Config()

	nodeCfg := NewNodeConfigFromNode(node)
	nodeCfg.IsLeader = wkutil.BoolToInt(leaderId == node.Id)
	nodeCfg.SlotCount = s.getNodeSlotCount(node.Id, cfg)
	nodeCfg.SlotLeaderCount = s.getNodeSlotLeaderCount(node.Id, cfg)
	nodeCfg.Term = cfg.Term
	nodeCfg.Uptime = myUptime(time.Since(s.uptime))
	nodeCfg.AppVersion = s.opts.AppVersion
	nodeCfg.ConfigVersion = cfg.Version
	return nodeCfg
}

func (s *Server) getNodeSlotCount(nodeId uint64, cfg *pb.Config) int {
	count := 0
	for _, st := range cfg.Slots {
		if wkutil.ArrayContainsUint64(st.Replicas, nodeId) {
			count++
		}
	}
	return count
}

func (s *Server) getNodeSlotLeaderCount(nodeId uint64, cfg *pb.Config) int {
	count := 0
	for _, st := range cfg.Slots {
		if st.Leader == nodeId {
			count++
		}
	}
	return count
}

func BindJSON(obj any, c *wkhttp.Context) ([]byte, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	if err := wkutil.ReadJSONByByte(bodyBytes, obj); err != nil {
		return nil, err
	}
	return bodyBytes, nil
}

func getChannelTypeFormat(channelType uint8) string {
	switch channelType {
	case wkproto.ChannelTypePerson:
		return "单聊"
	case wkproto.ChannelTypeGroup:
		return "群聊"
	case wkproto.ChannelTypeCustomerService:
		return "客服"
	case wkproto.ChannelTypeCommunity:
		return "社区"
	case wkproto.ChannelTypeCommunityTopic:
		return "社区话题"
	case wkproto.ChannelTypeInfo:
		return "资讯"
	case wkproto.ChannelTypeData:
		return "数据"
	default:
		return "未知"
	}
}
