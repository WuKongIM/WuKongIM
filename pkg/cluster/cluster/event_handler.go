package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type replicaAction interface {
	newProposeMessageWithLogs(logs []replica.Log) replica.Message
	newMsgSyncGetResp(to uint64, startIndex uint64, logs []replica.Log) replica.Message
	newMsgStoreAppendResp(index uint64) replica.Message
	newMsgApplyLogsRespMessage(index uint64) replica.Message

	lastLogIndexNoLock() uint64
	termNoLock() uint32
	step(msg replica.Message) error
	setLastIndex(index uint64) error
	setAppliedIndex(index uint64) error
	advance()
	getAndMergeLogs(msg replica.Message) ([]replica.Log, error)
	appendLogs(msg replica.Message) error
	applyLogs(msg replica.Message) error

	sendMessage(msg replica.Message)
}

type eventHandler struct {
	replicaAction       replicaAction
	proposeQueue        *proposeQueue // 提案队列
	appendLogStoreQueue *taskQueue
	applyLogStoreQueue  *taskQueue
	syncTaskQueue       *taskQueue // 同步任务队列
	getLogsTaskQueue    *taskQueue // 获取日志任务队列
	recvMessageQueue    *ReplicaMessageQueue
	messageWait         *messageWait
	opts                *Options
	doneC               chan struct{}

	wklog.Log
}

func newEventHandler(replicaAction replicaAction, log wklog.Log, opts *Options, doneC chan struct{}) *eventHandler {
	return &eventHandler{
		replicaAction:       replicaAction,
		proposeQueue:        newProposeQueue(),
		appendLogStoreQueue: newTaskQueue(opts.InitialTaskQueueCap),
		applyLogStoreQueue:  newTaskQueue(opts.InitialTaskQueueCap),
		syncTaskQueue:       newTaskQueue(opts.InitialTaskQueueCap),
		getLogsTaskQueue:    newTaskQueue(opts.InitialTaskQueueCap),
		Log:                 log,
		messageWait:         newMessageWait(),
		recvMessageQueue:    NewReplicaMessageQueue(opts.ReceiveQueueLength, false, opts.LazyFreeCycle, opts.MaxReceiveQueueSize),
		doneC:               doneC,
		opts:                opts,
	}
}

// 提案数据，并等待数据提交给大多数节点
func (e *eventHandler) proposeAndWaitCommits(ctx context.Context, logs []replica.Log, timeout time.Duration) ([]messageItem, error) {
	if len(logs) == 0 {
		return nil, errors.New("logs is empty")
	}

	_, proposeLogSpan := trace.GlobalTrace.StartSpan(ctx, "proposeLogs")

	// parentSpan.SetUint32("term", c.rc.Term())

	firstLog := logs[0]
	lastLog := logs[len(logs)-1]
	// c.Debug("add wait index", zap.Uint64("lastLogIndex", lastLog.Index), zap.Int("logsCount", len(logs)))
	// waitC, err := c.commitWait.addWaitIndex(lastLog.Index)
	// if err != nil {
	// 	c.mu.Unlock()
	// 	parentSpan.RecordError(err)
	// 	c.Error("add wait index failed", zap.Error(err))
	// 	return nil, err
	// }
	proposeLogSpan.SetUint64("firstMessageId", firstLog.MessageId)
	proposeLogSpan.SetUint64("lastMessageId", lastLog.MessageId)
	proposeLogSpan.SetInt("logCount", len(logs))

	// req := newProposeReq(c.rc.NewProposeMessageWithLogs(logs))
	// c.proposeQueue.push(req)

	// fmt.Println("advance start")
	// c.advance() // 已提按，上层可以进行推进提案了
	// fmt.Println("advance end")
	// select {
	// case err := <-req.result:
	// 	proposeLogSpan.End()
	// 	c.mu.Unlock()
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// case <-c.doneC:
	// 	proposeLogSpan.End()
	// 	c.mu.Unlock()
	// 	return nil, ErrStopped
	// }

	req := newProposeReq(logs)
	e.proposeQueue.push(req)

	messageIds := make([]uint64, 0, len(logs))
	for _, log := range logs {
		messageIds = append(messageIds, log.MessageId)
	}
	waitC := e.messageWait.addWait(ctx, messageIds)

	e.replicaAction.advance()

	proposeLogSpan.End()

	_, commitWaitSpan := trace.GlobalTrace.StartSpan(ctx, "commitWait")
	commitWaitSpan.SetString("messageIds", fmt.Sprintf("%v", messageIds))
	defer commitWaitSpan.End()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case items := <-waitC:
		e.Debug("finsh wait index", zap.Int("items", len(items)))
		return items, nil
	case <-timeoutCtx.Done():
		e.Debug("proposeAndWaitCommits timeout", zap.Int("logCount", len(logs)))
		commitWaitSpan.RecordError(timeoutCtx.Err())
		return nil, timeoutCtx.Err()
	case <-e.doneC:
		commitWaitSpan.RecordError(ErrStopped)
		return nil, ErrStopped
	}
}

func (e *eventHandler) handleEvents() (bool, error) {

	hasEvent := false
	var (
		err      error
		handleOk bool
	)

	// propose
	handleOk, err = e.handleProposes()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// append log task
	handleOk, err = e.handleAppendLogTask()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// recv message
	handleOk, err = e.handleRecvMessages()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// sync logs task
	handleOk, err = e.handleSyncTask()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// get logs task
	handleOk, err = e.handleGetLogTask()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// apply logs task
	handleOk, err = e.handleApplyLogTask()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	return hasEvent, nil
}

func (e *eventHandler) handleProposes() (bool, error) {
	// propose
	var (
		ok         bool = true
		proposeReq proposeReq
		err        error
		hasEvent   bool
	)
	for ok {
		proposeReq, ok = e.proposeQueue.pop()
		if !ok {
			break
		}
		for i, lg := range proposeReq.logs {
			lg.Index = e.replicaAction.lastLogIndexNoLock() + 1 + uint64(i)
			lg.Term = e.replicaAction.termNoLock()
			proposeReq.logs[i] = lg
			e.messageWait.didPropose(lg.MessageId, lg.Index)
		}

		err = e.replicaAction.step(e.replicaAction.newProposeMessageWithLogs(proposeReq.logs))
		if err != nil {
			e.Error("step propose message failed", zap.Error(err))
			return false, err
		}
		hasEvent = true

	}
	return hasEvent, nil
}

func (e *eventHandler) handleAppendLogTask() (bool, error) {
	firstTask := e.appendLogStoreQueue.first()
	var (
		hasEvent bool
	)
	for firstTask != nil {
		if !firstTask.isTaskFinished() {
			break
		}
		if firstTask.hasErr() {
			e.Panic("append log store message failed", zap.Error(firstTask.err()))
			return false, firstTask.err()
		}
		err := e.replicaAction.step(firstTask.resp())
		if err != nil {
			e.Panic("step local store message failed", zap.Error(err))
			return false, err
		}
		err = e.replicaAction.setLastIndex(firstTask.resp().Index) // TODO: 耗时操作，不应该放到ready里执行，后续要优化
		if err != nil {
			e.Panic("set last index failed", zap.Error(err))
			return false, err
		}
		hasEvent = true
		e.appendLogStoreQueue.removeFirst()
		firstTask = e.appendLogStoreQueue.first()
	}
	return hasEvent, nil
}

func (e *eventHandler) handleRecvMessages() (bool, error) {
	// recv message
	var hasEvent bool
	msgs := e.recvMessageQueue.Get()
	for _, msg := range msgs {
		err := e.replicaAction.step(msg)
		if err != nil {
			e.Error("step message failed", zap.Error(err))
			return false, err
		}
		hasEvent = true
	}
	return hasEvent, nil
}

func (e *eventHandler) handleSyncTask() (bool, error) {
	tasks := e.syncTaskQueue.getAll()
	var hasEvent bool
	for _, task := range tasks {
		if task.isTaskFinished() {
			if len(task.resp().Logs) > 0 {
				e.Debug("sync logs done", zap.Uint64("syncIndex", task.resp().Index), zap.Uint64("startLogIndex", task.resp().Logs[0].Index), zap.Uint64("endLogIndex", task.resp().Logs[len(task.resp().Logs)-1].Index), zap.Uint64("from", task.resp().From), zap.Uint64("index", task.resp().Index), zap.Int("logCount", len(task.resp().Logs)))
			}
			err := e.replicaAction.step(task.resp())
			if err != nil {
				e.Error("step sync task failed", zap.Error(err))
				return false, err
			}
			hasEvent = true
			e.syncTaskQueue.remove(task.taskKey())
		}
	}
	return hasEvent, nil
}

func (e *eventHandler) handleGetLogTask() (bool, error) {
	var (
		err      error
		hasEvent bool
	)
	getLogsTasks := e.getLogsTaskQueue.getAll()
	for _, getLogsTask := range getLogsTasks {
		if getLogsTask.isTaskFinished() {
			if getLogsTask.hasErr() {
				e.Error("get logs task error", zap.Error(getLogsTask.err()))
			} else {
				err = e.replicaAction.step(getLogsTask.resp())
				if err != nil {
					e.Error("step get logs task failed", zap.Error(err))
				}
			}
			e.getLogsTaskQueue.remove(getLogsTask.taskKey())
			hasEvent = true
		}
	}
	return hasEvent, nil
}

func (e *eventHandler) handleApplyLogTask() (bool, error) {
	firstTask := e.applyLogStoreQueue.first()
	var (
		hasEvent bool
	)
	for firstTask != nil {
		if !firstTask.isTaskFinished() {
			break
		}
		if firstTask.hasErr() {
			e.Panic("apply log store message failed", zap.Error(firstTask.err()))
			return false, firstTask.err()
		}
		err := e.replicaAction.step(firstTask.resp())
		if err != nil {
			e.Panic("step apply store message failed", zap.Error(err))
			return false, err
		}
		err = e.replicaAction.setAppliedIndex(firstTask.resp().Index) // TODO: 耗时操作，不应该放到ready里执行，后续要优化
		if err != nil {
			e.Panic("set applied index failed", zap.Error(err))
			return false, err
		}
		hasEvent = true
		e.applyLogStoreQueue.removeFirst()
		firstTask = e.applyLogStoreQueue.first()
	}
	return hasEvent, nil
}

func (e *eventHandler) addGetLogsTask(t task) {
	e.getLogsTaskQueue.add(t)
}

func (e *eventHandler) existsSyncTask(key string) bool {
	return e.syncTaskQueue.exists(key)
}

func (e *eventHandler) addSyncTask(t task) {
	e.syncTaskQueue.add(t)
}

func (e *eventHandler) handleRecvMessage(msg replica.Message) error {

	if msg.MsgType == replica.MsgSync { // 领导收到副本的同步请求
		// c.Debug("sync logs", zap.Uint64("index", msg.Index), zap.Uint64("from", msg.From), zap.Uint64("lastLogIndex", c.rc.LastLogIndex()))

		// ctxs := c.commitWait.spanContexts(c.LastLogIndex())

		// 如果有需要提交的span，则同时追踪sync请求

		messageWaitItems := e.messageWait.waitItemsWithStartSeq(msg.Index - 1)
		for _, messageWaitItem := range messageWaitItems {
			_, syncSpan := trace.GlobalTrace.StartSpan(messageWaitItem.ctx, fmt.Sprintf("logsSync[from %d]", msg.From))
			defer syncSpan.End()
			syncSpan.SetUint64("from", msg.From)
			syncSpan.SetUint32("term", msg.Term)
			syncSpan.SetUint64("startSyncIndex", msg.Index)
			syncSpan.SetUint64("lastLogIndex", e.replicaAction.lastLogIndexNoLock())
		}
		if len(messageWaitItems) > 0 {
			e.Debug("sync logs", zap.Uint64("index", msg.Index), zap.Uint64("from", msg.From), zap.Uint64("lastLogIndex", e.replicaAction.lastLogIndexNoLock()))
		}

	} else if msg.MsgType == replica.MsgSyncResp { // 副本收到领导的同步响应

		task := e.syncTaskQueue.get(getSyncTaskKey(msg.From, msg.Index))
		if task == nil {
			e.Warn("sync task not exists", zap.Uint64("from", msg.From), zap.Uint64("index", msg.Index))
			return nil
		}

		if !task.isTaskFinished() {

			task.setResp(msg)
			task.taskFinished()
			if len(msg.Logs) > 0 {
				e.Debug("sync task finished", zap.Uint64("from", msg.From), zap.Uint64("index", msg.Index), zap.Uint64("startLogIndex", msg.Logs[0].Index), zap.Uint64("endLogIndex", msg.Logs[len(msg.Logs)-1].Index), zap.Int("logCount", len(msg.Logs)))
				e.replicaAction.advance()
			}

		}
		return nil // MsgSyncResp消息由 syncTaskQueue处理
	}

	if added, stopped := e.recvMessageQueue.Add(msg); !added || stopped {
		e.Error("messageQueue add failed")
		return errors.New("messageQueue add failed")
	}
	e.replicaAction.advance() // 已接收到消息，推进分布式进程
	return nil
}

func (e *eventHandler) handleReadyMessages(msgs []replica.Message) {
	for _, msg := range msgs {
		if msg.To == e.opts.NodeID {
			e.handleLocalMsg(msg)
			continue
		}
		if msg.To == 0 {
			e.Error("msg.To is 0", zap.String("msg", msg.MsgType.String()))
			continue
		}

		go e.replicaAction.sendMessage(msg)

	}
}

func (e *eventHandler) handleLocalMsg(msg replica.Message) {
	if msg.To != e.opts.NodeID {
		e.Warn("handle local msg, but msg to is not self", zap.String("msgType", msg.MsgType.String()), zap.Uint64("to", msg.To))
		return
	}
	switch msg.MsgType {
	case replica.MsgSyncGet: // 处理sync get请求
		e.handleSyncGet(msg)
	case replica.MsgStoreAppend: // 处理store append请求
		e.handleStoreAppend(msg)
	case replica.MsgApplyLogsReq: // 处理apply logs请求
		e.handleApplyLogsReq(msg)
	}
}

func (e *eventHandler) handleSyncGet(msg replica.Message) {
	if msg.Index <= 0 {
		return
	}

	e.Debug("query logs", zap.Uint64("index", msg.Index), zap.Uint64("from", msg.From))
	tk := newGetLogsTask(msg.Index)

	tk.setExec(func() error {
		resultLogs, err := e.replicaAction.getAndMergeLogs(msg)
		if err != nil {
			e.Error("get logs error", zap.Error(err))
		}
		resp := e.replicaAction.newMsgSyncGetResp(msg.From, msg.Index, resultLogs)
		tk.setResp(resp)
		tk.taskFinished()
		if len(resultLogs) > 0 {
			e.replicaAction.advance()
		}

		return nil
	})
	e.addGetLogsTask(tk)
}

func (e *eventHandler) handleStoreAppend(msg replica.Message) {
	if len(msg.Logs) == 0 {
		return
	}

	lastLog := msg.Logs[len(msg.Logs)-1]

	tk := newStoreAppendTask(lastLog.Index)
	tk.setExec(func() error {
		err := e.replicaAction.appendLogs(msg)
		if err != nil {
			e.Panic("append logs error", zap.Error(err))
			return err
		}
		tk.setResp(e.replicaAction.newMsgStoreAppendResp(lastLog.Index))
		tk.taskFinished()
		e.replicaAction.advance()
		return nil
	})

	e.appendLogStoreQueue.add(tk)

}

// 处理应用日志请求
func (e *eventHandler) handleApplyLogsReq(msg replica.Message) {
	applyingIndex := msg.ApplyingIndex
	committedIndex := msg.CommittedIndex
	if applyingIndex > committedIndex {
		e.Debug("not apply logs req", zap.Uint64("applyingIndex", applyingIndex), zap.Uint64("committedIndex", committedIndex))
		return
	}
	e.Debug("apply logs req", zap.Uint64("applyingIndex", applyingIndex), zap.Uint64("committedIndex", committedIndex))
	tk := newApplyLogsTask(committedIndex)

	tk.setExec(func() error {
		err := e.replicaAction.applyLogs(msg)
		if err != nil {
			e.Panic("apply logs error", zap.Error(err))
			return err
		}
		tk.setResp(e.replicaAction.newMsgApplyLogsRespMessage(committedIndex))
		tk.taskFinished()
		e.replicaAction.advance()
		return nil
	})

	e.applyLogStoreQueue.add(tk)

}
