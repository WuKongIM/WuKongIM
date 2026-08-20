package replication

import (
	"sync"
	"testing"
	"time"
)

type replicationStageObservation struct {
	stage  string
	result string
	d      time.Duration
}

type recordingReplicationStageObserver struct {
	mu     sync.Mutex
	events []replicationStageObservation
}

func (o *recordingReplicationStageObserver) ObserveReplicationStage(stage string, result string, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, replicationStageObservation{stage: stage, result: result, d: d})
}

func (o *recordingReplicationStageObserver) snapshot() []replicationStageObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]replicationStageObservation(nil), o.events...)
}

func hasReplicationStage(events []replicationStageObservation, stage string, result string) bool {
	for _, event := range events {
		if event.stage == stage && event.result == result && event.d >= 0 {
			return true
		}
	}
	return false
}

func TestSampledStageObserverKeepsOneBoundedSeriesSamplePerInterval(t *testing.T) {
	sink := &recordingReplicationStageObserver{}
	observer := newSampledStageObserver(sink, 3)
	for index := 0; index < 7; index++ {
		observer.ObserveReplicationStage(stagePeerForegroundQueue, "ok", time.Duration(index))
	}
	events := sink.snapshot()
	if len(events) != 3 || events[0].d != 0 || events[1].d != 3 || events[2].d != 6 {
		t.Fatalf("sampled events = %+v, want observations 0, 3, and 6", events)
	}
}

func TestReplicationStageIndexIncludesEveryRuntimeStage(t *testing.T) {
	t.Parallel()

	stages := []string{
		stageQuorumLocalQueue,
		stageQuorumLocalStore,
		stageQuorumLocalEndToEnd,
		stagePeerForegroundQueue,
		stagePeerForegroundExchange,
		stagePeerForegroundEndToEnd,
		stagePeerHedgeQueue,
		stagePeerHedgeExchange,
		stagePeerHedgeEndToEnd,
		stagePeerBackgroundQueue,
		stagePeerBackgroundExchange,
		stagePeerBackgroundEndToEnd,
		stageFollowerForegroundStore,
		stageFollowerBackgroundStore,
	}
	seen := make(map[int]string, len(stages))
	for _, stage := range stages {
		index := replicationStageIndex(stage)
		if index < 0 {
			t.Fatalf("replicationStageIndex(%q) = %d, want registered stage", stage, index)
		}
		if previous, exists := seen[index]; exists {
			t.Fatalf("stages %q and %q share sample counter %d", previous, stage, index)
		}
		seen[index] = stage
	}
}
