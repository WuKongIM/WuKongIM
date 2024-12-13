package reactor

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type ready struct {
	queue       *msgQueue   // 消息队列
	state       *ReadyState // ready state
	offsetIndex uint64      // 当前偏移的下标
	endIndex    uint64      // 当前同步结束的index
	commitIndex uint64      // 已提交的下标
}

func newReady(logPrefix string) *ready {

	return &ready{
		queue: newMsgQueue(logPrefix),
		state: NewReadyState(options.RetryIntervalTick),
	}
}

func (r *ready) slice() []reactor.UserMessage {
	r.endIndex = 0
	msgs := r.queue.sliceWithSize(r.offsetIndex+1, r.queue.lastIndex+1, 0)
	if len(msgs) > 0 {
		r.endIndex = msgs[len(msgs)-1].Index()
	}
	return msgs
}

func (r *ready) sliceWith(startIndex, endIndex uint64) []reactor.UserMessage {
	if endIndex == 0 {
		endIndex = r.queue.lastIndex + 1
	}
	msgs := r.queue.sliceWithSize(startIndex, endIndex, 0)
	return msgs

}

func (r *ready) sliceAndTruncate() []reactor.UserMessage {
	msgs := r.queue.sliceWithSize(r.offsetIndex+1, r.queue.lastIndex+1, 0)
	if len(msgs) > 0 {
		r.endIndex = msgs[len(msgs)-1].Index()
	}
	r.truncate()
	return msgs
}

func (r *ready) truncate() {
	if r.queue.len() == 0 {
		return
	}
	r.queue.truncateTo(r.endIndex + 1)
	r.offsetIndex = r.endIndex
}

func (r *ready) truncateTo(index uint64) {
	r.queue.truncateTo(index)
}

func (r *ready) resetState() {
	r.state.Reset()
}

func (r *ready) reset() {
	r.state.Reset()
	r.queue.reset()
	r.offsetIndex = 0
	r.endIndex = 0
}

func (r *ready) has() bool {
	if r.state.processing {
		return false
	}

	return r.offsetIndex < r.queue.lastIndex
}

func (r *ready) tick() {
	r.state.Tick()
}

func (r *ready) startProcessing() {
	r.state.StartProcessing()
}

func (r *ready) processSuccess() {
	r.state.ProcessSuccess()
}

func (r *ready) processFail() {
	r.state.ProcessFail()
}

func (r *ready) isMaxRetry() bool {
	return r.state.IsMaxRetry()
}

func (r *ready) append(m reactor.UserMessage) {
	m.SetIndex(r.queue.lastIndex + 1)
	r.queue.append(m)
}

type outboundReady struct {
	queue       *msgQueue                 // 消息队列
	followers   map[uint64]*followerState // 副本数据状态
	offsetIndex uint64                    // 当前偏移的下标
	commitIndex uint64                    // 已提交的下标
	wklog.Log
}

func newOutboundReady(logPrefix string) *outboundReady {

	return &outboundReady{
		queue:     newMsgQueue(logPrefix),
		followers: map[uint64]*followerState{},
		Log:       wklog.NewWKLog(logPrefix),
	}
}

func (o *outboundReady) append(m reactor.UserMessage) {
	m.SetIndex(o.queue.lastIndex + 1)
	o.queue.append(m)
}

func (o *outboundReady) has() bool {
	for _, follower := range o.followers {
		// 需要等会再发起转发，别太快
		if follower.forwardIdleTick < options.OutboundForwardIntervalTick {
			return false
		}
		// 是否符合转发条件
		if follower.outboundForwardedIndex >= o.commitIndex && follower.outboundForwardedIndex < o.queue.lastIndex {
			return true
		}
	}
	return false
}

func (o *outboundReady) ready() []reactor.UserAction {
	var actions []reactor.UserAction
	var endIndex = o.queue.lastIndex
	for nodeId, follower := range o.followers {

		if follower.forwardIdleTick < options.OutboundForwardIntervalTick {
			continue
		}
		if follower.outboundForwardedIndex >= o.commitIndex && follower.outboundForwardedIndex < o.queue.lastIndex {
			if actions == nil {
				actions = make([]reactor.UserAction, 0, len(o.followers))
			}
			// 不能超过指定数量
			if o.queue.lastIndex-follower.outboundForwardedIndex > options.OutboundForwardMaxMessageCount {
				endIndex = o.queue.lastIndex - follower.outboundForwardedIndex + options.OutboundForwardMaxMessageCount
			}
			msgs := o.queue.sliceWithSize(follower.outboundForwardedIndex+1, endIndex+1, 0)
			if len(msgs) == 0 {
				continue
			}
			actions = append(actions, reactor.UserAction{
				Type:     reactor.UserActionOutboundForward,
				From:     options.NodeId,
				To:       nodeId,
				Messages: msgs,
			})
		}
	}
	return actions
}

func (o *outboundReady) updateFollowerIndex(nodeId uint64, index uint64) {
	follower := o.followers[nodeId]
	if follower == nil {
		o.Info("follower not exist", zap.Uint64("nodeId", nodeId))
		return
	}
	if index > follower.outboundForwardedIndex {
		follower.outboundForwardedIndex = index
		o.checkCommit()
	}
	follower.forwardIdleTick = 0
}

func (o *outboundReady) updateFollowerHeartbeat(nodeId uint64) {
	follower := o.followers[nodeId]
	if follower == nil {
		o.Info("follower not exist", zap.Uint64("nodeId", nodeId))
		return
	}
	follower.heartbeatIdleTick = 0
}

func (o *outboundReady) addNewFollower(nodeId uint64) {
	o.followers[nodeId] = newFollowerState(nodeId, o.commitIndex)
}

func (o *outboundReady) checkCommit() {
	var minIndex = o.commitIndex
	for _, follower := range o.followers {
		if follower.outboundForwardedIndex > minIndex {
			minIndex = follower.outboundForwardedIndex
		}
	}
	if minIndex > o.commitIndex {
		o.commitIndex = minIndex
		o.commit(minIndex)
	}
}

func (o *outboundReady) commit(index uint64) {
	o.queue.truncateTo(index + 1)
}

func (o *outboundReady) tick() {
	for _, follower := range o.followers {
		follower.heartbeatIdleTick++
		follower.forwardIdleTick++

		if follower.heartbeatIdleTick >= options.NodeHeartbeatTimeoutTick {
			o.Info("follower heartbeat timeout", zap.Uint64("nodeId", follower.nodeId))
			delete(o.followers, follower.nodeId)
			continue
		}
	}
}

func (o *outboundReady) reset() {
	o.queue.reset()
	o.commitIndex = 0
	o.offsetIndex = 0
	o.followers = map[uint64]*followerState{}

}

type followerState struct {
	nodeId                 uint64 // 节点id
	outboundForwardedIndex uint64 // 发件箱已转发索引
	forwardIdleTick        int    // 转发空闲tick
	heartbeatIdleTick      int    // 心跳空闲tick
}

func newFollowerState(nodeId uint64, outboundForwardedIndex uint64) *followerState {

	return &followerState{
		nodeId:                 nodeId,
		outboundForwardedIndex: outboundForwardedIndex,
	}
}
