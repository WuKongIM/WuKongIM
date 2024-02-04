package cluster

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

func (s *Server) ServerAPI(route *wkhttp.WKHttp, prefix string) {

	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimSuffix(prefix, "/")

	route.GET(fmt.Sprintf("%s/nodes", prefix), s.clusterInfoGet) // 获取所有节点

	// route.GET(fmt.Sprintf("%s/channel/clusterinfo", prefix), s.getAllClusterInfo) // 获取所有channel的集群信息
}

func (s *Server) clusterInfoGet(c *wkhttp.Context) {
	cfgServer := s.clusterEventListener.clusterconfigManager.clusterconfigServer
	cfg := cfgServer.ConfigManager().GetConfig()

	leaderId := cfgServer.Leader()

	nodeCfgs := make([]*NodeConfig, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		nodeCfg := NewNodeConfigFromNode(node)
		nodeCfg.IsLeader = wkutil.BoolToInt(leaderId == node.Id)
		nodeCfg.SlotCount = s.getNodeSlotCount(node.Id, cfg)
		nodeCfgs = append(nodeCfgs, nodeCfg)

	}

	c.JSON(http.StatusOK, NodeConfigTotal{
		Total: len(nodeCfgs),
		Nodes: nodeCfgs,
	})
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

type NodeConfigTotal struct {
	Total int           `json:"total"` // 总数
	Nodes []*NodeConfig `json:"nodes"`
}

type NodeConfig struct {
	Id            uint64 `json:"id"`                        // 节点ID
	IsLeader      int    `json:"is_leader,omitempty"`       // 是否是leader
	ClusterAddr   string `json:"cluster_addr"`              // 集群地址
	ApiServerAddr string `json:"api_server_addr,omitempty"` // API服务地址
	Online        int    `json:"online,omitempty"`          // 是否在线
	OfflineCount  int    `json:"offline_count,omitempty"`   // 下线次数
	LastOffline   string `json:"last_offline,omitempty"`    // 最后一次下线时间
	AllowVote     int    `json:"allow_vote"`                // 是否允许投票
	SlotCount     int    `json:"slot_count,omitempty"`      // 槽位数量
}

func NewNodeConfigFromNode(n *pb.Node) *NodeConfig {
	// lastOffline format string
	lastOffline := ""
	if n.LastOffline != 0 {
		lastOffline = wkutil.ToyyyyMMddHHmm(time.Unix(n.LastOffline, 0))
	}
	return &NodeConfig{
		Id:            n.Id,
		ClusterAddr:   n.ClusterAddr,
		ApiServerAddr: n.ApiServerAddr,
		Online:        wkutil.BoolToInt(n.Online),
		OfflineCount:  int(n.OfflineCount),
		LastOffline:   lastOffline,
		AllowVote:     wkutil.BoolToInt(n.AllowVote),
	}
}
