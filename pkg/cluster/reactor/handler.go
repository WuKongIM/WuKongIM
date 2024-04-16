package reactor

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type IHandler interface {
	// LastLogIndexAndTerm 获取最后一条日志的索引和任期
	LastLogIndexAndTerm() (uint64, uint32)
	HasReady() bool
	// Ready 获取ready事件
	Ready() replica.Ready
	// GetAndMergeLogs 获取并合并日志
	GetAndMergeLogs(msg replica.Message) ([]replica.Log, error)
	// AppendLog 追加日志
	AppendLog(logs []replica.Log) error
	// ApplyLog 应用日志 [startLogIndex,endLogIndex) 之间的日志
	ApplyLog(startLogIndex, endLogIndex uint64) error
	// SlowDown 降速
	SlowDown()
	// SetHardState 设置HardState
	SetHardState(hd replica.HardState)
	// Tick tick
	Tick()
	// Step 步进消息
	Step(m replica.Message) error
	// SetLastIndex 设置最后一条日志的索引
	SetLastIndex(index uint64) error
	// SetAppliedIndex 设置已应用的索引
	SetAppliedIndex(index uint64) error
	// IsPrepared 是否准备好
	IsPrepared() bool
}

type handler struct {
	key      string
	handler  IHandler
	msgQueue *MessageQueue

	proposeQueue *proposeQueue // 提案队列
	proposeWait  *proposeWait  // 提案等待

	appendLogStoreQueue *taskQueue // 追加日志任务队列
	applyLogStoreQueue  *taskQueue // 应用日志任务队列
	getLogsTaskQueue    *taskQueue // 获取日志任务队列

	sync struct {
		syncingLogIndex uint64        // 正在同步的日志索引
		syncStatus      syncStatus    // 是否正在同步
		startSyncTime   time.Time     // 开始同步时间
		syncTimeout     time.Duration // 同步超时时间
		resp            replica.Message
	}
	wklog.Log
}

func (h *handler) init(key string, handler IHandler, r *Reactor) {
	h.Log = wklog.NewWKLog(fmt.Sprintf("handler[%s]", key))
	h.key = key
	h.handler = handler
	h.msgQueue = r.newMessageQueue()

	h.appendLogStoreQueue = newTaskQueue(r.taskPool, r.opts.InitialTaskQueueCap)
	h.applyLogStoreQueue = newTaskQueue(r.taskPool, r.opts.InitialTaskQueueCap)
	h.getLogsTaskQueue = newTaskQueue(r.taskPool, r.opts.InitialTaskQueueCap)
	h.proposeQueue = newProposeQueue()
	h.proposeWait = newProposeWait()
	h.sync.syncTimeout = 5 * time.Second

}

func (h *handler) reset() {
	h.Log = nil
	h.handler = nil
	h.key = ""
	h.msgQueue.gc()
	h.msgQueue = nil
	h.appendLogStoreQueue = nil
	h.applyLogStoreQueue = nil
	h.getLogsTaskQueue = nil
	h.proposeQueue = nil
	h.proposeWait = nil
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

func (h *handler) addAppendLogStoreTask(task task) {
	h.appendLogStoreQueue.add(task)
}

func (h *handler) addApplyLogStoreTask(task task) {
	h.applyLogStoreQueue.add(task)
}

func (h *handler) addGetLogsTask(task task) {
	h.getLogsTaskQueue.add(task)
}

func (h *handler) getAndMergeLogs(msg replica.Message) ([]replica.Log, error) {
	return h.handler.GetAndMergeLogs(msg)
}

func (h *handler) appendLogs(logs []replica.Log) error {
	return h.handler.AppendLog(logs)
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

func (h *handler) addWait(ctx context.Context, key string, ids []uint64) chan []*ProposeResult {
	return h.proposeWait.add(ctx, key, ids)
}

func (h *handler) lastLogIndexAndTerm() (uint64, uint32) {
	return h.handler.LastLogIndexAndTerm()
}

func (h *handler) firstAppendLogStoreTask() task {
	return h.appendLogStoreQueue.first()
}

func (h *handler) removeFirstAppendLogStoreTask() {
	h.appendLogStoreQueue.removeFirst()
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

func (h *handler) setLastIndex(index uint64) error {
	return h.handler.SetLastIndex(index)
}

func (h *handler) setAppliedIndex(index uint64) error {
	return h.handler.SetAppliedIndex(index)
}

func (h *handler) isPrepared() bool {
	return h.handler.IsPrepared()
}

func (h *handler) tick() {
	h.handler.Tick()
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
