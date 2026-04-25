package cluster

import (
	"context"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
)

func TestNodeHealthSchedulerRefreshesDeadlinesOnObservation(t *testing.T) {
	now := time.Unix(100, 0)
	var timers []*recordingHealthTimer

	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		suspectTimeout: 3 * time.Second,
		deadTimeout:    10 * time.Second,
		now:            func() time.Time { return now },
		afterFunc: func(delay time.Duration, fn func()) healthTimer {
			timer := &recordingHealthTimer{delay: delay, fn: fn}
			timers = append(timers, timer)
			return timer
		},
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			return controllermeta.ClusterNode{
				NodeID:         1,
				Addr:           "127.0.0.1:7001",
				Status:         controllermeta.NodeStatusAlive,
				CapacityWeight: 1,
			}, nil
		},
		propose: func(context.Context, slotcontroller.Command) error { return nil },
	})

	scheduler.observe(nodeObservation{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		ObservedAt:     now,
		CapacityWeight: 1,
	})
	first := scheduler.nodes[1]
	if first.generation != 1 {
		t.Fatalf("generation after first observe = %d, want 1", first.generation)
	}
	if got, want := first.suspectAt, now.Add(3*time.Second); !got.Equal(want) {
		t.Fatalf("suspectAt after first observe = %v, want %v", got, want)
	}
	if got, want := first.deadAt, now.Add(10*time.Second); !got.Equal(want) {
		t.Fatalf("deadAt after first observe = %v, want %v", got, want)
	}

	now = now.Add(2 * time.Second)
	scheduler.observe(nodeObservation{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		ObservedAt:     now,
		CapacityWeight: 1,
	})
	second := scheduler.nodes[1]
	if second.generation != 2 {
		t.Fatalf("generation after refresh = %d, want 2", second.generation)
	}
	if got, want := second.suspectAt, now.Add(3*time.Second); !got.Equal(want) {
		t.Fatalf("suspectAt after refresh = %v, want %v", got, want)
	}
	if got, want := second.deadAt, now.Add(10*time.Second); !got.Equal(want) {
		t.Fatalf("deadAt after refresh = %v, want %v", got, want)
	}
	if len(timers) != 4 {
		t.Fatalf("timers scheduled = %d, want 4", len(timers))
	}
	if timers[0].stopCalls != 1 || timers[1].stopCalls != 1 {
		t.Fatalf("stale timers stopCalls = %d/%d, want 1/1", timers[0].stopCalls, timers[1].stopCalls)
	}
}

func TestNodeHealthSchedulerIgnoresStaleGenerationWakeup(t *testing.T) {
	now := time.Unix(200, 0)
	var proposals []slotcontroller.Command

	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		suspectTimeout: time.Second,
		deadTimeout:    2 * time.Second,
		now:            func() time.Time { return now },
		afterFunc: func(time.Duration, func()) healthTimer {
			return &recordingHealthTimer{}
		},
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			return controllermeta.ClusterNode{
				NodeID:         1,
				Addr:           "127.0.0.1:7001",
				Status:         controllermeta.NodeStatusAlive,
				CapacityWeight: 1,
			}, nil
		},
		propose: func(_ context.Context, cmd slotcontroller.Command) error {
			proposals = append(proposals, cmd)
			return nil
		},
	})

	scheduler.observe(nodeObservation{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		ObservedAt:     now,
		CapacityWeight: 1,
	})
	staleGeneration := scheduler.nodes[1].generation
	now = now.Add(500 * time.Millisecond)
	scheduler.observe(nodeObservation{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		ObservedAt:     now,
		CapacityWeight: 1,
	})

	scheduler.handleDeadline(1, staleGeneration, controllermeta.NodeStatusSuspect, now)

	if len(proposals) != 0 {
		t.Fatalf("stale deadline proposals = %#v, want none", proposals)
	}
}

func TestNodeHealthSchedulerProposesOnlyOnStatusEdge(t *testing.T) {
	now := time.Unix(300, 0)
	node := controllermeta.ClusterNode{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		Status:         controllermeta.NodeStatusAlive,
		CapacityWeight: 1,
	}
	var proposals []slotcontroller.Command

	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		suspectTimeout: time.Second,
		deadTimeout:    2 * time.Second,
		now:            func() time.Time { return now },
		afterFunc: func(time.Duration, func()) healthTimer {
			return &recordingHealthTimer{}
		},
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			return node, nil
		},
		propose: func(_ context.Context, cmd slotcontroller.Command) error {
			proposals = append(proposals, cmd)
			if cmd.NodeStatusUpdate != nil && len(cmd.NodeStatusUpdate.Transitions) == 1 {
				node.Status = cmd.NodeStatusUpdate.Transitions[0].NewStatus
			}
			return nil
		},
	})

	scheduler.observe(nodeObservation{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		ObservedAt:     now,
		CapacityWeight: 1,
	})
	state := scheduler.nodes[1]

	scheduler.handleDeadline(1, state.generation, controllermeta.NodeStatusSuspect, state.suspectAt)
	scheduler.handleDeadline(1, state.generation, controllermeta.NodeStatusSuspect, state.suspectAt)

	if len(proposals) != 1 {
		t.Fatalf("status edge proposals = %d, want 1", len(proposals))
	}
	if proposals[0].Kind != slotcontroller.CommandKindNodeStatusUpdate {
		t.Fatalf("proposal kind = %v, want node status update", proposals[0].Kind)
	}
	update := proposals[0].NodeStatusUpdate
	if update == nil || len(update.Transitions) != 1 {
		t.Fatalf("proposal update = %#v", update)
	}
	if update.Transitions[0].NewStatus != controllermeta.NodeStatusSuspect {
		t.Fatalf("proposal new status = %v, want suspect", update.Transitions[0].NewStatus)
	}
}

func TestNodeHealthSchedulerUsesMirrorForRepeatedAliveObservation(t *testing.T) {
	now := time.Unix(400, 0)
	loadCalls := 0

	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		suspectTimeout: time.Second,
		deadTimeout:    2 * time.Second,
		now:            func() time.Time { return now },
		afterFunc: func(time.Duration, func()) healthTimer {
			return &recordingHealthTimer{}
		},
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			loadCalls++
			return controllermeta.ClusterNode{
				NodeID:         1,
				Addr:           "127.0.0.1:7001",
				Status:         controllermeta.NodeStatusAlive,
				CapacityWeight: 1,
			}, nil
		},
		propose: func(context.Context, slotcontroller.Command) error { return nil },
	})

	scheduler.observe(nodeObservation{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		ObservedAt:     now,
		CapacityWeight: 1,
	})
	now = now.Add(200 * time.Millisecond)
	scheduler.observe(nodeObservation{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		ObservedAt:     now,
		CapacityWeight: 1,
	})

	if loadCalls != 1 {
		t.Fatalf("loadNode calls = %d, want 1", loadCalls)
	}
}

func TestNodeHealthSchedulerMirrorMissLoadsNodeAndBackfills(t *testing.T) {
	now := time.Unix(500, 0)
	loadCalls := 0

	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		suspectTimeout: time.Second,
		deadTimeout:    2 * time.Second,
		now:            func() time.Time { return now },
		afterFunc: func(time.Duration, func()) healthTimer {
			return &recordingHealthTimer{}
		},
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			loadCalls++
			return controllermeta.ClusterNode{
				NodeID:         1,
				Addr:           "127.0.0.1:7001",
				Status:         controllermeta.NodeStatusAlive,
				CapacityWeight: 1,
			}, nil
		},
		propose: func(context.Context, slotcontroller.Command) error { return nil },
	})

	scheduler.observe(nodeObservation{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		ObservedAt:     now,
		CapacityWeight: 1,
	})

	if loadCalls != 1 {
		t.Fatalf("loadNode calls = %d, want 1", loadCalls)
	}
	node, ok := scheduler.mirroredNode(1)
	if !ok {
		t.Fatal("mirroredNode(1) ok = false, want true")
	}
	if node.Status != controllermeta.NodeStatusAlive {
		t.Fatalf("mirroredNode(1).Status = %v, want alive", node.Status)
	}
}

func TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirror(t *testing.T) {
	now := time.Unix(600, 0)
	node := controllermeta.ClusterNode{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		Status:         controllermeta.NodeStatusAlive,
		CapacityWeight: 1,
	}

	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		suspectTimeout: time.Second,
		deadTimeout:    2 * time.Second,
		now:            func() time.Time { return now },
		afterFunc: func(time.Duration, func()) healthTimer {
			return &recordingHealthTimer{}
		},
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			return node, nil
		},
		propose: func(context.Context, slotcontroller.Command) error { return nil },
	})

	scheduler.mirrorNode(node)
	node.Status = controllermeta.NodeStatusSuspect

	scheduler.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeStatusUpdate,
		NodeStatusUpdate: &slotcontroller.NodeStatusUpdate{
			Transitions: []slotcontroller.NodeStatusTransition{{NodeID: 1, NewStatus: controllermeta.NodeStatusSuspect}},
		},
	})

	mirrored, ok := scheduler.mirroredNode(1)
	if !ok {
		t.Fatal("mirroredNode(1) ok = false, want true")
	}
	if mirrored.Status != controllermeta.NodeStatusSuspect {
		t.Fatalf("mirroredNode(1).Status = %v, want suspect", mirrored.Status)
	}
}

func TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirrorForNodeStatusUpdate(t *testing.T) {
	node := controllermeta.ClusterNode{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		Status:         controllermeta.NodeStatusAlive,
		CapacityWeight: 1,
	}

	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			return node, nil
		},
	})

	scheduler.mirrorNode(controllermeta.ClusterNode{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		Status:         controllermeta.NodeStatusDead,
		CapacityWeight: 1,
	})

	scheduler.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeStatusUpdate,
		NodeStatusUpdate: &slotcontroller.NodeStatusUpdate{
			Transitions: []slotcontroller.NodeStatusTransition{{
				NodeID:    1,
				NewStatus: controllermeta.NodeStatusSuspect,
			}},
		},
	})

	mirrored, ok := scheduler.mirroredNode(1)
	if !ok {
		t.Fatal("mirroredNode(1) ok = false, want true")
	}
	if mirrored.Status != controllermeta.NodeStatusAlive {
		t.Fatalf("mirroredNode(1).Status = %v, want alive from store refresh", mirrored.Status)
	}
}

func TestNodeHealthSchedulerHandleCommittedCommandRefreshesMirrorForOperatorRequest(t *testing.T) {
	node := controllermeta.ClusterNode{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		Status:         controllermeta.NodeStatusAlive,
		CapacityWeight: 1,
	}

	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			return node, nil
		},
	})

	scheduler.mirrorNode(controllermeta.ClusterNode{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		Status:         controllermeta.NodeStatusDraining,
		CapacityWeight: 1,
	})

	scheduler.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindOperatorRequest,
		Op: &slotcontroller.OperatorRequest{
			NodeID: 1,
			Kind:   slotcontroller.OperatorMarkNodeDraining,
		},
	})

	mirrored, ok := scheduler.mirroredNode(1)
	if !ok {
		t.Fatal("mirroredNode(1) ok = false, want true")
	}
	if mirrored.Status != controllermeta.NodeStatusAlive {
		t.Fatalf("mirroredNode(1).Status = %v, want alive from store refresh", mirrored.Status)
	}
}

func TestNodeHealthSchedulerResetClearsMirror(t *testing.T) {
	scheduler := newNodeHealthScheduler(nodeHealthSchedulerConfig{
		loadNode: func(context.Context, uint64) (controllermeta.ClusterNode, error) {
			return controllermeta.ClusterNode{}, nil
		},
	})
	scheduler.mirrorNode(controllermeta.ClusterNode{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		Status:         controllermeta.NodeStatusAlive,
		CapacityWeight: 1,
	})

	scheduler.reset()

	if _, ok := scheduler.mirroredNode(1); ok {
		t.Fatal("mirroredNode(1) ok = true after reset, want false")
	}
}

type recordingHealthTimer struct {
	delay     time.Duration
	fn        func()
	stopCalls int
}

func (t *recordingHealthTimer) Stop() bool {
	t.stopCalls++
	return true
}

func (t *recordingHealthTimer) fire() {
	if t.fn != nil {
		t.fn()
	}
}
