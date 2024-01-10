package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Channel struct {
	metaReplica    *replica.RawReplica // 频道元数据副本
	messageReplica *replica.RawReplica // 频道消息副本
	channelID      string
	channelType    uint8
	s              *Server

	clusterInfo *ChannelClusterInfo // 频道集群信息

	wklog.Log

	applyClusterInfoEventAdded atomic.Bool // 是否已添加应用频道集群信息事件
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
		Log:         wklog.NewWKLog(fmt.Sprintf("channel[%s:%d]", channelClusterInfo.ChannelID, channelClusterInfo.ChannelType)),
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
	err := c.metaReplica.ProposeOnlyLocal(data)
	if err != nil {
		return err
	}

	c.s.channelEventWorkerManager.AddEvent(ChannelEvent{
		EventType: ChannelEventTypeSendMetaNotifySync,
		Channel:   c,
		Priority:  PriorityHigh,
	})
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

	c.s.Debug("开始提案消息数据", zap.Any("data", data))

	lastIndex, err := c.messageReplica.LastIndex()
	if err != nil {
		return 0, err
	}
	err = c.messageReplica.ProposeOnlyLocal(data)
	if err != nil {
		return 0, err
	}

	c.s.channelEventWorkerManager.AddEvent(ChannelEvent{
		EventType: ChannelEventTypeSendMessageNotifySync,
		Channel:   c,
		Priority:  PriorityHigh,
	})

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

	startTime := time.Now().UnixMilli()
	c.Debug("开始批量提案消息数据", zap.ByteStrings("data", dataList))
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
	c.s.channelEventWorkerManager.AddEvent(ChannelEvent{
		EventType: ChannelEventTypeSendMessageNotifySync,
		Channel:   c,
		Priority:  PriorityHigh,
	})

	err = c.CheckAndCommitLogsOfMessage()
	if err != nil {
		c.s.Panic("CheckAndCommitLogs failed", zap.Error(err))
	}
	c.Debug("批量提案消息数据完成！", zap.Int64("cost", time.Now().UnixMilli()-startTime), zap.ByteStrings("data", dataList))
	return lastIndex, nil
}

func (c *Channel) GetLogs(startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	return c.metaReplica.GetLogs(startLogIndex, limit)
}

func (c *Channel) SetLeaderID(leaderID uint64) {
	c.metaReplica.SetLeaderID(leaderID)
	c.messageReplica.SetLeaderID(leaderID)
}

func (c *Channel) SetClusterInfo(cl *ChannelClusterInfo) {
	c.clusterInfo = cl
	c.messageReplica.SetReplicas(cl.Replicas)
	c.messageReplica.SetLeaderID(cl.LeaderID)
	c.metaReplica.SetReplicas(cl.Replicas)
	c.metaReplica.SetLeaderID(cl.LeaderID)
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

// 领导节点通知所有副本去同步
func (c *Channel) processEvent(event ChannelEvent) {
	if event.EventType == ChannelEventTypeSendMetaNotifySync { // 发送元数据同步通知 （领导执行）
		startTime := time.Now().UnixMilli()
		c.Debug("通知副本同步频道元数据", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		replicaNodeIDs := c.metaReplica.TriggerSendNotifySyncIfNeed()
		c.Debug("完成通知副本同步频道消息", zap.Int64("cost", time.Now().UnixMilli()-startTime), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		if len(replicaNodeIDs) == 0 {
			c.Debug("所有副本频道元数据已经同步到最新", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		} else {
			event.retry++
			c.s.channelManager.AddRetryEvent(event)
		}
	} else if event.EventType == ChannelEventTypeSendMessageNotifySync { // // 发送元数据同步通知 （领导执行）
		startTime := time.Now().UnixMilli()
		c.Debug("通知副本同步频道消息", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		replicaNodeIDs := c.messageReplica.TriggerSendNotifySyncIfNeed()
		c.Debug("完成通知副本同步频道消息", zap.Int64("cost", time.Now().UnixMilli()-startTime), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		if len(replicaNodeIDs) == 0 {
			c.Debug("所有副本频道消息已经同步到最新", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		} else {
			event.retry++
			c.s.channelManager.AddRetryEvent(event)
		}
	} else if event.EventType == ChannelEventTypeSyncMetaLogs { // 副本同步元数据 （副本执行）
		syncCount, err := c.metaReplica.RequestSyncLogsAndNotifyLeaderIfNeed()
		if err != nil {
			c.Warn("副本向领导同步频道元数据日志失败", zap.Error(err), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
			event.retry++
			c.s.channelManager.AddRetryEvent(event)
		} else if syncCount == 0 {
			c.Debug("副本已与领导的频道元数据日志一致", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		} else {
			event.retry++
			c.s.channelManager.AddRetryEvent(event)
		}
	} else if event.EventType == ChannelEventTypeSyncMessageLogs { // 副本同步消息 （副本执行）
		startTime := time.Now().UnixNano()
		c.Debug("开始向领导同步频道消息数据日志", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		syncCount, err := c.messageReplica.RequestSyncLogsAndNotifyLeaderIfNeed()
		if err != nil {
			event.retry++
			c.s.channelManager.AddRetryEvent(event)
			c.Warn("副本向领导同步频道消息数据日志失败", zap.Error(err), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		} else if syncCount == 0 {
			c.Debug("副本已与领导的频道消息数据日志一致", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		} else {
			event.retry++
			c.s.channelManager.AddRetryEvent(event)
		}
		c.Debug("向领导同步频道消息数据日志完成", zap.Int64("cost", (time.Now().UnixNano()-startTime)/1000000), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType), zap.Int("syncCount", syncCount))
	} else if event.EventType == ChannelEventTypeSendApplyClusterInfo { // 领导发送应用频道集群的通知
		startTime := time.Now().UnixNano()
		c.Debug("开始向副本应用频道集群信息", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		err := c.applyClusterInfo()
		if err != nil {
			c.Warn("向副本应用频道集群信息失败", zap.Error(err), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
			event.retry++
			c.s.channelManager.AddRetryEvent(event)
		} else {
			c.Debug("向副本应用频道集群信息完成", zap.Int64("cost", (time.Now().UnixNano()-startTime)/1000000), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
		}
	}
}

func (c *Channel) applyClusterInfo() error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	requestGroup, ctx := errgroup.WithContext(timeoutCtx)

	applyReplicaNodeIDs := make([]uint64, 0, len(c.clusterInfo.Replicas))

	var requestErr error
	for _, replicaNodeID := range c.clusterInfo.Replicas {
		if replicaNodeID == c.s.opts.NodeID {
			if !wkutil.ArrayContainsUint64(c.clusterInfo.ApplyReplicas, replicaNodeID) {
				applyReplicaNodeIDs = append(applyReplicaNodeIDs, replicaNodeID)
			}
			continue
		}
		if !wkutil.ArrayContainsUint64(c.clusterInfo.ApplyReplicas, replicaNodeID) {
			requestGroup.Go(func(rcID uint64) func() error {
				return func() error {
					err := c.s.nodeManager.requestApplyClusterInfo(ctx, rcID, c.clusterInfo)
					if err != nil {
						requestErr = err
						c.Error("向副本应用频道集群信息失败", zap.Error(err), zap.Uint64("replicaNodeID", rcID), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
					} else {
						applyReplicaNodeIDs = append(applyReplicaNodeIDs, rcID)
					}
					return nil
				}
			}(replicaNodeID))
		}
	}
	_ = requestGroup.Wait()
	cancel()

	if len(applyReplicaNodeIDs) > 0 {
		c.clusterInfo.ApplyReplicas = append(c.clusterInfo.ApplyReplicas, applyReplicaNodeIDs...)
		err := c.s.stateMachine.saveChannelClusterInfo(c.clusterInfo)
		if err != nil {
			c.Error("保存频道集群信息失败", zap.Error(err), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType))
			return err
		}
	}
	return requestErr
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
