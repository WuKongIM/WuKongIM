package cluster

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) ServerAPI(route *wkhttp.WKHttp, prefix string) {

	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimSuffix(prefix, "/")

	route.GET(fmt.Sprintf("%s/channel/clusterinfo", prefix), s.getAllClusterInfo) // 获取所有channel的集群信息
}

// 获取所有channel的集群信息
func (s *Server) getAllClusterInfo(c *wkhttp.Context) {
	offsetStr := c.Query("offset")
	limitStr := c.Query("limit")
	var offset, limit int64 = 0, 0
	if offsetStr != "" {
		offset, _ = strconv.ParseInt(offsetStr, 10, 64)
	}
	if limitStr != "" {
		limit, _ = strconv.ParseInt(limitStr, 10, 64)
	}

	channelClusterInfos, err := s.stateMachine.getChannelClusterInfos(int(offset), int(limit))
	if err != nil {
		s.Error("getChannelClusterInfos error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 获取频道的leader节点的最新日志信息
	leaderChannelClusterDetailRespMap, err := s.requestLeaderChannelClusterDetails(channelClusterInfos)
	if err != nil {
		s.Error("requestLeaderChannelLogInfos error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	channelClusterInfoDetails := make([]*channelClusterDetailInfo, 0, len(channelClusterInfos))

	for _, channelClusterInfo := range channelClusterInfos {
		channelClusterDetails := leaderChannelClusterDetailRespMap[channelClusterInfo.LeaderID]
		for _, channelClusterDetail := range channelClusterDetails {
			if channelClusterInfo.ChannelID == channelClusterDetail.ChannelID && channelClusterInfo.ChannelType == channelClusterDetail.ChannelType {
				channelClusterInfoDetails = append(channelClusterInfoDetails, channelClusterDetail)
			}
		}

	}

	c.JSON(http.StatusOK, channelClusterInfoDetails)

}

// 请求所有channel的leader节点的最新日志信息
func (s *Server) requestLeaderChannelClusterDetails(channelClusterInfos []*ChannelClusterInfo) (map[uint64][]*channelClusterDetailInfo, error) {
	if len(channelClusterInfos) == 0 {
		return nil, nil
	}
	leaderChannelClusterDetailReqs := make(map[uint64][]*channelClusterDetailoReq)
	for _, channelClusterInfo := range channelClusterInfos {
		leaderChannelClusterDetailReqs[channelClusterInfo.LeaderID] = append(leaderChannelClusterDetailReqs[channelClusterInfo.LeaderID], &channelClusterDetailoReq{
			ChannelID:   channelClusterInfo.ChannelID,
			ChannelType: channelClusterInfo.ChannelType,
		})

	}
	leaderChannelClusterDetailMap := make(map[uint64][]*channelClusterDetailInfo)

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for leaderID, reqs := range leaderChannelClusterDetailReqs {
		if leaderID == s.opts.NodeID {
			resps, err := s.getChannelClusterDetails(reqs)
			if err != nil {
				return nil, err
			}
			leaderChannelClusterDetailMap[leaderID] = resps
			continue
		}

		requestGroup.Go(func(ldID uint64, rqs []*channelClusterDetailoReq) func() error {
			return func() error {
				resp, err := s.requestLeaderChannelClusterDetail(ldID, rqs)
				if err != nil {
					return err
				}
				leaderChannelClusterDetailMap[ldID] = resp
				return nil
			}
		}(leaderID, reqs))

	}
	if err := requestGroup.Wait(); err != nil {
		return nil, err
	}

	return leaderChannelClusterDetailMap, nil
}

func (s *Server) requestLeaderChannelClusterDetail(leaderID uint64, reqs []*channelClusterDetailoReq) ([]*channelClusterDetailInfo, error) {
	resps, err := s.nodeManager.requestChannelClusterDetail(s.cancelCtx, leaderID, reqs)
	if err != nil {
		return nil, err
	}
	return resps, nil
}

type channelClusterDetailInfo struct {
	ChannelID                string             `json:"channel_id"`
	ChannelType              uint8              `json:"channel_type"`               // 集群ID
	Slot                     uint32             `json:"slot"`                       // slot
	LeaderID                 uint64             `json:"leader_id"`                  // leader节点ID
	LeaderLastMetaLogIndex   uint64             `json:"leader_last_meta_log_index"` // leader的最新元数据日志下标
	LeaderLastMetaAppendTime uint64             `json:"leader_last_meta_log_time"`  // leader最新元数据日志追加的时间
	LeaderLastMsgLogIndex    uint64             `json:"leader_last_msg_log_index"`  // leader的最新消息日志下标
	LeaderLastMsgAppendTime  uint64             `json:"leader_last_msg_log_time"`   // leader最新消息日志追加的时间
	Replicas                 []uint64           `json:"replicas"`                   // 副本列表
	ApplyReplicas            []uint64           `json:"apply_replicas"`             // 已成功应用配置的副本
	ReplicaMaxCount          uint16             `json:"replica_max_count"`          // 副本最大数量
	MetaSyncInfo             []*channelSyncInfo `json:"meta_sync_info"`             // 元数据日志同步信息
	MsgSyncInfo              []*channelSyncInfo `json:"msg_sync_info"`              // 消息日志同步信息
}

type channelSyncInfo struct {
	NodeID           uint64 `json:"node_id"`            // 节点ID
	NextLogIndex     uint64 `json:"next_log_index"`     // 下一条日志的索引
	LastSyncTime     uint64 `json:"last_sync_time"`     // 最后一次同步时间
	LaggingLogCount  uint64 `json:"lagging_log_count"`  // 落后的日志数量
	ReplicaSyncDelay uint64 `json:"replica_sync_delay"` // 副本同步延迟时间（单位毫秒） = 领导最后一次追加日志的时间 - 副本最后一次同步领导日志的时间
}

func newChannelSyncInfo(nodeID uint64, nextLogIndex uint64, lastSyncTime uint64) *channelSyncInfo {
	return &channelSyncInfo{
		NodeID:       nodeID,
		NextLogIndex: nextLogIndex,
		LastSyncTime: lastSyncTime,
	}
}

type channelClusterDetailoReq struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}
