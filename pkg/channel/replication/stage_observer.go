package replication

import (
	"sync/atomic"
	"time"
)

const defaultRuntimeStageSampleEvery uint64 = 32

const (
	stageQuorumLocalQueue        = "quorum_local_queue"
	stageQuorumLocalStore        = "quorum_local_store"
	stageQuorumLocalEndToEnd     = "quorum_local_end_to_end"
	stagePeerForegroundQueue     = "peer_foreground_queue"
	stagePeerForegroundExchange  = "peer_foreground_exchange"
	stagePeerForegroundEndToEnd  = "peer_foreground_end_to_end"
	stagePeerBackgroundQueue     = "peer_background_queue"
	stagePeerBackgroundExchange  = "peer_background_exchange"
	stagePeerBackgroundEndToEnd  = "peer_background_end_to_end"
	stageFollowerForegroundStore = "follower_foreground_store"
	stageFollowerBackgroundStore = "follower_background_store"
)

// StageObserver receives bounded replication latency stages. Implementations
// must remain non-blocking and must not retain proposal or Channel identity.
type StageObserver interface {
	ObserveReplicationStage(stage string, result string, d time.Duration)
}

type sampledStageObserver struct {
	sink     StageObserver
	every    uint64
	counters [11]atomic.Uint64
}

func newSampledStageObserver(sink StageObserver, every uint64) StageObserver {
	if sink == nil {
		return nil
	}
	if every <= 1 {
		return sink
	}
	return &sampledStageObserver{sink: sink, every: every}
}

func (o *sampledStageObserver) ObserveReplicationStage(stage string, result string, d time.Duration) {
	if o == nil || o.sink == nil {
		return
	}
	index := replicationStageIndex(stage)
	if index < 0 || o.counters[index].Add(1)%o.every != 1 {
		return
	}
	o.sink.ObserveReplicationStage(stage, result, d)
}

func replicationStageIndex(stage string) int {
	switch stage {
	case stageQuorumLocalQueue:
		return 0
	case stageQuorumLocalStore:
		return 1
	case stageQuorumLocalEndToEnd:
		return 2
	case stagePeerForegroundQueue:
		return 3
	case stagePeerForegroundExchange:
		return 4
	case stagePeerForegroundEndToEnd:
		return 5
	case stagePeerBackgroundQueue:
		return 6
	case stagePeerBackgroundExchange:
		return 7
	case stagePeerBackgroundEndToEnd:
		return 8
	case stageFollowerForegroundStore:
		return 9
	case stageFollowerBackgroundStore:
		return 10
	default:
		return -1
	}
}

func observeReplicationStage(observer StageObserver, stage string, err error, d time.Duration) {
	if observer == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	observer.ObserveReplicationStage(stage, result, d)
}

func peerStageNames(class peerWorkClass) (queue string, exchange string, endToEnd string) {
	if class == peerWorkBackground {
		return stagePeerBackgroundQueue, stagePeerBackgroundExchange, stagePeerBackgroundEndToEnd
	}
	return stagePeerForegroundQueue, stagePeerForegroundExchange, stagePeerForegroundEndToEnd
}
