package cluster

import (
	"context"
	"reflect"
	"strings"
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

	got, err := controller.Probe(context.Background(), model.ChannelRuntimeProbeQuery{
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

func TestChannelRuntimeBenchControllerGeneratedAllLoadedOmitsDetailedRows(t *testing.T) {
	node := &fakeChannelRuntimeBenchNode{
		nodeID: 9,
		probe: channelruntime.RuntimeProbeResult{
			Checked: 2, LoadedLeader: 1, LoadedFollower: 1,
			Channels: []channelruntime.RuntimeProbeChannel{
				{ChannelID: channelruntime.ChannelID{ID: "run-a-person-0", Type: 1}, Role: channelruntime.RoleLeader, Status: channelruntime.StatusActive},
				{ChannelID: channelruntime.ChannelID{ID: "run-a-person-1", Type: 1}, Role: channelruntime.RoleFollower, Status: channelruntime.StatusActive},
			},
		},
	}

	got, err := NewChannelRuntimeBenchController(node).Probe(context.Background(), model.ChannelRuntimeProbeQuery{
		RunID: "run-a", Profile: "person", ChannelType: 1, Range: model.ChannelRuntimeRange{Start: 0, End: 2},
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if got.Checked != 2 || got.LoadedLeader != 1 || got.LoadedFollower != 1 {
		t.Fatalf("aggregates = %+v, want checked=2 leader=1 follower=1", got)
	}
	if len(got.Channels) != 0 {
		t.Fatalf("generated detailed channels = %#v, want omitted", got.Channels)
	}
}

func TestChannelRuntimeBenchControllerProbesExplicitChannelsUnchanged(t *testing.T) {
	requested := []model.ChannelRuntimeChannelIdentity{
		{ChannelID: "canonical-person-b", ChannelType: 1},
		{ChannelID: "canonical-person-missing", ChannelType: 1},
		{ChannelID: "canonical-person-a", ChannelType: 1},
	}
	node := &fakeChannelRuntimeBenchNode{
		nodeID: 12,
		probe: channelruntime.RuntimeProbeResult{
			Checked:        3,
			LoadedLeader:   1,
			LoadedFollower: 1,
			Channels: []channelruntime.RuntimeProbeChannel{
				{
					ChannelID: channelruntime.ChannelID{ID: "canonical-person-b", Type: 1},
					Role:      channelruntime.RoleFollower, Status: channelruntime.StatusCreating,
					LEO: 20, HW: 18, CheckpointHW: 17, LeaderEpoch: 9, ChannelEpoch: 7,
				},
				{
					ChannelID: channelruntime.ChannelID{ID: "canonical-person-a", Type: 1},
					Role:      channelruntime.RoleLeader, Status: channelruntime.StatusActive,
					LEO: 33, HW: 31, CheckpointHW: 29, LeaderEpoch: 11, ChannelEpoch: 8,
				},
			},
			Missing: []channelruntime.ChannelID{{ID: "canonical-person-missing", Type: 1}},
		},
	}
	controller := NewChannelRuntimeBenchController(node)

	got, err := controller.Probe(context.Background(), model.ChannelRuntimeProbeQuery{Channels: requested})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	wantSelector := channelruntime.RuntimeSelector{ChannelIDs: []channelruntime.ChannelID{
		{ID: "canonical-person-b", Type: 1},
		{ID: "canonical-person-missing", Type: 1},
		{ID: "canonical-person-a", Type: 1},
	}}
	if !reflect.DeepEqual(node.probeSelector, wantSelector) {
		t.Fatalf("probe selector = %#v, want exact explicit identities %#v", node.probeSelector, wantSelector)
	}
	wantChannels := []model.ChannelRuntimeProbeChannel{
		{ChannelID: "canonical-person-b", ChannelType: 1, Role: "follower", Status: "creating", LEO: 20, HW: 18, CheckpointHW: 17, LeaderEpoch: 9, ChannelEpoch: 7},
		{ChannelID: "canonical-person-missing", ChannelType: 1, Role: "missing", Status: "missing"},
		{ChannelID: "canonical-person-a", ChannelType: 1, Role: "leader", Status: "active", LEO: 33, HW: 31, CheckpointHW: 29, LeaderEpoch: 11, ChannelEpoch: 8},
	}
	if !reflect.DeepEqual(got.Channels, wantChannels) {
		t.Fatalf("probe channels = %#v, want ordered detailed rows %#v", got.Channels, wantChannels)
	}
	if !reflect.DeepEqual(got.Missing, []string{"canonical-person-missing"}) {
		t.Fatalf("missing = %#v, want explicit missing identity", got.Missing)
	}
}

func TestChannelRuntimeBenchControllerRejectsUnrepresentableProbeEpoch(t *testing.T) {
	const channelID = "canonical-person-overflow"
	node := &fakeChannelRuntimeBenchNode{
		probe: channelruntime.RuntimeProbeResult{
			Checked:      1,
			LoadedLeader: 1,
			Channels: []channelruntime.RuntimeProbeChannel{{
				ChannelID: channelruntime.ChannelID{ID: channelID, Type: 1},
				Role:      channelruntime.RoleLeader, Status: channelruntime.StatusActive,
				LeaderEpoch: uint64(^uint32(0)) + 1,
			}},
		},
	}
	controller := NewChannelRuntimeBenchController(node)

	_, err := controller.Probe(context.Background(), model.ChannelRuntimeProbeQuery{
		Channels: []model.ChannelRuntimeChannelIdentity{{ChannelID: channelID, ChannelType: 1}},
	})
	if err == nil {
		t.Fatal("Probe() error = nil, want unrepresentable epoch failure")
	}
	if got := model.ChannelRuntimeProbeFailureReasonOf(err); got != model.ChannelRuntimeProbeFailureInvalidEvidence {
		t.Fatalf("Probe() failure reason = %q, want invalid evidence", got)
	}
	if strings.Contains(err.Error(), channelID) {
		t.Fatalf("Probe() error exposed channel identity: %v", err)
	}
}

func TestChannelRuntimeBenchControllerRejectsInvalidExplicitEvidenceCover(t *testing.T) {
	first := channelruntime.ChannelID{ID: "person|a:1/with:separators", Type: 1}
	second := channelruntime.ChannelID{ID: "person|b:2/with:separators", Type: 1}
	extra := channelruntime.ChannelID{ID: "person|extra:3/with:separators", Type: 1}
	base := func() channelruntime.RuntimeProbeResult {
		return channelruntime.RuntimeProbeResult{
			Checked: 2, LoadedLeader: 1,
			Channels: []channelruntime.RuntimeProbeChannel{{ChannelID: first, Role: channelruntime.RoleLeader, Status: channelruntime.StatusActive}},
			Missing:  []channelruntime.ChannelID{second},
		}
	}
	tests := []struct {
		name   string
		mutate func(*channelruntime.RuntimeProbeResult)
	}{
		{name: "duplicate loaded", mutate: func(r *channelruntime.RuntimeProbeResult) { r.Channels = append(r.Channels, r.Channels[0]) }},
		{name: "duplicate missing", mutate: func(r *channelruntime.RuntimeProbeResult) { r.Missing = append(r.Missing, second) }},
		{name: "extra loaded", mutate: func(r *channelruntime.RuntimeProbeResult) {
			r.Channels = append(r.Channels, channelruntime.RuntimeProbeChannel{ChannelID: extra})
		}},
		{name: "extra missing", mutate: func(r *channelruntime.RuntimeProbeResult) { r.Missing = append(r.Missing, extra) }},
		{name: "loaded and missing", mutate: func(r *channelruntime.RuntimeProbeResult) { r.Missing = append(r.Missing, first) }},
		{name: "omission", mutate: func(r *channelruntime.RuntimeProbeResult) { r.Missing = nil }},
		{name: "loaded aggregate mismatch", mutate: func(r *channelruntime.RuntimeProbeResult) { r.LoadedLeader = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := base()
			tt.mutate(&probe)
			controller := NewChannelRuntimeBenchController(&fakeChannelRuntimeBenchNode{probe: probe})
			_, err := controller.Probe(context.Background(), model.ChannelRuntimeProbeQuery{Channels: []model.ChannelRuntimeChannelIdentity{
				{ChannelID: first.ID, ChannelType: first.Type},
				{ChannelID: second.ID, ChannelType: second.Type},
			}})
			if got := model.ChannelRuntimeProbeFailureReasonOf(err); got != model.ChannelRuntimeProbeFailureInvalidEvidence {
				t.Fatalf("failure reason = %q, want %q (err=%v)", got, model.ChannelRuntimeProbeFailureInvalidEvidence, err)
			}
			if err != nil && (strings.Contains(err.Error(), first.ID) || strings.Contains(err.Error(), second.ID) || strings.Contains(err.Error(), extra.ID)) {
				t.Fatalf("error exposed channel identity: %v", err)
			}
		})
	}
}

func TestChannelRuntimeBenchControllerClassifiesExplicitRuntimeFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want model.ChannelRuntimeProbeFailureReason
	}{
		{name: "canceled", err: context.Canceled, want: model.ChannelRuntimeProbeFailureCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: model.ChannelRuntimeProbeFailureDeadline},
		{name: "runtime unavailable", err: channelruntime.ErrClosed, want: model.ChannelRuntimeProbeFailureRuntimeUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := NewChannelRuntimeBenchController(&fakeChannelRuntimeBenchNode{probeErr: tt.err})
			_, err := controller.Probe(context.Background(), model.ChannelRuntimeProbeQuery{Channels: []model.ChannelRuntimeChannelIdentity{{ChannelID: "sentinel|private", ChannelType: 1}}})
			if got := model.ChannelRuntimeProbeFailureReasonOf(err); got != tt.want {
				t.Fatalf("failure reason = %q, want %q", got, tt.want)
			}
			if strings.Contains(err.Error(), "sentinel|private") {
				t.Fatalf("error exposed identity: %v", err)
			}
		})
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
	probeErr error
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
	return n.probe, n.probeErr
}

func (n *fakeChannelRuntimeBenchNode) ChannelRuntimeEvict(_ context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error) {
	n.evictSelector = selector
	return n.evict, nil
}
