package management

import "context"

// ChannelObservationTarget pins one observation to the current Channel Leader
// and authority. Observing it must never load or recover a Channel runtime.
type ChannelObservationTarget struct {
	ChannelID       string
	ChannelType     int64
	LeaderID        uint64
	Epoch           uint64
	LeaderEpoch     uint64
	RouteGeneration uint64
}

// ChannelObservation separates in-memory residency from durable lifecycle.
type ChannelObservation struct {
	Target ChannelObservationTarget
	// Known is false when residency cannot be established under the exact fence.
	Known bool
	// Active means the observed Leader runtime is currently loaded.
	Active bool
	// MaxMessageSeq is absent unless the requested committed tail was established.
	MaxMessageSeq *uint64
}

// ChannelObserver performs bounded, aligned, non-activating Leader observations.
type ChannelObserver interface {
	ObserveChannels(context.Context, []ChannelObservationTarget, bool) ([]ChannelObservation, error)
}

// LocalChannelObserver handles one bounded observation batch on its serving node.
type LocalChannelObserver interface {
	ObserveLocalChannels(context.Context, []ChannelObservationTarget, bool) ([]ChannelObservation, error)
}

// observeChannelRuntimeRows enriches only the emitted page. Failed, partial or
// foreign observations remain unknown and never fall back to activating reads.
func (a *App) observeChannelRuntimeRows(ctx context.Context, rows []ChannelRuntimeMeta, includeMaxMessageSeq bool) error {
	if len(rows) == 0 || a.channelObservations == nil {
		return ctx.Err()
	}
	targets := make([]ChannelObservationTarget, len(rows))
	for i := range rows {
		targets[i] = rows[i].observationTarget
	}
	observations, err := a.channelObservations.ObserveChannels(ctx, targets, includeMaxMessageSeq)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || len(observations) != len(rows) {
		return nil
	}
	for i, observation := range observations {
		if observation.Target != targets[i] || !observation.Known {
			continue
		}
		rows[i].RuntimeStatus = "inactive"
		if observation.Active {
			rows[i].RuntimeStatus = "active"
		}
		if includeMaxMessageSeq && observation.MaxMessageSeq != nil {
			seq := *observation.MaxMessageSeq
			rows[i].MaxMessageSeq = &seq
		}
	}
	return nil
}
