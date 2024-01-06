package cluster

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Channel struct {
	metaReplica        *replica.RawReplica // 频道元数据副本
	messageReplica     *replica.RawReplica // 频道消息副本
	channelID          string
	channelType        uint8
	s                  *Server
	hasMessageEvent    atomic.Bool // 是否有消息事件
	hasMetaEvent       atomic.Bool // 是否有元数据事件
	messageProposeLock sync.Mutex

	clusterInfo *ChannelClusterInfo // 频道集群信息
}

func NewChannel(channelClusterInfo *ChannelClusterInfo, s *Server, metaAppliedIndex uint64, messageAppliedIndex uint64, channelMetaSyncInfos []*replica.SyncInfo, channelMessageSyncInfos []*replica.SyncInfo, dataDir string, metaTransport replica.ITransport, messageTransport replica.ITransport, metaStorage IShardLogStorage, messageStorage IShardLogStorage, onMetaApply func(logs []replica.Log) (uint64, error), onMessageApply func(logs []replica.Log) (uint64, error)) *Channel {
	shardNo := GetChannelKey(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType)

	metaLastSyncInfoMap := make(map[uint64]replica.SyncInfo)
	for _, channelMetaSyncInfo := range channelMetaSyncInfos {
		metaLastSyncInfoMap[channelMetaSyncInfo.NodeID] = *channelMetaSyncInfo
	}

	messageLastSyncInfoMap := make(map[uint64]replica.SyncInfo)
	for _, channelMessageSyncInfo := range channelMessageSyncInfos {
		messageLastSyncInfoMap[channelMessageSyncInfo.NodeID] = *channelMessageSyncInfo
	}

	return &Channel{
		channelID:   channelClusterInfo.ChannelID,
		channelType: channelClusterInfo.ChannelType,
		clusterInfo: channelClusterInfo,
		s:           s,
		metaReplica: replica.NewRawReplica(
			s.opts.NodeID,
			shardNo,
			replica.WithReplicas(channelClusterInfo.Replicas),
			replica.WithTransport(metaTransport),
			replica.WithAppliedIndex(metaAppliedIndex),
			replica.WithStorage(newProxyReplicaStorage(shardNo, metaStorage)),
			replica.WithOnApply(onMetaApply),
			replica.WithLastSyncInfoMap(metaLastSyncInfoMap),
		),
		messageReplica: replica.NewRawReplica(
			s.opts.NodeID,
			shardNo,
			replica.WithReplicas(channelClusterInfo.Replicas),
			replica.WithTransport(messageTransport),
			replica.WithAppliedIndex(messageAppliedIndex),
			replica.WithStorage(newProxyReplicaStorage(shardNo, messageStorage)),
			replica.WithOnApply(onMessageApply),
			replica.WithLastSyncInfoMap(messageLastSyncInfoMap),
		),
	}
}

func (c *Channel) ProposeMeta(data []byte) error {
	c.s.Debug("ProposeToMeta------------->")
	err := c.metaReplica.ProposeOnlyLocal(data)
	if err != nil {
		return err
	}
	c.hasMetaEvent.Store(true)
	select {
	case c.s.channelManager.sendSyncNotifyC <- c:
	case <-c.s.channelManager.stopper.ShouldStop():
	}
	err = c.CheckAndCommitLogsOfMeta()
	if err != nil {
		c.s.Panic("CheckAndCommitLogs failed", zap.Error(err))
	}
	return nil
}

// 提交消息并返回最新日志下标
func (c *Channel) ProposeMessage(data []byte) (uint64, error) {
	if len(data) == 0 {
		return 0, nil
	}
	c.messageProposeLock.Lock()
	defer c.messageProposeLock.Unlock()

	c.s.Debug("开始提案消息数据", zap.Any("data", data))

	lastIndex, err := c.messageReplica.LastIndex()
	if err != nil {
		return 0, err
	}
	err = c.messageReplica.ProposeOnlyLocal(data)
	if err != nil {
		return 0, err
	}

	c.hasMessageEvent.Store(true)

	select {
	case c.s.channelManager.sendSyncNotifyC <- c:
	case <-c.s.channelManager.stopper.ShouldStop():
	}
	err = c.CheckAndCommitLogsOfMessage()
	if err != nil {
		c.s.Panic("CheckAndCommitLogs failed", zap.Error(err))
	}
	c.s.Debug("消息数据提案完成！", zap.Any("data", data))
	return lastIndex, nil
}

// 批量提案消息并返回对应日志下标
func (c *Channel) ProposeMessages(dataList [][]byte) (uint64, error) {
	if len(dataList) == 0 {
		return 0, nil
	}

	c.messageProposeLock.Lock()
	defer c.messageProposeLock.Unlock()

	lastIndex, err := c.messageReplica.LastIndex()
	if err != nil {
		return 0, err
	}
	for _, data := range dataList {
		err = c.messageReplica.ProposeOnlyLocal(data)
		if err != nil {
			return 0, err
		}
	}
	c.hasMessageEvent.Store(true)

	select {
	case c.s.channelManager.sendSyncNotifyC <- c:
	case <-c.s.channelManager.stopper.ShouldStop():
	}
	err = c.CheckAndCommitLogsOfMessage()
	if err != nil {
		c.s.Panic("CheckAndCommitLogs failed", zap.Error(err))
	}
	return lastIndex, nil
}

func (c *Channel) GetLogs(startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	return c.metaReplica.GetLogs(startLogIndex, limit)
}

func (c *Channel) SetLeaderID(leaderID uint64) {
	c.metaReplica.SetLeaderID(leaderID)
	c.messageReplica.SetLeaderID(leaderID)
}

func (c *Channel) IsLeader() bool {

	return c.metaReplica.IsLeader()
}

func (c *Channel) LeaderID() uint64 {
	return c.metaReplica.LeaderID()
}

func (c *Channel) SetReplicas(replicas []uint64) {
	c.metaReplica.SetReplicas(replicas)
	c.messageReplica.SetReplicas(replicas)
}

func (c *Channel) SyncMetaLogs(nodeID uint64, startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	return c.metaReplica.SyncLogs(nodeID, startLogIndex, limit)
}

func (c *Channel) SyncMessageLogs(nodeID uint64, startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	return c.messageReplica.SyncLogs(nodeID, startLogIndex, limit)
}
func (c *Channel) CheckAndCommitLogsOfMeta() error {
	return c.metaReplica.CheckAndCommitLogs()
}

func (c *Channel) CheckAndCommitLogsOfMessage() error {
	return c.messageReplica.CheckAndCommitLogs()
}

func (c *Channel) GetChannelKey() string {
	return GetChannelKey(c.channelID, c.channelType)
}

func (c *Channel) GetClusterInfo() *ChannelClusterInfo {
	return c.clusterInfo
}

func GetChannelKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%d-%s", channelType, channelID)
}

func GetChannelFromChannelKey(channelKey string) (string, uint8) {
	var channelID string
	var channelType uint8
	channelStrs := strings.Split(channelKey, "-")
	if len(channelStrs) == 2 {
		channelTypeI64, _ := strconv.ParseUint(channelStrs[0], 10, 64)
		channelType = uint8(channelTypeI64)
		channelID = channelStrs[1]
	}
	return channelID, channelType
}
