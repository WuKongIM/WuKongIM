package cluster

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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
	wklog.Log
	mu             sync.Mutex
	cfg            wkdb.ChannelClusterConfig
	pausePropopose atomic.Bool // 是否暂停提案

	sendConfigTimeoutTick int // 发送配置超时（达到这个tick表示，需要发送配置请求了）

	learnerToLock sync.Mutex

	s *Server
}

func newChannel(channelId string, channelType uint8, s *Server) *channel {
	key := wkutil.ChannelToKey(channelId, channelType)
	c := &channel{
		key:                   key,
		channelId:             channelId,
		channelType:           channelType,
		sendConfigTimeoutTick: 10,
		opts:                  s.opts,
		Log:                   wklog.NewWKLog(fmt.Sprintf("cluster.channel[%s]", key)),
		s:                     s,
	}

	appliedIdx, err := c.opts.MessageLogStorage.AppliedIndex(c.key)
	if err != nil {
		c.Panic("get applied index error", zap.Error(err))

	}
	lastIndex, lastTerm, err := c.opts.MessageLogStorage.LastIndexAndTerm(c.key)
	if err != nil {
		c.Panic("get last index and term error", zap.Error(err))
	}
	rc := replica.New(
		c.opts.NodeId,
		replica.WithLogPrefix(fmt.Sprintf("channel-%s", c.key)),
		replica.WithAppliedIndex(appliedIdx),
		replica.WithElectionOn(false),
		replica.WithAutoRoleSwith(true),
		replica.WithLastIndex(lastIndex),
		replica.WithLastTerm(lastTerm),
		replica.WithStorage(newProxyReplicaStorage(c.key, c.opts.MessageLogStorage)),
	)
	c.rc = rc
	return c
}

func (c *channel) Role() replica.Role {
	return c.rc.Role()
}

func (c *channel) calcRole() replica.Role {
	var role = replica.RoleUnknown

	if c.cfg.LeaderId == c.opts.NodeId {
		role = replica.RoleLeader
	} else if wkutil.ArrayContainsUint64(c.cfg.Learners, c.opts.NodeId) {
		role = replica.RoleLearner
	} else if wkutil.ArrayContainsUint64(c.cfg.Replicas, c.opts.NodeId) {
		role = replica.RoleFollower
	}
	return role
}

func (c *channel) switchConfig(cfg wkdb.ChannelClusterConfig) error {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()

	// c.Info("switch config", zap.Uint32("slotId", c.s.getSlotId(c.channelId)), zap.String("cfg", cfg.String()))

	var role = c.calcRole()

	if role == replica.RoleUnknown {
		c.Info("switch config, role is unknown, remove channel", zap.String("cfg", cfg.String()))
		c.s.channelManager.remove(c)
		return nil
	}

	replicaCfg := replica.Config{
		MigrateFrom: cfg.MigrateFrom,
		MigrateTo:   cfg.MigrateTo,
		Replicas:    cfg.Replicas,
		Learners:    cfg.Learners,
		Version:     cfg.ConfVersion,
		Leader:      cfg.LeaderId,
		Role:        role,
		Term:        cfg.Term,
	}

	return c.s.channelManager.channelReactor.StepWait(c.key, replica.Message{
		MsgType: replica.MsgConfigResp,
		Config:  replicaCfg,
	})
}

func (c *channel) onReplicaConfigChange(oldCfg, newCfg replica.Config) {

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

func (c *channel) GetLogs(startIndex uint64, endIndex uint64) ([]replica.Log, error) {
	return c.getLogs(startIndex, endIndex, uint64(c.opts.LogSyncLimitSizeOfEach))
}

func (c *channel) ApplyLogs(startIndex, endIndex uint64) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if endIndex == 0 {
		return 0, nil
	}

	lastAppliedIndex, err := c.opts.MessageLogStorage.AppliedIndex(c.key)
	if err != nil {
		return 0, nil
	}
	if endIndex-1 <= lastAppliedIndex {
		c.Error("not apply logs, applied < lastApplied", zap.Uint64("applied", endIndex-1), zap.Uint64("lastApplied", lastAppliedIndex))
		return 0, nil
	}

	err = c.opts.MessageLogStorage.SetAppliedIndex(c.key, endIndex-1)
	if err != nil {
		return 0, err
	}
	return 0, nil
}

func (c *channel) AppliedIndex() (uint64, error) {
	return c.opts.MessageLogStorage.AppliedIndex(c.key)
}

func (c *channel) SetHardState(hd replica.HardState) {

	if c.cfg.LeaderId != 0 && hd.LeaderId != c.cfg.LeaderId {
		c.Info("channel leader change", zap.Uint64("oldLeader", c.cfg.LeaderId), zap.Uint64("newLeader", hd.LeaderId))
	}

	// if hd.LeaderId == 0 {
	// 	return
	// }
	c.cfg.LeaderId = hd.LeaderId
	c.cfg.Term = hd.Term
	c.cfg.ConfVersion = hd.ConfVersion

	// err := c.opts.ChannelClusterStorage.Save(c.cfg)
	// if err != nil {
	// 	c.Warn("save channel cluster config error", zap.Error(err))
	// }
}

func (c *channel) Tick() {
	c.rc.Tick()

	// if c.isLeader() {
	// 	c.sendConfigTick++
	// 	if c.sendConfigTick >= c.sendConfigTimeoutTick {
	// 		if c.isLeader() {
	// 			err := c.sendConfigReqToSlotLeader(c, c.cfg.ConfVersion)
	// 			if err != nil {
	// 				c.Error("send config req to slot leader error", zap.Error(err))
	// 			}
	// 		}
	// 	}
	// }

}

func (c *channel) Step(m replica.Message) error {
	return c.rc.Step(m)
}

func (c *channel) LeaderId() uint64 {
	return c.leaderId()
}

func (c *channel) SetSpeedLevel(level replica.SpeedLevel) {
	c.rc.SetSpeedLevel(level)
}

func (c *channel) SpeedLevel() replica.SpeedLevel {
	return c.rc.SpeedLevel()
}

func (c *channel) PausePropopose() bool {
	return c.pausePropopose.Load()
}

func (c *channel) SaveConfig(cfg replica.Config) error {
	// c.mu.Lock()
	// defer c.mu.Unlock()

	// c.cfg.MigrateFrom = cfg.MigrateFrom
	// c.cfg.MigrateTo = cfg.MigrateTo
	// c.cfg.Replicas = cfg.Replicas
	// c.cfg.Learners = cfg.Learners
	// c.cfg.ConfVersion = cfg.Version

	// err := c.opts.ChannelClusterStorage.Save(c.cfg)
	// if err != nil {
	// 	c.Error("save channel cluster config error", zap.Error(err))
	// 	return err
	// }

	return nil
}

func (c *channel) SetLeaderTermStartIndex(term uint32, index uint64) error {

	return c.opts.MessageLogStorage.SetLeaderTermStartIndex(c.key, term, index)
}

func (c *channel) LeaderTermStartIndex(term uint32) (uint64, error) {
	return c.opts.MessageLogStorage.LeaderTermStartIndex(c.key, term)
}

func (c *channel) LeaderLastTerm() (uint32, error) {
	return c.opts.MessageLogStorage.LeaderLastTerm(c.key)
}

func (c *channel) DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error {
	return c.opts.MessageLogStorage.DeleteLeaderTermStartIndexGreaterThanTerm(c.key, term)
}

func (c *channel) TruncateLogTo(index uint64) error {
	return c.opts.MessageLogStorage.TruncateLogTo(c.key, index)
}

func (c *channel) LearnerToFollower(learnerId uint64) error {
	c.Info("learner to  follower", zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType), zap.Uint64("learnerId", learnerId))

	return c.learnerTo(learnerId)
}

func (c *channel) LearnerToLeader(learnerId uint64) error {
	c.Info("learner to  leader", zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType), zap.Uint64("learnerId", learnerId))
	return c.learnerTo(learnerId)
}

func (c *channel) FollowerToLeader(followerId uint64) error {

	c.learnerToLock.Lock()
	defer c.learnerToLock.Unlock()

	c.Info("follower to leader", zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType), zap.Uint64("followerId", followerId))

	channelClusterCfg, err := c.s.loadOnlyChannelClusterConfig(c.channelId, c.channelType)
	if err != nil {
		c.Error("onReplicaConfigChange failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return err
	}
	if wkdb.IsEmptyChannelClusterConfig(channelClusterCfg) {
		return fmt.Errorf("FollowerToLeader: channel cluster config is empty")
	}
	if channelClusterCfg.MigrateFrom == 0 || channelClusterCfg.MigrateTo == 0 {
		return fmt.Errorf("FollowerToLeader: there is no migration")
	}

	if !wkutil.ArrayContainsUint64(channelClusterCfg.Replicas, followerId) {
		c.Error("FollowerToLeader: follower not in replicas", zap.Uint64("followerId", followerId))
		return fmt.Errorf("follower not in replicas")
	}

	createdAt := time.Now()
	updatedAt := time.Now()
	newChannelClusterCfg := channelClusterCfg.Clone()
	newChannelClusterCfg.Term = newChannelClusterCfg.Term + 1
	newChannelClusterCfg.LeaderId = followerId
	newChannelClusterCfg.MigrateFrom = 0
	newChannelClusterCfg.MigrateTo = 0
	newChannelClusterCfg.ConfVersion = uint64(time.Now().UnixNano())
	newChannelClusterCfg.CreatedAt = &createdAt
	newChannelClusterCfg.UpdatedAt = &updatedAt

	err = c.proposeAndUpdateChannelClusterConfig(newChannelClusterCfg)
	if err != nil {
		c.Error("FollowerToLeader: proposeAndUpdateChannelClusterConfig failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return err
	}

	// 发送配置给新领导
	err = c.s.SendChannelClusterConfigUpdate(newChannelClusterCfg.ChannelId, newChannelClusterCfg.ChannelType, newChannelClusterCfg.LeaderId)
	if err != nil {
		c.Error("FollowerToLeader: sendChannelClusterConfigUpdate failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return err
	}

	return nil
}

func (c *channel) learnerTo(learnerId uint64) error {

	c.learnerToLock.Lock()
	defer c.learnerToLock.Unlock()

	channelClusterCfg, err := c.s.loadOnlyChannelClusterConfig(c.channelId, c.channelType)
	if err != nil {
		c.Error("onReplicaConfigChange failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return err
	}
	if wkdb.IsEmptyChannelClusterConfig(channelClusterCfg) {
		return fmt.Errorf("LearnerToFollower: channel cluster config is empty")
	}

	if channelClusterCfg.MigrateFrom == 0 || channelClusterCfg.MigrateTo == 0 {
		return fmt.Errorf("LearnerToFollower: there is no migration")
	}

	if channelClusterCfg.MigrateTo != learnerId {
		c.Error("LearnerToFollower: learnerId is not equal to migrateTo", zap.Uint64("learnerId", learnerId), zap.Uint64("migrateTo", channelClusterCfg.MigrateTo))
		return fmt.Errorf("LearnerToFollower: learnerId is not equal to migrateTo")
	}

	channelClusterCfg.Learners = wkutil.RemoveUint64(channelClusterCfg.Learners, learnerId)
	channelClusterCfg.Replicas = wkutil.RemoveUint64(channelClusterCfg.Replicas, channelClusterCfg.MigrateFrom)

	if !wkutil.ArrayContainsUint64(channelClusterCfg.Replicas, learnerId) {
		channelClusterCfg.Replicas = append(channelClusterCfg.Replicas, learnerId)
	}

	var learnerIsLeader = false // 学习者是新的领导者
	// 如果迁移的是领导节点，则将学习者设置为领导者
	if channelClusterCfg.MigrateFrom == c.leaderId() {
		channelClusterCfg.Term = channelClusterCfg.Term + 1
		channelClusterCfg.LeaderId = learnerId
		channelClusterCfg.Status = wkdb.ChannelClusterStatusNormal
		learnerIsLeader = true

	}

	createdAt := time.Now()
	updatedAt := time.Now()
	channelClusterCfg.MigrateFrom = 0
	channelClusterCfg.MigrateTo = 0
	channelClusterCfg.ConfVersion = uint64(time.Now().UnixNano())
	channelClusterCfg.CreatedAt = &createdAt
	channelClusterCfg.UpdatedAt = &updatedAt

	err = c.proposeAndUpdateChannelClusterConfig(channelClusterCfg)
	if err != nil {
		c.Error("LearnerToFollower: proposeAndUpdateChannelClusterConfig failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return err
	}

	// 如果是学习者是新的领导，则通知新领导更新配置
	if learnerIsLeader {
		err = c.s.SendChannelClusterConfigUpdate(channelClusterCfg.ChannelId, channelClusterCfg.ChannelType, channelClusterCfg.LeaderId)
		if err != nil {
			c.Error("LearnerToFollower: sendChannelClusterConfigUpdate failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
			return err
		}
	}

	return nil
}

func (c *channel) proposeAndUpdateChannelClusterConfig(cfg wkdb.ChannelClusterConfig) error {
	// 保存配置
	err := c.opts.ChannelClusterStorage.Propose(cfg)
	if err != nil {
		c.Error("propose channel cluster config failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return err
	}

	// 生效配置
	err = c.switchConfig(cfg)
	if err != nil {
		c.Error("proposeAndUpdateChannelClusterConfig: switch config failed", zap.Error(err), zap.String("channelId", c.channelId), zap.Uint8("channelType", c.channelType))
		return err
	}

	return nil

}

func (c *channel) getLogs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	logs, err := c.opts.MessageLogStorage.Logs(c.key, startLogIndex, endLogIndex, limitSize)
	if err != nil {
		c.Error("get logs error", zap.Error(err))
		return nil, err
	}
	return logs, nil
}

func (c *channel) DetailLogOn(on bool) {
	c.rc.DetailLogOn(on)
}
