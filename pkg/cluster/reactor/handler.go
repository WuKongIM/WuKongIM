package reactor

import (
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type IHandler interface {
	// LastLogIndexAndTerm 获取最后一条日志的索引和任期
	LastLogIndexAndTerm() (uint64, uint32)
	HasReady() bool
	// Ready 获取ready事件
	Ready() replica.Ready
	// ApplyLog 应用日志 [startLogIndex,endLogIndex) 之间的日志

	// SlowDown 降速
	// SlowDown()
	// SetSpeedLevel 设置同步速度等级
	SetSpeedLevel(level replica.SpeedLevel)
	// SpeedLevel 获取当前同步速度等级
	SpeedLevel() replica.SpeedLevel
	// SetHardState 设置HardState
	SetHardState(hd replica.HardState)
	// Tick tick
	Tick()
	// Step 步进消息
	Step(m replica.Message) error
	// SetAppliedIndex 设置已应用的索引
	// SetAppliedIndex(index uint64) error
	// IsPrepared 是否准备好
	// AppliedIndex 获取已应用的索引
	AppliedIndex() (uint64, error)
	// 领导者Id
	LeaderId() uint64
	// PausePropopose 是否暂停提案
	PausePropopose() bool

	// LearnerToFollower 学习者转追随者
	LearnerToFollower(learnerId uint64) error
	// LearnerToLeader 学习者转领导者
	LearnerToLeader(learnerId uint64) error

	// FollowerToLeader 追随者转领导者
	FollowerToLeader(followerId uint64) error

	// 保存分布式配置
	SaveConfig(cfg replica.Config) error
	// ApplyLogs 应用日志
	ApplyLogs(startIndex, endIndex uint64) (uint64, error)
	// GetLogs 获取日志
	GetLogs(startLogIndex, endLogIndex uint64) ([]replica.Log, error)

	// AppendLogs 追加日志
	// AppendLogs(logs []replica.Log) error

	// SetLeaderTermStartIndex 设置领导任期和开始索引
	SetLeaderTermStartIndex(term uint32, index uint64) error

	// LeaderTermStartIndex 获取任期对应的开始索引
	LeaderTermStartIndex(term uint32) (uint64, error)

	// LeaderLastTerm 获取最后一条领导日志的任期
	LeaderLastTerm() (uint32, error)

	// DeleteLeaderTermStartIndexGreaterThanTerm 删除大于term的领导任期和开始索引
	DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error

	// TruncateLog 截断日志, 从index开始截断,index不能等于0 （保留下来的内容不包含index）
	// [1,2,3,4,5,6] truncate to 4 = [1,2,3]
	TruncateLogTo(index uint64) error
}

type handler struct {
	key      string
	no       string // handler唯一编号
	handler  IHandler
	msgQueue *MessageQueue

	lastIndex atomic.Uint64 // 当前频道最后一条日志索引

	proposeWait *proposeWait // 提案等待

	proposeIntervalTick int // 提案间隔tick数量

	hardState replica.HardState

	lastLeaderTerm atomic.Uint32 // 最新领导的任期

	syncTimeoutTick int // 同步超时tick次数

	wklog.Log
	r *Reactor
}

func (h *handler) init(key string, handler IHandler, r *Reactor) {
	h.r = r
	var b strings.Builder
	b.WriteString("handler[")
	b.WriteString(key)
	b.WriteString("]")
	h.Log = wklog.NewWKLog(fmt.Sprintf("handler[%s]", b.String()))
	h.key = key
	h.handler = handler
	h.msgQueue = r.newMessageQueue()
	h.lastIndex.Store(0)

	h.proposeWait = newProposeWait(fmt.Sprintf("[%d]%s", r.opts.NodeId, key))

}

func (h *handler) reset() {
	h.Log = nil
	h.handler = nil
	h.key = ""
	h.msgQueue = nil
	h.msgQueue = nil
	h.proposeWait = nil
	h.proposeIntervalTick = 0
	h.hardState = replica.HardState{}

}

func (h *handler) ready() replica.Ready {
	return h.handler.Ready()
}

func (h *handler) hasReady() bool {
	return h.handler.HasReady()
}

func (h *handler) step(m replica.Message) error {
	if m.HandlerNo != "" && m.HandlerNo != h.no {
		h.Warn("step failed,ignore，message does not belong to it", zap.String("msgType", m.MsgType.String()), zap.String("expectHandlerNo", h.no), zap.String("acthandlerNo", m.HandlerNo))
		return nil
	}
	return h.handler.Step(m)
}

func (h *handler) setHardState(hd replica.HardState) {
	h.hardState = hd
	h.handler.SetHardState(hd)
}

func (h *handler) didPropose(key string, minIndex uint64, maxIndex uint64, term uint32) {
	h.proposeWait.didPropose(key, minIndex, maxIndex, term)
}

func (h *handler) didCommit(startLogIndex uint64, endLogIndex uint64) {
	h.proposeWait.didCommit(startLogIndex, endLogIndex)
}

func (h *handler) addWait(key string, minId, maxId uint64) *proposeProgress {
	return h.proposeWait.add(key, minId, maxId)
}

func (h *handler) removeWait(key string) {
	if h.proposeWait == nil {
		return
	}
	h.proposeWait.remove(key)
}

func (h *handler) lastLogIndexAndTerm() (uint64, uint32) {
	return h.handler.LastLogIndexAndTerm()
}

func (h *handler) tick() {
	h.handler.Tick()
	h.proposeIntervalTick++

	if h.r.opts.AutoSlowDownOn {
		if h.syncTimeoutTick >= h.r.opts.SyncTimeoutMaxTick { // 同步超时超过指定次数，则停止同步
			h.setSpeedLevel(replica.LevelStop)
		}
	}

}

func (h *handler) resetProposeIntervalTick() {
	h.proposeIntervalTick = 0
	h.resetSlowDown()
}

func (h *handler) slowDown() {

	if !h.isLeader() {
		return
	}

	speedLevel := h.speedLevel()

	newSpeedLevel := speedLevel

	switch speedLevel {
	case replica.LevelFast:
		newSpeedLevel = replica.LevelNormal
	case replica.LevelNormal:
		newSpeedLevel = replica.LevelMiddle
	case replica.LevelMiddle:
		newSpeedLevel = replica.LevelSlow
	case replica.LevelSlow:
		newSpeedLevel = replica.LevelSlowest
	case replica.LevelSlowest:
		newSpeedLevel = replica.LevelStop
	}
	h.setSpeedLevel(newSpeedLevel)

}

func (h *handler) speedLevel() replica.SpeedLevel {
	return h.handler.SpeedLevel()
}

func (h *handler) setSpeedLevel(level replica.SpeedLevel) {
	h.handler.SetSpeedLevel(level)
}

func (h *handler) resetSlowDown() {
	h.handler.SetSpeedLevel(replica.LevelFast)
}

// 是否需要降速
func (h *handler) shouldSlowDown() bool {

	speedLevel := h.speedLevel()

	switch speedLevel {
	case replica.LevelFast:
		if h.proposeIntervalTick > LevelFastTick {
			return true
		}
		return false
	case replica.LevelNormal:
		if h.proposeIntervalTick > LevelNormal {
			return true
		}
		return false
	case replica.LevelMiddle:
		if h.proposeIntervalTick > LevelMiddle {
			return true
		}
		return false
	case replica.LevelSlow:
		if h.proposeIntervalTick > LevelSlow {
			return true
		}
		return false
	case replica.LevelSlowest:
		if h.proposeIntervalTick > LevelSlowest {
			return true
		}
		return false
	case replica.LevelStop:
		return false
	}
	return false
}

func (h *handler) shouldDestroy() bool {
	return h.proposeIntervalTick > LevelDestroy
}

func (h *handler) isLeader() bool {
	return h.handler.LeaderId() == h.r.opts.NodeId
}

func (h *handler) leaderId() uint64 {
	return h.handler.LeaderId()
}

func (h *handler) pausePropopose() bool {
	return h.handler.PausePropopose()

}

func (h *handler) learnerToFollower(learnerId uint64) error {
	if h.handler == nil {
		return nil
	}
	return h.handler.LearnerToFollower(learnerId)
}

func (h *handler) learnerToLeader(learnerId uint64) error {
	if h.handler == nil {
		return nil
	}
	return h.handler.LearnerToLeader(learnerId)
}

func (h *handler) followerToLeader(followerId uint64) error {
	if h.handler == nil {
		return nil
	}
	return h.handler.FollowerToLeader(followerId)
}

func (h *handler) setLastLeaderTerm(term uint32) {
	h.lastLeaderTerm.Store(term)
}

func (h *handler) getLastLeaderTerm() uint32 {
	return h.lastLeaderTerm.Load()
}
