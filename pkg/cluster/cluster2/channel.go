package cluster

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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
	mu  sync.Mutex
	cfg wkdb.ChannelClusterConfig
}

func newChannel(channelId string, channelType uint8, opts *Options) *channel {
	key := ChannelToKey(channelId, channelType)
	c := &channel{
		key:         key,
		channelId:   channelId,
		channelType: channelType,
		opts:        opts,
		Log:         wklog.NewWKLog(fmt.Sprintf("cluster.channel[%s]", key)),
	}
	return c
}

func (c *channel) bootstrap(cfg wkdb.ChannelClusterConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	rc := replica.New(c.opts.NodeId, replica.WithLogPrefix(fmt.Sprintf("channel-%s", c.key)), replica.WithElectionOn(false), replica.WithReplicas(cfg.Replicas), replica.WithStorage(newProxyReplicaStorage(c.key, c.opts.MessageLogStorage)))
	c.rc = rc
	if cfg.LeaderId == c.opts.NodeId {
		rc.BecomeLeader(cfg.Term)
	} else {
		rc.BecomeFollower(cfg.Term, cfg.LeaderId)
	}
	c.isPrepared = true
	c.cfg = cfg
	return nil
}

func (c *channel) leaderId() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.LeaderId
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
	err := c.opts.MessageLogStorage.AppendLog(c.key, logs)
	if err != nil {
		c.Panic("append log error", zap.Error(err))
	}
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
	err := c.opts.ChannelClusterStorage.Save(wkdb.ChannelClusterConfig{
		ChannelId:       c.channelId,
		ChannelType:     c.channelType,
		LeaderId:        hd.LeaderId,
		Term:            hd.Term,
		Replicas:        c.cfg.Replicas,
		ReplicaMaxCount: c.cfg.ReplicaMaxCount,
	})
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

func (c *channel) IsLeader() bool {
	return c.isLeader()
}

func (c *channel) getLogs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	logs, err := c.opts.MessageLogStorage.Logs(c.key, startLogIndex, endLogIndex, limitSize)
	if err != nil {
		c.Error("get logs error", zap.Error(err))
		return nil, err
	}
	return logs, nil
}
