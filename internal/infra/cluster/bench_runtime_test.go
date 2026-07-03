package cluster

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestChannelRuntimeBenchControllerMapsSnapshot(t *testing.T) {
	node := &fakeChannelRuntimeBenchNode{
		nodeID: 7,
		snapshot: channelruntime.RuntimeSnapshot{
			ActiveTotal:             10,
			ActiveLeader:            4,
			ActiveFollower:          6,
			FollowerParked:          2,
			ActivationRejectedTotal: 3,
			Reactors: []channelruntime.RuntimeReactorSnapshot{
				{ReactorID: 1, Leader: 2, Follower: 3, Parked: 1, MailboxDepth: 5},
			},
			WorkerQueues: []channelruntime.RuntimeWorkerQueue{
				{Pool: "append", Depth: 8},
			},
		},
	}
	controller := NewChannelRuntimeBenchController(node)

	got, err := controller.Snapshot(context.Background(), model.ChannelRuntimeQuery{
		RunID:   "run-a",
		Profile: "activate-groups",
	})
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	want := model.ChannelRuntimeSnapshot{
		Version:                 "bench/v1",
		NodeID:                  7,
		RunID:                   "run-a",
		Profile:                 "activate-groups",
		ActiveTotal:             10,
		ActiveLeader:            4,
		ActiveFollower:          6,
		FollowerParked:          2,
		ActivationRejectedTotal: 3,
		Reactors: []model.ChannelRuntimeReactorSnapshot{
			{ReactorID: 1, Leader: 2, Follower: 3, Parked: 1, MailboxDepth: 5},
		},
		WorkerQueues: []model.ChannelRuntimeWorkerQueue{
			{Pool: "append", Depth: 8},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot() = %#v, want %#v", got, want)
	}
}

func TestChannelRuntimeBenchControllerExpandsProbeRange(t *testing.T) {
	node := &fakeChannelRuntimeBenchNode{
		nodeID: 9,
		probe: channelruntime.RuntimeProbeResult{
			Checked:        3,
			LoadedLeader:   1,
			LoadedFollower: 1,
			Missing:        []channelruntime.ChannelID{{ID: "run-a-activate-groups-4", Type: 2}},
		},
	}
	controller := NewChannelRuntimeBenchController(node)

	got, err := controller.Probe(context.Background(), model.ChannelRuntimeQuery{
		RunID:       " run-a ",
		Profile:     " activate-groups ",
		ChannelType: 2,
		Range:       model.ChannelRuntimeRange{Start: 2, End: 5},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	wantSelector := channelruntime.RuntimeSelector{ChannelIDs: []channelruntime.ChannelID{
		{ID: "run-a-activate-groups-2", Type: 2},
		{ID: "run-a-activate-groups-3", Type: 2},
		{ID: "run-a-activate-groups-4", Type: 2},
	}}
	if !reflect.DeepEqual(node.probeSelector, wantSelector) {
		t.Fatalf("probe selector = %#v, want %#v", node.probeSelector, wantSelector)
	}

	want := model.ChannelRuntimeProbeResult{
		Version:        "bench/v1",
		NodeID:         9,
		RunID:          " run-a ",
		Profile:        " activate-groups ",
		Checked:        3,
		LoadedLeader:   1,
		LoadedFollower: 1,
		Missing:        []string{"run-a-activate-groups-4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Probe() = %#v, want %#v", got, want)
	}
}

func TestChannelRuntimeBenchControllerMapsEvictResult(t *testing.T) {
	node := &fakeChannelRuntimeBenchNode{
		nodeID: 11,
		evict: channelruntime.RuntimeEvictResult{
			Requested:   4,
			Evicted:     2,
			SkippedBusy: 1,
			Missing:     1,
		},
	}
	controller := NewChannelRuntimeBenchController(node)

	got, err := controller.Evict(context.Background(), model.ChannelRuntimeQuery{
		RunID:       "run-b",
		Profile:     "activate-groups",
		ChannelType: 2,
		Range:       model.ChannelRuntimeRange{Start: 3, End: 7},
	})
	if err != nil {
		t.Fatalf("Evict() error = %v", err)
	}

	want := model.ChannelRuntimeEvictResult{
		Version:     "bench/v1",
		NodeID:      11,
		RunID:       "run-b",
		Profile:     "activate-groups",
		Requested:   4,
		Evicted:     2,
		SkippedBusy: 1,
		Missing:     1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Evict() = %#v, want %#v", got, want)
	}
}

type fakeChannelRuntimeBenchNode struct {
	nodeID uint64

	snapshot channelruntime.RuntimeSnapshot
	probe    channelruntime.RuntimeProbeResult
	evict    channelruntime.RuntimeEvictResult

	probeSelector channelruntime.RuntimeSelector
	evictSelector channelruntime.RuntimeSelector
}

func (n *fakeChannelRuntimeBenchNode) NodeID() uint64 {
	return n.nodeID
}

func (n *fakeChannelRuntimeBenchNode) ChannelRuntimeSnapshot(context.Context) (channelruntime.RuntimeSnapshot, error) {
	return n.snapshot, nil
}

func (n *fakeChannelRuntimeBenchNode) ChannelRuntimeProbe(_ context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error) {
	n.probeSelector = selector
	return n.probe, nil
}

func (n *fakeChannelRuntimeBenchNode) ChannelRuntimeEvict(_ context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error) {
	n.evictSelector = selector
	return n.evict, nil
}
