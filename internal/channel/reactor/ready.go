package reactor

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type ready struct {
	queue       *msgQueue   // 消息队列
	state       *readyState // ready state
	offsetIndex uint64      // 当前偏移的下标
	endIndex    uint64      // 当前同步结束的index
}

func newReady(logPrefix string) *ready {

	return &ready{
		queue: newMsgQueue(logPrefix),
		state: newReadyState(options.RetryIntervalTick),
	}
}

// func (r *ready) slice() []*reactor.UserMessage {
// 	r.endIndex = 0
// 	msgs := r.queue.sliceWithSize(r.offsetIndex+1, r.queue.lastIndex+1, 0)
// 	if len(msgs) > 0 {
// 		r.endIndex = msgs[len(msgs)-1].Index
// 	}
// 	return msgs
// }

// func (r *ready) sliceWith(startIndex, endIndex uint64) []*reactor.UserMessage {
// 	if endIndex == 0 {
// 		endIndex = r.queue.lastIndex + 1
// 	}
// 	msgs := r.queue.sliceWithSize(startIndex, endIndex, 0)
// 	return msgs

// }

func (r *ready) sliceAndTruncate() []*reactor.ChannelMessage {
	msgs := r.queue.sliceWithSize(r.offsetIndex+1, r.queue.lastIndex+1, 0)
	if len(msgs) > 0 {
		r.endIndex = msgs[len(msgs)-1].Index
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

func (r *ready) append(m *reactor.ChannelMessage) {
	m.Index = r.queue.lastIndex + 1
	r.queue.append(m)
}

type outboundReady struct {
	queue       *msgQueue                // 消息队列
	replicas    map[uint64]*replicaState // 副本数据状态
	offsetIndex uint64                   // 当前偏移的下标
	channel     *Channel
	wklog.Log
}

func newOutboundReady(logPrefix string, channel *Channel) *outboundReady {

	return &outboundReady{
		queue:    newMsgQueue(logPrefix),
		replicas: map[uint64]*replicaState{},
		Log:      wklog.NewWKLog(logPrefix),
		channel:  channel,
	}
}

func (o *outboundReady) append(m *reactor.ChannelMessage) {
	m.Index = o.queue.lastIndex + 1
	o.queue.append(m)
}

func (o *outboundReady) has() bool {
	return o.offsetIndex < o.queue.lastIndex
}

func (o *outboundReady) ready() []reactor.ChannelAction {
	var actions []reactor.ChannelAction
	var endIndex = o.queue.lastIndex
	msgs := o.queue.sliceWithSize(o.offsetIndex+1, endIndex+1, 0)
	if len(msgs) == 0 {
		return nil
	}
	lastIndex := msgs[len(msgs)-1].Index
	o.queue.truncateTo(lastIndex + 1)

	o.offsetIndex = lastIndex

	hasToNode := false
	for _, msg := range msgs {
		// 如果ToNode有值，说明指定了接收的节点，这种情况需要判断是不是当前节点
		if msg.ToNode != 0 {
			actions = append(actions, reactor.ChannelAction{
				FakeChannelId: o.channel.channelId,
				ChannelType:   o.channel.channelType,
				Type:          reactor.ChannelActionOutboundForward,
				From:          options.NodeId,
				To:            msg.ToNode,
				Messages: []*reactor.ChannelMessage{
					msg,
				},
			})
			hasToNode = true
			continue
		}
	}

	var newMsgs []*reactor.ChannelMessage
	if hasToNode {
		for _, msg := range msgs {
			if msg.ToNode == 0 {
				newMsgs = append(newMsgs, msg)
			}
		}
	} else {
		newMsgs = msgs
	}
	if len(newMsgs) > 0 {
		for nodeId := range o.replicas {
			actions = append(actions, reactor.ChannelAction{
				FakeChannelId: o.channel.channelId,
				ChannelType:   o.channel.channelType,
				Type:          reactor.ChannelActionOutboundForward,
				From:          options.NodeId,
				To:            nodeId,
				Messages:      newMsgs,
			})
		}
	}

	return actions
}

// func (o *outboundReady) updateReplicaIndex(nodeId uint64, index uint64) {
// 	replica := o.replicas[nodeId]
// 	if replica == nil {
// 		o.Info("replica not exist", zap.Uint64("nodeId", nodeId))
// 		return
// 	}
// 	if index > replica.outboundForwardedIndex {
// 		replica.outboundForwardedIndex = index
// 		// o.checkCommit()
// 	}
// }

func (o *outboundReady) updateReplicaHeartbeat(nodeId uint64) {
	replica := o.replicas[nodeId]
	if replica == nil {
		o.Info("replica not exist", zap.Uint64("nodeId", nodeId))
		return
	}
	replica.heartbeatIdleTick = 0
}

func (o *outboundReady) addNewReplica(nodeId uint64) {
	o.replicas[nodeId] = newReplicaState(nodeId, 0)
}

// func (o *outboundReady) checkCommit() {
// 	var minIndex = o.commitIndex
// 	for _, replica := range o.replicas {
// 		if replica.outboundForwardedIndex > minIndex {
// 			minIndex = replica.outboundForwardedIndex
// 		}
// 	}
// 	if minIndex > o.commitIndex {
// 		o.commitIndex = minIndex
// 		o.commit(minIndex)
// 	}
// }

// func (o *outboundReady) commit(index uint64) {
// 	o.queue.truncateTo(index + 1)
// }

func (o *outboundReady) tick() {
	for _, replica := range o.replicas {
		replica.heartbeatIdleTick++

		if replica.heartbeatIdleTick >= options.NodeHeartbeatTimeoutTick {
			o.Info("replica heartbeat timeout", zap.Uint64("nodeId", replica.nodeId))
			delete(o.replicas, replica.nodeId)
			continue
		}
	}
}

func (o *outboundReady) reset() {
	o.queue.reset()
	// o.commitIndex = 0
	o.offsetIndex = 0
	o.replicas = map[uint64]*replicaState{}

}

type replicaState struct {
	nodeId                 uint64 // 节点id
	outboundForwardedIndex uint64 // 发件箱已转发索引
	heartbeatIdleTick      int    // 心跳空闲tick
}

func newReplicaState(nodeId uint64, outboundForwardedIndex uint64) *replicaState {

	return &replicaState{
		nodeId:                 nodeId,
		outboundForwardedIndex: outboundForwardedIndex,
	}
}
