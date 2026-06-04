package sendtrace

import (
	"encoding/base64"
	"strconv"
	"sync/atomic"
	"time"
)

// Stage identifies a stable step in the message send path.
type Stage string

const (
	StageGatewayAsyncDispatchWait      Stage = "gateway.async_dispatch_wait"
	StageGatewayMessagesSend           Stage = "gateway.messages_send"
	StageGatewayWriteSendack           Stage = "gateway.write_sendack"
	StageMessageSendDurable            Stage = "message.send_durable"
	StageChannelAppendLocal            Stage = "channel.append.local"
	StageChannelAppendForward          Stage = "channel.append.forward"
	StageReplicaLeaderQueueWait        Stage = "replica.leader.queue_wait"
	StageReplicaLeaderLocalDurable     Stage = "replica.leader.local_durable"
	StageReplicaLeaderDurableMuWait    Stage = "replica.leader.durable_mutex_wait"
	StageReplicaLeaderDurableAppend    Stage = "replica.leader.durable_append_store"
	StageReplicaLeaderQuorumWait       Stage = "replica.leader.quorum_wait"
	StageReplicaFollowerApplyDurable   Stage = "replica.follower.apply_durable"
	StageStoreCommitQueueWait          Stage = "store.commit.queue_wait"
	StageStoreCommitBatchCollect       Stage = "store.commit.batch_collect"
	StageStoreCommitPebbleSync         Stage = "store.commit.pebble_sync"
	StageStoreCommitPublish            Stage = "store.commit.publish"
	StageRuntimeFollowerRetryScheduled Stage = "runtime.follower_retry_scheduled"
	StageRuntimeFetchRequestSend       Stage = "runtime.fetch_request_send"
	StageRuntimeLanePollRequestSend    Stage = "runtime.lane_poll_request_send"
	StageRuntimeLaneCursorDeltaSend    Stage = "runtime.lane_cursor_delta_send"
)

// Result describes the observed outcome of a send trace event.
type Result string

const (
	ResultOK       Result = "ok"
	ResultError    Result = "error"
	ResultTimeout  Result = "timeout"
	ResultCanceled Result = "canceled"
	ResultPartial  Result = "partial"
	ResultDropped  Result = "dropped"
	ResultSkipped  Result = "skipped"
)

// Event describes one low-cost fact observed in the message send path.
type Event struct {
	// TraceID correlates events that belong to the same diagnostics trace.
	TraceID string
	// Stage identifies the stable processing step that emitted this event.
	Stage Stage
	// At is the local node timestamp when the event was recorded.
	At time.Time
	// Duration is the elapsed time observed for this stage.
	Duration time.Duration
	// NodeID is the local cluster node that observed the event.
	NodeID uint64
	// PeerNodeID is the remote cluster node involved in this event, when any.
	PeerNodeID uint64
	// ChannelKey is the diagnostics-safe channel identifier used for lookups.
	ChannelKey string
	// ClientMsgNo is the client message number used for idempotency correlation.
	ClientMsgNo string
	// MessageSeq is the single message sequence associated with this event.
	MessageSeq uint64
	// RangeStart is the first message sequence covered by a range event.
	RangeStart uint64
	// RangeEnd is the last message sequence covered by a range event.
	RangeEnd uint64
	// FromUID is the sending UID used for diagnostics sampling and must not be exposed in manager event DTOs.
	FromUID string
	// Service identifies the node-local service or RPC path involved in this event.
	Service string
	// Result records the stable outcome token for the event.
	Result Result
	// ErrorCode records the stable error classification when Result is not ok.
	ErrorCode string
	// Error stores a bounded, non-payload error summary for diagnostics.
	Error string
	// Attempt records the retry or delivery attempt associated with the event.
	Attempt int
	// RequestCount records how many logical requests were included in a batch event.
	RequestCount int
	// RecordCount records how many durable log records were included in a batch event.
	RecordCount int
	// ByteCount records the approximate payload bytes included in a batch event.
	ByteCount int
	// QueueDepth records the observed queue depth when a batch event was emitted.
	QueueDepth int
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

// ChannelKeyFromID returns the diagnostics-safe channel key for a channel id and type.
func ChannelKeyFromID(channelID string, channelType uint8) string {
	if channelID == "" || channelType == 0 {
		return ""
	}
	encodedID := base64.RawURLEncoding.EncodeToString([]byte(channelID))
	buf := make([]byte, 0, len("channel/")+4+1+len(encodedID))
	buf = append(buf, "channel/"...)
	buf = strconv.AppendUint(buf, uint64(channelType), 10)
	buf = append(buf, '/')
	buf = append(buf, encodedID...)
	return string(buf)
}

// Sink receives send trace events from hot-path instrumentation.
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

// Enabled reports whether send trace events have an active receiver.
func Enabled() bool {
	holder := activeSink.Load()
	return holder != nil && holder.sink != nil
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
