package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type channel struct {
	channelID                  string
	channelType                uint8
	rc                         *replica.Replica      // 副本服务
	destroy                    bool                  // 是否已经销毁
	clusterConfig              *ChannelClusterConfig // 分布式配置
	maxHandleReadyCountOfBatch int                   // 每批次处理ready的最大数量
	opts                       *Options
	lastActivity               time.Time // 最后一次活跃时间
	commitWait                 *commitWait
	doneC                      chan struct{}
	wklog.Log
	prev *channel
	next *channel

	sync.Mutex
}

func newChannel(clusterConfig *ChannelClusterConfig, appliedIdx uint64, lastSyncInfoMap map[uint64]*replica.SyncInfo, opts *Options) *channel {
	shardNo := ChannelKey(clusterConfig.ChannelID, clusterConfig.ChannelType)
	rc := replica.New(opts.NodeID, shardNo, replica.WithAppliedIndex(appliedIdx), replica.WithLastSyncInfoMap(lastSyncInfoMap), replica.WithReplicas(clusterConfig.Replicas), replica.WithStorage(newProxyReplicaStorage(shardNo, opts.MessageLogStorage)))
	return &channel{
		maxHandleReadyCountOfBatch: 50,
		rc:                         rc,
		opts:                       opts,
		Log:                        wklog.NewWKLog(fmt.Sprintf("Channel[%s]", shardNo)),
		commitWait:                 newCommitWait(),
		lastActivity:               time.Now(),
		channelID:                  clusterConfig.ChannelID,
		channelType:                clusterConfig.ChannelType,
		clusterConfig:              clusterConfig,
		doneC:                      make(chan struct{}),
	}
}

func (c *channel) updateClusterConfig(clusterConfig *ChannelClusterConfig) {
	c.Lock()
	defer c.Unlock()
	c.clusterConfig = clusterConfig
	c.rc.SetReplicas(clusterConfig.Replicas)
	if clusterConfig.LeaderId == c.opts.NodeID {
		c.rc.BecomeLeader(clusterConfig.Term)
	} else {
		c.rc.BecomeFollower(clusterConfig.Term, clusterConfig.LeaderId)
	}
}

func (c *channel) ready() replica.Ready {
	if c.destroy {
		return replica.Ready{}
	}
	c.Lock()
	defer c.Unlock()
	return c.rc.Ready()
}

func (c *channel) hasReady() bool {
	if c.destroy {
		return false
	}
	c.Lock()
	defer c.Unlock()
	return c.rc.HasReady()
}

// 任命为领导
func (c *channel) appointLeader(term uint32) error {

	return c.stepLock(replica.Message{
		MsgType:           replica.MsgAppointLeaderReq,
		AppointmentLeader: c.opts.NodeID,
		Term:              term,
	})

}

// 任命指定节点为领导
func (c *channel) appointLeaderTo(term uint32, to uint64) error {
	return c.stepLock(replica.Message{
		MsgType:           replica.MsgAppointLeaderReq,
		AppointmentLeader: to,
		Term:              term,
	})
}

func (c *channel) stepLock(msg replica.Message) error {
	c.Lock()
	defer c.Unlock()
	return c.step(msg)
}

func (c *channel) step(msg replica.Message) error {
	if c.destroy {
		return errors.New("channel destroy, can not step")
	}
	c.lastActivity = time.Now()
	return c.rc.Step(msg)
}

func (c *channel) propose(data []byte) error {
	if c.destroy {
		return errors.New("channel destroy, can not propose")
	}
	return c.stepLock(c.rc.NewProposeMessage(data))
}

// 提案数据，并等待数据提交给大多数节点
func (c *channel) proposeAndWaitCommit(data []byte, timeout time.Duration) (uint64, error) {

	c.Lock()
	if c.destroy {
		c.Unlock()
		return 0, errors.New("channel destroy, can not propose")
	}
	msg := c.rc.NewProposeMessage(data)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	waitC := c.commitWait.addWaitIndex(msg.Index)
	err := c.step(msg)
	if err != nil {
		c.Unlock()
		return 0, err
	}
	c.Unlock()

	select {
	case <-waitC:
		fmt.Println("waitC--->", msg.Index)
		return msg.Index, nil
	case <-timeoutCtx.Done():
		return 0, timeoutCtx.Err()
	case <-c.doneC:
		return 0, ErrStopped
	}
}

func (c *channel) channelKey() string {
	return ChannelKey(c.channelID, c.channelType)
}

func (c *channel) makeDestroy() {
	c.destroy = true
	close(c.doneC)
}

func (c *channel) isDestroy() bool {
	return c.destroy
}

func (c *channel) getLastActivity() time.Time {
	return c.lastActivity
}

func (c *channel) handleLocalMsg(msg replica.Message) {
	if c.destroy {
		c.Warn("handle local msg, but channel is destroy")
		return
	}
	if msg.To != c.opts.NodeID {
		c.Warn("handle local msg, but msg to is not self", zap.String("msgType", msg.MsgType.String()), zap.Uint64("to", msg.To), zap.Uint64("self", c.opts.NodeID))
		return
	}
	c.lastActivity = time.Now()
	switch msg.MsgType {
	case replica.MsgApplyLogsReq: // 处理apply logs请求
		c.handleApplyLogsReq(msg)
	}
}

// 处理应用日志请求
func (c *channel) handleApplyLogsReq(msg replica.Message) {
	if len(msg.Logs) == 0 {
		return
	}
	lastLog := msg.Logs[len(msg.Logs)-1]
	c.Debug("commit wait", zap.Uint64("lastLogIndex", lastLog.Index))
	c.commitWait.commitIndex(lastLog.Index)
	c.Debug("commit wait done", zap.Uint64("lastLogIndex", lastLog.Index))

	err := c.stepLock(c.rc.NewMsgApplyLogsRespMessage(lastLog.Index))
	if err != nil {
		c.Error("step apply logs resp failed", zap.Error(err))
	}
}

func (c *channel) handleMessage(msg replica.Message) error {
	return c.stepLock(msg)
}

func (c *channel) isLeader() bool {
	return c.rc.IsLeader()
}
