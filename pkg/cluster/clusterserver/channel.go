package cluster

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

var _ reactor.IHandler = &channel{}

type channel struct {
	key         string
	channelId   string
	channelType uint8
	rc          *replica.Replica
	opts        *Options
	isPrepared  bool
	wklog.Log
	mu                    sync.Mutex
	cfg                   wkdb.ChannelClusterConfig
	pausePropopose        atomic.Bool // 是否暂停提案
	onReplicaConfigChange func(cfg *replica.Config)
}

func newChannel(channelId string, channelType uint8, opts *Options, onReplicaConfigChange func(ch *channel, cfg *replica.Config)) *channel {
	key := ChannelToKey(channelId, channelType)
	c := &channel{
		key:         key,
		channelId:   channelId,
		channelType: channelType,
		opts:        opts,
		Log:         wklog.NewWKLog(fmt.Sprintf("cluster.channel[%s]", key)),
	}
	c.onReplicaConfigChange = func(cfg *replica.Config) {
		onReplicaConfigChange(c, cfg)
	}
	return c
}

func (c *channel) bootstrap(cfg wkdb.ChannelClusterConfig) error {
	appliedIdx, err := c.opts.MessageLogStorage.AppliedIndex(c.key)
	if err != nil {
		c.Error("get applied index error", zap.Error(err))
		return err

	}
	rc := replica.New(
		c.opts.NodeId,
		replica.WithLogPrefix(fmt.Sprintf("channel-%s", c.key)),
		replica.WithAppliedIndex(appliedIdx),
		replica.WithElectionOn(false),
		replica.WithConfig(&replica.Config{
			Replicas: cfg.Replicas,
			Learners: cfg.Learners,
			Version:  cfg.ConfVersion,
		}),
		replica.WithStorage(newProxyReplicaStorage(c.key, c.opts.MessageLogStorage)),
		replica.WithOnConfigChange(c.onReplicaConfigChange),
		replica.WithAutoLearnerToFollower(true),
	)
	c.rc = rc

	err = c.switchConfig(cfg)
	if err != nil {
		return err
	}
	c.isPrepared = true
	return nil
}

func (c *channel) switchConfig(cfg wkdb.ChannelClusterConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	isLearner := false
	if len(cfg.Learners) > 0 {
		for _, l := range cfg.Learners {
			if l == c.opts.NodeId {
				isLearner = true
				break
			}
		}
	}

	replicaCfg := &replica.Config{
		Replicas: cfg.Replicas,
		Learners: cfg.Learners,
		Version:  cfg.ConfVersion,
	}
	c.rc.SwitchConfig(replicaCfg)

	if isLearner {
		c.rc.BecomeLearner(cfg.Term, cfg.LeaderId)
	} else {
		if cfg.Status == wkdb.ChannelClusterStatusCandidate { // 槽进入候选者状态
			if c.rc.IsLeader() { // 领导节点不能直接转candidate，replica里会panic，领导转换成follower是一样的
				c.rc.BecomeFollower(cfg.Term, 0)
			} else {
				c.rc.BecomeCandidateWithTerm(cfg.Term)
			}

		} else if cfg.Status == wkdb.ChannelClusterStatusLeaderTransfer { // 槽进入领导者转移状态
			if cfg.LeaderId == c.opts.NodeId && cfg.LeaderTransferTo != cfg.LeaderId { // 如果当前槽领导将要被转移，则先暂停提案，等需要转移的节点的日志追上来
				c.pausePropopose.Store(true)
			}
		} else if cfg.Status == wkdb.ChannelClusterStatusNormal { // 槽进入正常状态
			if cfg.LeaderId == c.opts.NodeId {
				c.rc.BecomeLeader(cfg.Term)
			} else {
				c.rc.BecomeFollower(cfg.Term, cfg.LeaderId)
			}
		}
	}
	c.cfg = cfg
	return nil
}

func (c *channel) leaderId() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.LeaderId
}

func (c *channel) term() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Term
}

func (c *channel) isLeader() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.LeaderId == c.opts.NodeId
}

// --------------------------IHandler-------------------------------

func (c *channel) LastLogIndexAndTerm() (uint64, uint32) {
	return c.rc.LastLogIndex(), c.rc.Term()
}

func (c *channel) HasReady() bool {
	return c.rc.HasReady()
}

func (c *channel) Ready() replica.Ready {
	return c.rc.Ready()
}

func (c *channel) GetAndMergeLogs(lastIndex uint64, msg replica.Message) ([]replica.Log, error) {
	unstableLogs := msg.Logs
	startIndex := msg.Index
	if len(unstableLogs) > 0 {
		startIndex = unstableLogs[len(unstableLogs)-1].Index + 1
	}
	var err error
	if lastIndex == 0 {
		lastIndex, err = c.opts.MessageLogStorage.LastIndex(c.key)
		if err != nil {
			c.Error("GetAndMergeLogs: get last index error", zap.Error(err))
			return nil, err
		}
	}
	var resultLogs []replica.Log
	if startIndex <= lastIndex {
		logs, err := c.getLogs(startIndex, lastIndex+1, uint64(c.opts.LogSyncLimitSizeOfEach))
		if err != nil {
			c.Error("get logs error", zap.Error(err), zap.Uint64("startIndex", startIndex), zap.Uint64("lastIndex", lastIndex))
			return nil, err
		}
		resultLogs = extend(unstableLogs, logs)
	} else {
		resultLogs = unstableLogs

	}

	return resultLogs, nil
}

func (c *channel) AppendLog(logs []replica.Log) error {
	if len(logs) == 0 {
		return nil
	}
	start := time.Now()
	lastLog := logs[len(logs)-1]
	err := c.opts.MessageLogStorage.AppendLog(c.key, logs)
	if err != nil {
		c.Panic("append log error", zap.Error(err))
	}
	c.Debug("append log done", zap.Uint64("lastLogIndex", lastLog.Index), zap.Duration("cost", time.Since(start)))
	return nil
}

func (c *channel) ApplyLog(startLogIndex, endLogIndex uint64) error {
	return nil
}

func (c *channel) SlowDown() {
	c.rc.SlowDown()
}

func (c *channel) SetSpeedLevel(level replica.SpeedLevel) {
	c.rc.SetSpeedLevel(level)
}

func (c *channel) SpeedLevel() replica.SpeedLevel {
	return c.rc.SpeedLevel()
}

func (c *channel) SetHardState(hd replica.HardState) {

	c.cfg = wkdb.ChannelClusterConfig{
		ChannelId:       c.channelId,
		ChannelType:     c.channelType,
		LeaderId:        hd.LeaderId,
		Term:            hd.Term,
		Replicas:        c.cfg.Replicas,
		ReplicaMaxCount: c.cfg.ReplicaMaxCount,
	}

	err := c.opts.ChannelClusterStorage.Save(c.cfg)
	if err != nil {
		c.Warn("save channel cluster config error", zap.Error(err))
	}
}

func (c *channel) Tick() {
	c.rc.Tick()
}

func (c *channel) Step(m replica.Message) error {
	return c.rc.Step(m)
}

func (c *channel) SetLastIndex(index uint64) error {
	return c.setLastIndex(index)
}

func (c *channel) setLastIndex(index uint64) error {
	err := c.opts.MessageLogStorage.SetLastIndex(c.key, index)
	if err != nil {
		c.Error("set last index error", zap.Error(err))
	}
	return err
}

func (c *channel) SetAppliedIndex(index uint64) error {
	return c.setAppliedIndex(index)
}

func (c *channel) setAppliedIndex(index uint64) error {
	err := c.opts.MessageLogStorage.SetAppliedIndex(c.key, index)
	if err != nil {
		c.Error("set applied index error", zap.Error(err))
	}
	return err
}

func (c *channel) IsPrepared() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isPrepared
}

func (c *channel) replicaCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cfg.Replicas)
}

func (c *channel) LeaderId() uint64 {
	return c.leaderId()
}

func (c *channel) PausePropopose() bool {

	return false
}

func (c *channel) getLogs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	logs, err := c.opts.MessageLogStorage.Logs(c.key, startLogIndex, endLogIndex, limitSize)
	if err != nil {
		c.Error("get logs error", zap.Error(err))
		return nil, err
	}
	return logs, nil
}
