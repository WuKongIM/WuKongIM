package reactor

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

// RuntimeSnapshot summarizes loaded ChannelV2 runtimes across all reactors.
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
		for _, missing := range probe.Missing {
			missingCounts[missing]++
		}
	}
	for _, id := range ids {
		if missingCounts[id] == 0 {
			continue
		}
		result.Missing = append(result.Missing, id)
		missingCounts[id]--
	}
	return result, submitErr
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
	}
	if event.Future != nil {
		event.Future.Complete(Result{RuntimeProbe: result})
	}
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
