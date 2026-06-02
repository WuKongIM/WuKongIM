package delivery

import "context"

// Partitioner lists the current delivery authority partitions.
type Partitioner interface {
	Partitions(context.Context) ([]Partition, error)
}

// PlannerOptions configures a delivery fanout planner.
type PlannerOptions struct {
	// Partitioner provides authority partitions; nil uses a single default partition.
	Partitioner Partitioner
}

// Planner converts committed envelopes into partition-scoped fanout tasks.
type Planner struct {
	partitioner Partitioner
}

// NewPlanner creates a planner for delivery fanout tasks.
func NewPlanner(opts PlannerOptions) *Planner {
	return &Planner{partitioner: opts.Partitioner}
}

// Plan builds one fanout task per authority partition unless the envelope targets scoped UIDs.
func (p *Planner) Plan(ctx context.Context, env Envelope) ([]FanoutTask, error) {
	if len(env.MessageScopedUIDs) > 0 {
		return []FanoutTask{newDefaultFanoutTask(env)}, nil
	}
	if p == nil || p.partitioner == nil {
		return []FanoutTask{newDefaultFanoutTask(env)}, nil
	}

	partitions, err := p.partitioner.Partitions(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]FanoutTask, 0, len(partitions))
	for _, partition := range partitions {
		tasks = append(tasks, FanoutTask{
			Envelope:  cloneEnvelope(env),
			Partition: partition,
			Attempt:   1,
		})
	}
	return tasks, nil
}

func newDefaultFanoutTask(env Envelope) FanoutTask {
	return FanoutTask{
		Envelope:  cloneEnvelope(env),
		Partition: Partition{ID: 1},
		Attempt:   1,
	}
}

func cloneEnvelope(env Envelope) Envelope {
	env.Payload = append([]byte(nil), env.Payload...)
	env.MessageScopedUIDs = append([]string(nil), env.MessageScopedUIDs...)
	return env
}
