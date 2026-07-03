package fsm

import (
	"reflect"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

// TaskTransition describes one durable Controller task state edge.
type TaskTransition struct {
	// AppliedRaftIndex is the Raft log index that produced this task edge.
	AppliedRaftIndex uint64
	// AppliedRaftTerm is the Raft term that produced this task edge.
	AppliedRaftTerm uint64
	// CommandKind identifies the Controller command that produced this edge.
	CommandKind command.Kind
	// IssuedAt records the command proposer timestamp normalized to UTC.
	IssuedAt time.Time
	// Before is the active task before the command when BeforeValid is true.
	Before state.ReconcileTask
	// BeforeValid reports whether Before contains a real pre-command task.
	BeforeValid bool
	// After is the active task after the command when AfterValid is true.
	After state.ReconcileTask
	// AfterValid reports whether After contains a real post-command task.
	AfterValid bool
	// ParticipantNode identifies the reporting participant for progress commands.
	ParticipantNode uint64
}

func taskTransitionsForCommand(index uint64, term uint64, cmd command.Command, before []state.ReconcileTask, after []state.ReconcileTask) []TaskTransition {
	beforeByID := taskMapByID(before)
	afterByID := taskMapByID(after)
	ids := make([]string, 0, len(beforeByID)+len(afterByID))
	seen := make(map[string]struct{}, len(beforeByID)+len(afterByID))
	for taskID := range beforeByID {
		seen[taskID] = struct{}{}
		ids = append(ids, taskID)
	}
	for taskID := range afterByID {
		if _, ok := seen[taskID]; ok {
			continue
		}
		ids = append(ids, taskID)
	}
	sort.Strings(ids)

	transitions := make([]TaskTransition, 0, len(ids))
	for _, taskID := range ids {
		beforeTask, beforeValid := beforeByID[taskID]
		afterTask, afterValid := afterByID[taskID]
		if beforeValid && afterValid && reflect.DeepEqual(beforeTask, afterTask) {
			continue
		}
		transition := TaskTransition{
			AppliedRaftIndex: index,
			AppliedRaftTerm:  term,
			CommandKind:      cmd.Kind,
			IssuedAt:         commandIssuedAt(cmd.IssuedAt),
			BeforeValid:      beforeValid,
			AfterValid:       afterValid,
			ParticipantNode:  transitionParticipantNode(cmd),
		}
		if beforeValid {
			transition.Before = cloneTask(beforeTask)
		}
		if afterValid {
			transition.After = cloneTask(afterTask)
		}
		transitions = append(transitions, transition)
	}
	return transitions
}

func taskMapByID(tasks []state.ReconcileTask) map[string]state.ReconcileTask {
	out := make(map[string]state.ReconcileTask, len(tasks))
	for _, task := range tasks {
		if task.TaskID == "" {
			continue
		}
		out[task.TaskID] = cloneTask(task)
	}
	return out
}

func cloneTasks(tasks []state.ReconcileTask) []state.ReconcileTask {
	out := make([]state.ReconcileTask, len(tasks))
	for i := range tasks {
		out[i] = cloneTask(tasks[i])
	}
	return out
}

func transitionParticipantNode(cmd command.Command) uint64 {
	if cmd.TaskProgress != nil {
		return cmd.TaskProgress.ParticipantNodeID
	}
	return 0
}
