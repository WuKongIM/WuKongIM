package runtime

import (
	"errors"
	"slices"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

const (
	defaultFetchMaxBytes                 = 1 << 20
	defaultFollowerReplicationRetryDelay = 10 * time.Millisecond
)

type replicationRetryState struct {
	pending      bool
	timer        *time.Timer
	timerVersion uint64
}

func (r *runtime) OnReplication(key core.ChannelKey) {
	if r.isClosed() {
		return
	}
	r.processReplication(key)
}

func (r *runtime) enqueueReplication(key core.ChannelKey, peer core.NodeID) {
	if r.isClosed() {
		return
	}
	ch, ok := r.lookupChannel(key)
	if !ok {
		return
	}
	ch.enqueueReplication(peer)
	ch.markReplication()
	r.enqueueScheduler(key, PriorityNormal)
}

func (r *runtime) processReplication(key core.ChannelKey) {
	if r.isClosed() {
		return
	}
	ch, ok := r.lookupChannel(key)
	if !ok {
		return
	}
	meta := ch.metaSnapshot()
	if r.longPollEnabled() && meta.Leader != 0 && meta.Leader != r.cfg.LocalNode {
		r.processFollowerLongPoll(ch, meta)
		return
	}
	if r.longPollEnabled() && meta.Leader == r.cfg.LocalNode {
		r.processLeaderLongPoll(ch, meta)
		return
	}

	var failedPeers []core.NodeID
	scheduleRetry := false
	for {
		peer, ok := ch.popReplicationPeer()
		if !ok {
			break
		}
		state := ch.Status()
		env := Envelope{
			Peer:       peer,
			ChannelKey: key,
			Epoch:      state.Epoch,
			Generation: ch.gen,
			RequestID:  r.requestID.Add(1),
			Kind:       MessageKindFetchRequest,
			FetchRequest: &FetchRequestEnvelope{
				ChannelKey:  key,
				Epoch:       state.Epoch,
				Generation:  ch.gen,
				ReplicaID:   r.cfg.LocalNode,
				FetchOffset: state.LEO,
				OffsetEpoch: state.OffsetEpoch,
				MaxBytes:    defaultFetchMaxBytes,
			},
		}
		if state.Role == core.ReplicaRoleLeader && !state.CommitReady {
			env.Kind = MessageKindReconcileProbeRequest
			env.FetchRequest = nil
			env.ReconcileProbeRequest = &ReconcileProbeRequestEnvelope{
				ChannelKey: key,
				Epoch:      state.Epoch,
				Generation: ch.gen,
				ReplicaID:  r.cfg.LocalNode,
			}
		}
		sendStartedAt := r.cfg.Now()
		err := r.sendEnvelope(env)
		if env.Kind == MessageKindFetchRequest {
			sendtrace.Record(sendtrace.Event{
				Stage:      sendtrace.StageRuntimeFetchRequestSend,
				At:         sendStartedAt,
				Duration:   sendtrace.Elapsed(sendStartedAt, r.cfg.Now()),
				NodeID:     uint64(r.cfg.LocalNode),
				PeerNodeID: uint64(peer),
				ChannelKey: string(key),
			})
		}
		if err == nil {
			r.clearReplicationRetry(key, peer)
			continue
		}
		if !errors.Is(err, ErrBackpressured) {
			failedPeers = append(failedPeers, peer)
			if r.markReplicationRetry(key, peer) {
				scheduleRetry = true
			}
		}
	}

	if len(failedPeers) == 0 {
		return
	}
	for _, peer := range failedPeers {
		ch.enqueueReplication(peer)
		r.scheduleFollowerReplication(key, peer)
	}
	ch.markReplication()
	if scheduleRetry {
		r.enqueueScheduler(key, PriorityNormal)
	}
}

func (r *runtime) processLeaderLongPoll(ch *channel, meta core.Meta) {
	r.markLeaderLaneReady(ch, laneReadyData)

	state := ch.Status()
	var failedPeers []core.NodeID
	scheduleRetry := false
	for {
		peer, ok := ch.popReplicationPeer()
		if !ok {
			break
		}
		if !r.shouldSendLeaderProbe(ch, meta, state, peer) {
			r.clearReplicationRetry(ch.key, peer)
			continue
		}
		env := Envelope{
			Peer:       peer,
			ChannelKey: ch.key,
			Epoch:      state.Epoch,
			Generation: ch.gen,
			RequestID:  r.requestID.Add(1),
			Kind:       MessageKindReconcileProbeRequest,
			ReconcileProbeRequest: &ReconcileProbeRequestEnvelope{
				ChannelKey: ch.key,
				Epoch:      state.Epoch,
				Generation: ch.gen,
				ReplicaID:  r.cfg.LocalNode,
			},
		}
		err := r.sendEnvelope(env)
		if err == nil {
			r.clearReplicationRetry(ch.key, peer)
			continue
		}
		if !errors.Is(err, ErrBackpressured) {
			failedPeers = append(failedPeers, peer)
			if r.markReplicationRetry(ch.key, peer) {
				scheduleRetry = true
			}
		}
	}

	if len(failedPeers) == 0 {
		return
	}
	for _, peer := range failedPeers {
		ch.enqueueReplication(peer)
		r.scheduleFollowerReplication(ch.key, peer)
	}
	ch.markReplication()
	if scheduleRetry {
		r.enqueueScheduler(ch.key, PriorityNormal)
	}
}

func (r *runtime) markLeaderLaneReady(ch *channel, ready laneReadyMask) {
	state := ch.Status()
	for _, target := range ch.replicationTargetsSnapshot() {
		session, ok := r.leaderLanes.Session(target)
		if !ok {
			continue
		}
		switch ready {
		case laneReadyHWOnly:
			session.MarkHWOnlyReady(ch.key, state.Epoch)
		default:
			session.MarkDataReady(ch.key, state.Epoch)
		}
	}
}

func (r *runtime) onChannelAppend(key core.ChannelKey) {
	r.onChannelReady(key, laneReadyData)
}

func (r *runtime) onChannelCommit(key core.ChannelKey) {
	r.onChannelReady(key, laneReadyHWOnly)
}

func (r *runtime) onChannelReady(key core.ChannelKey, ready laneReadyMask) {
	if !r.longPollEnabled() {
		return
	}
	ch, ok := r.lookupChannel(key)
	if !ok {
		return
	}
	meta := ch.metaSnapshot()
	if meta.Leader != r.cfg.LocalNode {
		return
	}
	r.markLeaderLaneReady(ch, ready)
	state := ch.Status()
	queuedWakeUp := false
	for _, target := range ch.replicationTargetsSnapshot() {
		if !r.shouldSendLeaderProbe(ch, meta, state, target.Peer) {
			continue
		}
		ch.enqueueReplication(target.Peer)
		queuedWakeUp = true
	}
	if queuedWakeUp {
		ch.markReplication()
		r.enqueueScheduler(key, PriorityNormal)
	}
}

func (r *runtime) processFollowerLongPoll(ch *channel, meta core.Meta) {
	for {
		peer, ok := ch.popReplicationPeer()
		if !ok {
			return
		}
		if peer == 0 || peer != meta.Leader {
			continue
		}
		manager := r.ensureLaneManager(peer)
		manager.MarkChannelPending(ch.key)
		laneID := manager.LaneFor(ch.key)
		r.scheduleLaneDispatch(peer, laneID)
	}
}

func (r *runtime) shouldSendLeaderProbe(ch *channel, meta core.Meta, state core.ReplicaState, peer core.NodeID) bool {
	if ch == nil {
		return false
	}
	if !r.isReplicationPeerValid(meta, peer) {
		return false
	}
	if r.peerRequests.hasChannelInflight(ch.key, peer) {
		return false
	}
	if !state.CommitReady {
		return true
	}
	return !r.leaderLaneTracksChannel(ch.key, state.Epoch, peer)
}

func (r *runtime) leaderLaneTracksChannel(key core.ChannelKey, epoch uint64, peer core.NodeID) bool {
	if !r.longPollEnabled() {
		return false
	}
	session, ok := r.leaderLanes.Session(PeerLaneKey{
		Peer:   peer,
		LaneID: laneIDFor(key, r.cfg.LongPollLaneCount),
	})
	if !ok {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	trackedEpoch, ok := session.channelEpoch[key]
	if !ok {
		return false
	}
	return trackedEpoch == 0 || epoch == 0 || trackedEpoch == epoch
}

func (r *runtime) scheduleFollowerReplication(key core.ChannelKey, leader core.NodeID) {
	if r.isClosed() {
		return
	}
	ch, ok := r.lookupChannel(key)
	if !ok {
		return
	}
	meta := ch.metaSnapshot()

	r.replicationRetryMu.Lock()
	defer r.replicationRetryMu.Unlock()
	if !r.isReplicationPeerValid(meta, leader) {
		return
	}

	state := r.replicationRetryStateLocked(key, leader)
	if state.timer != nil {
		return
	}
	state.timerVersion++
	version := state.timerVersion
	scheduledAt := r.cfg.Now()
	state.timer = time.AfterFunc(r.cfg.FollowerReplicationRetryInterval, func() {
		r.fireFollowerReplicationRetry(key, leader, version)
	})
	sendtrace.Record(sendtrace.Event{
		Stage:      sendtrace.StageRuntimeFollowerRetryScheduled,
		At:         scheduledAt,
		Duration:   r.cfg.FollowerReplicationRetryInterval,
		NodeID:     uint64(r.cfg.LocalNode),
		PeerNodeID: uint64(leader),
		ChannelKey: string(key),
	})
}

func (r *runtime) retryReplication(key core.ChannelKey, peer core.NodeID, schedule bool) {
	if r.isClosed() {
		return
	}
	ch, ok := r.lookupChannel(key)
	if !ok {
		return
	}
	ch.enqueueReplication(peer)
	ch.markReplication()
	if schedule && r.markReplicationRetry(key, peer) {
		r.enqueueScheduler(key, PriorityNormal)
	}
}

func (r *runtime) markReplicationRetry(key core.ChannelKey, peer core.NodeID) bool {
	r.replicationRetryMu.Lock()
	defer r.replicationRetryMu.Unlock()

	state := r.replicationRetryStateLocked(key, peer)
	if state.pending {
		return false
	}
	state.pending = true
	return true
}

func (r *runtime) clearReplicationRetry(key core.ChannelKey, peer core.NodeID) {
	r.replicationRetryMu.Lock()
	r.clearReplicationRetryLocked(key, peer)
	r.replicationRetryMu.Unlock()
}

func (r *runtime) clearStaleReplicationRetries(key core.ChannelKey, meta core.Meta) []*time.Timer {
	r.replicationRetryMu.Lock()
	defer r.replicationRetryMu.Unlock()

	peers, ok := r.replicationRetry[key]
	if !ok {
		return nil
	}

	timers := make([]*time.Timer, 0, len(peers))
	for peer, state := range peers {
		if r.isReplicationPeerValid(meta, peer) {
			continue
		}
		state.pending = false
		state.timerVersion++
		if state.timer != nil {
			timers = append(timers, state.timer)
			state.timer = nil
		}
		delete(peers, peer)
	}
	if len(peers) == 0 {
		delete(r.replicationRetry, key)
	}
	return timers
}

func (r *runtime) clearReplicationRetries(key core.ChannelKey, keepPeer core.NodeID, keepMatching bool) []*time.Timer {
	r.replicationRetryMu.Lock()
	defer r.replicationRetryMu.Unlock()

	peers, ok := r.replicationRetry[key]
	if !ok {
		return nil
	}

	timers := make([]*time.Timer, 0, len(peers))
	for peer, state := range peers {
		if keepMatching && peer == keepPeer {
			continue
		}
		state.pending = false
		state.timerVersion++
		if state.timer != nil {
			timers = append(timers, state.timer)
			state.timer = nil
		}
		delete(peers, peer)
	}
	if len(peers) == 0 {
		delete(r.replicationRetry, key)
	}
	return timers
}

func (r *runtime) clearAllReplicationRetries() []*time.Timer {
	r.replicationRetryMu.Lock()
	defer r.replicationRetryMu.Unlock()

	if len(r.replicationRetry) == 0 {
		return nil
	}
	var timers []*time.Timer
	for key, peers := range r.replicationRetry {
		for peer, state := range peers {
			state.pending = false
			state.timerVersion++
			if state.timer != nil {
				timers = append(timers, state.timer)
				state.timer = nil
			}
			delete(peers, peer)
		}
		delete(r.replicationRetry, key)
	}
	return timers
}

func (r *runtime) fireFollowerReplicationRetry(key core.ChannelKey, peer core.NodeID, version uint64) {
	if r.isClosed() {
		return
	}
	r.replicationRetryMu.Lock()
	peers, ok := r.replicationRetry[key]
	if !ok {
		r.replicationRetryMu.Unlock()
		return
	}
	state, ok := peers[peer]
	if !ok || state.timer == nil || state.timerVersion != version {
		r.replicationRetryMu.Unlock()
		return
	}
	state.timer = nil

	ch, ok := r.lookupChannel(key)
	if !ok {
		state.pending = false
		r.dropReplicationRetryStateLocked(key, peer)
		r.replicationRetryMu.Unlock()
		return
	}
	meta := ch.metaSnapshot()
	if !r.isReplicationPeerValid(meta, peer) {
		state.pending = false
		r.dropReplicationRetryStateLocked(key, peer)
		r.replicationRetryMu.Unlock()
		return
	}
	r.replicationRetryMu.Unlock()

	r.retryReplication(key, peer, false)
	r.enqueueScheduler(key, PriorityNormal)
}

func (r *runtime) replicationRetryStateLocked(key core.ChannelKey, peer core.NodeID) *replicationRetryState {
	peers, ok := r.replicationRetry[key]
	if !ok {
		peers = make(map[core.NodeID]*replicationRetryState)
		r.replicationRetry[key] = peers
	}
	state, ok := peers[peer]
	if !ok {
		state = &replicationRetryState{}
		peers[peer] = state
	}
	return state
}

func (r *runtime) clearReplicationRetryLocked(key core.ChannelKey, peer core.NodeID) {
	peers, ok := r.replicationRetry[key]
	if !ok {
		return
	}
	state, ok := peers[peer]
	if !ok {
		return
	}
	state.pending = false
	r.dropReplicationRetryStateLocked(key, peer)
}

func (r *runtime) dropReplicationRetryStateLocked(key core.ChannelKey, peer core.NodeID) {
	peers, ok := r.replicationRetry[key]
	if !ok {
		return
	}
	state, ok := peers[peer]
	if !ok || state.pending || state.timer != nil {
		return
	}
	delete(peers, peer)
	if len(peers) == 0 {
		delete(r.replicationRetry, key)
	}
}

func stopTimers(timers []*time.Timer) {
	for _, timer := range timers {
		if timer != nil {
			timer.Stop()
		}
	}
}

func (r *runtime) isReplicationPeerValid(meta core.Meta, peer core.NodeID) bool {
	if peer == 0 || peer == r.cfg.LocalNode {
		return false
	}
	if meta.Leader == r.cfg.LocalNode {
		return slices.Contains(meta.Replicas, peer)
	}
	return meta.Leader == peer
}
