package channel

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
	rafttype "github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type Server struct {
	raftGroups []*raftgroup.RaftGroup
	opts       *Options
	storage    *storage
	wklog.Log

	// 正在唤醒的频道
	wake struct {
		sync.RWMutex
		channels map[string]bool
	}
}

func NewServer(opts *Options) *Server {
	s := &Server{
		opts: opts,
		Log:  wklog.NewWKLog("channel.Server"),
	}
	s.storage = newStorage(opts.Store.WKDB())
	for i := 0; i < opts.GroupCount; i++ {
		rg := raftgroup.New(raftgroup.NewOptions(raftgroup.WithTransport(opts.Transport), raftgroup.WithStorage(s.storage)))
		s.raftGroups = append(s.raftGroups, rg)
	}
	s.wake.channels = make(map[string]bool)

	return s
}

func (s *Server) Start() error {

	for _, rg := range s.raftGroups {
		err := rg.Start()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) Stop() {
	for _, rg := range s.raftGroups {
		rg.Stop()
	}
}

// 获取配置
func (s *Server) getOrCreateChannelClusterConfigFromSlotLeader(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {
	channelKey := wkutil.ChannelToKey(channelId, channelType)
	rg := s.getRaftGroup(channelKey)
	raft := rg.GetRaft(channelKey)
	if raft != nil {
		ch := raft.(*Channel)
		if ch.IsLeader() {
			return ch.cfg, nil
		}
	}
	return s.opts.Cluster.GetOrCreateChannelClusterConfigFromSlotLeader(channelId, channelType)
}

// 唤醒频道领导
func (s *Server) WakeLeaderIfNeed(clusterConfig wkdb.ChannelClusterConfig) error {
	channelKey := wkutil.ChannelToKey(clusterConfig.ChannelId, clusterConfig.ChannelType)
	rg := s.getRaftGroup(channelKey)

	raft := rg.GetRaft(channelKey)
	if raft != nil {
		return nil
	}

	if clusterConfig.LeaderId != s.opts.NodeId {
		return nil
	}
	ch, err := createChannel(clusterConfig, s)
	if err != nil {
		return err
	}
	rg.AddRaft(ch)
	return nil
}

func (s *Server) WakeFollowerfNeed(channelId string, channelType uint8) error {
	clusterConfig, err := s.opts.Cluster.GetOrCreateChannelClusterConfigFromSlotLeader(channelId, channelType)
	if err != nil {
		return err
	}
	isReplica := false
	for _, nodeId := range clusterConfig.Replicas {
		if nodeId == s.opts.NodeId {
			isReplica = true
			break
		}
	}
	if !isReplica {
		for _, nodeId := range clusterConfig.Learners {
			if nodeId == s.opts.NodeId {
				isReplica = true
				break
			}
		}
	}

	channelKey := wkutil.ChannelToKey(clusterConfig.ChannelId, clusterConfig.ChannelType)
	rg := s.getRaftGroup(channelKey)
	if isReplica {
		ch, err := createChannel(clusterConfig, s)
		if err != nil {
			return err
		}
		rg.AddRaft(ch)
	}
	return nil
}

// 异步唤醒频道副本
func (s *Server) wakeFollowerIfNeedAsync(channelId string, channelType uint8) {

	s.wake.Lock()
	channelKey := wkutil.ChannelToKey(channelId, channelType)
	if ok := s.wake.channels[channelKey]; ok {
		s.wake.Unlock()
		return
	}
	s.wake.channels[channelKey] = true
	s.wake.Unlock()

	go func() {
		err := s.WakeFollowerfNeed(channelId, channelType)
		if err != nil {
			s.Error("wake channel failed", zap.Error(err))
		}
		s.wake.Lock()
		delete(s.wake.channels, channelKey)
		s.wake.Unlock()
	}()
}

func (s *Server) AddEvent(channelId string, channelType uint8, e rafttype.Event) {

	// 添加事件到对应的频道
	channelKey := wkutil.ChannelToKey(channelId, channelType)
	rg := s.getRaftGroup(channelKey)

	// 如果领导发过来的ping消息，则需要判断是否需要唤醒副本频道
	if rg == nil && e.Type == rafttype.Ping {
		s.wakeFollowerIfNeedAsync(channelId, channelType)
	} else {
		rg.AddEvent(channelKey, e)
	}

}

func (s *Server) getRaftGroup(channelKey string) *raftgroup.RaftGroup {
	index := int(fnv32(channelKey) % uint32(s.opts.GroupCount))
	return s.raftGroups[index]
}

func fnv32(key string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	hash := offset32
	for i := 0; i < len(key); i++ {
		hash ^= int(key[i])
		hash *= prime32
	}
	return uint32(hash)
}

// 创建一个频道的分布式配置
func (s *Server) createChannelClusterConfig(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {
	allowVoteNodes := s.opts.Node.AllowVoteAndJoinedNodes() // 获取允许投票的在线节点
	if len(allowVoteNodes) == 0 {
		return wkdb.EmptyChannelClusterConfig, ErrNoAllowVoteNode
	}

	createdAt := time.Now()
	updatedAt := time.Now()
	clusterConfig := wkdb.ChannelClusterConfig{
		ChannelId:       channelId,
		ChannelType:     channelType,
		ReplicaMaxCount: uint16(s.opts.ChannelMaxReplicaCount),
		Term:            1,
		LeaderId:        s.opts.NodeId,
		CreatedAt:       &createdAt,
		UpdatedAt:       &updatedAt,
	}
	replicaIds := make([]uint64, 0, s.opts.ChannelMaxReplicaCount)
	replicaIds = append(replicaIds, s.opts.NodeId) // 默认当前节点是领导，所以加入到副本列表中

	// 随机选择副本
	newAllowVoteNodes := make([]*types.Node, 0, len(allowVoteNodes))
	newAllowVoteNodes = append(newAllowVoteNodes, allowVoteNodes...)
	rand.Shuffle(len(newAllowVoteNodes), func(i, j int) {
		newAllowVoteNodes[i], newAllowVoteNodes[j] = newAllowVoteNodes[j], newAllowVoteNodes[i]
	})

	for _, allowVoteNode := range newAllowVoteNodes {
		if allowVoteNode.Id == s.opts.NodeId {
			continue
		}
		if len(replicaIds) >= int(s.opts.ChannelMaxReplicaCount) {
			break
		}
		replicaIds = append(replicaIds, allowVoteNode.Id)

	}
	clusterConfig.Replicas = replicaIds
	return clusterConfig, nil
}
