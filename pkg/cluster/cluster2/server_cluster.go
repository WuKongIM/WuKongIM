package cluster

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/icluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) LeaderIdOfChannel(ctx context.Context, channelId string, channelType uint8) (nodeId uint64, err error) {
	ch, err := s.loadOrCreateChannel(ctx, channelId, channelType)
	if err != nil {
		return 0, err
	}
	return ch.leaderId(), nil
}

func (s *Server) LeaderOfChannel(ctx context.Context, channelId string, channelType uint8) (nodeInfo *pb.Node, err error) {
	ch, err := s.loadOrCreateChannel(ctx, channelId, channelType)
	if err != nil {
		return nil, err
	}
	leaderId := ch.leaderId()
	if leaderId == 0 {
		s.Error("LeaderOfChannel: leader not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		return nil, ErrNotLeader
	}
	node := s.clusterEventServer.Node(leaderId)
	if node == nil {
		s.Error("LeaderOfChannel: node not found", zap.Uint64("leaderId", leaderId))
		return nil, ErrNodeNotExist
	}
	return node, nil
}

func (s *Server) LeaderOfChannelForRead(channelId string, channelType uint8) (*pb.Node, error) {
	cfg, err := s.loadChannelClusterConfig(channelId, channelType)
	if err != nil {
		return nil, err
	}
	if cfg.LeaderId == 0 {
		return nil, ErrNotLeader
	}
	node := s.clusterEventServer.Node(cfg.LeaderId)
	if node == nil {
		return nil, ErrNodeNotExist
	}
	return node, nil
}

func (s *Server) SlotLeaderIdOfChannel(channelId string, channelType uint8) (nodeID uint64, err error) {
	slotId := s.getSlotId(channelId)
	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		return 0, ErrSlotNotFound
	}
	return slot.Leader, nil
}

func (s *Server) SlotLeaderOfChannel(channelId string, channelType uint8) (*pb.Node, error) {
	slotId := s.getSlotId(channelId)
	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		s.Error("SlotLeaderOfChannel failed, slot not exist", zap.Uint32("slotId", slotId))
		return nil, ErrSlotNotFound
	}

	if slot.Leader == 0 {
		s.Error("SlotLeaderOfChannel failed, slot leader not found", zap.Uint32("slotId", slotId))
		return nil, ErrSlotLeaderNotFound
	}
	node := s.clusterEventServer.Node(slot.Leader)
	if node == nil {
		s.Error("SlotLeaderOfChannel failed, slot leader node not found", zap.Uint32("slotId", slotId), zap.Uint64("slotLeader", slot.Leader))
		return nil, ErrNodeNotExist
	}
	return node, nil
}

func (s *Server) IsSlotLeaderOfChannel(channelID string, channelType uint8) (bool, error) {
	slotId := s.getSlotId(channelID)
	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		return false, ErrSlotNotFound
	}
	return slot.Leader == s.opts.NodeId, nil
}

func (s *Server) IsLeaderOfChannel(ctx context.Context, channelId string, channelType uint8) (bool, error) {
	ch, err := s.loadOrCreateChannel(ctx, channelId, channelType)
	if err != nil {
		return false, err
	}
	return ch.leaderId() == s.opts.NodeId, nil

}

func (s *Server) NodeInfoById(nodeId uint64) (*pb.Node, error) {
	return s.clusterEventServer.Node(nodeId), nil
}

func (s *Server) Route(path string, handler wkserver.Handler) {
	s.netServer.Route(path, handler)
}

func (s *Server) RequestWithContext(ctx context.Context, toNodeId uint64, path string, body []byte) (*proto.Response, error) {
	node := s.nodeManager.node(toNodeId)
	if node == nil {
		return nil, ErrNodeNotFound
	}
	return node.requestWithContext(ctx, path, body)
}

func (s *Server) Send(toNodeId uint64, msg *proto.Message) error {
	node := s.nodeManager.node(toNodeId)
	if node == nil {
		return ErrNodeNotFound
	}
	return node.send(msg)
}

func (s *Server) OnMessage(f func(msg *proto.Message)) {
	s.onMessageFnc = f
}

func (s *Server) NodeIsOnline(nodeId uint64) bool {
	return s.clusterEventServer.NodeOnline(nodeId)
}

func (s *Server) ProposeChannelMessages(ctx context.Context, channelId string, channelType uint8, logs []replica.Log) ([]icluster.ProposeResult, error) {

	// 加载或创建频道
	ch, err := s.loadOrCreateChannel(ctx, channelId, channelType)
	if err != nil {
		return nil, err
	}

	var (
		results []reactor.ProposeResult
	)
	if !ch.isLeader() { // 如果当前节点不是频道的领导者，向频道的领导者发送提案请求
		resp, err := s.requestChannelProposeMessage(ch.leaderId(), channelId, channelType, logs)
		if err != nil {
			return nil, err
		}
		results = resp.ProposeResults
	} else { // 如果当前节点是频道的领导者，直接提案
		results, err = s.channelManager.proposeAndWait(ctx, channelId, channelType, logs)
		if err != nil {
			return nil, err
		}
	}
	// 将结果转换为接口类型
	iresults := make([]icluster.ProposeResult, len(results))
	for i, result := range results {
		iresults[i] = result
	}
	return iresults, nil
}

func (s *Server) ProposeToSlot(ctx context.Context, slotId uint32, logs []replica.Log) ([]icluster.ProposeResult, error) {

	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		s.Error("ProposeToSlot failed, slot not exist", zap.Uint32("slotId", slotId))
		return nil, ErrSlotNotExist
	}

	var results []reactor.ProposeResult
	var err error
	if slot.Leader != s.opts.NodeId {
		slotLeaderNode := s.nodeManager.node(slot.Leader)
		if slotLeaderNode == nil {
			s.Error("ProposeToSlot failed, slot leader node not exist", zap.Uint32("slotId", slotId), zap.Uint64("slotLeader", slot.Leader))
			return nil, ErrNodeNotExist
		}
		timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ProposeTimeout)
		defer cancel()
		resp, err := slotLeaderNode.requestSlotPropose(timeoutCtx, &SlotProposeReq{
			SlotId: slotId,
			Logs:   logs,
		})
		if err != nil {
			return nil, err
		}
		results = resp.ProposeResults
	} else {
		results, err = s.slotManager.proposeAndWait(ctx, slotId, logs)
		if err != nil {
			return nil, err
		}
	}
	iresults := make([]icluster.ProposeResult, len(results))
	for i, result := range results {
		iresults[i] = result
	}
	return iresults, nil
}

func (s *Server) ProposeDataToSlot(ctx context.Context, slotId uint32, data []byte) (icluster.ProposeResult, error) {
	logId := uint64(s.logIdGen.Generate().Int64())
	results, err := s.ProposeToSlot(ctx, slotId, []replica.Log{
		{
			Id:   logId,
			Data: data,
		},
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

func (s *Server) GetSlotId(v string) uint32 {
	return s.getSlotId(v)
}

func (s *Server) loadOrCreateChannel(ctx context.Context, channelId string, channelType uint8) (*channel, error) {
	start := time.Now()
	slotId := s.getSlotId(channelId)
	slotInfo := s.clusterEventServer.Slot(slotId)
	if slotInfo == nil {
		s.Error("loadOrCreateChannel failed, slot info not exist", zap.Uint32("slotId", slotId))
		return nil, ErrSlotNotExist
	}
	s.channelKeyLock.Lock(channelId)
	defer s.channelKeyLock.Unlock(channelId)

	var ch *channel
	channelHandler := s.channelManager.get(channelId, channelType)
	if channelHandler == nil {
		ch = newChannel(channelId, channelType, s.opts)
	} else {
		ch = channelHandler.(*channel)
	}

	if ch.IsPrepared() && s.clusterEventServer.NodeOnline(ch.leaderId()) { // 如果频道已经准备好了并且领导者在线，直接返回
		return ch, nil
	}

	// 获取频道的分布式配置
	clusterCfg, err := s.getChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("getChannelClusterConfig failed", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		return nil, err
	}

	needProposeCfg := false               // 是否需要提案配置
	if slotInfo.Leader == s.opts.NodeId { // 当前节点是频道所在槽的领导者（意味着此节点有权选举频道的领导）
		if wkdb.IsEmptyChannelClusterConfig(clusterCfg) {
			clusterCfg, err = s.createChannelClusterConfig(channelId, channelType)
			if err != nil {
				s.Error("createChannelClusterConfig failed", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
				return nil, err
			}
			needProposeCfg = true
		}
		if s.needElectionLeader(clusterCfg) { // 判断是否需要选举频道的领导
			// 开始选举频道的领导
			clusterCfg, err = s.electionChannelLeader(ctx, clusterCfg, ch)
			if err != nil {
				s.Error("electionChannelLeader failed", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
				return nil, err
			}
			if wkdb.IsEmptyChannelClusterConfig(clusterCfg) {
				s.Error("electionChannelLeader failed, empty config", zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
				return nil, ErrEmptyChannelClusterConfig
			}
			needProposeCfg = true
		}
	} else {
		// 如果没有配置向频道槽领导请求频道的分布式配置
		if wkdb.IsEmptyChannelClusterConfig(clusterCfg) || !s.clusterEventServer.NodeOnline(clusterCfg.LeaderId) {
			clusterCfg, err = s.requestChannelClusterConfigFromSlotLeader(channelId, channelType)
			if err != nil {
				s.Error("requestChannelClusterConfigFromSlotLeader failed", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
				return nil, err
			}
			// TODO: 这里是否需要将请求到的clusterCfg保存到本地，如果保存，后续clusterCfg更新，此节点将不会跟着更新了，不保存的话，每次都需要请求
		}
	}

	if needProposeCfg {
		// 提案配置到频道所在槽的分布式存储来保存
		err = s.opts.ChannelClusterStorage.Propose(s.cancelCtx, clusterCfg)
		if err != nil {
			s.Error("propose channel cluster config failed", zap.Uint32("slotId", slotId), zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			return nil, err
		}
	}
	if wkutil.ArrayContainsUint64(clusterCfg.Replicas, s.opts.NodeId) { // 只有当前节点在副本列表中才启动频道的分布式
		s.channelManager.remove(ch)
		s.channelManager.add(ch)
		// 启动频道的分布式
		err = ch.bootstrap(clusterCfg)
		if err != nil {
			s.Error("channel bootstrap failed", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			return nil, err
		}
	}
	ch.Info("loadOrCreateChannel success", zap.Duration("cost", time.Since(start)), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
	return ch, nil
}

func (s *Server) createChannelClusterConfig(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {
	allowVoteNodes := s.clusterEventServer.AllowVoteNodes() // 获取允许投票的节点
	if len(allowVoteNodes) == 0 {
		return wkdb.EmptyChannelClusterConfig, ErrNoAllowVoteNode
	}

	clusterConfig := wkdb.ChannelClusterConfig{
		ChannelId:       channelId,
		ChannelType:     channelType,
		ReplicaMaxCount: uint16(s.opts.ChannelMaxReplicaCount),
		Term:            1,
		LeaderId:        s.opts.NodeId,
	}
	replicaIds := make([]uint64, 0, s.opts.ChannelMaxReplicaCount)
	replicaIds = append(replicaIds, s.opts.NodeId) // 默认当前节点是领导，所以加入到副本列表中

	// 随机选择副本
	newAllowVoteNodes := make([]*pb.Node, 0, len(allowVoteNodes))
	newAllowVoteNodes = append(newAllowVoteNodes, allowVoteNodes...)
	rand.Shuffle(len(newAllowVoteNodes), func(i, j int) {
		newAllowVoteNodes[i], newAllowVoteNodes[j] = newAllowVoteNodes[j], newAllowVoteNodes[i]
	})

	for _, allowVoteNode := range newAllowVoteNodes {
		if allowVoteNode.Id == s.opts.NodeId {
			continue
		}
		replicaIds = append(replicaIds, allowVoteNode.Id)
		if len(replicaIds) >= int(s.opts.ChannelMaxReplicaCount) {
			break
		}
	}
	clusterConfig.Replicas = replicaIds
	return clusterConfig, nil
}

func (s *Server) getChannelClusterConfig(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {

	return s.opts.ChannelClusterStorage.Get(channelId, channelType)
}

func (s *Server) needElectionLeader(cfg wkdb.ChannelClusterConfig) bool {

	// 如果频道的领导者为空，说明需要选举领导
	if cfg.LeaderId == 0 {
		return true
	}
	// 如果频道领导不在线，说明需要选举领导
	if !s.clusterEventServer.NodeOnline(cfg.LeaderId) {
		return true
	}
	return false
}

func (s *Server) electionChannelLeader(ctx context.Context, cfg wkdb.ChannelClusterConfig, ch *channel) (wkdb.ChannelClusterConfig, error) {

	resultC := make(chan electionResp, 1)
	req := electionReq{
		ch:      ch,
		cfg:     cfg,
		resultC: resultC,
	}
	// 向选举管理器提交选举请求
	err := s.channelElectionManager.addElectionReq(req)
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}

	select {
	case resp := <-resultC:
		s.Debug("electionChannelLeader success", zap.Uint64("leaderId", resp.cfg.LeaderId), zap.Uint32("term", resp.cfg.Term))
		return resp.cfg, resp.err
	case <-ctx.Done():
		return wkdb.EmptyChannelClusterConfig, ctx.Err()
	case <-s.stopC:
		return wkdb.EmptyChannelClusterConfig, ErrStopped
	}

}

// 从频道所在槽获取频道的分布式信息
func (s *Server) requestChannelClusterConfigFromSlotLeader(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {
	slotId := s.getSlotId(channelId)
	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		return wkdb.EmptyChannelClusterConfig, ErrSlotNotExist
	}
	node := s.nodeManager.node(slot.Leader)
	if node == nil {
		return wkdb.EmptyChannelClusterConfig, fmt.Errorf("not found slot leader node")
	}
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	clusterConfig, err := node.requestChannelClusterConfig(timeoutCtx, &ChannelClusterConfigReq{
		ChannelId:   channelId,
		ChannelType: channelType,
	})
	if err != nil {
		s.Error("requestChannelClusterConfigFromSlotLeader failed", zap.Error(err), zap.Uint64("slotLeader", slot.Leader), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
		return wkdb.EmptyChannelClusterConfig, err
	}
	return clusterConfig, nil
}

func (s *Server) loadChannelClusterConfig(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {
	ch := s.channelManager.get(channelId, channelType)
	if ch != nil { // 如果频道已经存在，直接返回
		return ch.(*channel).cfg, nil
	}
	slotId := s.getSlotId(channelId)
	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		s.Warn("loadChannelOnlyRead failed, slot not exist", zap.Uint32("slotId", slotId))
		return wkdb.EmptyChannelClusterConfig, ErrSlotNotExist
	}

	clusterConfig, err := s.opts.ChannelClusterStorage.Get(channelId, channelType)
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}

	if slot.Leader == s.opts.NodeId {
		if wkdb.IsEmptyChannelClusterConfig(clusterConfig) {
			s.Error("channel cluster config is not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			return wkdb.EmptyChannelClusterConfig, fmt.Errorf("channel cluster config is not found")
		}
	} else if wkdb.IsEmptyChannelClusterConfig(clusterConfig) {
		// 向频道所在槽的领导请求频道的分布式配置（这种情况不需要保存clusterConfig，因为说明此节点不是槽领导也不是频道副本，如果保存clusterConfig，后续clusterConfig更新，则此节点将不会跟着更新了）
		clusterConfig, err = s.requestChannelClusterConfigFromSlotLeader(channelId, channelType)
		if err != nil {
			return wkdb.EmptyChannelClusterConfig, err
		}
	}
	if wkdb.IsEmptyChannelClusterConfig(clusterConfig) {
		s.Error("channel cluster config is not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		return wkdb.EmptyChannelClusterConfig, fmt.Errorf("channel cluster config is not found")
	}
	return clusterConfig, nil
}

func (s *Server) requestChannelProposeMessage(to uint64, channelId string, channelType uint8, logs []replica.Log) (*ChannelProposeResp, error) {
	node := s.nodeManager.node(to)
	if node == nil {
		s.Error("node is not found", zap.Uint64("nodeID", to))
		return nil, ErrNodeNotFound
	}
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	resp, err := node.requestChannelProposeMessage(timeoutCtx, &ChannelProposeReq{
		ChannelId:   channelId,
		ChannelType: channelType,
		Logs:        logs,
	})
	defer cancel()
	if err != nil {
		s.Error("requestChannelProposeMessage failed", zap.Error(err))
		return nil, err
	}
	return resp, nil
}
