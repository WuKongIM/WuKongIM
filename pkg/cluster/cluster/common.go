package cluster

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (s *Server) getLocalNodeInfo() *NodeConfig {
	leaderId := s.cfgServer.LeaderId()
	node := s.cfgServer.Node(s.opts.ConfigOptions.NodeId)

	cfg := s.cfgServer.GetClusterConfig()

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

func (s *Server) getNodeSlotCount(nodeId uint64, cfg *types.Config) int {
	count := 0
	for _, st := range cfg.Slots {
		if wkutil.ArrayContainsUint64(st.Replicas, nodeId) {
			count++
		}
	}
	return count
}

func (s *Server) getNodeSlotLeaderCount(nodeId uint64, cfg *types.Config) int {
	count := 0
	for _, st := range cfg.Slots {
		if st.Leader == nodeId {
			count++
		}
	}
	return count
}

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

func (s *Server) requestChannelStatus(nodeId uint64, channels []*channelBase, headers map[string]string) ([]*channelStatusResp, error) {
	node := s.cfgServer.Node(nodeId)
	if node == nil {
		return nil, fmt.Errorf("node not found, nodeId:%d", nodeId)
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, s.formatPath("/channel/status"))
	resp, err := network.Post(fullUrl, []byte(wkutil.ToJSON(map[string]interface{}{
		"channels": channels,
	})), headers)
	if err != nil {
		s.Error("requestChannelStatus error", zap.Error(err))
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		s.Error("requestChannelStatus failed", zap.Int("statusCode", resp.StatusCode))
		return nil, errors.New("requestChannelStatus failed")
	}

	var statusResp []*channelStatusResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &statusResp)
	if err != nil {
		return nil, err
	}
	return statusResp, nil
}

// 请求指定节点的配置信息
func (s *Server) requestNodeInfo(nodeId uint64, headers map[string]string) (*NodeConfig, error) {
	node := s.cfgServer.Node(nodeId)
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
