package reactor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

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
	// GetAndMergeLogs 获取并合并日志
	GetAndMergeLogs(lastIndex uint64, msg replica.Message) ([]replica.Log, error)
	// ApplyLog 应用日志 [startLogIndex,endLogIndex) 之间的日志
	ApplyLog(startLogIndex, endLogIndex uint64) error
	// SlowDown 降速
	SlowDown()
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
	SetAppliedIndex(index uint64) error
	// IsPrepared 是否准备好
	IsPrepared() bool
	// 领导者Id
	LeaderId() uint64
	// PausePropopose 是否暂停提案
	PausePropopose() bool
}

type handler struct {
	key      string
	handler  IHandler
	msgQueue *MessageQueue

	lastIndex     atomic.Uint64 // 当前频道最后一条日志索引
	lastIndexLock sync.Mutex

	appliedIndex     atomic.Uint64 // 已应用的索引
	appliedIndexLock sync.Mutex

	proposeQueue *proposeQueue // 提案队列
	proposeWait  *proposeWait  // 提案等待

	applyLogStoreQueue *taskQueue // 应用日志任务队列
	getLogsTaskQueue   *taskQueue // 获取日志任务队列

	proposeIntervalTick int // 提案间隔tick数量

	sync struct {
		syncingLogIndex uint64        // 正在同步的日志索引
		syncStatus      syncStatus    // 是否正在同步
		startSyncTime   time.Time     // 开始同步时间
		syncTimeout     time.Duration // 同步超时时间
		resp            replica.Message
	}
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

	h.applyLogStoreQueue = newTaskQueue(r, r.opts.InitialTaskQueueCap)
	h.getLogsTaskQueue = newTaskQueue(r, r.opts.InitialTaskQueueCap)
	h.proposeQueue = newProposeQueue()
	h.proposeWait = newProposeWait(key)
	h.sync.syncTimeout = 5 * time.Second

}

func (h *handler) reset() {
	h.Log = nil
	h.handler = nil
	h.key = ""
	h.msgQueue = nil
	h.msgQueue = nil
	h.applyLogStoreQueue = nil
	h.getLogsTaskQueue = nil
	h.proposeQueue = nil
	h.proposeWait = nil
	h.proposeIntervalTick = 0
	h.resetSync()
}

func (h *handler) resetSync() {
	h.sync.syncStatus = syncStatusNone
	h.sync.syncingLogIndex = 0
	h.sync.startSyncTime = time.Time{}
	h.sync.resp = replica.EmptyMessage

}

func (h *handler) ready() replica.Ready {
	return h.handler.Ready()
}

func (h *handler) hasReady() bool {
	return h.handler.HasReady()
}

func (h *handler) setHardState(hd replica.HardState) {
	h.handler.SetHardState(hd)
}

func (h *handler) addMessage(m Message) {
	h.msgQueue.Add(m)
}

func (h *handler) getMessages() []Message {
	return h.msgQueue.Get()
}

func (h *handler) addApplyLogStoreTask(task task) {
	h.applyLogStoreQueue.add(task)
}

func (h *handler) addGetLogsTask(task task) {
	h.getLogsTaskQueue.add(task)
}

func (h *handler) getAndMergeLogs(lastIndex uint64, msg replica.Message) ([]replica.Log, error) {
	return h.handler.GetAndMergeLogs(lastIndex, msg)
}

func (h *handler) applyLogs(startLogIndex, endLogIndex uint64) error {
	return h.handler.ApplyLog(startLogIndex, endLogIndex)
}

func (h *handler) addPropose(req proposeReq) {
	h.proposeQueue.push(req)
}

func (h *handler) popPropose() (proposeReq, bool) {
	return h.proposeQueue.pop()
}

func (h *handler) didPropose(key string, logId uint64, logIndex uint64) {
	h.proposeWait.didPropose(key, logId, logIndex)
}

func (h *handler) didCommit(startLogIndex uint64, endLogIndex uint64) {
	h.proposeWait.didCommit(startLogIndex, endLogIndex)
}

func (h *handler) addWait(ctx context.Context, key string, ids []uint64) chan []ProposeResult {
	return h.proposeWait.add(ctx, key, ids)
}

func (h *handler) removeWait(key string) {
	h.proposeWait.remove(key)
}

func (h *handler) lastLogIndexAndTerm() (uint64, uint32) {
	return h.handler.LastLogIndexAndTerm()
}

func (h *handler) allGetLogsTask() []task {
	return h.getLogsTaskQueue.getAll()
}

func (h *handler) removeGetLogsTask(key string) {
	h.getLogsTaskQueue.remove(key)
}

func (h *handler) firstApplyLogStoreTask() task {
	return h.applyLogStoreQueue.first()
}

func (h *handler) removeFirstApplyLogStoreTask() {
	h.applyLogStoreQueue.removeFirst()
}

func (h *handler) setAppliedIndex() error {
	h.appliedIndexLock.Lock()
	defer h.appliedIndexLock.Unlock()
	return h.handler.SetAppliedIndex(h.appliedIndex.Load())
}

func (h *handler) isPrepared() bool {
	return h.handler.IsPrepared()
}

func (h *handler) tick() {
	h.handler.Tick()
	h.proposeIntervalTick++
}

func (h *handler) resetProposeIntervalTick() {
	h.proposeIntervalTick = 0
	h.resetSlowDown()
}

func (h *handler) slowDown() {
	h.handler.SlowDown()
}

func (h *handler) speedLevel() replica.SpeedLevel {
	return h.handler.SpeedLevel()
}

func (h *handler) resetSlowDown() {
	h.handler.SetSpeedLevel(replica.LevelFast)
}

// 是否需要降速
func (h *handler) shouldSlowDown() bool {
	switch h.speedLevel() {
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

func (h *handler) getAndResetMsgSyncResp() (replica.Message, bool) {

	switch h.sync.syncStatus {
	case syncStatusSynced:
		h.sync.syncStatus = syncStatusNone
		return h.sync.resp, true
	default:
		return replica.EmptyMessage, false

	}
}

func (h *handler) setMsgSyncResp(msg replica.Message) {
	if h.sync.syncStatus != syncStatusSyncing {
		h.Warn("setMsgSyncResp: syncStatus != syncStatusSyncing", zap.Uint8("syncStatus", uint8(h.sync.syncStatus)), zap.Uint64("msgIndex", msg.Index), zap.Uint64("syncingLogIndex", h.sync.syncingLogIndex), zap.Uint64("resp.index", h.sync.resp.Index))
		return
	}

	if msg.MsgType != replica.MsgSyncResp {
		h.Warn("setMsgSyncResp: msgType != MsgSyncResp", zap.String("msgType", msg.MsgType.String()))
		return
	}

	if msg.Index != h.sync.syncingLogIndex {
		h.Warn("setMsgSyncResp: msg.Index != c.sync.syncingLogIndex", zap.Uint64("msgIndex", msg.Index), zap.Uint64("syncingLogIndex", h.sync.syncingLogIndex))
		return
	}
	h.sync.resp = msg
	h.sync.syncStatus = syncStatusSynced
}

func (h *handler) step(m replica.Message) error {
	return h.handler.Step(m)
}
