package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/zap"
)

type channel struct {
	channelID                  string
	channelType                uint8
	rc                         *replica.Replica              // 副本服务
	destroy                    bool                          // 是否已经销毁
	clusterConfig              *wkstore.ChannelClusterConfig // 分布式配置
	maxHandleReadyCountOfBatch int                           // 每批次处理ready的最大数量
	opts                       *Options
	lastActivity               time.Time // 最后一次活跃时间
	commitWait                 *commitWait
	doneC                      chan struct{}
	wklog.Log
	prev *channel
	next *channel

	mu           deadlock.Mutex
	localstorage *localStorage
}

func newChannel(clusterConfig *wkstore.ChannelClusterConfig, appliedIdx uint64, localstorage *localStorage, opts *Options) *channel {
	shardNo := ChannelKey(clusterConfig.ChannelID, clusterConfig.ChannelType)
	rc := replica.New(opts.NodeID, shardNo, replica.WithAppliedIndex(appliedIdx), replica.WithReplicas(clusterConfig.Replicas), replica.WithStorage(newProxyReplicaStorage(shardNo, opts.MessageLogStorage, localstorage)))
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
		localstorage:               localstorage,
	}
}

func (c *channel) updateClusterConfig(clusterConfig *wkstore.ChannelClusterConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rc.Ready()
}

func (c *channel) hasReady() bool {
	if c.destroy {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	err := c.step(msg)
	c.mu.Unlock()
	return err

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

func (c *channel) proposeAndWaitCommit(data []byte, timeout time.Duration) (uint64, error) {
	if c.destroy {
		return 0, errors.New("channel destroy, can not propose")
	}
	lastIndexs, err := c.proposeAndWaitCommits([][]byte{data}, timeout)
	if err != nil {
		return 0, err
	}
	if len(lastIndexs) == 0 {
		return 0, errors.New("lastIndexs is empty")
	}
	return lastIndexs[0], nil
}

func (c *channel) becomeLeader(term uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rc.BecomeLeader(term)
}

// 提案数据，并等待数据提交给大多数节点
func (c *channel) proposeAndWaitCommits(data [][]byte, timeout time.Duration) ([]uint64, error) {
	if len(data) == 0 {
		return nil, errors.New("data is empty")
	}
	c.mu.Lock()
	if c.destroy {
		c.mu.Unlock()
		return nil, errors.New("channel destroy, can not propose")
	}
	logs := make([]replica.Log, 0, len(data))
	for i, d := range data {
		logs = append(logs,
			replica.Log{
				Index: c.rc.LastLogIndex() + uint64(1+i),
				Term:  c.rc.Term(),
				Data:  d,
			},
		)
	}
	lastLog := logs[len(logs)-1]
	c.Debug("add wait index", zap.Uint64("lastLogIndex", lastLog.Index), zap.Int("logsCount", len(logs)))
	waitC, err := c.commitWait.addWaitIndex(lastLog.Index)
	if err != nil {
		c.mu.Unlock()
		c.Error("add wait index failed", zap.Error(err))
		return nil, err
	}
	err = c.step(c.rc.NewProposeMessageWithLogs(logs))
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.mu.Unlock()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case <-waitC:
		seqs := make([]uint64, 0, len(logs))
		for _, log := range logs {
			seqs = append(seqs, log.Index)
		}
		c.Debug("finsh wait index", zap.Uint64s("seqs", seqs))
		return seqs, nil
	case <-timeoutCtx.Done():
		c.Debug("proposeAndWaitCommits timeout", zap.Uint64("lastLogIndex", lastLog.Index), zap.Int("logCount", len(logs)))
		return nil, timeoutCtx.Err()
	case <-c.doneC:
		return nil, ErrStopped
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
	case replica.MsgStoreAppend: // 处理store append请求
		c.handleStoreAppend(msg)
	case replica.MsgApplyLogsReq: // 处理apply logs请求
		c.handleApplyLogsReq(msg)
	}
}

func (c *channel) handleStoreAppend(msg replica.Message) {
	if len(msg.Logs) == 0 {
		return
	}
	shardNo := ChannelKey(c.channelID, c.channelType)
	err := c.opts.MessageLogStorage.AppendLog(shardNo, msg.Logs)
	if err != nil {
		c.Panic("append log error", zap.Error(err))
	}

	err = c.stepLock(c.rc.NewMsgStoreAppendResp(msg.Logs[len(msg.Logs)-1].Index))
	if err != nil {
		c.Panic("step store append resp failed", zap.Error(err))
	}
}

// 处理应用日志请求
func (c *channel) handleApplyLogsReq(msg replica.Message) {
	if msg.CommittedIndex <= 0 || msg.AppliedIndex >= msg.CommittedIndex {
		return
	}
	c.Debug("commit wait", zap.Uint64("lastLogIndex", msg.CommittedIndex))
	c.commitWait.commitIndex(msg.CommittedIndex)
	c.Debug("commit wait done", zap.Uint64("lastLogIndex", msg.CommittedIndex))

	shardNo := ChannelKey(c.channelID, c.channelType)
	err := c.localstorage.setAppliedIndex(shardNo, msg.CommittedIndex)
	if err != nil {
		c.Error("set applied index failed", zap.Error(err))
		return
	}
	err = c.stepLock(c.rc.NewMsgApplyLogsRespMessage(msg.CommittedIndex))
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

func (c *channel) leaderId() uint64 {
	return c.rc.LeaderId()
}

func (c *channel) getClusterConfig() *wkstore.ChannelClusterConfig {
	return c.clusterConfig
}

type ichannel interface {
	isLeader() bool
	proposeAndWaitCommits(data [][]byte, timeout time.Duration) ([]uint64, error)
	leaderId() uint64
	handleMessage(msg replica.Message) error
	getClusterConfig() *wkstore.ChannelClusterConfig
}

type proxyChannel struct {
	nodeId     uint64
	clusterCfg *wkstore.ChannelClusterConfig
}

func newProxyChannel(nodeId uint64, clusterCfg *wkstore.ChannelClusterConfig) *proxyChannel {
	return &proxyChannel{
		nodeId:     nodeId,
		clusterCfg: clusterCfg,
	}
}

func (p *proxyChannel) isLeader() bool {
	return p.clusterCfg.LeaderId == p.nodeId
}

func (p *proxyChannel) proposeAndWaitCommits(data [][]byte, timeout time.Duration) ([]uint64, error) {
	panic("proposeAndWaitCommits: implement me")
}

func (p *proxyChannel) leaderId() uint64 {
	return p.clusterCfg.LeaderId
}

func (p *proxyChannel) handleMessage(msg replica.Message) error {
	panic("handleMessage: implement me")
}

func (p *proxyChannel) getClusterConfig() *wkstore.ChannelClusterConfig {
	return p.clusterCfg
}
