package core

import "testing"

func TestObserverByteAggregationRetainsEveryLane(t *testing.T) {
	sink := &laneByteObserver{}
	drain := &ObserverDrain{target: sink}
	for i := 0; i < 320; i++ {
		for priority := PriorityRaft; priority <= PriorityBulk; priority++ {
			if !drain.aggregate(Event{Name: "sent_bytes", Kind: FrameKindRPCRequest, Priority: priority, Bytes: int(priority)}) {
				t.Fatal("byte event was not aggregated")
			}
		}
	}
	drain.drainAggregates()
	if len(sink.events) != 4 {
		t.Fatalf("lane groups = %d, want 4", len(sink.events))
	}
	for _, e := range sink.events {
		if e.Bytes != int(e.Priority)*320 || e.Count != 320 {
			t.Fatalf("byte aggregation sampled or merged lanes: %+v", e)
		}
	}
}

type laneByteObserver struct{ events []Event }

func (o *laneByteObserver) ObserveTransport(e Event) { o.events = append(o.events, e) }
