package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

const runtimeDrainPollInterval = 10 * time.Millisecond

// RuntimeSnapshot summarizes loaded Channel runtimes across all reactors.
func (g *Group) RuntimeSnapshot(ctx context.Context) (ch.RuntimeSnapshot, error) {
	if g == nil || g.closed.Load() {
		return ch.RuntimeSnapshot{}, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot := ch.RuntimeSnapshot{NodeID: g.cfg.LocalNode}
	futures := make([]*Future, 0, len(g.reactors))
	for _, reactor := range g.reactors {
		if err := ctx.Err(); err != nil {
			return ch.RuntimeSnapshot{}, err
		}
		future := NewFuture()
		if err := reactor.Submit(eventPriority(EventRuntimeSnapshot), Event{Kind: EventRuntimeSnapshot, Future: future}); err != nil {
			return ch.RuntimeSnapshot{}, err
		}
		futures = append(futures, future)
	}
	for _, future := range futures {
		result, err := future.Await(ctx)
		if err != nil {
			return ch.RuntimeSnapshot{}, err
		}
		reactorSnapshot := result.RuntimeSnapshot
		snapshot.Reactors = append(snapshot.Reactors, reactorSnapshot)
		snapshot.ActiveLeader += reactorSnapshot.Leader
		snapshot.ActiveFollower += reactorSnapshot.Follower
		snapshot.FollowerParked += reactorSnapshot.Parked
		snapshot.ActivationRejectedTotal += result.RuntimeActivationRejectedTotal
	}
	snapshot.ActiveTotal = snapshot.ActiveLeader + snapshot.ActiveFollower
	snapshot.WorkerQueues = runtimeWorkerQueues(g.pools)
	return snapshot, nil
}

// RuntimeProbe inspects selected channel runtimes on their owning reactors.
func (g *Group) RuntimeProbe(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeProbeResult, error) {
	if g == nil || g.closed.Load() {
		return ch.RuntimeProbeResult{}, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ids := append([]ch.ChannelID(nil), selector.ChannelIDs...)
	result := ch.RuntimeProbeResult{}
	if len(ids) == 0 {
		return result, nil
	}
	submissions, submitErr := g.submitRuntimeSelected(ctx, EventRuntimeProbe, ids)
	missingCounts := make(map[ch.ChannelID]int)
	loadedByID := make(map[ch.ChannelID][]ch.RuntimeProbeChannel)
	for _, submission := range submissions {
		future := submission.future
		reactorResult, err := future.Await(ctx)
		if err != nil {
			return ch.RuntimeProbeResult{}, err
		}
		probe := reactorResult.RuntimeProbe
		result.Checked += probe.Checked
		result.LoadedLeader += probe.LoadedLeader
		result.LoadedFollower += probe.LoadedFollower
		for _, channel := range probe.Channels {
			loadedByID[channel.ChannelID] = append(loadedByID[channel.ChannelID], channel)
		}
		for _, missing := range probe.Missing {
			missingCounts[missing]++
		}
	}
	for _, id := range ids {
		if missingCounts[id] == 0 {
			if loaded := loadedByID[id]; len(loaded) > 0 {
				result.Channels = append(result.Channels, loaded[0])
				loadedByID[id] = loaded[1:]
			}
			continue
		}
		result.Missing = append(result.Missing, id)
		missingCounts[id]--
	}
	return result, submitErr
}

// DrainChannel waits until a fenced local leader has no admitted append work and HW covers LEO.
func (g *Group) DrainChannel(ctx context.Context, req ch.DrainChannelRequest) (ch.DrainChannelResult, error) {
	if g == nil || g.closed.Load() {
		return ch.DrainChannelResult{}, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req.ChannelID.ID == "" || req.LeaderEpoch == 0 || req.FenceVersion == 0 {
		return ch.DrainChannelResult{}, ch.ErrInvalidConfig
	}
	deadline := time.Time{}
	if req.Timeout > 0 {
		deadline = time.Now().Add(req.Timeout)
	}
	for {
		result, err := g.checkDrainChannel(ctx, req)
		if err != nil || result.Drained {
			return result, err
		}
		if deadline.IsZero() {
			return result, nil
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return result, nil
		}
		if wait > runtimeDrainPollInterval {
			wait = runtimeDrainPollInterval
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return result, ctx.Err()
		}
	}
}

func (g *Group) checkDrainChannel(ctx context.Context, req ch.DrainChannelRequest) (ch.DrainChannelResult, error) {
	key := ch.ChannelKeyForID(req.ChannelID)
	future, err := g.Submit(ctx, key, Event{Kind: EventDrainChannel, Key: key, DrainChannel: req})
	if err != nil {
		return ch.DrainChannelResult{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.DrainChannelResult{}, err
	}
	return result.DrainChannel, nil
}

// RuntimeEvict evicts selected safe channel runtimes on their owning reactors.
func (g *Group) RuntimeEvict(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeEvictResult, error) {
	if g == nil || g.closed.Load() {
		return ch.RuntimeEvictResult{}, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ids := append([]ch.ChannelID(nil), selector.ChannelIDs...)
	result := ch.RuntimeEvictResult{Requested: len(ids)}
	if len(ids) == 0 {
		return result, nil
	}
	submissions, submitErr := g.submitRuntimeSelected(ctx, EventRuntimeEvict, ids)
	for _, submission := range submissions {
		future := submission.future
		reactorResult, err := future.Await(ctx)
		if err != nil {
			return ch.RuntimeEvictResult{}, err
		}
		evict := reactorResult.RuntimeEvict
		result.Evicted += evict.Evicted
		result.SkippedBusy += evict.SkippedBusy
		result.Missing += evict.Missing
	}
	return result, submitErr
}

type runtimeSubmission struct {
	future *Future
}

func (g *Group) submitRuntimeSelected(ctx context.Context, kind EventKind, ids []ch.ChannelID) ([]runtimeSubmission, error) {
	byReactor := make([][]ch.ChannelID, len(g.reactors))
	for _, id := range ids {
		index := g.router.PickIndex(ch.ChannelKeyForID(id))
		byReactor[index] = append(byReactor[index], id)
	}
	submissions := make([]runtimeSubmission, 0, len(byReactor))
	for index, selected := range byReactor {
		if len(selected) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return submissions, err
		}
		future := NewFuture()
		event := Event{Kind: kind, RuntimeChannelIDs: append([]ch.ChannelID(nil), selected...), Future: future}
		if err := g.reactors[index].Submit(eventPriority(kind), event); err != nil {
			return submissions, err
		}
		submissions = append(submissions, runtimeSubmission{future: future})
	}
	return submissions, nil
}

func runtimeWorkerQueues(pools *worker.Pools) []ch.RuntimeWorkerQueue {
	if pools == nil {
		return nil
	}
	queues := make([]ch.RuntimeWorkerQueue, 0, 4)
	for _, pool := range []*worker.Pool{pools.StoreAppend, pools.StoreRead, pools.StoreApply, pools.RPC} {
		if pool == nil {
			continue
		}
		queues = append(queues, ch.RuntimeWorkerQueue{Pool: pool.Name(), Depth: pool.QueueDepth()})
	}
	return queues
}

func (r *Reactor) handleRuntimeSnapshot(event Event) {
	snapshot := ch.RuntimeReactorSnapshot{ReactorID: r.cfg.ID}
	for _, rc := range r.channels {
		if rc == nil || rc.state == nil {
			continue
		}
		switch rc.state.Role {
		case ch.RoleLeader:
			snapshot.Leader++
		case ch.RoleFollower:
			snapshot.Follower++
			if rc.replication.parked {
				snapshot.Parked++
			}
		}
	}
	snapshot.MailboxDepth = r.mailbox.Depth(PriorityHigh) + r.mailbox.Depth(PriorityNormal) + r.mailbox.Depth(PriorityLow)
	if event.Future != nil {
		event.Future.Complete(Result{RuntimeSnapshot: snapshot, RuntimeActivationRejectedTotal: r.activationRejectedTotal})
	}
}

func (r *Reactor) handleRuntimeProbe(event Event) {
	result := ch.RuntimeProbeResult{Checked: len(event.RuntimeChannelIDs)}
	for _, id := range event.RuntimeChannelIDs {
		rc := r.channels[ch.ChannelKeyForID(id)]
		if rc == nil || rc.state == nil {
			result.Missing = append(result.Missing, id)
			continue
		}
		switch rc.state.Role {
		case ch.RoleLeader:
			result.LoadedLeader++
		case ch.RoleFollower:
			result.LoadedFollower++
		}
		result.Channels = append(result.Channels, ch.RuntimeProbeChannel{
			ChannelID:          rc.state.ID,
			LeaderEpoch:        rc.state.LeaderEpoch,
			ChannelEpoch:       rc.state.Epoch,
			Role:               rc.state.Role,
			Status:             rc.state.Status,
			LEO:                rc.state.LEO,
			HW:                 rc.state.HW,
			CheckpointHW:       rc.state.CheckpointHW,
			WriteFence:         rc.state.WriteFence,
			InflightAppend:     rc.state.InflightAppend != nil,
			PendingAppendCount: len(rc.state.PendingAppends),
		})
	}
	if event.Future != nil {
		event.Future.Complete(Result{RuntimeProbe: result})
	}
}

func (r *Reactor) handleDrainChannel(event Event) {
	result, err := r.checkDrainChannel(event)
	if event.Future != nil {
		event.Future.Complete(Result{DrainChannel: result, Err: err})
	}
}

func (r *Reactor) checkDrainChannel(event Event) (ch.DrainChannelResult, error) {
	req := event.DrainChannel
	key := event.Key
	if key == "" {
		key = ch.ChannelKeyForID(req.ChannelID)
	}
	rc := r.channels[key]
	if rc == nil || rc.state == nil {
		return ch.DrainChannelResult{}, ch.ErrChannelNotFound
	}
	state := rc.state
	result := ch.DrainChannelResult{LEO: state.LEO, HW: state.HW}
	if state.ID != req.ChannelID || state.LeaderEpoch != req.LeaderEpoch {
		return result, ch.ErrStaleMeta
	}
	if state.Role != ch.RoleLeader {
		return result, ch.ErrNotLeader
	}
	if !state.WriteFence.Set() || state.WriteFence.Version != req.FenceVersion {
		return result, ch.ErrStaleMeta
	}
	result.Drained = state.InflightAppend == nil && len(state.PendingAppends) == 0 && state.HW >= state.LEO
	return result, nil
}

func (r *Reactor) handleRuntimeEvict(event Event) {
	result := ch.RuntimeEvictResult{Requested: len(event.RuntimeChannelIDs)}
	for _, id := range event.RuntimeChannelIDs {
		key := ch.ChannelKeyForID(id)
		rc := r.channels[key]
		if rc == nil {
			result.Missing++
			continue
		}
		if rc.loading != nil && rc.state == nil && rc.pending == nil {
			r.completeStoreLoadFutures(rc.loading, Result{Err: ch.ErrClosed})
			delete(r.channels, key)
			result.Evicted++
			continue
		}
		if rc.pending != nil && rc.state == nil {
			r.releasePendingMeta(key, rc, ch.ErrClosed)
			result.Evicted++
			continue
		}
		if r.evictRuntimeChannel(key, rc, "bench runtime evict") {
			result.Evicted++
			continue
		}
		result.SkippedBusy++
	}
	if event.Future != nil {
		event.Future.Complete(Result{RuntimeEvict: result})
	}
}
