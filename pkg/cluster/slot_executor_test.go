package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestSlotExecutorExecuteUsesClusterScopedExecutionHook(t *testing.T) {
	clusterA := newObserverTestCluster(t, ObserverHooks{})
	clusterB := newObserverTestCluster(t, ObserverHooks{})
	sentinel := errors.New("slot executor hook failed")

	restore := clusterA.SetManagedSlotExecutionTestHook(func(slotID uint32, task controllermeta.ReconcileTask) error {
		if slotID != 1 {
			t.Fatalf("hook slotID = %d, want 1", slotID)
		}
		if task.SlotID != 1 {
			t.Fatalf("hook task slotID = %d, want 1", task.SlotID)
		}
		return sentinel
	})
	defer restore()

	state := assignmentTaskState{
		assignment: controllermeta.SlotAssignment{SlotID: 1},
		task:       controllermeta.ReconcileTask{SlotID: 1},
	}

	if err := newSlotExecutor(clusterA).Execute(context.Background(), state); !errors.Is(err, sentinel) {
		t.Fatalf("slotExecutor.Execute() error = %v, want %v", err, sentinel)
	}
	if err := newSlotExecutor(clusterB).Execute(context.Background(), state); err != nil {
		t.Fatalf("slotExecutor.Execute() error = %v, want nil", err)
	}
}

func TestSlotExecutorExecuteRepairAndRebalanceStepOrder(t *testing.T) {
	testCases := []struct {
		name string
		kind controllermeta.TaskKind
	}{
		{name: "repair", kind: controllermeta.TaskKindRepair},
		{name: "rebalance", kind: controllermeta.TaskKindRebalance},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &Cluster{}
			var calls []string
			executor := newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{
				prepareSlot: func(slotID multiraft.SlotID, desiredPeers []uint64) {
					if slotID != 1 {
						t.Fatalf("prepareSlot() slotID = %d, want 1", slotID)
					}
					calls = append(calls, "PrepareSlot")
				},
				changeSlotConfig: func(_ context.Context, slotID multiraft.SlotID, change multiraft.ConfigChange) error {
					if slotID != 1 {
						t.Fatalf("changeSlotConfig() slotID = %d, want 1", slotID)
					}
					calls = append(calls, configChangeStepName(change.Type))
					return nil
				},
				waitForCatchUp: func(_ context.Context, slotID multiraft.SlotID, targetNode multiraft.NodeID) error {
					if slotID != 1 {
						t.Fatalf("waitForCatchUp() slotID = %d, want 1", slotID)
					}
					if targetNode != 2 {
						t.Fatalf("waitForCatchUp() targetNode = %d, want 2", targetNode)
					}
					calls = append(calls, "CatchUp")
					return nil
				},
				ensureLeaderMovedOffSource: func(_ context.Context, slotID multiraft.SlotID, sourceNode, targetNode multiraft.NodeID) error {
					if slotID != 1 {
						t.Fatalf("ensureLeaderMovedOffSource() slotID = %d, want 1", slotID)
					}
					if sourceNode != 3 || targetNode != 2 {
						t.Fatalf("ensureLeaderMovedOffSource() source=%d target=%d, want source=3 target=2", sourceNode, targetNode)
					}
					calls = append(calls, "MoveLeaderOffSource")
					return nil
				},
			})

			err := executor.Execute(context.Background(), assignmentTaskState{
				assignment: controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2}},
				task: controllermeta.ReconcileTask{
					SlotID:     1,
					Kind:       tc.kind,
					TargetNode: 2,
					SourceNode: 3,
				},
			})
			if err != nil {
				t.Fatalf("slotExecutor.Execute() error = %v, want nil", err)
			}
			want := []string{
				"PrepareSlot",
				"AddLearner",
				"CatchUp",
				"PromoteLearner",
				"CatchUp",
				"MoveLeaderOffSource",
				"RemoveVoter",
			}
			if !slices.Equal(calls, want) {
				t.Fatalf("slotExecutor.Execute() calls = %v, want %v", calls, want)
			}
		})
	}
}

func TestSlotExecutorExecuteStopsOnStepError(t *testing.T) {
	cluster := &Cluster{}
	sentinel := errors.New("promote failed")
	var calls []string
	executor := newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{
		prepareSlot: func(_ multiraft.SlotID, _ []uint64) {
			calls = append(calls, "PrepareSlot")
		},
		changeSlotConfig: func(_ context.Context, _ multiraft.SlotID, change multiraft.ConfigChange) error {
			step := configChangeStepName(change.Type)
			calls = append(calls, step)
			if change.Type == multiraft.PromoteLearner {
				return sentinel
			}
			return nil
		},
		waitForCatchUp: func(_ context.Context, _ multiraft.SlotID, _ multiraft.NodeID) error {
			calls = append(calls, "CatchUp")
			return nil
		},
		ensureLeaderMovedOffSource: func(_ context.Context, _ multiraft.SlotID, _, _ multiraft.NodeID) error {
			calls = append(calls, "MoveLeaderOffSource")
			return nil
		},
	})

	err := executor.Execute(context.Background(), assignmentTaskState{
		assignment: controllermeta.SlotAssignment{SlotID: 1},
		task: controllermeta.ReconcileTask{
			SlotID:     1,
			Kind:       controllermeta.TaskKindRepair,
			TargetNode: 2,
			SourceNode: 3,
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("slotExecutor.Execute() error = %v, want %v", err, sentinel)
	}
	want := []string{
		"PrepareSlot",
		"AddLearner",
		"CatchUp",
		"PromoteLearner",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("slotExecutor.Execute() calls = %v, want %v", calls, want)
	}
}

func configChangeStepName(change multiraft.ChangeType) string {
	switch change {
	case multiraft.AddLearner:
		return "AddLearner"
	case multiraft.PromoteLearner:
		return "PromoteLearner"
	case multiraft.RemoveVoter:
		return "RemoveVoter"
	default:
		return fmt.Sprintf("UnknownChangeType(%d)", change)
	}
}
