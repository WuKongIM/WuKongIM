package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/auth"
	"github.com/WuKongIM/WuKongIM/pkg/auth/resource"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) channelMigrate(c *wkhttp.Context) {

	var req struct {
		MigrateFrom uint64 `json:"migrate_from"` // 迁移的原节点
		MigrateTo   uint64 `json:"migrate_to"`   // 迁移的目标节点
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("BindJSON error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	// 获取频道所属槽领导的id
	nodeId, err := s.SlotLeaderIdOfChannel(channelId, channelType)
	if err != nil {
		s.Error("channelMigrate: LeaderIdOfChannel error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if nodeId != s.opts.NodeId {
		c.ForwardWithBody(fmt.Sprintf("%s%s", s.clusterEventServer.Node(nodeId).ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	// 获取频道的分布式配置
	clusterConfig, err := s.getChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("channelMigrate: getChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if !wkutil.ArrayContainsUint64(clusterConfig.Replicas, req.MigrateFrom) {
		c.ResponseError(errors.New("MigrateFrom not in replicas"))
		return
	}

	if wkutil.ArrayContainsUint64(clusterConfig.Replicas, req.MigrateTo) && req.MigrateFrom != clusterConfig.LeaderId {
		c.ResponseError(errors.New("transition between followers is not supported"))
		return
	}

	newClusterConfig := clusterConfig.Clone()
	if newClusterConfig.MigrateFrom != 0 || newClusterConfig.MigrateTo != 0 {
		c.ResponseError(errors.New("migrate is in progress"))
		return
	}

	// 保存配置
	newClusterConfig.MigrateFrom = req.MigrateFrom
	newClusterConfig.MigrateTo = req.MigrateTo
	newClusterConfig.ConfVersion = uint64(time.Now().UnixNano())

	if !wkutil.ArrayContainsUint64(clusterConfig.Replicas, req.MigrateTo) {
		// 将要目标节点加入学习者中
		newClusterConfig.Learners = append(newClusterConfig.Learners, req.MigrateTo)
	}

	// 提案保存配置
	err = s.opts.ChannelClusterStorage.Propose(newClusterConfig)
	if err != nil {
		s.Error("channelMigrate: Save error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 如果频道领导不是当前节点，则发送最新配置给频道领导 （这里就算发送失败也没问题，因为频道领导会间隔比对自己与槽领导的配置）
	if newClusterConfig.LeaderId != s.opts.NodeId {
		err = s.SendChannelClusterConfigUpdate(channelId, channelType, newClusterConfig.LeaderId)
		if err != nil {
			s.Error("channelMigrate: sendChannelClusterConfigUpdate error", zap.Error(err))
			c.ResponseError(err)
			return
		}
	} else {
		s.UpdateChannelClusterConfig(newClusterConfig)
	}

	// 如果目标节点不是当前节点，则发送最新配置给目标节点
	if req.MigrateTo != s.opts.NodeId {
		err = s.SendChannelClusterConfigUpdate(channelId, channelType, req.MigrateTo)
		if err != nil {
			s.Error("channelMigrate: sendChannelClusterConfigUpdate error", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}
	c.ResponseOK()

}

func (s *Server) channelClusterConfig(c *wkhttp.Context) {

	start := time.Now()
	defer func() {
		end := time.Since(start)
		if end > time.Millisecond*200 {
			s.Warn("channelClusterConfig slow", zap.Duration("cost", end))
		}
	}()

	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if nodeId > 0 && nodeId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(nodeId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	slotId := s.getSlotId(channelId)
	st := s.clusterEventServer.Slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("slot not found"))
		return
	}

	if st.Leader != s.opts.NodeId {
		s.Error("slot leader is not current node", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId), zap.Uint64("slotLeader", st.Leader))
		c.ResponseError(errors.New("slot leader is not current node"))
		return
	}

	clusterConfig, err := s.getChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("getChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, clusterConfig)
}

func (s *Server) channelStart(c *wkhttp.Context) {

	if !s.opts.Auth.HasPermissionWithContext(c, resource.ClusterChannel.Start, auth.ActionWrite) {
		c.ResponseStatus(http.StatusUnauthorized)
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	cfg, err := s.loadOnlyChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("loadOnlyChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if cfg.LeaderId == 0 {
		s.Error("leader not found", zap.String("cfg", cfg.String()))
		c.ResponseError(errors.New("leader not found"))
		return
	}

	if cfg.LeaderId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(cfg.LeaderId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()
	_, err = s.loadOrCreateChannel(timeoutCtx, channelId, channelType)
	if err != nil {
		s.Error("loadOrCreateChannel error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

func (s *Server) channelStop(c *wkhttp.Context) {

	if !s.opts.Auth.HasPermissionWithContext(c, resource.ClusterChannel.Stop, auth.ActionWrite) {
		c.ResponseStatus(http.StatusUnauthorized)
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	cfg, err := s.loadOnlyChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("loadOnlyChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if cfg.LeaderId == 0 {
		s.Error("leader not found", zap.String("cfg", cfg.String()))
		c.ResponseError(errors.New("leader not found"))
		return
	}

	if cfg.LeaderId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(cfg.LeaderId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	handler := s.channelManager.get(channelId, channelType)
	if handler != nil {
		s.channelManager.remove(handler.(*channel))
	}
	c.ResponseOK()
}

func (s *Server) channelStatus(c *wkhttp.Context) {
	var req struct {
		Channels []channelBase `json:"channels"`
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("BindJSON error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	resps := make([]*channelStatusResp, 0, len(req.Channels))
	for _, ch := range req.Channels {
		lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(wkutil.ChannelToKey(ch.ChannelId, ch.ChannelType))
		if err != nil {
			s.Error("LastIndexAndAppendTime error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		handler := s.channelManager.get(ch.ChannelId, ch.ChannelType)
		if handler != nil {
			resps = append(resps, &channelStatusResp{
				channelBase: ch,
				Running:     1,
				LastMsgSeq:  lastMsgSeq,
				LastMsgTime: lastAppendTime,
			})
		} else {
			resps = append(resps, &channelStatusResp{
				channelBase: ch,
				Running:     0,
				LastMsgSeq:  lastMsgSeq,
				LastMsgTime: lastAppendTime,
			})

		}
	}

	c.JSON(http.StatusOK, resps)

}

func (s *Server) channelReplicas(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	slotId := s.getSlotId(channelId)

	st := s.clusterEventServer.Slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("slot not found"))
		return
	}

	if st.Leader == 0 {
		s.Error("slot leader is 0", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("slot leader is 0"))
		return
	}

	slotLeaderNode := s.clusterEventServer.Node(st.Leader)
	if slotLeaderNode == nil {
		s.Error("slot leader node not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId), zap.Uint64("slotLeader", st.Leader))
		c.ResponseError(errors.New("slot leader node not found"))
		return

	}

	if st.Leader != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", slotLeaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}

	channelClusterConfig, err := s.getChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("getChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	replicas := make([]*channelReplicaDetailResp, 0, len(channelClusterConfig.Replicas))

	replicaIds := make([]uint64, 0, len(channelClusterConfig.Replicas)+len(channelClusterConfig.Learners))
	replicaIds = append(replicaIds, channelClusterConfig.Replicas...)
	replicaIds = append(replicaIds, channelClusterConfig.Learners...)

	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for _, replicaId := range replicaIds {
		replicaId := replicaId

		if !s.NodeIsOnline(replicaId) {
			continue
		}

		if replicaId == s.opts.NodeId {
			running := s.channelManager.exist(channelId, channelType)
			lastMsgSeq, lastTime, err := s.opts.DB.GetChannelLastMessageSeq(channelId, channelType)
			if err != nil {
				s.Error("GetChannelLastMessageSeq error", zap.Error(err))
				c.ResponseError(err)
				return
			}
			replicaResp := &channelReplicaResp{
				ReplicaId:   s.opts.NodeId,
				Running:     wkutil.BoolToInt(running),
				LastMsgSeq:  lastMsgSeq,
				LastMsgTime: lastTime,
			}
			lastMsgTimeFormat := ""
			if replicaResp.LastMsgTime != 0 {
				lastMsgTimeFormat = myUptime(time.Since(time.Unix(int64(replicaResp.LastMsgTime/1e9), 0)))
			}
			replicas = append(replicas, &channelReplicaDetailResp{
				channelReplicaResp: *replicaResp,
				Role:               s.getReplicaRole(channelClusterConfig, replicaId),
				RoleFormat:         s.getReplicaRoleFormat(channelClusterConfig, replicaId),
				LastMsgTimeFormat:  lastMsgTimeFormat,
			})
			continue

		}

		requestGroup.Go(func() error {
			replicaResp, err := s.requestChannelLocalReplica(replicaId, channelId, channelType, c.CopyRequestHeader(c.Request))
			if err != nil {
				return err
			}

			lastMsgTimeFormat := ""
			if replicaResp.LastMsgTime != 0 {
				lastMsgTimeFormat = myUptime(time.Since(time.Unix(int64(replicaResp.LastMsgTime/1e9), 0)))
			}

			replicas = append(replicas, &channelReplicaDetailResp{
				channelReplicaResp: *replicaResp,
				Role:               s.getReplicaRole(channelClusterConfig, replicaId),
				RoleFormat:         s.getReplicaRoleFormat(channelClusterConfig, replicaId),
				LastMsgTimeFormat:  lastMsgTimeFormat,
			})
			return nil
		})

	}

	err = requestGroup.Wait()
	if err != nil {
		s.Error("channelReplicas requestGroup error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, replicas)

}

func (s *Server) getReplicaRoleFormat(clusterConfig wkdb.ChannelClusterConfig, replicaId uint64) string {
	if replicaId == clusterConfig.LeaderId {
		return "leader"
	}
	if wkutil.ArrayContainsUint64(clusterConfig.Learners, replicaId) {
		return "learner"
	}
	return "follower"
}

func (s *Server) getReplicaRole(clusterConfig wkdb.ChannelClusterConfig, replicaId uint64) int {
	if replicaId == clusterConfig.LeaderId {
		return int(replica.RoleLeader)
	}
	if wkutil.ArrayContainsUint64(clusterConfig.Learners, replicaId) {
		return int(replica.RoleLearner)
	}
	return int(replica.RoleFollower)

}

func (s *Server) requestChannelLocalReplica(nodeId uint64, channelId string, channelType uint8, headers map[string]string) (*channelReplicaResp, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestChannelLocalReplica failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, s.formatPath(fmt.Sprintf("/channels/%s/%d/localReplica", channelId, channelType)))
	resp, err := network.Get(fullUrl, nil, headers)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("requestChannelLocalReplica failed, status code: %d", resp.StatusCode)
	}

	var replicaResp *channelReplicaResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &replicaResp)
	if err != nil {
		return nil, err
	}
	return replicaResp, nil
}

func (s *Server) channelLocalReplica(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	running := s.channelManager.exist(channelId, channelType)

	lastMsgSeq, lastTime, err := s.opts.DB.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		s.Error("GetChannelLastMessageSeq error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	var term uint32
	if lastMsgSeq > 0 {
		msg, err := s.opts.DB.LoadMsg(channelId, channelType, lastMsgSeq)
		if err != nil {
			s.Error("LoadMsg error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		term = uint32(msg.Term)
	}

	resp := &channelReplicaResp{
		ReplicaId:   s.opts.NodeId,
		Running:     wkutil.BoolToInt(running),
		LastMsgSeq:  lastMsgSeq,
		LastMsgTime: lastTime,
		Term:        term,
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) clusterLogs(c *wkhttp.Context) {
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	if nodeId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(nodeId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	slotId := wkutil.ParseUint32(c.Query("slot")) // slot id

	// 日志类型
	logType := LogType(wkutil.ParseInt(c.Query("log_type")))
	if logType == LogTypeUnknown {
		logType = LogTypeConfig
	}

	pre := wkutil.ParseUint64(c.Query("pre"))
	next := wkutil.ParseUint64(c.Query("next"))
	limit := wkutil.ParseInt(c.Query("limit"))

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var (
		start        uint64
		end          uint64
		logs         []replica.Log
		err          error
		appliedIndex uint64
		lastLogIndex uint64
	)

	if next > 0 {
		end = next
		if next > uint64(limit) {
			start = next - uint64(limit) - 1
		} else {
			start = 0
		}
	} else if pre > 0 {
		start = pre + 1
		end = pre + uint64(limit) + 1
	}

	if logType == LogTypeConfig {
		logs, err = s.clusterEventServer.GetLogsInReverseOrder(start, end, limit)
		if err != nil {
			s.Error("config: GetLogsInReverseOrder error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		appliedIndex, err = s.clusterEventServer.AppliedLogIndex()
		if err != nil {
			s.Error("AppliedLogIndex error", zap.Error(err))
			c.ResponseError(err)
			return
		}

		lastLogIndex, err = s.clusterEventServer.LastLogIndex()
		if err != nil {
			s.Error("LastLogIndex error", zap.Error(err))
			c.ResponseError(err)
			return
		}

	} else if logType == LogTypeSlot {
		shardNo := SlotIdToKey(slotId)
		logs, err = s.slotStorage.GetLogsInReverseOrder(shardNo, start, end, limit)
		if err != nil {
			s.Error("slot: GetLogsInReverseOrder error", zap.Error(err))
			c.ResponseError(err)
			return

		}
		appliedIndex, err = s.slotStorage.AppliedIndex(shardNo)
		if err != nil {
			s.Error("slot: AppliedLogIndex error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		lastLogIndex, err = s.slotStorage.LastIndex(shardNo)
		if err != nil {
			s.Error("slot: LastLogIndex error", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}

	resps := make([]*LogResp, 0, len(logs))

	for _, log := range logs {
		resp, err := NewLogRespFromLog(log, logType)
		if err != nil {
			s.Error("NewLogRespFromLog error", zap.Error(err), zap.Uint64("index", log.Index), zap.Uint32("term", log.Term))
			c.ResponseError(err)
			return
		}
		resps = append(resps, resp)
	}

	var (
		newNext uint64
		newPre  uint64
		more    int = 1
	)
	if len(logs) > 0 {
		newNext = logs[len(logs)-1].Index
		newPre = logs[0].Index
	}
	if newPre == 1 {
		more = 0
	}

	c.JSON(http.StatusOK, LogRespTotal{
		Next:    newNext,
		Pre:     newPre,
		More:    more,
		Applied: appliedIndex,
		Last:    lastLogIndex,
		Logs:    resps,
	})
}

func (s *Server) requestChannelStatus(nodeId uint64, channels []*channelBase, headers map[string]string) ([]*channelStatusResp, error) {
	node := s.clusterEventServer.Node(nodeId)
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

func (s *Server) clusterInfoGet(c *wkhttp.Context) {

	leaderId := s.clusterEventServer.LeaderId()
	if leaderId == 0 {
		c.ResponseError(errors.New("leader not found"))
		return
	}
	if leaderId != s.opts.NodeId {
		leaderNode := s.clusterEventServer.Node(leaderId)
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}
	cfg := s.clusterEventServer.Config()
	c.JSON(http.StatusOK, cfg)
}
