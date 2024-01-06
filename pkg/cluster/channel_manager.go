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
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type ChannelManager struct {
	stopper *syncutil.Stopper

	sendSyncNotifyC chan *Channel // 触发发送元数据副本的同步通知
	syncC           chan channelSyncNotify

	channelQueue *channelQueue // 进行中的频道
	wklog.Log

	eachOfPopSize               int // 每次取出的频道数量
	s                           *Server
	channelMetaTransportSync    *channelMetaTransportSync    // 元数据同步协议
	channelMessageTransportSync *channelMessageTransportSync // 消息同步协议
	pebbleStorage               *PebbleStorage
	messageStorage              *MessageStorage
}

func NewChannelManager(s *Server) *ChannelManager {
	return &ChannelManager{
		sendSyncNotifyC:             make(chan *Channel, 100),
		syncC:                       make(chan channelSyncNotify, 100),
		stopper:                     syncutil.NewStopper(),
		channelQueue:                newChannelQueue(),
		Log:                         wklog.NewWKLog(fmt.Sprintf("ChannelManager[%d]", s.opts.NodeID)),
		eachOfPopSize:               10,
		s:                           s,
		pebbleStorage:               NewPebbleStorage(path.Join(s.opts.DataDir, "channellogdb")),
		messageStorage:              NewMessageStorage(s.opts.NodeID, path.Join(s.opts.DataDir, "messagedb"), s.clusterEventManager.GetSlotCount(), s.opts.DecodeMessageFnc),
		channelMetaTransportSync:    newChannelMetaTransportSync(s),
		channelMessageTransportSync: newChannelMessageTransportSync(s),
	}
}

func (c *ChannelManager) Start() error {
	err := c.pebbleStorage.Open()
	if err != nil {
		return err
	}
	err = c.messageStorage.Open()
	if err != nil {
		return err
	}
	c.stopper.RunWorker(c.loop)
	return nil
}

func (c *ChannelManager) Stop() {
	c.stopper.Stop()
	c.pebbleStorage.Close()
	c.messageStorage.Close()

}

// 获取频道并根据需要进行选举
func (c *ChannelManager) GetChannel(channelID string, channelType uint8) (*Channel, error) {
	channel := c.channelQueue.Get(channelID, channelType)
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
			var needPropose = false
			if clusterInfo == nil {
				clusterInfo, err = c.createChannelClusterInfo(channelID, channelType) // 如果槽领导节点不存在频道集群配置，那么此频道集群一定没初始化（注意：是一定没初始化），所以创建一个初始化集群配置
				if err != nil {
					c.Error("create channel cluster info failed", zap.Error(err))
					return nil, err
				}
				needPropose = true
			} else if !c.s.clusterEventManager.NodeIsOnline(clusterInfo.LeaderID) { // 如果频道的领导离线了，则进行重新选举
				leaderID, err := c.electionChannelLeader(clusterInfo) // 选举一个新的领导
				if err != nil {
					return nil, err
				}
				clusterInfo.LeaderID = leaderID
				needPropose = true
			}
			if needPropose {
				c.Debug("提议更新频道集群配置", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Any("clusterInfo", clusterInfo))
				err = c.s.ProposeChannelClusterInfo(clusterInfo) // 更新集群配置（会通知副本同步）
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

	}
	return channel, nil
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
	messageAppliedIndex, err := c.messageStorage.LastIndex(shardNo) // 消息的最新日志下标就是应用下标，因为消息只有日志结构，没有状态机
	if err != nil {
		return nil, err
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
	channel := NewChannel(channelClusterInfo, c.s, metaAppliedIndex, messageAppliedIndex, channelMetaSyncInfos, channelMessageSyncInfos, path.Join(c.s.opts.DataDir, "channels", shardNo), c.channelMetaTransportSync, c.channelMessageTransportSync, c.pebbleStorage, c.messageStorage, c.onMetaApply(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType), c.onMessageApply(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType))
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
		requestGroup.Go(func(rcID uint64) func() error {

			return func() error {
				resp, err := c.s.nodeManager.requestChannelLogInfo(ctx, rcID, &ChannelLogInfoReportRequest{
					ChannelID:   clusterInfo.ChannelID,
					ChannelType: clusterInfo.ChannelType,
				})
				if err != nil {
					return err
				}
				channelLogLock.Lock()
				channelLogInfoMap[rcID] = resp
				channelLogLock.Unlock()
				return nil
			}
		}(replicaID))

	}

	if err := requestGroup.Wait(); err != nil {
		cancel()
		return 0, err
	}
	cancel()

	if len(channelLogInfoMap) == 0 {
		return 0, errors.New("not found channel log info")
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

// TODO：这里可以做成分区的，每个分区有一个goroutine去处理，这样可以提高并发
func (c *ChannelManager) loop() {
	tick := time.NewTicker(time.Millisecond * 500)
	for {
		select {
		case channel := <-c.sendSyncNotifyC: // 领导节点通知副本去同步
			c.channelQueue.Push(channel)
			c.triggerSendNotifySync(channel)
		case <-tick.C: // 定时触发为通知成功的频道
			channels := c.channelQueue.PeekAndBack(c.eachOfPopSize)
			for _, channel := range channels {
				c.triggerSendNotifySync(channel)
			}
		case req := <-c.syncC: // 副本收到频道同步日志的通知
			c.handleSyncNotify(req.channel, req.req)

		case <-c.stopper.ShouldStop():
			return
		}
	}
}

// 领导节点通知所有副本去同步
func (c *ChannelManager) triggerSendNotifySync(ch *Channel) {
	if ch.hasMetaEvent.Load() {
		c.Debug("通知副本同步频道元数据", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
		replicaNodeIDs := ch.metaReplica.TriggerSendNotifySyncIfNeed()
		if len(replicaNodeIDs) == 0 {
			c.Debug("所有副本频道元数据已经同步到最新", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
			ch.hasMetaEvent.Store(false)
			if !ch.hasMessageEvent.Load() {
				c.channelQueue.Remove(ch.channelID, ch.channelType) // 如果频道元数据和消息都已经同步了，那么就从队列中移除
			}
		}
	}
	if ch.hasMessageEvent.Load() {
		c.Debug("通知副本同步频道消息", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
		replicaNodeIDs := ch.messageReplica.TriggerSendNotifySyncIfNeed()
		if len(replicaNodeIDs) == 0 {
			c.Debug("所有副本频道消息已经同步到最新", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
			ch.hasMessageEvent.Store(false)
			if !ch.hasMetaEvent.Load() {
				c.channelQueue.Remove(ch.channelID, ch.channelType) // 如果频道元数据和消息都已经同步了，那么就从队列中移除
			}
		}
	}
}

// 副本处理领导节点的通知
func (c *ChannelManager) handleSyncNotify(ch *Channel, req *replica.SyncNotify) {
	var syncCount int
	var err error
	if ch.hasMetaEvent.Load() {
		syncCount, err = ch.metaReplica.RequestSyncLogsAndNotifyLeaderIfNeed()
		if err != nil {
			c.Warn("副本向领导同步频道元数据日志失败！", zap.Error(err), zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
		} else if syncCount == 0 {
			c.Debug("副本已与领导的频道元数据日志一致！", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
			ch.hasMetaEvent.Store(false)
			if !ch.hasMessageEvent.Load() {
				c.channelQueue.Remove(ch.channelID, ch.channelType) // 如果频道元数据和消息都已经同步了，那么就从队列中移除
			}
		}
	}

	if ch.hasMessageEvent.Load() {
		syncCount, err = ch.messageReplica.RequestSyncLogsAndNotifyLeaderIfNeed()
		if err != nil {
			c.Warn("副本向领导同步频道消息数据日志失败！", zap.Error(err), zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
		} else if syncCount == 0 {
			c.Debug("副本已与领导的频道消息数据日志一致！", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType))
			ch.hasMessageEvent.Store(false)
			if !ch.hasMetaEvent.Load() {
				c.channelQueue.Remove(ch.channelID, ch.channelType) // 如果频道元数据和消息都已经同步了，那么就从队列中移除
			}
		}

	}

}

// 副本节点收到领导节点的通知，触发同步日志请求
func (c *ChannelManager) triggerHandleSyncNotify(channel *Channel, req *replica.SyncNotify) {
	select {
	case c.syncC <- channelSyncNotify{
		channel: channel,
		req:     req,
	}:
	case <-c.stopper.ShouldStop():
		return
	}
}

func (c *ChannelManager) onMetaApply(channelID string, channelType uint8) func(logs []replica.Log) (uint64, error) {

	return func(logs []replica.Log) (uint64, error) {
		c.Debug("onMetaApply--------------->", zap.Any("logs", logs))
		return logs[len(logs)-1].Index, nil
	}
}

func (c *ChannelManager) onMessageApply(channelID string, channelType uint8) func(logs []replica.Log) (uint64, error) {
	return func(logs []replica.Log) (uint64, error) {
		fmt.Println("onMessageApply--------------------->", logs[len(logs)-1].Index)
		return logs[len(logs)-1].Index, nil
	}
}

type channelQueue struct {
	channelIndexMap map[string]int
	channels        []*Channel
	resetCount      int
	sync.Mutex
}

func newChannelQueue() *channelQueue {
	return &channelQueue{
		channelIndexMap: make(map[string]int),
		channels:        make([]*Channel, 0, 1000),
		resetCount:      1000,
	}
}

func (c *channelQueue) Push(ch *Channel) {
	c.Lock()
	defer c.Unlock()
	if len(c.channels) >= c.resetCount {
		c.restIndex()
	}
	if c.exist(ch.channelID, ch.channelType) {
		return
	}
	c.channels = append(c.channels, ch)
	c.channelIndexMap[ch.GetChannelKey()] = len(c.channels) - 1

}

func (c *channelQueue) PeekAndBack(size int) []*Channel {
	c.Lock()
	defer c.Unlock()
	if len(c.channels) == 0 {
		return nil
	}

	var chs []*Channel
	if size > len(c.channels) {
		chs = c.channels
	} else {
		chs = c.channels[:size]
	}
	if len(chs) == 0 {
		return nil
	}
	for i := len(chs) - 1; i >= 0; i-- {
		ch := chs[i]
		c.channels = append(c.channels, ch)
		if ch != nil {
			c.channelIndexMap[ch.GetChannelKey()] = len(c.channels) - 1
		}
	}
	return chs
}

func (c *channelQueue) Remove(channelID string, channelType uint8) {
	c.Lock()
	defer c.Unlock()

	channelKey := GetChannelKey(channelID, channelType)
	idx, ok := c.channelIndexMap[channelKey]
	if !ok {
		return
	}
	c.channels[idx] = nil

	delete(c.channelIndexMap, channelKey)

	c.restIndex()
}

func (c *channelQueue) Get(channelID string, channelType uint8) *Channel {
	c.Lock()
	defer c.Unlock()
	channelKey := GetChannelKey(channelID, channelType)
	idx, ok := c.channelIndexMap[channelKey]
	if !ok {
		return nil
	}
	return c.channels[idx]
}

func (c *channelQueue) Exist(channelID string, channelType uint8) bool {
	c.Lock()
	defer c.Unlock()
	return c.exist(channelID, channelType)
}

func (c *channelQueue) exist(channelID string, channelType uint8) bool {
	channelKey := GetChannelKey(channelID, channelType)
	_, ok := c.channelIndexMap[channelKey]
	return ok
}

func (c *channelQueue) restIndex() {
	newChannels := make([]*Channel, 0, len(c.channels))
	for _, channel := range c.channels {
		if channel != nil {
			newChannels = append(newChannels, channel)
		}
	}
	c.channels = newChannels

	for idx, ch := range newChannels {
		c.channelIndexMap[ch.GetChannelKey()] = idx
	}
}
