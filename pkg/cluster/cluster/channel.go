package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/atomic"
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
	lastActivity               atomic.Time // 最后一次活跃时间
	commitWait                 *commitWait
	messageWait                *messageWait
	traceRecord                *traceRecord
	doneC                      chan struct{}
	wklog.Log
	prev *channel
	next *channel

	mu                  deadlock.Mutex
	localstorage        *localStorage
	recvMessageQueue    *ReplicaMessageQueue
	appendLogStoreQueue *taskQueue
	applyLogStoreQueue  *taskQueue
	proposeQueue        *proposeQueue // 提案队列
	syncTaskQueue       *taskQueue    // 同步任务队列
	getLogsTaskQueue    *taskQueue    // 获取日志任务队列

	leaderId atomic.Uint64
	advance  func() // 推进分布式进程
}

func newChannel(clusterConfig *wkstore.ChannelClusterConfig, appliedIdx uint64, localstorage *localStorage, advance func(), opts *Options) *channel {
	shardNo := ChannelKey(clusterConfig.ChannelID, clusterConfig.ChannelType)
	rc := replica.New(opts.NodeID, shardNo, replica.WithAppliedIndex(appliedIdx), replica.WithReplicas(clusterConfig.Replicas), replica.WithStorage(newProxyReplicaStorage(shardNo, opts.MessageLogStorage, localstorage)))
	ch := &channel{
		maxHandleReadyCountOfBatch: 50,
		rc:                         rc,
		opts:                       opts,
		Log:                        wklog.NewWKLog(fmt.Sprintf("Channel[%s]", shardNo)),
		commitWait:                 newCommitWait(),
		messageWait:                newMessageWait(),
		channelID:                  clusterConfig.ChannelID,
		channelType:                clusterConfig.ChannelType,
		clusterConfig:              clusterConfig,
		doneC:                      make(chan struct{}),
		localstorage:               localstorage,
		traceRecord:                newTraceRecord(),
		proposeQueue:               newProposeQueue(),
		appendLogStoreQueue:        newTaskQueue(opts.InitialTaskQueueCap),
		applyLogStoreQueue:         newTaskQueue(opts.InitialTaskQueueCap),
		syncTaskQueue:              newTaskQueue(opts.InitialTaskQueueCap),
		getLogsTaskQueue:           newTaskQueue(opts.InitialTaskQueueCap),
		advance:                    advance,
		recvMessageQueue:           NewReplicaMessageQueue(opts.ReceiveQueueLength, false, opts.LazyFreeCycle, opts.MaxReceiveQueueSize),
	}
	ch.lastActivity.Store(time.Now())
	return ch
}

func (c *channel) updateClusterConfig(clusterConfig *wkstore.ChannelClusterConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clusterConfig = clusterConfig
	c.rc.SetReplicas(clusterConfig.Replicas)
	c.setLeaderId(clusterConfig.LeaderId)

	if clusterConfig.LeaderId == c.opts.NodeID {
		c.rc.BecomeLeader(clusterConfig.Term)
	} else {
		c.rc.BecomeFollower(clusterConfig.Term, clusterConfig.LeaderId)
	}
}

func (c *channel) updateClusterConfigLeaderIdAndTerm(term uint32, leaderId uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clusterConfig.Term = term
	c.clusterConfig.LeaderId = leaderId
}

func (c *channel) ready() replica.Ready {
	if c.destroy {
		return replica.Ready{}
	}
	return c.rc.Ready()
}

func (c *channel) hasReady() bool {
	if c.destroy {
		return false
	}
	return c.rc.HasReady()
}

// // 任命为领导
// func (c *channel) appointLeader(term uint32) error {

// 	return c.stepLock(replica.Message{
// 		MsgType:           replica.MsgAppointLeaderReq,
// 		AppointmentLeader: c.opts.NodeID,
// 		Term:              term,
// 	})

// }

// // 任命指定节点为领导
// func (c *channel) appointLeaderTo(term uint32, to uint64) error {
// 	return c.stepLock(replica.Message{
// 		MsgType:           replica.MsgAppointLeaderReq,
// 		AppointmentLeader: to,
// 		Term:              term,
// 	})
// }

// func (c *channel) stepLock(msg replica.Message) error {
// 	c.mu.Lock()
// 	err := c.step(msg)
// 	c.mu.Unlock()
// 	return err

// }

func (c *channel) step(msg replica.Message) error {
	if c.destroy {
		return errors.New("channel destroy, can not step")
	}
	c.lastActivity.Store(time.Now())
	return c.rc.Step(msg)
}

// 提案数据，并等待数据提交给大多数节点
func (c *channel) proposeAndWaitCommits(ctx context.Context, logs []replica.Log, timeout time.Duration) ([]messageItem, error) {
	if len(logs) == 0 {
		return nil, errors.New("logs is empty")
	}

	if c.destroy {
		return nil, errors.New("channel destroy, can not propose")
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
	c.proposeQueue.push(req)

	messageIds := make([]uint64, 0, len(logs))
	for _, log := range logs {
		messageIds = append(messageIds, log.MessageId)
	}
	waitC := c.messageWait.addWait(ctx, messageIds)

	c.advance()

	proposeLogSpan.End()

	_, commitWaitSpan := trace.GlobalTrace.StartSpan(ctx, "commitWait")
	commitWaitSpan.SetString("messageIds", fmt.Sprintf("%v", messageIds))
	defer commitWaitSpan.End()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case items := <-waitC:
		c.Debug("finsh wait index", zap.Int("items", len(items)))
		return items, nil
	case <-timeoutCtx.Done():
		c.Debug("proposeAndWaitCommits timeout", zap.Int("logCount", len(logs)))
		commitWaitSpan.RecordError(timeoutCtx.Err())
		return nil, timeoutCtx.Err()
	case <-c.doneC:
		commitWaitSpan.RecordError(ErrStopped)
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
	return c.lastActivity.Load()
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
	c.lastActivity.Store(time.Now())
	switch msg.MsgType {
	case replica.MsgSyncGet: // 处理sync get请求
		c.handleSyncGet(msg)
	case replica.MsgStoreAppend: // 处理store append请求
		c.handleStoreAppend(msg)
	case replica.MsgApplyLogsReq: // 处理apply logs请求
		c.handleApplyLogsReq(msg)
	}
}

func (c *channel) handleSyncGet(msg replica.Message) {
	if msg.Index <= 0 {
		return
	}

	c.Debug("query logs", zap.Uint64("index", msg.Index), zap.Uint64("from", msg.From))
	tk := newGetLogsTask(msg.Index)

	tk.setExec(func() error {
		resultLogs, err := c.getAndMergeLogs(msg)
		if err != nil {
			c.Error("get logs error", zap.Error(err))
		}
		resp := c.rc.NewMsgSyncGetResp(msg.From, msg.Index, resultLogs)
		tk.setResp(resp)
		tk.taskFinished()
		if len(resultLogs) > 0 {
			c.advance()
		}

		return nil
	})
	c.getLogsTaskQueue.add(tk)
}

func (c *channel) getAndMergeLogs(msg replica.Message) ([]replica.Log, error) {

	unstableLogs := msg.Logs
	startIndex := msg.Index
	if len(unstableLogs) > 0 {
		startIndex = unstableLogs[len(unstableLogs)-1].Index + 1
	}

	messageWaitItems := c.messageWait.waitItemsWithStartSeq(startIndex)
	spans := make([]trace.Span, 0, len(messageWaitItems))
	for _, messageWaitItem := range messageWaitItems {
		_, span := trace.GlobalTrace.StartSpan(messageWaitItem.ctx, fmt.Sprintf("logsGet[node %d]", c.opts.NodeID))
		defer span.End()
		span.SetUint64("startIndex", startIndex)
		span.SetInt("unstableLogs", len(unstableLogs))
		spans = append(spans, span)

	}

	lastIndex, err := c.opts.MessageLogStorage.LastIndex(ChannelKey(c.channelID, c.channelType))
	if err != nil {
		c.Error("handleSyncGet: get last index error", zap.Error(err))
		return nil, err
	}
	var resultLogs []replica.Log
	if startIndex <= lastIndex {
		logs, err := c.getLogs(startIndex, lastIndex+1, uint64(c.opts.LogSyncLimitSizeOfEach))
		if err != nil {
			c.Error("get logs error", zap.Error(err), zap.Uint64("startIndex", startIndex), zap.Uint64("lastIndex", lastIndex))
			return nil, err
		}
		resultLogs = extend(logs, unstableLogs)
	} else {
		// c.Warn("handleSyncGet: startIndex > lastIndex", zap.Uint64("startIndex", startIndex), zap.Uint64("lastIndex", lastIndex))
	}
	for _, span := range spans {
		span.SetUint64("lastIndex", lastIndex)
		span.SetInt("resultLogs", len(resultLogs))
	}
	return resultLogs, nil
}

func extend(dst, vals []replica.Log) []replica.Log {
	need := len(dst) + len(vals)
	if need <= cap(dst) {
		return append(dst, vals...) // does not allocate
	}
	buf := make([]replica.Log, need) // allocates precisely what's needed
	copy(buf, dst)
	copy(buf[len(dst):], vals)
	return buf
}

func (c *channel) getLogs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	logs, err := c.opts.MessageLogStorage.Logs(ChannelKey(c.channelID, c.channelType), startLogIndex, endLogIndex, limitSize)
	if err != nil {
		c.Error("get logs error", zap.Error(err))
		return nil, err
	}
	return logs, nil
}

func (c *channel) handleStoreAppend(msg replica.Message) {
	if len(msg.Logs) == 0 {
		return
	}

	lastLog := msg.Logs[len(msg.Logs)-1]

	tk := newStoreAppendTask(lastLog.Index)
	tk.setExec(func() error {
		err := c.appendLogs(msg)
		if err != nil {
			c.Panic("append logs error", zap.Error(err))
			return err
		}
		tk.setResp(c.rc.NewMsgStoreAppendResp(lastLog.Index))
		tk.taskFinished()
		c.advance()
		return nil
	})

	c.appendLogStoreQueue.add(tk)

}

func (c *channel) appendLogs(msg replica.Message) error {
	shardNo := ChannelKey(c.channelID, c.channelType)

	firstLog := msg.Logs[0]
	lastLog := msg.Logs[len(msg.Logs)-1]

	messageWaitItems := c.messageWait.waitItemsWithRange(firstLog.Index, lastLog.Index+1)
	for _, messageWaitItem := range messageWaitItems {
		_, span := trace.GlobalTrace.StartSpan(messageWaitItem.ctx, fmt.Sprintf("logsAppend[node %d]", c.opts.NodeID))
		defer span.End()
		span.SetInt("logCount", len(msg.Logs))
		span.SetUint64("firstLogIndex", firstLog.Index)
		span.SetUint64("lastLogIndex", lastLog.Index)
	}

	start := time.Now()

	c.Debug("append log", zap.Uint64("lastLogIndex", lastLog.Index))
	err := c.opts.MessageLogStorage.AppendLog(shardNo, msg.Logs)
	if err != nil {
		c.Panic("append log error", zap.Error(err))
	}
	c.Debug("append log done", zap.Uint64("lastLogIndex", lastLog.Index), zap.Duration("cost", time.Since(start)))

	return nil

}

func (c *channel) setLastIndex(lastIndex uint64) error {
	shardNo := ChannelKey(c.channelID, c.channelType)
	err := c.opts.MessageLogStorage.SetLastIndex(shardNo, lastIndex)
	if err != nil {
		c.Error("set last index error", zap.Error(err))
		return err
	}
	return nil
}

func (c *channel) setAppliedIndex(appliedIndex uint64) error {
	shardNo := ChannelKey(c.channelID, c.channelType)
	err := c.localstorage.setAppliedIndex(shardNo, appliedIndex)
	if err != nil {
		c.Error("set applied index error", zap.Error(err))
		return err
	}
	return nil
}

// 处理应用日志请求
func (c *channel) handleApplyLogsReq(msg replica.Message) {
	if msg.CommittedIndex <= 0 || msg.AppliedIndex >= msg.CommittedIndex {
		c.Debug("not apply logs req", zap.Uint64("appliedIndex", msg.AppliedIndex), zap.Uint64("committedIndex", msg.CommittedIndex))
		return
	}
	c.Debug("apply logs req", zap.Uint64("appliedIndex", msg.AppliedIndex), zap.Uint64("committedIndex", msg.CommittedIndex))
	tk := newApplyLogsTask(msg.CommittedIndex)

	tk.setExec(func() error {
		err := c.applyLogs(msg)
		if err != nil {
			c.Panic("apply logs error", zap.Error(err))
			return err
		}
		tk.setResp(c.rc.NewMsgApplyLogsRespMessage(msg.CommittedIndex))
		tk.taskFinished()
		c.advance()
		return nil
	})

	c.applyLogStoreQueue.add(tk)

}

func (c *channel) applyLogs(msg replica.Message) error {
	if msg.AppliedIndex > msg.CommittedIndex {
		return fmt.Errorf("appliedIndex > committedIndex, appliedIndex: %d, committedIndex: %d", msg.AppliedIndex, msg.CommittedIndex)
	}
	messageWaitItems := c.messageWait.waitItemsWithRange(msg.AppliedIndex, msg.CommittedIndex+1)
	spans := make([]trace.Span, 0, len(messageWaitItems))
	for _, messageWaitItem := range messageWaitItems {
		_, span := trace.GlobalTrace.StartSpan(messageWaitItem.ctx, fmt.Sprintf("logsCommit[node %d]", c.opts.NodeID))
		defer span.End()
		span.SetUint64("appliedIndex", msg.AppliedIndex)
		span.SetUint64("committedIndex", msg.CommittedIndex)
		spans = append(spans, span)
	}

	start := time.Now()
	c.Debug("commit wait", zap.Uint64("committedIndex", msg.CommittedIndex))
	c.messageWait.didCommit(msg.AppliedIndex, msg.CommittedIndex+1)
	for _, span := range spans {
		span.End()
	}
	c.Debug("commit wait done", zap.Duration("cost", time.Since(start)), zap.Uint64("committedIndex", msg.CommittedIndex))
	return nil

}

func (c *channel) handleRecvMessage(msg replica.Message) error {
	if c.destroy {
		return errors.New("channel destroy, can not handleMessage")
	}
	c.lastActivity.Store(time.Now())

	if msg.MsgType == replica.MsgSync { // 领导收到副本的同步请求
		// c.Debug("sync logs", zap.Uint64("index", msg.Index), zap.Uint64("from", msg.From), zap.Uint64("lastLogIndex", c.rc.LastLogIndex()))

		// ctxs := c.commitWait.spanContexts(c.LastLogIndex())

		// 如果有需要提交的span，则同时追踪sync请求

		messageWaitItems := c.messageWait.waitItemsWithStartSeq(msg.Index)
		for _, messageWaitItem := range messageWaitItems {
			_, syncSpan := trace.GlobalTrace.StartSpan(messageWaitItem.ctx, fmt.Sprintf("logsSync[from %d]", msg.From))
			defer syncSpan.End()
			syncSpan.SetUint64("from", msg.From)
			syncSpan.SetUint32("term", msg.Term)
			syncSpan.SetUint64("startSyncIndex", msg.Index)
			syncSpan.SetUint64("lastLogIndex", c.rc.LastLogIndex())
		}

		// proposeCtxs := c.traceRecord.getProposeContextsWithRange(msg.CommittedIndex, c.rc.LastLogIndex())

		// // var traceIDs [][16]byte // 追踪ID
		// // var spanIDs [][8]byte   // 跨度ID
		// for _, proposeCtx := range proposeCtxs {
		// 	_, syncSpan := trace.GlobalTrace.StartSpan(proposeCtx, fmt.Sprintf("logsSync[from %d]", msg.From))
		// 	syncSpan.SetUint64("from", msg.From)
		// 	syncSpan.SetUint32("term", msg.Term)
		// 	syncSpan.SetUint64("startSyncIndex", msg.Index)
		// 	syncSpan.SetUint64("lastLogIndex", c.rc.LastLogIndex())

		// 	syncSpan.End()

		// 	// c.traceRecord.addSyncSpan(msg.From, msg.Index, syncSpan, syncSpanCtx)

		// 	// fmt.Println("sync span", syncSpan.SpanContext().TraceID().String(), syncSpan.SpanContext().SpanID().String())
		// 	// traceIDs = append(traceIDs, syncSpan.SpanContext().TraceID())
		// 	// spanIDs = append(spanIDs, syncSpan.SpanContext().SpanID())

		// 	// msg.TraceIDs = traceIDs
		// 	// msg.SpanIDs = spanIDs
		// }

	} else if msg.MsgType == replica.MsgSyncResp { // 副本收到领导的同步响应

		task := c.syncTaskQueue.get(getSyncTaskKey(msg.From, msg.Index))
		if task == nil {
			c.Warn("sync task not exists", zap.Uint64("from", msg.From), zap.Uint64("index", msg.Index))
			return nil
		}

		// if len(msg.TraceIDs) > 0 {
		// 	for i, traceID := range msg.TraceIDs {
		// 		spanID := msg.SpanIDs[i]
		// 		spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		// 			TraceID:    traceID,
		// 			SpanID:     spanID,
		// 			Remote:     true,
		// 			TraceState: trace.TraceState{},
		// 		})

		// 		fmt.Println("sync resp spanCtx", spanCtx.TraceID(), spanCtx.SpanID())

		// 		span := trace.SpanFromContext(trace.ContextWithRemoteSpanContext(context.Background(), spanCtx))
		// 		span.SetInt("logCount", len(msg.Logs))
		// 		span.End()
		// 		// firstIndex := msg.Index
		// 		// lastIndex := c.LastLogIndex()
		// 		// if len(msg.Logs) > 0 {
		// 		// 	lastIndex = msg.Logs[len(msg.Logs)-1].Index
		// 		// }
		// 		// ctx := trace.ContextWithRemoteSpanContext(context.Background(), spanCtx)
		// 		// c.traceRecord.addProposeSpanRange(firstIndex, lastIndex, nil, ctx)

		// 	}
		// }
		if !task.isTaskFinished() {

			task.setResp(msg)
			task.taskFinished()
			if len(msg.Logs) > 0 {
				c.Debug("sync task finished", zap.Uint64("from", msg.From), zap.Uint64("index", msg.Index), zap.Uint64("startLogIndex", msg.Logs[0].Index), zap.Uint64("endLogIndex", msg.Logs[len(msg.Logs)-1].Index), zap.Int("logCount", len(msg.Logs)))
				c.advance()
			}

		}
		return nil // MsgSyncResp消息由 syncTaskQueue处理
	}

	if added, stopped := c.recvMessageQueue.Add(msg); !added || stopped {
		c.Error("messageQueue add failed")
		return errors.New("messageQueue add failed")
	}
	c.advance() // 已接收到消息，推进分布式进程
	return nil
}

func (c *channel) handleReadyMessages(msgs []replica.Message) {

	for _, msg := range msgs {
		if msg.To == c.opts.NodeID {
			c.handleLocalMsg(msg)
			continue
		}
		if msg.To == 0 {
			c.Error("msg.To is 0", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType), zap.String("msg", msg.MsgType.String()))
			continue
		}

		go c.sendMessage(msg)

	}
}

func (c *channel) sendMessage(msg replica.Message) {
	shardNo := c.channelKey()
	protMsg, err := NewMessage(shardNo, msg, MsgChannelMsg)
	if err != nil {
		c.Error("new message error", zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType), zap.Error(err))
		return
	}
	if msg.MsgType != replica.MsgSync && msg.MsgType != replica.MsgSyncResp && msg.MsgType != replica.MsgPing && msg.MsgType != replica.MsgPong {
		c.Info("发送消息", zap.Uint64("id", msg.Id), zap.String("msgType", msg.MsgType.String()), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType), zap.Uint64("to", msg.To), zap.Uint32("term", msg.Term), zap.Uint64("index", msg.Index))
	}

	if msg.MsgType == replica.MsgSync {
		task := newSyncTask(msg.To, msg.Index)
		if !c.syncTaskQueue.exists(task.taskKey()) {
			c.syncTaskQueue.add(task)
		} else {
			c.Debug("sync task exists", zap.Uint64("to", msg.To), zap.Uint64("index", msg.Index))
		}

	}
	// trace
	traceOutgoingMessage(trace.ClusterKindChannel, msg)

	// 发送消息
	err = c.opts.Transport.Send(msg.To, protMsg, nil)
	if err != nil {
		c.Warn("send msg error", zap.String("msgType", msg.MsgType.String()), zap.Uint64("to", msg.To), zap.String("channelID", c.channelID), zap.Uint8("channelType", c.channelType), zap.Error(err))
	}
}

func (c *channel) handleEvents() (bool, error) {
	if c.destroy {
		return false, errors.New("channel destroy, can not handle message")
	}
	hasEvent := false
	var (
		err      error
		handleOk bool
	)

	// propose
	handleOk, err = c.handleProposes()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// append log task
	handleOk, err = c.handleAppendLogTask()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// recv message
	handleOk, err = c.handleRecvMessages()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// sync logs task
	handleOk, err = c.handleSyncTask()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// get logs task
	handleOk, err = c.handleGetLogTask()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	// apply logs task
	handleOk, err = c.handleApplyLogTask()
	if err != nil {
		return false, err
	}
	if handleOk {
		hasEvent = true
	}

	return hasEvent, nil
}

func (c *channel) handleProposes() (bool, error) {
	// propose
	var (
		ok         bool = true
		proposeReq proposeReq
		err        error
		hasEvent   bool
	)
	for ok {
		proposeReq, ok = c.proposeQueue.pop()
		if !ok {
			break
		}
		for i, lg := range proposeReq.logs {
			lg.Index = c.rc.LastLogIndex() + 1 + uint64(i)
			lg.Term = c.rc.Term()
			proposeReq.logs[i] = lg
			c.messageWait.didPropose(lg.MessageId, lg.Index)
		}

		err = c.step(c.rc.NewProposeMessageWithLogs(proposeReq.logs))
		select {
		case proposeReq.result <- err:
		default:
			c.Warn("propose result channel is full")
		}
		hasEvent = true

	}
	return hasEvent, nil
}

func (c *channel) handleAppendLogTask() (bool, error) {
	firstTask := c.appendLogStoreQueue.first()
	var (
		hasEvent bool
	)
	for firstTask != nil {
		if !firstTask.isTaskFinished() {
			break
		}
		if firstTask.hasErr() {
			c.Panic("append log store message failed", zap.Error(firstTask.err()))
			return false, firstTask.err()
		}
		err := c.step(firstTask.resp())
		if err != nil {
			c.Panic("step local store message failed", zap.Error(err))
			return false, err
		}
		err = c.setLastIndex(firstTask.resp().Index) // TODO: 耗时操作，不应该放到ready里执行，后续要优化
		if err != nil {
			c.Panic("set last index failed", zap.Error(err))
			return false, err
		}
		hasEvent = true
		c.appendLogStoreQueue.removeFirst()
		firstTask = c.appendLogStoreQueue.first()
	}
	return hasEvent, nil
}

func (c *channel) handleRecvMessages() (bool, error) {
	// recv message
	var hasEvent bool
	msgs := c.recvMessageQueue.Get()
	for _, msg := range msgs {
		err := c.step(msg)
		if err != nil {
			c.Error("step message failed", zap.Error(err))
			return false, err
		}
		hasEvent = true
	}
	return hasEvent, nil
}

func (c *channel) handleSyncTask() (bool, error) {
	tasks := c.syncTaskQueue.getAll()
	var hasEvent bool
	for _, task := range tasks {
		if task.isTaskFinished() {
			if len(task.resp().Logs) > 0 {
				c.Debug("sync logs done", zap.Uint64("syncIndex", task.resp().Index), zap.Uint64("startLogIndex", task.resp().Logs[0].Index), zap.Uint64("endLogIndex", task.resp().Logs[len(task.resp().Logs)-1].Index), zap.Uint64("from", task.resp().From), zap.Uint64("index", task.resp().Index), zap.Int("logCount", len(task.resp().Logs)))
			}
			err := c.step(task.resp())
			if err != nil {
				c.Error("step sync task failed", zap.Error(err))
				return false, err
			}
			hasEvent = true
			c.syncTaskQueue.remove(task.taskKey())
		}
	}
	return hasEvent, nil
}

func (c *channel) handleGetLogTask() (bool, error) {
	var (
		err      error
		hasEvent bool
	)
	getLogsTasks := c.getLogsTaskQueue.getAll()
	for _, getLogsTask := range getLogsTasks {
		if getLogsTask.isTaskFinished() {
			if getLogsTask.hasErr() {
				c.Error("get logs task error", zap.Error(getLogsTask.err()))
			} else {
				err = c.step(getLogsTask.resp())
				if err != nil {
					c.Error("step get logs task failed", zap.Error(err))
				}
			}
			c.getLogsTaskQueue.remove(getLogsTask.taskKey())
			hasEvent = true
		}
	}
	return hasEvent, nil
}

func (c *channel) handleApplyLogTask() (bool, error) {
	firstTask := c.applyLogStoreQueue.first()
	var (
		hasEvent bool
	)
	for firstTask != nil {
		if !firstTask.isTaskFinished() {
			break
		}
		if firstTask.hasErr() {
			c.Panic("apply log store message failed", zap.Error(firstTask.err()))
			return false, firstTask.err()
		}
		err := c.step(firstTask.resp())
		if err != nil {
			c.Panic("step apply store message failed", zap.Error(err))
			return false, err
		}
		err = c.setAppliedIndex(firstTask.resp().CommittedIndex) // TODO: 耗时操作，不应该放到ready里执行，后续要优化
		if err != nil {
			c.Panic("set applied index failed", zap.Error(err))
			return false, err
		}
		hasEvent = true
		c.applyLogStoreQueue.removeFirst()
		firstTask = c.applyLogStoreQueue.first()
	}
	return hasEvent, nil
}

// func (c *channel) LastLogIndex() uint64 {
// 	c.mu.Lock()
// 	defer c.mu.Unlock()
// 	return c.rc.LastLogIndex()
// }

// func (c *channel) Term() uint32 {
// 	c.mu.Lock()
// 	defer c.mu.Unlock()
// 	return c.rc.Term()
// }

func (c *channel) setLeaderId(leaderId uint64) {
	c.leaderId.Store(leaderId)
}

func (c *channel) IsLeader() bool {

	return c.opts.NodeID == c.LeaderId()
}

func (c *channel) LeaderId() uint64 {
	return c.leaderId.Load()
}

func (c *channel) getClusterConfig() *wkstore.ChannelClusterConfig {
	return c.clusterConfig
}

type ichannel interface {
	IsLeader() bool
	proposeAndWaitCommits(ctx context.Context, logs []replica.Log, timeout time.Duration) ([]messageItem, error)
	LeaderId() uint64
	handleRecvMessage(msg replica.Message) error
	getClusterConfig() *wkstore.ChannelClusterConfig
}

type proxyChannel struct {
	nodeId     uint64
	clusterCfg *wkstore.ChannelClusterConfig
	mu         sync.Mutex
}

func newProxyChannel(nodeId uint64, clusterCfg *wkstore.ChannelClusterConfig) *proxyChannel {
	return &proxyChannel{
		nodeId:     nodeId,
		clusterCfg: clusterCfg,
	}
}

func (p *proxyChannel) IsLeader() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clusterCfg.LeaderId == p.nodeId
}

func (p *proxyChannel) proposeAndWaitCommits(ctx context.Context, logs []replica.Log, timeout time.Duration) ([]messageItem, error) {
	panic("proposeAndWaitCommits: implement me")
}

func (p *proxyChannel) LeaderId() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clusterCfg.LeaderId
}

func (p *proxyChannel) handleRecvMessage(msg replica.Message) error {
	panic("handleMessage: implement me")
}

func (p *proxyChannel) getClusterConfig() *wkstore.ChannelClusterConfig {
	return p.clusterCfg
}
