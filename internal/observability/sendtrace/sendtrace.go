package sendtrace

import (
	"sync/atomic"
	"time"
)

type Stage string

const (
	StageGatewayMessagesSend           Stage = "gateway.messages_send"
	StageGatewayWriteSendack           Stage = "gateway.write_sendack"
	StageMessageSendDurable            Stage = "message.send_durable"
	StageChannelAppendLocal            Stage = "channel.append.local"
	StageChannelAppendForward          Stage = "channel.append.forward"
	StageReplicaLeaderQueueWait        Stage = "replica.leader.queue_wait"
	StageReplicaLeaderLocalDurable     Stage = "replica.leader.local_durable"
	StageReplicaLeaderQuorumWait       Stage = "replica.leader.quorum_wait"
	StageReplicaFollowerApplyDurable   Stage = "replica.follower.apply_durable"
	StageRuntimeFollowerRetryScheduled Stage = "runtime.follower_retry_scheduled"
	StageRuntimeFetchRequestSend       Stage = "runtime.fetch_request_send"
	StageRuntimeLanePollRequestSend    Stage = "runtime.lane_poll_request_send"
	StageRuntimeLaneCursorDeltaSend    Stage = "runtime.lane_cursor_delta_send"
)

type Event struct {
	Stage       Stage
	At          time.Time
	Duration    time.Duration
	NodeID      uint64
	PeerNodeID  uint64
	ChannelKey  string
	ClientMsgNo string
	MessageSeq  uint64
	RangeStart  uint64
	RangeEnd    uint64
}

func (e Event) ContainsMessageSeq(seq uint64) bool {
	if e.MessageSeq == seq {
		return true
	}
	if e.RangeStart == 0 || e.RangeEnd == 0 {
		return false
	}
	return seq >= e.RangeStart && seq <= e.RangeEnd
}

func Elapsed(start, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

type Sink interface {
	RecordSendTrace(Event)
}

type sinkHolder struct {
	sink Sink
}

var activeSink atomic.Pointer[sinkHolder]

func SetSink(sink Sink) func() {
	previous := activeSink.Swap(&sinkHolder{sink: sink})
	return func() {
		activeSink.Store(previous)
	}
}

func Record(event Event) {
	holder := activeSink.Load()
	if holder == nil || holder.sink == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	holder.sink.RecordSendTrace(event)
}
