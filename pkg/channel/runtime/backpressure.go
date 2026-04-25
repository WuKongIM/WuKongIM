package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type peerRequestState struct {
	mu       sync.Mutex
	inflight map[core.NodeID]int
	groups   map[channelPeerKey]inflightReservation
	queued   map[core.NodeID]*peerEnvelopeQueue
}

type backpressureRetryState struct {
	timer        *time.Timer
	timerVersion uint64
}

type channelPeerKey struct {
	channelKey core.ChannelKey
	peer       core.NodeID
}

type inflightReservation struct {
	generation uint64
	requestID  uint64
}

type peerEnvelopeQueue struct {
	items []Envelope
	head  int
}

type deferredEnvelope struct {
	env     Envelope
	onError func(error)
}

func newPeerRequestState() peerRequestState {
	return peerRequestState{
		inflight: make(map[core.NodeID]int),
		groups:   make(map[channelPeerKey]inflightReservation),
		queued:   make(map[core.NodeID]*peerEnvelopeQueue),
	}
}

func (s *peerRequestState) tryAcquire(peer core.NodeID, limit int) bool {
	if limit <= 0 {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[peer] >= limit {
		return false
	}
	s.inflight[peer]++
	return true
}

func (s *peerRequestState) tryAcquireChannel(env Envelope) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	channelPeer := channelPeerKey{channelKey: env.ChannelKey, peer: env.Peer}
	if _, ok := s.groups[channelPeer]; ok {
		return false
	}
	s.groups[channelPeer] = inflightReservation{
		generation: env.Generation,
		requestID:  env.RequestID,
	}
	return true
}

func (s *peerRequestState) enqueue(env Envelope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queueLocked(env.Peer)
	q.enqueue(env)
}

func (s *peerRequestState) queuedCount(peer core.NodeID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queued[peer]
	if q == nil {
		return 0
	}
	return q.len()
}

func (s *peerRequestState) release(peer core.NodeID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[peer] > 0 {
		s.inflight[peer]--
	}
}

func (s *peerRequestState) releaseChannel(key core.ChannelKey, peer core.NodeID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.groups, channelPeerKey{channelKey: key, peer: peer})
}

func (s *peerRequestState) hasChannelInflight(key core.ChannelKey, peer core.NodeID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.groups[channelPeerKey{channelKey: key, peer: peer}]
	return ok
}

func (s *peerRequestState) clearChannel(key core.ChannelKey) []core.NodeID {
	s.mu.Lock()
	defer s.mu.Unlock()

	affected := make(map[core.NodeID]struct{})
	for channelPeer := range s.groups {
		if channelPeer.channelKey != key {
			continue
		}
		delete(s.groups, channelPeer)
		if s.inflight[channelPeer.peer] > 0 {
			s.inflight[channelPeer.peer]--
		}
		affected[channelPeer.peer] = struct{}{}
	}
	for peer, queue := range s.queued {
		if queue == nil {
			continue
		}
		if queue.dropChannel(key) {
			affected[peer] = struct{}{}
		}
	}
	peers := make([]core.NodeID, 0, len(affected))
	for peer := range affected {
		peers = append(peers, peer)
	}
	return peers
}

func (s *peerRequestState) clearChannelInvalidPeers(key core.ChannelKey, allow func(core.NodeID) bool) []core.NodeID {
	s.mu.Lock()
	defer s.mu.Unlock()

	affected := make(map[core.NodeID]struct{})
	for channelPeer := range s.groups {
		if channelPeer.channelKey != key {
			continue
		}
		if allow != nil && allow(channelPeer.peer) {
			continue
		}
		delete(s.groups, channelPeer)
		if s.inflight[channelPeer.peer] > 0 {
			s.inflight[channelPeer.peer]--
		}
		affected[channelPeer.peer] = struct{}{}
	}
	for peer, queue := range s.queued {
		if queue == nil {
			continue
		}
		if allow != nil && allow(peer) {
			continue
		}
		if queue.dropChannel(key) {
			affected[peer] = struct{}{}
		}
	}
	peers := make([]core.NodeID, 0, len(affected))
	for peer := range affected {
		peers = append(peers, peer)
	}
	return peers
}

func (s *peerRequestState) clearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight = make(map[core.NodeID]int)
	s.groups = make(map[channelPeerKey]inflightReservation)
	s.queued = make(map[core.NodeID]*peerEnvelopeQueue)
}

func (s *peerRequestState) releaseInflightForEnvelope(env Envelope) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := channelPeerKey{channelKey: env.ChannelKey, peer: env.Peer}
	reservation, ok := s.groups[key]
	if !ok {
		return false
	}
	if reservation.generation != 0 && env.Generation != 0 && reservation.generation != env.Generation {
		return false
	}
	if reservation.requestID != env.RequestID {
		return false
	}
	delete(s.groups, key)
	if s.inflight[env.Peer] > 0 {
		s.inflight[env.Peer]--
	}
	return true
}

func (s *peerRequestState) popQueued(peer core.NodeID) (Envelope, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queued[peer]
	if q == nil {
		return Envelope{}, false
	}
	return q.pop()
}

func (s *peerRequestState) queueLocked(peer core.NodeID) *peerEnvelopeQueue {
	if q, ok := s.queued[peer]; ok {
		return q
	}
	q := &peerEnvelopeQueue{}
	s.queued[peer] = q
	return q
}

func (q *peerEnvelopeQueue) enqueue(env Envelope) {
	if env.Kind == MessageKindFetchRequest || env.Kind == MessageKindReconcileProbeRequest {
		for i := q.head; i < len(q.items); i++ {
			if q.items[i].Kind == env.Kind && q.items[i].ChannelKey == env.ChannelKey {
				q.items[i] = env
				return
			}
		}
	}
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	} else if q.head > 0 && len(q.items) == cap(q.items) {
		q.compact()
	}
	q.items = append(q.items, env)
}

func (q *peerEnvelopeQueue) pop() (Envelope, bool) {
	if q.head >= len(q.items) {
		return Envelope{}, false
	}

	env := q.items[q.head]
	q.items[q.head] = Envelope{}
	q.head++
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	}
	return env, true
}

func (q *peerEnvelopeQueue) len() int {
	return len(q.items) - q.head
}

func (q *peerEnvelopeQueue) dropChannel(key core.ChannelKey) bool {
	if q.head >= len(q.items) {
		return false
	}

	write := 0
	removed := false
	for i := q.head; i < len(q.items); i++ {
		env := q.items[i]
		if env.ChannelKey == key {
			removed = true
			continue
		}
		q.items[write] = env
		write++
	}
	for i := write; i < len(q.items); i++ {
		q.items[i] = Envelope{}
	}
	q.items = q.items[:write]
	q.head = 0
	return removed
}

func (q *peerEnvelopeQueue) compact() {
	n := copy(q.items, q.items[q.head:])
	for i := n; i < len(q.items); i++ {
		q.items[i] = Envelope{}
	}
	q.items = q.items[:n]
	q.head = 0
}

func (r *runtime) sendEnvelope(env Envelope) error {
	if r.beforePeerSessionHook != nil {
		r.beforePeerSessionHook(env)
	}
	r.sendCoordMu.Lock()
	r.sendCoordActive.Add(1)
	defer func() {
		r.sendCoordActive.Add(-1)
		r.sendCoordMu.Unlock()
	}()

	err := r.sendEnvelopeLocked(env)
	r.drainDeferredSyncWorkLocked()
	if err != nil {
		return err
	}
	return nil
}

func (r *runtime) sendEnvelopeLocked(env Envelope) error {
	if r.isClosed() {
		return ErrChannelNotFound
	}
	if r.shouldDropOutboundEnvelope(env) {
		if env.Kind == MessageKindFetchRequest || env.Kind == MessageKindReconcileProbeRequest {
			r.clearReplicationRetry(env.ChannelKey, env.Peer)
		}
		return nil
	}
	env = r.refreshFetchEnvelope(env)
	if r.shouldDropOutboundEnvelope(env) {
		if env.Kind == MessageKindFetchRequest || env.Kind == MessageKindReconcileProbeRequest {
			r.clearReplicationRetry(env.ChannelKey, env.Peer)
		}
		return nil
	}
	if r.afterOutboundValidationHook != nil {
		r.afterOutboundValidationHook(env)
	}
	trackInflight := env.Kind == MessageKindFetchRequest || env.Kind == MessageKindReconcileProbeRequest

	session := r.peerSession(env.Peer)
	if state := session.Backpressure(); state.Level == BackpressureHard {
		r.peerRequests.enqueue(env)
		r.scheduleBackpressureRetry(env.Peer)
		return ErrBackpressured
	}

	if trackInflight && !r.peerRequests.tryAcquireChannel(env) {
		r.peerRequests.enqueue(env)
		return ErrBackpressured
	}
	if trackInflight && !r.peerRequests.tryAcquire(env.Peer, r.cfg.Limits.MaxFetchInflightPeer) {
		r.peerRequests.releaseChannel(env.ChannelKey, env.Peer)
		r.peerRequests.enqueue(env)
		return ErrBackpressured
	}

	if env.Kind == MessageKindLanePollRequest {
		// Long-poll RPCs can legitimately park for maxWait, so don't keep
		// global send coordination locked across the blocking session.Send.
		r.sendCoordMu.Unlock()
		err := session.Send(env)
		r.sendCoordMu.Lock()
		return err
	}
	if err := session.Send(env); err != nil {
		if trackInflight {
			r.peerRequests.release(env.Peer)
			r.peerRequests.releaseChannel(env.ChannelKey, env.Peer)
		}
		return err
	}
	return nil
}

func (r *runtime) beginSyncDelivery() {
	r.syncDeliveryMu.Lock()
	r.syncDeliveryDepth++
	r.syncDeliveryMu.Unlock()
}

func (r *runtime) endSyncDelivery() {
	r.syncDeliveryMu.Lock()
	if r.syncDeliveryDepth > 0 {
		r.syncDeliveryDepth--
	}
	r.syncDeliveryMu.Unlock()
}

func (r *runtime) syncDeliveryActive() bool {
	r.syncDeliveryMu.Lock()
	defer r.syncDeliveryMu.Unlock()
	return r.syncDeliveryDepth > 0
}

func (r *runtime) deferSyncEnvelope(env Envelope, onError func(error)) {
	r.syncDeliveryMu.Lock()
	r.syncDeferredSends = append(r.syncDeferredSends, deferredEnvelope{
		env:     env,
		onError: onError,
	})
	r.syncDeliveryMu.Unlock()
}

func (r *runtime) deferPeerDrain(peer core.NodeID) {
	r.syncDeliveryMu.Lock()
	if r.syncDeferredPeerDrains == nil {
		r.syncDeferredPeerDrains = make(map[core.NodeID]struct{})
	}
	r.syncDeferredPeerDrains[peer] = struct{}{}
	r.syncDeliveryMu.Unlock()
}

func (r *runtime) popDeferredSyncWork() (deferredEnvelope, core.NodeID, bool) {
	r.syncDeliveryMu.Lock()
	defer r.syncDeliveryMu.Unlock()

	for i, next := range r.syncDeferredSends {
		if next.env.Kind == MessageKindFetchRequest || next.env.Kind == MessageKindReconcileProbeRequest {
			continue
		}
		copy(r.syncDeferredSends[i:], r.syncDeferredSends[i+1:])
		r.syncDeferredSends = r.syncDeferredSends[:len(r.syncDeferredSends)-1]
		return next, 0, true
	}
	for peer := range r.syncDeferredPeerDrains {
		delete(r.syncDeferredPeerDrains, peer)
		return deferredEnvelope{}, peer, true
	}
	if len(r.syncDeferredSends) > 0 {
		next := r.syncDeferredSends[0]
		copy(r.syncDeferredSends, r.syncDeferredSends[1:])
		r.syncDeferredSends = r.syncDeferredSends[:len(r.syncDeferredSends)-1]
		return next, 0, true
	}
	return deferredEnvelope{}, 0, false
}

func (r *runtime) drainDeferredSyncWorkLocked() {
	for {
		next, peer, ok := r.popDeferredSyncWork()
		if !ok {
			return
		}
		if next.env.Kind != 0 {
			if r.beforePeerSessionHook != nil {
				r.beforePeerSessionHook(next.env)
			}
			err := r.sendEnvelopeLocked(next.env)
			if next.onError != nil {
				next.onError(err)
			}
			continue
		}
		r.drainPeerQueueLocked(peer)
	}
}

func (r *runtime) hasDeferredSyncWork() bool {
	r.syncDeliveryMu.Lock()
	defer r.syncDeliveryMu.Unlock()

	return len(r.syncDeferredSends) > 0 || len(r.syncDeferredPeerDrains) > 0
}

func (r *runtime) drainDeferredSyncWorkIfIdle() {
	if r.sendCoordActive.Load() > 0 || !r.hasDeferredSyncWork() {
		return
	}

	r.sendCoordMu.Lock()
	r.sendCoordActive.Add(1)
	defer func() {
		r.sendCoordActive.Add(-1)
		r.sendCoordMu.Unlock()
	}()
	r.drainDeferredSyncWorkLocked()
}

func (r *runtime) sendOrDeferEnvelope(env Envelope, onError func(error)) {
	if r.syncDeliveryActive() {
		r.deferSyncEnvelope(env, onError)
		return
	}
	err := r.sendEnvelope(env)
	if onError != nil {
		onError(err)
	}
}

func (r *runtime) shouldDropOutboundEnvelope(env Envelope) bool {
	ch, ok := r.lookupChannel(env.ChannelKey)
	if !ok {
		return true
	}
	if env.Generation != 0 && ch.gen != env.Generation {
		return true
	}
	switch env.Kind {
	case MessageKindFetchRequest, MessageKindReconcileProbeRequest, MessageKindLanePollRequest:
		return !r.isReplicationPeerValid(ch.metaSnapshot(), env.Peer)
	}
	return false
}

func (r *runtime) refreshFetchEnvelope(env Envelope) Envelope {
	if env.Kind != MessageKindFetchRequest || env.FetchRequest == nil {
		return env
	}

	ch, ok := r.lookupChannel(env.ChannelKey)
	if !ok {
		return env
	}

	state := ch.Status()
	req := *env.FetchRequest
	req.ChannelKey = env.ChannelKey
	req.Epoch = state.Epoch
	req.Generation = ch.gen
	req.ReplicaID = r.cfg.LocalNode
	req.FetchOffset = state.LEO
	req.OffsetEpoch = state.OffsetEpoch
	if req.MaxBytes <= 0 {
		req.MaxBytes = defaultFetchMaxBytes
	}
	env.Epoch = state.Epoch
	env.Generation = ch.gen
	env.FetchRequest = &req
	return env
}

func (r *runtime) queuedPeerRequests(peer core.NodeID) int {
	return r.peerRequests.queuedCount(peer)
}

func (r *runtime) releasePeerInflight(peer core.NodeID) {
	r.peerRequests.release(peer)
}

func (r *runtime) releaseChannelInflight(key core.ChannelKey, peer core.NodeID) {
	r.peerRequests.releaseChannel(key, peer)
}

func (r *runtime) drainPeerQueue(peer core.NodeID) {
	if r.isClosed() {
		return
	}
	r.sendCoordMu.Lock()
	defer r.sendCoordMu.Unlock()
	r.drainPeerQueueLocked(peer)
}

func (r *runtime) drainPeerQueueLocked(peer core.NodeID) {
	env, ok := r.peerRequests.popQueued(peer)
	if !ok {
		r.clearBackpressureRetry(peer)
		return
	}
	if r.beforePeerSessionHook != nil {
		r.beforePeerSessionHook(env)
	}
	if err := r.sendEnvelopeLocked(env); err != nil && !errors.Is(err, ErrBackpressured) {
		r.retryReplication(env.ChannelKey, env.Peer, true)
		return
	}
	if env.Kind == MessageKindFetchRequest || env.Kind == MessageKindReconcileProbeRequest {
		r.clearReplicationRetry(env.ChannelKey, env.Peer)
	}
}

func (r *runtime) releaseInflightForEnvelope(env Envelope) bool {
	return r.peerRequests.releaseInflightForEnvelope(env)
}

func (r *runtime) handleEnvelope(env Envelope) {
	if r.isClosed() {
		return
	}
	if env.Sync {
		r.beginSyncDelivery()
		defer func() {
			r.endSyncDelivery()
			r.drainDeferredSyncWorkIfIdle()
		}()
	}
	var (
		ch        *channel
		knownDrop bool
	)
	if env.Kind == MessageKindLanePollResponse {
		if env.LanePollResponse == nil {
			return
		}
		r.handleLanePollResponse(env.Peer, *env.LanePollResponse)
		return
	}

	active, ok := r.lookupChannel(env.ChannelKey)
	if ok && active.gen == env.Generation {
		ch = active
	} else if (env.Kind == MessageKindFetchResponse || env.Kind == MessageKindReconcileProbeResponse) && r.tombstones.contains(env.ChannelKey, env.Generation) {
		knownDrop = true
	}

	if (env.Kind == MessageKindFetchResponse || env.Kind == MessageKindReconcileProbeResponse) && knownDrop {
		if r.releaseInflightForEnvelope(env) {
			r.drainPeerQueueOrDefer(env.Peer)
		}
		return
	}
	if env.Kind == MessageKindFetchFailure {
		if r.releaseInflightForEnvelope(env) {
			if ch != nil {
				r.retryReplication(env.ChannelKey, env.Peer, true)
				r.scheduleFollowerReplication(env.ChannelKey, env.Peer)
			}
			r.drainPeerQueueOrDefer(env.Peer)
		}
		return
	}

	if ch == nil {
		if env.Kind == MessageKindReconcileProbeResponse {
			if r.releaseInflightForEnvelope(env) {
				r.drainPeerQueueOrDefer(env.Peer)
			}
		}
		return
	}

	if env.Kind == MessageKindFetchResponse || env.Kind == MessageKindReconcileProbeResponse {
		if r.deliverEnvelope(ch, env) {
			if r.releaseInflightForEnvelope(env) {
				r.drainPeerQueueOrDefer(env.Peer)
			}
		}
		return
	}
	_ = r.deliverEnvelope(ch, env)
}

func (r *runtime) drainPeerQueueOrDefer(peer core.NodeID) {
	if r.syncDeliveryActive() {
		r.deferPeerDrain(peer)
		return
	}
	r.drainPeerQueue(peer)
}

func (r *runtime) scheduleBackpressureRetry(peer core.NodeID) {
	if r.isClosed() {
		return
	}
	interval := r.cfg.FollowerReplicationRetryInterval
	if interval <= 0 {
		interval = defaultFollowerReplicationRetryDelay
	}

	r.backpressureMu.Lock()
	defer r.backpressureMu.Unlock()

	state, ok := r.backpressureRetry[peer]
	if !ok {
		state = &backpressureRetryState{}
		r.backpressureRetry[peer] = state
	}
	if state.timer != nil {
		return
	}
	state.timerVersion++
	version := state.timerVersion
	state.timer = time.AfterFunc(interval, func() {
		r.fireBackpressureRetry(peer, version)
	})
}

func (r *runtime) fireBackpressureRetry(peer core.NodeID, version uint64) {
	if r.isClosed() {
		return
	}

	r.backpressureMu.Lock()
	state, ok := r.backpressureRetry[peer]
	if !ok || state.timer == nil || state.timerVersion != version {
		r.backpressureMu.Unlock()
		return
	}
	state.timer = nil
	r.backpressureMu.Unlock()

	if r.queuedPeerRequests(peer) == 0 {
		r.clearBackpressureRetry(peer)
		return
	}

	r.drainPeerQueue(peer)

	if r.queuedPeerRequests(peer) > 0 {
		r.scheduleBackpressureRetry(peer)
		return
	}
	r.clearBackpressureRetry(peer)
}

func (r *runtime) clearBackpressureRetry(peer core.NodeID) {
	r.backpressureMu.Lock()
	state, ok := r.backpressureRetry[peer]
	if !ok {
		r.backpressureMu.Unlock()
		return
	}
	var timer *time.Timer
	if state.timer != nil {
		timer = state.timer
		state.timer = nil
	}
	delete(r.backpressureRetry, peer)
	r.backpressureMu.Unlock()

	if timer != nil {
		timer.Stop()
	}
}

func (r *runtime) clearAllBackpressureRetries() []*time.Timer {
	r.backpressureMu.Lock()
	defer r.backpressureMu.Unlock()

	if len(r.backpressureRetry) == 0 {
		return nil
	}
	timers := make([]*time.Timer, 0, len(r.backpressureRetry))
	for peer, state := range r.backpressureRetry {
		if state.timer != nil {
			timers = append(timers, state.timer)
			state.timer = nil
		}
		delete(r.backpressureRetry, peer)
	}
	return timers
}

func (r *runtime) deliverEnvelope(ch *channel, env Envelope) bool {
	switch env.Kind {
	case MessageKindFetchResponse:
		state := ch.Status()
		if env.Epoch != state.Epoch {
			return true
		}
		if env.FetchResponse == nil {
			return false
		}
		return r.applyFetchResponseEnvelope(ch, env.Peer, *env.FetchResponse) == nil
	case MessageKindReconcileProbeResponse:
		state := ch.Status()
		if env.Epoch != state.Epoch {
			return true
		}
		if env.ReconcileProbeResponse == nil {
			return false
		}
		return r.applyReconcileProbeResponseEnvelope(ch, *env.ReconcileProbeResponse) == nil
	}
	return true
}

func (r *runtime) handleLanePollResponse(peer core.NodeID, resp LanePollResponseEnvelope) {
	manager, ok := r.laneManager(peer)
	if !ok {
		r.cfg.Logger.Warn("lane poll response dropped, no lane manager",
			wklog.Event("repl.diag.lane_resp_no_manager"),
			wklog.Uint64("peer", uint64(peer)),
			wklog.Int("laneID", int(resp.LaneID)),
			wklog.Int("items", len(resp.Items)),
		)
		return
	}
	// r.cfg.Logger.Debug("follower received lane poll response",
	// 	wklog.Event("repl.diag.lane_resp_received"),
	// 	wklog.Uint64("peer", uint64(peer)),
	// 	wklog.Int("laneID", int(resp.LaneID)),
	// 	wklog.Int("items", len(resp.Items)),
	// 	wklog.Bool("timedOut", resp.TimedOut),
	// 	wklog.String("status", string(resp.Status)),
	// )
	reissue := manager.ApplyResponse(resp)
	for _, item := range resp.Items {
		ch, ok := r.lookupChannel(item.ChannelKey)
		if !ok {
			r.cfg.Logger.Warn("lane poll response item skipped, channel not found",
				wklog.Event("repl.diag.lane_resp_item_no_channel"),
				wklog.String("channelKey", string(item.ChannelKey)),
				wklog.Uint64("peer", uint64(peer)),
			)
			continue
		}
		fetchResp := FetchResponseEnvelope{
			ChannelKey: item.ChannelKey,
			Epoch:      item.ChannelEpoch,
			Generation: ch.gen,
			LeaderHW:   item.LeaderHW,
			Records:    item.Records,
			TruncateTo: item.TruncateTo,
		}
		_ = r.applyFetchResponseEnvelope(ch, peer, fetchResp)
	}
	if !reissue {
		return
	}
	r.reissueLanePoll(peer, resp.LaneID)
}

func (r *runtime) reissueLanePoll(peer core.NodeID, laneID uint16) {
	r.scheduleLaneDispatch(peer, laneID)
}

func (r *runtime) applyFetchResponseEnvelope(ch *channel, peer core.NodeID, env FetchResponseEnvelope) error {
	if err := ch.replica.ApplyFetch(context.Background(), core.ReplicaApplyFetchRequest{
		ChannelKey: env.ChannelKey,
		Epoch:      env.Epoch,
		Leader:     peer,
		TruncateTo: env.TruncateTo,
		Records:    env.Records,
		LeaderHW:   env.LeaderHW,
	}); err != nil {
		return err
	}

	meta := ch.metaSnapshot()
	if meta.Leader != r.cfg.LocalNode {
		state := ch.Status()
		shouldReportProgress := len(env.Records) > 0 || env.TruncateTo != nil || state.LEO > env.LeaderHW
		if r.longPollEnabled() {
			if shouldReportProgress {
				r.ensureLaneManager(meta.Leader).MarkCursorDelta(LaneCursorDelta{
					ChannelKey:   ch.key,
					ChannelEpoch: state.Epoch,
					MatchOffset:  state.LEO,
					OffsetEpoch:  state.OffsetEpoch,
				})
			}
			return nil
		}
		if len(env.Records) == 0 && env.TruncateTo == nil {
			r.retryReplication(ch.key, meta.Leader, true)
			return nil
		}
		state = ch.Status()
		r.sendOrDeferEnvelope(Envelope{
			Peer:       meta.Leader,
			ChannelKey: ch.key,
			Epoch:      meta.Epoch,
			Generation: ch.gen,
			RequestID:  r.requestID.Add(1),
			Kind:       MessageKindFetchRequest,
			FetchRequest: &FetchRequestEnvelope{
				ChannelKey:  ch.key,
				Epoch:       meta.Epoch,
				Generation:  ch.gen,
				ReplicaID:   r.cfg.LocalNode,
				FetchOffset: state.LEO,
				OffsetEpoch: state.OffsetEpoch,
				MaxBytes:    defaultFetchMaxBytes,
			},
		}, func(err error) {
			if err != nil && !errors.Is(err, ErrBackpressured) {
				r.retryReplication(ch.key, meta.Leader, true)
			}
		})
	}
	return nil
}

func (r *runtime) applyReconcileProbeResponseEnvelope(ch *channel, env ReconcileProbeResponseEnvelope) error {
	return ch.replica.ApplyReconcileProof(context.Background(), core.ReplicaReconcileProof{
		ChannelKey:   env.ChannelKey,
		Epoch:        env.Epoch,
		ReplicaID:    env.ReplicaID,
		OffsetEpoch:  env.OffsetEpoch,
		LogEndOffset: env.LogEndOffset,
		CheckpointHW: env.CheckpointHW,
	})
}

func (r *runtime) ServeFetch(ctx context.Context, req FetchRequestEnvelope) (FetchResponseEnvelope, error) {
	ch, _, err := r.ensureChannelForIngress(ctx, req.ChannelKey, ActivationSourceFetch)
	if err != nil {
		return FetchResponseEnvelope{}, err
	}
	if ch.gen != req.Generation {
		return FetchResponseEnvelope{}, ErrGenerationMismatch
	}

	meta := ch.metaSnapshot()
	if req.Epoch != meta.Epoch {
		return FetchResponseEnvelope{}, core.ErrStaleMeta
	}

	fetchReq := core.ReplicaFetchRequest{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		ReplicaID:   req.ReplicaID,
		FetchOffset: req.FetchOffset,
		OffsetEpoch: req.OffsetEpoch,
		MaxBytes:    req.MaxBytes,
	}
	for {
		version := ch.replicaChangeVersion()
		result, err := ch.replica.Fetch(ctx, fetchReq)
		if err != nil {
			return FetchResponseEnvelope{}, err
		}
		resp := FetchResponseEnvelope{
			ChannelKey: req.ChannelKey,
			Epoch:      result.Epoch,
			Generation: req.Generation,
			TruncateTo: result.TruncateTo,
			LeaderHW:   result.HW,
			Records:    result.Records,
		}
		if !shouldLongPollFetchResponse(resp) || !FetchLongPollEnabled(ctx) {
			return resp, nil
		}

		waitCtx, cancel := longPollFetchContext(ctx, r.cfg.FollowerReplicationRetryInterval)
		changed := ch.waitReplicaChange(waitCtx, version)
		cancel()
		if !changed {
			return resp, nil
		}
	}
}

func shouldLongPollFetchResponse(resp FetchResponseEnvelope) bool {
	return resp.TruncateTo == nil && len(resp.Records) == 0
}

func longPollFetchContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

func (r *runtime) ServeReconcileProbe(ctx context.Context, req ReconcileProbeRequestEnvelope) (ReconcileProbeResponseEnvelope, error) {
	ch, _, err := r.ensureChannelForIngress(ctx, req.ChannelKey, ActivationSourceProbe)
	if err != nil {
		return ReconcileProbeResponseEnvelope{}, err
	}
	if req.Generation != 0 && ch.gen != req.Generation {
		return ReconcileProbeResponseEnvelope{}, ErrGenerationMismatch
	}

	meta := ch.metaSnapshot()
	if req.Epoch != meta.Epoch {
		return ReconcileProbeResponseEnvelope{}, core.ErrStaleMeta
	}

	state := ch.Status()
	return ReconcileProbeResponseEnvelope{
		ChannelKey:   req.ChannelKey,
		Epoch:        state.Epoch,
		Generation:   ch.gen,
		ReplicaID:    r.cfg.LocalNode,
		OffsetEpoch:  state.OffsetEpoch,
		LogEndOffset: state.LEO,
		CheckpointHW: state.CheckpointHW,
	}, nil
}

type nopTransport struct {
	mu      sync.Mutex
	handler func(Envelope)
}

func (t *nopTransport) Send(core.NodeID, Envelope) error {
	return nil
}

func (t *nopTransport) RegisterHandler(fn func(Envelope)) {
	t.mu.Lock()
	t.handler = fn
	t.mu.Unlock()
}

type nopPeerSessionManager struct{}

func (nopPeerSessionManager) Session(core.NodeID) PeerSession {
	return nopPeerSession{}
}

type nopPeerSession struct{}

func (nopPeerSession) Send(Envelope) error             { return nil }
func (nopPeerSession) Backpressure() BackpressureState { return BackpressureState{} }
func (nopPeerSession) Close() error                    { return nil }
