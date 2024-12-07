package clusterconfig

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

var _ reactor.IHandler = &handler{}

type handler struct {
	rc   *replica.Replica
	cfg  *Config
	opts *Options
	wklog.Log
	leaderId   uint64
	storage    *PebbleShardLogStorage
	mu         sync.Mutex
	isPrepared atomic.Bool
	s          *Server

	// 学习者转换中
	learnerTrans atomic.Bool
}

func newHandler(cfg *Config, storage *PebbleShardLogStorage, s *Server) *handler {

	h := &handler{
		cfg:     cfg,
		opts:    s.opts,
		Log:     wklog.NewWKLog(fmt.Sprintf("clusterconfig.handler[%d]", s.opts.NodeId)),
		storage: storage,
		s:       s,
	}
	// replicas := make([]uint64, 0, len(s.opts.InitNodes))
	// for replicaId := range s.opts.InitNodes {
	// 	replicas = append(replicas, replicaId)
	// }
	h.isPrepared.Store(true)

	lastIndex, lastTerm, err := storage.LastIndexAndTerm()
	if err != nil {
		h.Panic("get last index and term error", zap.Error(err))
	}

	appliedIndex, err := storage.AppliedIndex()
	if err != nil {
		h.Panic("get applied index error", zap.Error(err))
	}

	h.rc = replica.New(s.opts.NodeId,
		replica.WithLogPrefix("config"),
		replica.WithElectionOn(true),
		replica.WithElectionIntervalTick(cfg.opts.ElectionIntervalTick),
		replica.WithHeartbeatIntervalTick(cfg.opts.HeartbeatIntervalTick),
		replica.WithStorage(h.storage),
		replica.WithLastIndex(lastIndex),
		replica.WithLastTerm(lastTerm),
		replica.WithAppliedIndex(appliedIndex),
		replica.WithAutoRoleSwith(true),
	)
	return h
}

// func (h *handler) updateConfig() {
// 	nodes := h.cfg.allowVoteNodes()
// 	replicas := make([]uint64, 0, len(nodes))
// 	for _, node := range nodes {
// 		replicas = append(replicas, node.Id)
// 	}

// 	for replicaId := range h.opts.InitNodes {
// 		exist := false
// 		for _, replica := range replicas {
// 			if replicaId == replica {
// 				exist = true
// 				break
// 			}
// 		}
// 		if !exist {
// 			replicas = append(replicas, replicaId)
// 		}

// 	}

// 	h.s.configReactor.Step(h.s.handlerKey, replica.Message{
// 		MsgType: replica.MsgConfigResp,
// 		Config: replica.Config{
// 			Replicas: replicas,
// 		},
// 	})

// }

// 获取某个副本的最新配置版本（领导节点才有这个信息）
func (h *handler) configVersion(replicaId uint64) uint64 {
	return h.rc.GetReplicaLastLog(replicaId)
}

// -------------------- implement IHandler --------------------

// LastLogIndexAndTerm 获取最后一条日志的索引和任期
func (h *handler) LastLogIndexAndTerm() (uint64, uint32) {
	return h.rc.LastLogIndex(), h.rc.Term()
}

func (h *handler) HasReady() bool {
	return h.rc.HasReady()
}

// Ready 获取ready事件
func (h *handler) Ready() replica.Ready {
	return h.rc.Ready()
}

func (h *handler) ApplyLogs(startIndex, endIndex uint64) (uint64, error) {

	logs, err := h.getLogs(startIndex, endIndex)
	if err != nil {
		h.Error("get logs error", zap.Error(err))
		return 0, err
	}
	if len(logs) == 0 {
		h.Error("no logs to apply", zap.Uint64("startIndex", startIndex), zap.Uint64("endIndex", endIndex))
		return 0, fmt.Errorf("no logs to apply")
	}
	lastLog := logs[len(logs)-1]

	err = h.s.apply(logs)
	if err != nil {
		h.Error("apply config error", zap.Error(err))
		return 0, err
	}
	err = h.storage.setAppliedIndex(lastLog.Index)
	if err != nil {
		h.Error("set applied index error", zap.Error(err))
		return 0, err
	}

	// h.updateConfig() // 更新配置 (TODO：这里应该判断下，只有节点改变才更新)

	// 触发配置已应用事件
	h.opts.Event.OnAppliedConfig()

	appliedSize := uint64(0)
	for _, log := range logs {
		appliedSize += uint64(log.LogSize())
	}

	return appliedSize, err
}

func (h *handler) AppliedIndex() (uint64, error) {
	return h.storage.AppliedIndex()
}

func (h *handler) GetLogs(startLogIndex, endLogIndex uint64) ([]replica.Log, error) {
	return h.getLogs(startLogIndex, endLogIndex)
}

func (h *handler) getLogs(startLogIndex uint64, endLogIndex uint64) ([]replica.Log, error) {
	logs, err := h.storage.Logs(startLogIndex, endLogIndex, 0)
	if err != nil {
		h.Error("get logs error", zap.Error(err))
		return nil, err
	}
	return logs, nil
}

// SetHardState 设置HardState
func (h *handler) SetHardState(hd replica.HardState) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if hd.LeaderId != h.leaderId {
		h.Info("config leader change", zap.Uint64("oldLeader", h.leaderId), zap.Uint64("newLeader", hd.LeaderId))
	}
	h.leaderId = hd.LeaderId

}

// Tick tick
func (h *handler) Tick() {
	h.rc.Tick()

}

// Step 步进消息
func (h *handler) Step(m replica.Message) error {
	return h.rc.Step(m)
}

// // SetLastIndex 设置最后一条日志的索引
// func (h *handler) SetLastIndex(index uint64) error {
// 	return h.storage.SetLastIndex(index)
// }

// // SetAppliedIndex 设置已应用的索引
// func (h *handler) SetAppliedIndex(index uint64) error {
// 	return h.storage.SetAppliedIndex(index)
// }

// IsPrepared 是否准备好
func (h *handler) IsPrepared() bool {
	return h.isPrepared.Load()
}

func (h *handler) SetIsPrepared(prepared bool) {
	h.isPrepared.Store(prepared)
}

func (h *handler) IsLeader() bool {
	return h.isLeader()
}

func (h *handler) SetSpeedLevel(level replica.SpeedLevel) {
	h.rc.SetSpeedLevel(level)
}

func (h *handler) SpeedLevel() replica.SpeedLevel {
	return h.rc.SpeedLevel()
}

func (h *handler) isLeader() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opts.NodeId == h.leaderId
}

func (h *handler) LeaderId() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.leaderId
}

func (h *handler) PausePropopose() bool {

	return false
}

func (h *handler) LearnerToFollower(learnerId uint64) error {
	return h.learnerTo(learnerId)
}

func (h *handler) LearnerToLeader(learnerId uint64) error {
	return h.learnerTo(learnerId)
}

func (h *handler) FollowerToLeader(followerId uint64) error {
	return nil
}

func (h *handler) learnerTo(learnerId uint64) error {

	if h.learnerTrans.Load() {
		return nil
	}

	h.learnerTrans.Store(true)

	defer h.learnerTrans.Store(false)

	return h.s.ProposeJoining(learnerId)

}

func (h *handler) SaveConfig(cfg replica.Config) error {

	return nil
}

func (h *handler) AppendLogs(logs []replica.Log) error {
	return h.storage.AppendLog(logs)
}

func (h *handler) SetLeaderTermStartIndex(term uint32, index uint64) error {
	return h.storage.SetLeaderTermStartIndex(term, index)
}

func (h *handler) LeaderTermStartIndex(term uint32) (uint64, error) {
	return h.storage.LeaderTermStartIndex(term)
}

func (h *handler) LeaderLastTerm() (uint32, error) {
	return h.storage.LeaderLastTerm()
}

func (h *handler) DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error {
	return h.storage.DeleteLeaderTermStartIndexGreaterThanTerm(term)
}

func (h *handler) TruncateLogTo(index uint64) error {
	return h.storage.TruncateLogTo(index)
}
