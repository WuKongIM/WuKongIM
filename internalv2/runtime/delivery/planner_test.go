package delivery

import (
	"context"
	"testing"
)

func TestPlannerBuildsOneTaskPerAuthorityPartition(t *testing.T) {
	planner := NewPlanner(PlannerOptions{
		Partitioner: staticPartitioner{
			partitions: []Partition{
				{ID: 1, LeaderNodeID: 10, HashSlotStart: 0, HashSlotEnd: 9},
				{ID: 2, LeaderNodeID: 20, HashSlotStart: 10, HashSlotEnd: 19},
			},
		},
	})

	tasks, err := planner.Plan(context.Background(), Envelope{MessageID: 1001, ChannelID: "c1"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("Plan() task count = %d, want 2", len(tasks))
	}
	if tasks[0].Partition.LeaderNodeID != 10 || tasks[1].Partition.LeaderNodeID != 20 {
		t.Fatalf("Plan() leader node IDs = %d,%d, want 10,20", tasks[0].Partition.LeaderNodeID, tasks[1].Partition.LeaderNodeID)
	}
	for i, task := range tasks {
		if task.Attempt != 1 {
			t.Fatalf("tasks[%d].Attempt = %d, want 1", i, task.Attempt)
		}
	}
}

func TestPlannerClonesEnvelope(t *testing.T) {
	payload := []byte("payload")
	scoped := []string{"u1", "u2"}
	env := Envelope{
		MessageID:         1001,
		Payload:           payload,
		MessageScopedUIDs: scoped,
	}
	planner := NewPlanner(PlannerOptions{})

	tasks, err := planner.Plan(context.Background(), env)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Plan() task count = %d, want 1", len(tasks))
	}

	payload[0] = 'X'
	scoped[0] = "mutated"

	if got := string(tasks[0].Envelope.Payload); got != "payload" {
		t.Fatalf("task payload = %q, want %q", got, "payload")
	}
	if got := tasks[0].Envelope.MessageScopedUIDs[0]; got != "u1" {
		t.Fatalf("task scoped uid = %q, want %q", got, "u1")
	}
	if len(tasks[0].Envelope.Payload) > 0 && &tasks[0].Envelope.Payload[0] == &payload[0] {
		t.Fatal("task envelope payload shares memory with caller")
	}
	if len(tasks[0].Envelope.MessageScopedUIDs) > 0 && &tasks[0].Envelope.MessageScopedUIDs[0] == &scoped[0] {
		t.Fatal("task envelope scoped UIDs share memory with caller")
	}
}

func TestPlannerScopedUIDsUseSingleTaskAcrossPartitions(t *testing.T) {
	planner := NewPlanner(PlannerOptions{
		Partitioner: staticPartitioner{
			partitions: []Partition{
				{ID: 1, LeaderNodeID: 10},
				{ID: 2, LeaderNodeID: 20},
			},
		},
	})
	payload := []byte("payload")
	scopedUIDs := []string{"u1", "u2"}
	env := Envelope{
		MessageID:         1001,
		Payload:           payload,
		MessageScopedUIDs: scopedUIDs,
	}

	tasks, err := planner.Plan(context.Background(), env)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Plan() task count = %d, want 1", len(tasks))
	}
	if tasks[0].Partition.ID != 1 {
		t.Fatalf("scoped task partition ID = %d, want 1", tasks[0].Partition.ID)
	}
	if tasks[0].Attempt != 1 {
		t.Fatalf("scoped task attempt = %d, want 1", tasks[0].Attempt)
	}

	payload[0] = 'X'
	scopedUIDs[0] = "mutated"
	if got := string(tasks[0].Envelope.Payload); got != "payload" {
		t.Fatalf("scoped task payload = %q, want payload", got)
	}
	if got := tasks[0].Envelope.MessageScopedUIDs[0]; got != "u1" {
		t.Fatalf("scoped task uid = %q, want u1", got)
	}
}

type staticPartitioner struct {
	partitions []Partition
}

func (p staticPartitioner) Partitions(context.Context) ([]Partition, error) {
	return p.partitions, nil
}
