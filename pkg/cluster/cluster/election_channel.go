package cluster

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) getOrCreateChannelClusterConfigFromLocal(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {
	// 获取频道槽领导
	slotLeaderId, err := s.SlotLeaderIdOfChannel(channelId, channelType)
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}

	if s.opts.ConfigOptions.NodeId != slotLeaderId {
		s.Error("not slot leader", zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		return wkdb.EmptyChannelClusterConfig, errors.New("not slot leader")
	}

	cfg, err := s.db.GetChannelClusterConfig(channelId, channelType)
	if err != nil && err != wkdb.ErrNotFound {
		return wkdb.EmptyChannelClusterConfig, err
	}

	// 如果配置不存在，则创建一个新的配置
	if wkdb.IsEmptyChannelClusterConfig(cfg) {
		cfg, err = s.createChannelClusterConfig(channelId, channelType)
		if err != nil {
			return wkdb.EmptyChannelClusterConfig, err
		}

		version, err := s.store.SaveChannelClusterConfig(cfg)
		if err != nil {
			return wkdb.EmptyChannelClusterConfig, err
		}
		cfg.ConfVersion = version
		return cfg, nil
	}

	propose := false

	// 视情况是否需要加入新的副本
	changed, err := s.joinNewRepliceIfNeed(&cfg)
	if err != nil {
		s.Foucs("joinNewRepliceIfNeed failed", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.String("cfg", cfg.String()))
	}
	if changed {
		propose = true
	}

	// 视情况是否需要变更领导节点
	changed, err = s.switchLeaderIfNeed(&cfg)
	if err != nil {
		s.Foucs("switchLeaderIfNeed failed", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.String("cfg", cfg.String()))
	}
	if changed {
		propose = true
	}

	// 保存配置
	if propose {
		version, err := s.store.SaveChannelClusterConfig(cfg)
		if err != nil {
			return wkdb.EmptyChannelClusterConfig, err
		}
		cfg.ConfVersion = version
	}

	return cfg, nil
}

// 视情况是否需要加入新的副本
func (s *Server) joinNewRepliceIfNeed(cfg *wkdb.ChannelClusterConfig) (bool, error) {
	if len(cfg.Replicas) >= int(cfg.ReplicaMaxCount) {
		return false, nil
	}
	if len(cfg.Learners) > 0 {
		return false, nil
	}
	allowVoteNodes := s.cfgServer.AllowVoteAndJoinedOnlineNodes()
	if len(allowVoteNodes) == 0 {
		return false, errors.New("no allow vote nodes")
	}

	newReplicaIds := make([]uint64, 0, len(allowVoteNodes)-len(cfg.Replicas))
	for _, node := range allowVoteNodes {
		if !wkutil.ArrayContainsUint64(cfg.Replicas, node.Id) {
			newReplicaIds = append(newReplicaIds, node.Id)
		}
	}
	// 打乱顺序，防止每次都是相同的节点加入
	rand.Shuffle(len(newReplicaIds), func(i, j int) {
		newReplicaIds[i], newReplicaIds[j] = newReplicaIds[j], newReplicaIds[i]
	})

	// 将新节点加入到学习者列表
	for _, newReplicaId := range newReplicaIds {
		cfg.MigrateFrom = newReplicaId
		cfg.MigrateTo = newReplicaId
		cfg.Learners = append(cfg.Learners, newReplicaId)
		if len(cfg.Learners)+len(cfg.Replicas) >= int(cfg.ReplicaMaxCount) {
			break
		}
	}

	return true, nil

}

// 视情况是否需要变更领导节点
func (s *Server) switchLeaderIfNeed(cfg *wkdb.ChannelClusterConfig) (bool, error) {
	if cfg.LeaderId != 0 && s.cfgServer.NodeIsOnline(cfg.LeaderId) {
		return false, nil
	}

	// 获得在线的副本
	onlineRepics := make([]uint64, 0, len(cfg.Replicas))
	for _, replicaId := range cfg.Replicas {
		if s.cfgServer.NodeIsOnline(replicaId) {
			onlineRepics = append(onlineRepics, replicaId)
		}
	}

	// 获得在线的副本的最新日志信息
	replicaLastLogInfos, err := s.requestChannelLastLogInfos(onlineRepics, cfg.ChannelId, cfg.ChannelType)
	if err != nil {
		s.Error("requestChannelLastLogInfos failed", zap.Error(err))
		return false, err
	}
	if len(replicaLastLogInfos) == 0 {
		s.Info("没有获取到频道的最新日志信息！！！", zap.String("channelId", cfg.ChannelId), zap.Uint8("channelType", cfg.ChannelType))
		return false, errors.New("no replica last log infos")
	}

	// 合法的最小选举人数
	quorum := int(cfg.ReplicaMaxCount/2) + 1
	if len(replicaLastLogInfos) < quorum {
		s.Foucs("no enough replica last log infos", zap.Int("quorum", quorum), zap.Int("replicaLastLogInfos", len(replicaLastLogInfos)))
		return false, errors.New("no enough replica last log infos")
	}

	// 选举新的领导
	newLeaderId, err := s.electionNewLeader(replicaLastLogInfos)
	if err != nil {
		s.Error("electionNewLeader failed", zap.Error(err))
		return false, err
	}

	if newLeaderId == 0 {
		s.Foucs("no new leader", zap.String("channelId", cfg.ChannelId), zap.Uint8("channelType", cfg.ChannelType), zap.Int("quorum", quorum), zap.Int("replicaLastLogInfos", len(replicaLastLogInfos)))
		return false, errors.New("no new leader")
	}
	cfg.LeaderId = newLeaderId
	cfg.Term = cfg.Term + 1

	s.Info("switch leader success", zap.String("channelId", cfg.ChannelId), zap.Uint8("channelType", cfg.ChannelType), zap.Uint64("newLeaderId", newLeaderId))

	return true, nil
}

func (s *Server) electionNewLeader(replicaLastLogInfos map[uint64]*ChannelLastLogInfoResponse) (uint64, error) {

	var leaderId uint64 = 0     // 新的领导节点
	var lastLogIndex uint64 = 0 // 最新日志索引
	var lastLogTerm uint32 = 0  // 最新日志任期
	var leaderTerm uint32 = 0   // 领导任期

	for replicaId, candidate := range replicaLastLogInfos {
		// 候选者的任期号（Term）不小于自身的当前任期号。
		if candidate.Term < leaderTerm {
			continue
		}
		// 候选者的最后日志条目的任期号较大，或者
		// 如果任期号相同，候选者的日志更长。
		if candidate.LogTerm > lastLogTerm {
			leaderId = replicaId
			lastLogIndex = candidate.LogIndex
			lastLogTerm = candidate.LogTerm
			leaderTerm = candidate.Term
		} else if candidate.LogTerm == lastLogTerm {
			if candidate.LogIndex > lastLogIndex {
				leaderId = replicaId
				lastLogIndex = candidate.LogIndex
				lastLogTerm = candidate.LogTerm
				leaderTerm = candidate.Term
			}
		}
	}
	return leaderId, nil

}

// 请求副本的最新日志信息
func (s *Server) requestChannelLastLogInfos(replices []uint64, channelId string, channelType uint8) (map[uint64]*ChannelLastLogInfoResponse, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*4)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	respMap := make(map[uint64]*ChannelLastLogInfoResponse)

	for _, nodeId := range replices {
		nodeId := nodeId

		if nodeId == s.opts.ConfigOptions.NodeId {
			resp, err := s.getChannelLastLogInfo(channelId, channelType)
			if err != nil {
				s.Error("getChannelLastLogInfo failed", zap.Uint64("nodeId", nodeId), zap.Error(err))
				continue
			}
			respMap[nodeId] = resp
			continue
		}

		requestGroup.Go(func() error {
			resp, err := s.rpcClient.RequestChannelLastLogInfo(nodeId, channelId, channelType)
			if err != nil {
				s.Error("RequestChannelLastLogInfo failed", zap.Uint64("nodeId", nodeId), zap.Error(err))
				return err
			}
			respMap[nodeId] = resp
			return nil
		})
	}

	_ = requestGroup.Wait()

	return respMap, nil
}

// 创建一个频道的分布式配置
func (s *Server) createChannelClusterConfig(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {
	allowVoteNodes := s.cfgServer.AllowVoteAndJoinedOnlineNodes() // 获取允许投票的在线节点
	if len(allowVoteNodes) == 0 {
		return wkdb.EmptyChannelClusterConfig, errors.New("no allow vote nodes")
	}

	createdAt := time.Now()
	updatedAt := time.Now()
	clusterConfig := wkdb.ChannelClusterConfig{
		ChannelId:       channelId,
		ChannelType:     channelType,
		ReplicaMaxCount: uint16(s.opts.ConfigOptions.ChannelMaxReplicaCount),
		Term:            1,
		LeaderId:        s.opts.ConfigOptions.NodeId,
		CreatedAt:       &createdAt,
		UpdatedAt:       &updatedAt,
	}
	replicaIds := make([]uint64, 0, s.opts.ConfigOptions.ChannelMaxReplicaCount)
	replicaIds = append(replicaIds, s.opts.ConfigOptions.NodeId) // 默认当前节点是领导，所以加入到副本列表中

	// 随机选择副本
	newAllowVoteNodes := make([]*types.Node, 0, len(allowVoteNodes))
	newAllowVoteNodes = append(newAllowVoteNodes, allowVoteNodes...)
	rand.Shuffle(len(newAllowVoteNodes), func(i, j int) {
		newAllowVoteNodes[i], newAllowVoteNodes[j] = newAllowVoteNodes[j], newAllowVoteNodes[i]
	})

	for _, allowVoteNode := range newAllowVoteNodes {
		if allowVoteNode.Id == s.opts.ConfigOptions.NodeId {
			continue
		}
		if len(replicaIds) >= int(s.opts.ConfigOptions.ChannelMaxReplicaCount) {
			break
		}
		replicaIds = append(replicaIds, allowVoteNode.Id)

	}
	clusterConfig.Replicas = replicaIds
	return clusterConfig, nil
}
