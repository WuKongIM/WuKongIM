package cluster

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type ChannelManager struct {
	stopper        *syncutil.Stopper
	channelCache   *lru.Cache[string, *Channel] // 频道缓存
	channelKeyLock *keylock.KeyLock

	wklog.Log

	eachOfPopSize               int // 每次取出的频道数量
	s                           *Server
	channelMetaTransportSync    *channelMetaTransportSync    // 元数据同步协议
	channelMessageTransportSync *channelMessageTransportSync // 消息同步协议
	pebbleStorage               *PebbleStorage
}

func NewChannelManager(s *Server) *ChannelManager {
	ch := &ChannelManager{
		stopper:                     syncutil.NewStopper(),
		Log:                         wklog.NewWKLog(fmt.Sprintf("ChannelManager[%d]", s.opts.NodeID)),
		eachOfPopSize:               10,
		s:                           s,
		pebbleStorage:               NewPebbleStorage(path.Join(s.opts.DataDir, "channellogdb")),
		channelMetaTransportSync:    newChannelMetaTransportSync(s),
		channelMessageTransportSync: newChannelMessageTransportSync(s),
		channelKeyLock:              keylock.NewKeyLock(),
	}

	channelCache, err := lru.NewWithEvict(s.opts.ChannelMaxCacheCount, func(key string, value *Channel) {
	})
	if err != nil {
		ch.Panic("new channel cache failed", zap.Error(err))
	}
	ch.channelCache = channelCache
	return ch
}

func (c *ChannelManager) Start() error {
	err := c.pebbleStorage.Open()
	if err != nil {
		return err
	}
	c.stopper.RunWorker(c.channelKeyLock.StartCleanLoop)
	return nil
}

func (c *ChannelManager) Stop() {
	c.channelKeyLock.StopCleanLoop()
	c.stopper.Stop()
	c.pebbleStorage.Close()

}

// 获取频道并根据需要进行选举
func (c *ChannelManager) GetChannel(channelID string, channelType uint8) (*Channel, error) {
	channelKey := GetChannelKey(channelID, channelType)
	c.channelKeyLock.Lock(channelKey)
	defer c.channelKeyLock.Unlock(channelKey)

	channel, _ := c.channelCache.Get(channelKey)
	if channel == nil {
		slotID := c.s.GetSlotID(channelID)
		slot := c.s.clusterEventManager.GetSlot(slotID)
		if slot == nil {
			return nil, fmt.Errorf("not found slot[%d]", slotID)
		}
		clusterInfo, err := c.s.GetChannelClusterInfo(channelID, channelType)
		if err != nil {
			return nil, err
		}
		if slot.Leader == c.s.opts.NodeID { // 频道所在槽的领导是当前节点
			if clusterInfo == nil { // 没有集群信息则创建一个新的集群信息
				clusterInfo, err = c.createChannelClusterInfo(channelID, channelType) // 如果槽领导节点不存在频道集群配置，那么此频道集群一定没初始化（注意：是一定没初始化），所以创建一个初始化集群配置
				if err != nil {
					c.Error("create channel cluster info failed", zap.Error(err))
					return nil, err
				}
				c.Debug("提议更新频道集群配置", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Any("clusterInfo", clusterInfo))
				err = c.s.ProposeChannelClusterInfoToSlot(clusterInfo) // 更新集群配置（会通知副本同步）
				if err != nil {
					return nil, err
				}
			}

		} else {
			if clusterInfo == nil { // 如果非领导节点的频道集群信息为空，则去请求频道所在槽领导节点的频道集群信息，然后更新到本地
				clusterInfo, err = c.requestChannelCluster(channelID, channelType)
				if err != nil {
					c.Error("request channel cluster info failed", zap.Error(err))
					return nil, err
				}
				err = c.s.stateMachine.saveChannelClusterInfo(clusterInfo) // 保存频道集群信息到本节点（仅仅保存到节点本地）
				if err != nil {
					c.Error("save channel cluster info failed", zap.Error(err))
					return nil, err
				}
			}
		}
		if clusterInfo == nil {
			return nil, fmt.Errorf("not found cluster info")
		}
		channel, err = c.newChannelByClusterInfo(clusterInfo)
		if err != nil {
			c.Error("newChannelByClusterInfo failed", zap.Error(err))
			return nil, err
		}
		c.channelCache.Add(GetChannelKey(channelID, channelType), channel)
	}
	err := c.electionIfNeed(channel)
	if err != nil {
		c.Error("频道选举失败！", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return nil, err
	}
	c.sendApplyClusterInfoEventIfNeed(channel) // 根据需要发送应用集群配置事件

	return channel, nil
}

// 根据需要发送应用集群配置事件
func (c *ChannelManager) sendApplyClusterInfoEventIfNeed(channel *Channel) {
	if channel.IsLeader() && !channel.applyClusterInfoEventAdded.Load() {
		sendEvent := false
		for _, replicaID := range channel.clusterInfo.Replicas {
			exist := false
			for _, appyReplicaID := range channel.clusterInfo.ApplyReplicas {
				if replicaID == appyReplicaID {
					exist = true
					break
				}
			}
			if !exist {
				sendEvent = true
			}
		}
		if sendEvent {
			c.s.channelEventWorkerManager.AddEvent(ChannelEvent{
				Channel:   channel,
				EventType: ChannelEventTypeSendApplyClusterInfo,
				Priority:  PriorityHigh,
			})
			channel.applyClusterInfoEventAdded.Store(true)
		}
	}
}

func (c *ChannelManager) UpdateChannelCacheIfNeed(cc *ChannelClusterInfo) {
	ch, ok := c.channelCache.Get(GetChannelKey(cc.ChannelID, cc.ChannelType))
	if ok {
		ch.SetClusterInfo(cc)
	}
}

func (c *ChannelManager) electionIfNeed(channel *Channel) error {
	clusterInfo := channel.clusterInfo
	if clusterInfo == nil {
		return errors.New("channel clusterinfo is not found")
	}
	channelID := clusterInfo.ChannelID
	channelType := clusterInfo.ChannelType
	slotID := c.s.GetSlotID(channelID)
	slot := c.s.clusterEventManager.GetSlot(slotID)
	if slot == nil {
		return fmt.Errorf("not found slot[%d]", slotID)
	}

	if slot.Leader != c.s.opts.NodeID { // 频道所在槽的领导不是当前节点(只有频道所在槽的领导才有权限进行选举)
		if !c.s.clusterEventManager.NodeIsOnline(clusterInfo.LeaderID) { // 如果领导不在线了,频道的集群配置肯定变更了，则重新获取集群配置
			newClusterInfo, err := c.requestChannelCluster(channelID, channelType)
			if err != nil {
				c.Error("向槽领导节点获取频道集群配置失败！", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
				return err
			}
			err = c.s.stateMachine.saveChannelClusterInfo(newClusterInfo) // 保存频道集群信息到本节点（仅仅保存到节点本地）
			if err != nil {
				c.Error("save channel cluster info failed", zap.Error(err))
				return err
			}
			channel.SetClusterInfo(newClusterInfo)
			return nil
		}
		return nil
	}

	if c.s.clusterEventManager.NodeIsOnline(clusterInfo.LeaderID) { // 领导在线，不需要进行选举
		return nil
	}

	// 开始进行选举
	leaderID, err := c.electionChannelLeader(clusterInfo) // 选举一个新的领导
	if err != nil {
		return err
	}
	clusterInfo.LeaderID = leaderID
	clusterInfo.ApplyReplicas = nil
	clusterInfo.ConfigVersion = uint64(time.Now().UnixNano())
	channel.applyClusterInfoEventAdded.Store(false)
	channel.SetClusterInfo(clusterInfo)

	c.Debug("提议更新频道集群配置", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Any("clusterInfo", clusterInfo))
	err = c.s.ProposeChannelClusterInfoToSlot(clusterInfo) // 更新集群配置（会通知副本同步）
	if err != nil {
		return err
	}

	return nil
}

// 请求频道的集群信息
func (c *ChannelManager) requestChannelCluster(channelID string, channelType uint8) (*ChannelClusterInfo, error) {
	slotID := c.s.GetSlotID(channelID)
	slot := c.s.clusterEventManager.GetSlot(slotID)
	if slot == nil {
		return nil, fmt.Errorf("not found slot[%d]", slotID)
	}
	c.Debug("向槽领导请求频道集群配置", zap.Uint32("slotID", slotID), zap.Uint64("slotLeaderID", slot.Leader), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
	clusterInfo, err := c.s.nodeManager.requestChannelClusterInfo(c.s.cancelCtx, slot.Leader, &ChannelClusterInfoRequest{
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	return clusterInfo, err
}

// 进行频道选举
func (c *ChannelManager) createChannelClusterInfo(channelID string, channelType uint8) (*ChannelClusterInfo, error) {
	onlineNodes := c.s.clusterEventManager.GetAllOnlineNode() // 在线节点

	clusterInfo := &ChannelClusterInfo{
		ChannelID:       channelID,
		ChannelType:     channelType,
		ReplicaMaxCount: c.s.opts.ChannelReplicaCount,
		ConfigVersion:   uint64(time.Now().UnixNano()),
	}
	replicaIDs := make([]uint64, 0, c.s.opts.ChannelReplicaCount)

	// 选定当前槽领导节点作为频道领导节点（发送消息的时候这样可以省一次网络转发）
	clusterInfo.LeaderID = c.s.opts.NodeID
	replicaIDs = append(replicaIDs, c.s.opts.NodeID)

	// 随机选择副本
	newOnlineNodes := make([]*pb.Node, 0, len(onlineNodes))
	newOnlineNodes = append(newOnlineNodes, onlineNodes...)
	rand.Shuffle(len(newOnlineNodes), func(i, j int) {
		newOnlineNodes[i], newOnlineNodes[j] = newOnlineNodes[j], newOnlineNodes[i]
	})

	for _, onlineNode := range newOnlineNodes {
		if onlineNode.Id != clusterInfo.LeaderID {
			replicaIDs = append(replicaIDs, onlineNode.Id)
		}
		if len(replicaIDs) >= int(c.s.opts.ChannelReplicaCount) {
			break
		}
	}
	clusterInfo.Replicas = replicaIDs

	c.Debug("频道集群配置初始化成功！", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("leaderID", clusterInfo.LeaderID), zap.Any("replicas", clusterInfo.Replicas))

	return clusterInfo, nil
}

func (c *ChannelManager) newChannelByClusterInfo(channelClusterInfo *ChannelClusterInfo) (*Channel, error) {
	shardNo := GetChannelKey(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType)
	// 获取当前节点已应用的日志
	metaAppliedIndex, err := c.pebbleStorage.GetAppliedIndex(shardNo)
	if err != nil {
		return nil, err
	}
	var messageAppliedIndex uint64
	if c.s.opts.MessageLogStorage != nil {
		messageAppliedIndex, err = c.s.opts.MessageLogStorage.LastIndex(shardNo) // 消息的最新日志下标就是应用下标，因为消息只有日志结构，没有状态机
		if err != nil {
			return nil, err
		}
	}

	var channelMetaSyncInfos []*replica.SyncInfo
	var channelMessageSyncInfos []*replica.SyncInfo

	if channelClusterInfo.LeaderID == c.s.opts.NodeID {
		channelMetaSyncInfos, err = c.s.stateMachine.getChannelSyncInfos(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType, LogKindMeta)
		if err != nil {
			c.Error("getChannelSyncInfos failed", zap.Error(err))
			return nil, err
		}
		channelMessageSyncInfos, err = c.s.stateMachine.getChannelSyncInfos(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType, LogKindMessage)
		if err != nil {
			c.Error("getChannelSyncInfos failed", zap.Error(err))
			return nil, err
		}
	}

	c.Debug("频道初始化成功", zap.String("channelID", channelClusterInfo.ChannelID), zap.Uint8("channelType", channelClusterInfo.ChannelType), zap.Uint64("leaderID", channelClusterInfo.LeaderID), zap.Uint64s("replicas", channelClusterInfo.Replicas), zap.Uint64("metaAppliedIndex", metaAppliedIndex), zap.Uint64("messageAppliedIndex", messageAppliedIndex))
	channel := NewChannel(channelClusterInfo, c.s, metaAppliedIndex, messageAppliedIndex, channelMetaSyncInfos, channelMessageSyncInfos, path.Join(c.s.opts.DataDir, "channels", shardNo), c.channelMetaTransportSync, c.channelMessageTransportSync, c.pebbleStorage, c.s.opts.MessageLogStorage, c.onMetaApply(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType), c.onMessageApply(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType))
	channel.SetLeaderID(channelClusterInfo.LeaderID)
	return channel, nil
}

// 选举频道领导
func (c *ChannelManager) electionChannelLeader(clusterInfo *ChannelClusterInfo) (uint64, error) {

	c.Debug("开始频道领导选举", zap.String("channelID", clusterInfo.ChannelID), zap.Uint8("channelType", clusterInfo.ChannelType))

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	requestGroup, ctx := errgroup.WithContext(timeoutCtx)

	shardNo := GetChannelKey(clusterInfo.ChannelID, clusterInfo.ChannelType)
	channelLogLock := sync.Mutex{}
	channelLogInfoMap := make(map[uint64]*ChannelLogInfoReportResponse, 0)
	for _, replicaID := range clusterInfo.Replicas {
		if replicaID == c.s.opts.NodeID {
			lastLogIndex, err := c.s.channelManager.pebbleStorage.LastIndex(shardNo)
			if err != nil {
				cancel()
				return 0, err
			}
			channelLogLock.Lock()
			channelLogInfoMap[replicaID] = &ChannelLogInfoReportResponse{
				LogIndex:        lastLogIndex,
				MessageLogIndex: 0,
			}
			channelLogLock.Unlock()
			continue
		}
		if !c.s.clusterEventManager.NodeIsOnline(replicaID) {
			continue
		}
		requestGroup.Go(func(rcID uint64) func() error {

			return func() error {
				resp, err := c.s.nodeManager.requestChannelLogInfo(ctx, rcID, &ChannelLogInfoReportRequest{
					ChannelID:   clusterInfo.ChannelID,
					ChannelType: clusterInfo.ChannelType,
				})
				if err != nil {
					return nil // TODO: 这里不要返回err 返回err后后续的请求将不会再执行
				}
				channelLogLock.Lock()
				channelLogInfoMap[rcID] = resp
				channelLogLock.Unlock()
				return nil
			}
		}(replicaID))

	}
	_ = requestGroup.Wait()
	cancel()

	if len(channelLogInfoMap) == 0 {
		return 0, errors.New("not found channel log info")
	}
	if len(channelLogInfoMap) < len(c.s.clusterEventManager.GetAllowVoteNodes())/2 {
		return 0, errors.New("replica node not enough,can not election channel leader")
	}
	leaderID := c.channelLeaderIDByLogInfo(channelLogInfoMap)

	c.Debug("频道领导选举成功！", zap.Uint64("leaderID", leaderID), zap.String("channelID", clusterInfo.ChannelID), zap.Uint8("channelType", clusterInfo.ChannelType))
	return leaderID, nil
}

// 通过日志高度选举频道领导
func (c *ChannelManager) channelLeaderIDByLogInfo(channelLogInfoMap map[uint64]*ChannelLogInfoReportResponse) uint64 {
	var leaderID uint64 = 0
	var leaderLogIndex uint64 = 0
	var leaderMessageLogIndex uint64 = 0
	for nodeID, resp := range channelLogInfoMap {
		if resp.LogIndex > leaderLogIndex {
			leaderID = nodeID
			leaderLogIndex = resp.LogIndex
			leaderMessageLogIndex = resp.MessageLogIndex
		} else if resp.LogIndex == leaderLogIndex && resp.MessageLogIndex > leaderMessageLogIndex {
			leaderID = nodeID
			leaderLogIndex = resp.LogIndex
			leaderMessageLogIndex = resp.MessageLogIndex
		} else if resp.LogIndex == leaderLogIndex && resp.MessageLogIndex == leaderMessageLogIndex {
			if leaderID == 0 || nodeID < leaderID {
				leaderID = nodeID
				leaderLogIndex = resp.LogIndex
				leaderMessageLogIndex = resp.MessageLogIndex
			}
		}
	}
	return leaderID
}

func (c *ChannelManager) AddRetryEvent(event ChannelEvent) {

}

// 副本收到领导同步元数据日志的通知
func (c *ChannelManager) recvMetaLogSyncNotify(channel *Channel, req *replica.SyncNotify) {
	c.s.channelEventWorkerManager.AddEvent(ChannelEvent{
		Channel:   channel,
		EventType: ChannelEventTypeSyncMetaLogs,
		Priority:  PriorityHigh,
	})
}

// 副本收到领导同步消息日志的通知
func (c *ChannelManager) recvMessageLogSyncNotify(channel *Channel, req *replica.SyncNotify) {
	c.s.channelEventWorkerManager.AddEvent(ChannelEvent{
		Channel:   channel,
		EventType: ChannelEventTypeSyncMessageLogs,
		Priority:  PriorityHigh,
	})
}

func (c *ChannelManager) onMetaApply(channelID string, channelType uint8) func(logs []replica.Log) (uint64, error) {

	return func(logs []replica.Log) (uint64, error) {
		if c.s.opts.OnChannelMetaApply != nil {
			err := c.s.opts.OnChannelMetaApply(channelID, channelType, logs)
			if err != nil {
				return 0, err
			}
		}
		return logs[len(logs)-1].Index, nil
	}
}

func (c *ChannelManager) onMessageApply(channelID string, channelType uint8) func(logs []replica.Log) (uint64, error) {
	return func(logs []replica.Log) (uint64, error) {
		return logs[len(logs)-1].Index, nil
	}
}
