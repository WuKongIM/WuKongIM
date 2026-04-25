package runtime

import (
	"hash/fnv"
	"sort"
	"sync"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const lanePollProtocolVersion = 1

type PeerLaneManagerConfig struct {
	Peer      core.NodeID
	LaneCount int
	MaxWait   time.Duration
	Budget    LanePollBudget
}

type followerLaneState struct {
	sessionID      uint64
	sessionEpoch   uint64
	membership     map[core.ChannelKey]LaneMembership
	membershipVer  uint64
	inflightVer    uint64
	dirtyCursor    map[core.ChannelKey]LaneCursorDelta
	inflightCursor []core.ChannelKey
	pending        bool
	needOpen       bool
	inflight       bool
}

type PeerLaneManager struct {
	mu        sync.Mutex
	peer      core.NodeID
	laneCount uint16
	maxWait   time.Duration
	budget    LanePollBudget
	lanes     []followerLaneState
}

func newPeerLaneManager(cfg PeerLaneManagerConfig) *PeerLaneManager {
	if cfg.LaneCount <= 0 {
		cfg.LaneCount = 1
	}
	if cfg.Budget.MaxChannels <= 0 {
		cfg.Budget.MaxChannels = 1
	}
	mgr := &PeerLaneManager{
		peer:      cfg.Peer,
		laneCount: uint16(cfg.LaneCount),
		maxWait:   cfg.MaxWait,
		budget:    cfg.Budget,
		lanes:     make([]followerLaneState, cfg.LaneCount),
	}
	for i := range mgr.lanes {
		mgr.lanes[i].membership = make(map[core.ChannelKey]LaneMembership)
		mgr.lanes[i].dirtyCursor = make(map[core.ChannelKey]LaneCursorDelta)
	}
	return mgr
}

func (m *PeerLaneManager) LaneFor(key core.ChannelKey) uint16 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return uint16(hasher.Sum32() % uint32(m.laneCount))
}

func (m *PeerLaneManager) UpsertChannel(key core.ChannelKey, epoch uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	laneID := m.LaneFor(key)
	lane := &m.lanes[laneID]
	member := LaneMembership{ChannelKey: key, ChannelEpoch: epoch}
	current, ok := lane.membership[key]
	if ok && current == member {
		return false
	}
	lane.membership[key] = member
	delete(lane.dirtyCursor, key)
	lane.membershipVer++
	lane.pending = true
	lane.needOpen = true
	return true
}

func (m *PeerLaneManager) RemoveChannel(key core.ChannelKey) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	laneID := m.LaneFor(key)
	lane := &m.lanes[laneID]
	if _, ok := lane.membership[key]; !ok {
		return false
	}
	delete(lane.membership, key)
	delete(lane.dirtyCursor, key)
	lane.membershipVer++
	lane.pending = len(lane.membership) > 0
	lane.needOpen = len(lane.membership) > 0
	return true
}

func (m *PeerLaneManager) MarkChannelPending(key core.ChannelKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	laneID := m.LaneFor(key)
	lane := &m.lanes[laneID]
	if len(lane.membership) == 0 {
		return
	}
	lane.pending = true
}

func (m *PeerLaneManager) MarkCursorDelta(delta LaneCursorDelta) {
	m.mu.Lock()
	defer m.mu.Unlock()

	laneID := m.LaneFor(delta.ChannelKey)
	lane := &m.lanes[laneID]
	member, ok := lane.membership[delta.ChannelKey]
	if !ok {
		return
	}
	if delta.ChannelEpoch != 0 && member.ChannelEpoch != 0 && delta.ChannelEpoch != member.ChannelEpoch {
		return
	}
	lane.dirtyCursor[delta.ChannelKey] = delta
	lane.pending = true
}

func (m *PeerLaneManager) NextRequest(laneID uint16) (LanePollRequestEnvelope, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if int(laneID) >= len(m.lanes) {
		return LanePollRequestEnvelope{}, false
	}
	lane := &m.lanes[laneID]
	if lane.inflight || !lane.pending || len(lane.membership) == 0 {
		return LanePollRequestEnvelope{}, false
	}

	req := LanePollRequestEnvelope{
		LaneID:          laneID,
		LaneCount:       m.laneCount,
		SessionID:       lane.sessionID,
		SessionEpoch:    lane.sessionEpoch,
		ProtocolVersion: lanePollProtocolVersion,
		MaxWait:         m.maxWait,
		MaxBytes:        m.budget.MaxBytes,
		MaxChannels:     m.budget.MaxChannels,
	}
	if lane.needOpen || lane.sessionID == 0 {
		req.Op = LanePollOpOpen
		req.FullMembership = sortedLaneMembership(lane.membership)
	} else {
		req.Op = LanePollOpPoll
		req.MembershipVersionHint = lane.membershipVer
	}
	req.CursorDelta, lane.inflightCursor = sortedLaneCursorDelta(lane.dirtyCursor)
	lane.inflightVer = lane.membershipVer
	lane.inflight = true
	lane.pending = false
	return req, true
}

func (m *PeerLaneManager) ApplyResponse(resp LanePollResponseEnvelope) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if int(resp.LaneID) >= len(m.lanes) {
		return false
	}
	lane := &m.lanes[resp.LaneID]
	lane.inflight = false
	for _, key := range lane.inflightCursor {
		delete(lane.dirtyCursor, key)
	}
	lane.inflightCursor = nil

	switch resp.Status {
	case LanePollStatusNeedReset:
		lane.sessionID = 0
		lane.sessionEpoch = 0
		lane.needOpen = true
		lane.pending = len(lane.membership) > 0
		return lane.pending
	case LanePollStatusOK:
		if resp.SessionID != 0 {
			lane.sessionID = resp.SessionID
		}
		if resp.SessionEpoch != 0 {
			lane.sessionEpoch = resp.SessionEpoch
		}
		if lane.membershipVer == lane.inflightVer {
			lane.needOpen = false
		}
		lane.pending = len(lane.membership) > 0
		return lane.pending
	default:
		lane.pending = false
		return false
	}
}

func (m *PeerLaneManager) SendFailed(laneID uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if int(laneID) >= len(m.lanes) {
		return
	}
	lane := &m.lanes[laneID]
	lane.inflight = false
	lane.pending = len(lane.membership) > 0
}

func (m *PeerLaneManager) AnyChannel(laneID uint16) (core.ChannelKey, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if int(laneID) >= len(m.lanes) {
		return "", false
	}
	for key := range m.lanes[laneID].membership {
		return key, true
	}
	return "", false
}

func (m *PeerLaneManager) Empty() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, lane := range m.lanes {
		if len(lane.membership) > 0 {
			return false
		}
	}
	return true
}

func sortedLaneMembership(members map[core.ChannelKey]LaneMembership) []LaneMembership {
	if len(members) == 0 {
		return nil
	}
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	out := make([]LaneMembership, 0, len(keys))
	for _, key := range keys {
		out = append(out, members[core.ChannelKey(key)])
	}
	return out
}

func sortedLaneCursorDelta(cursor map[core.ChannelKey]LaneCursorDelta) ([]LaneCursorDelta, []core.ChannelKey) {
	if len(cursor) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(cursor))
	for key := range cursor {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	out := make([]LaneCursorDelta, 0, len(keys))
	drained := make([]core.ChannelKey, 0, len(keys))
	for _, key := range keys {
		channelKey := core.ChannelKey(key)
		out = append(out, cursor[channelKey])
		drained = append(drained, channelKey)
	}
	return out, drained
}

func laneIDFor(key core.ChannelKey, laneCount int) uint16 {
	if laneCount <= 0 {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return uint16(hasher.Sum32() % uint32(laneCount))
}

func (r *runtime) longPollEnabled() bool {
	return r.cfg.LongPollLaneCount > 0
}

func (r *runtime) laneManager(peer core.NodeID) (*PeerLaneManager, bool) {
	r.laneMu.Lock()
	defer r.laneMu.Unlock()
	manager, ok := r.lanes[peer]
	return manager, ok
}

func (r *runtime) ensureLaneManager(peer core.NodeID) *PeerLaneManager {
	r.laneMu.Lock()
	defer r.laneMu.Unlock()

	if manager, ok := r.lanes[peer]; ok {
		return manager
	}
	manager := newPeerLaneManager(PeerLaneManagerConfig{
		Peer:      peer,
		LaneCount: r.cfg.LongPollLaneCount,
		MaxWait:   r.cfg.LongPollMaxWait,
		Budget: LanePollBudget{
			MaxBytes:    r.cfg.LongPollMaxBytes,
			MaxChannels: r.cfg.LongPollMaxChannels,
		},
	})
	r.lanes[peer] = manager
	return manager
}

func (r *runtime) deleteLaneManager(peer core.NodeID) {
	r.laneMu.Lock()
	delete(r.lanes, peer)
	r.laneMu.Unlock()
}

func (r *runtime) syncFollowerLaneMembership(previous *core.Meta, next core.Meta) {
	if !r.longPollEnabled() {
		return
	}
	if previous != nil &&
		previous.Leader != 0 &&
		previous.Leader != r.cfg.LocalNode &&
		next.Leader != 0 &&
		next.Leader == previous.Leader {
		manager := r.ensureLaneManager(next.Leader)
		laneID := manager.LaneFor(next.Key)
		if manager.UpsertChannel(next.Key, next.Epoch) {
			r.scheduleLaneDispatch(next.Leader, laneID)
		}
		return
	}
	if previous != nil && previous.Leader != 0 && previous.Leader != r.cfg.LocalNode {
		if manager, ok := r.laneManager(previous.Leader); ok {
			laneID := manager.LaneFor(previous.Key)
			if manager.RemoveChannel(previous.Key) {
				r.cfg.Logger.Warn("follower lane membership removed",
					wklog.Event("repl.diag.lane_membership_removed"),
					wklog.Uint64("prevLeader", uint64(previous.Leader)),
					wklog.Uint64("nextLeader", uint64(next.Leader)),
					wklog.String("channelKey", string(previous.Key)),
					wklog.Uint64("prevEpoch", previous.Epoch),
					wklog.Uint64("nextEpoch", next.Epoch),
				)
				if manager.Empty() {
					r.deleteLaneManager(previous.Leader)
				} else {
					r.scheduleLaneDispatch(previous.Leader, laneID)
				}
			}
		}
	}
	if next.Leader != 0 && next.Leader != r.cfg.LocalNode {
		manager := r.ensureLaneManager(next.Leader)
		laneID := manager.LaneFor(next.Key)
		if manager.UpsertChannel(next.Key, next.Epoch) && previous != nil {
			r.scheduleLaneDispatch(next.Leader, laneID)
		}
	}
}

func (r *runtime) syncLeaderLaneTargets(ch *channel, next core.Meta) {
	if !r.longPollEnabled() || ch == nil {
		return
	}

	for _, target := range ch.replicationTargetsSnapshot() {
		if session, ok := r.leaderLanes.Session(target); ok {
			session.ForgetChannel(ch.key)
		}
	}
	ch.setReplicationTargets(nil)
	r.leaderLanes.SetReplicationTargets(ch.key, nil)

	if next.Leader != r.cfg.LocalNode {
		return
	}
	targets := make([]PeerLaneKey, 0, len(next.Replicas))
	laneID := laneIDFor(next.Key, r.cfg.LongPollLaneCount)
	for _, peer := range next.Replicas {
		if peer == 0 || peer == r.cfg.LocalNode {
			continue
		}
		targets = append(targets, PeerLaneKey{Peer: peer, LaneID: laneID})
	}
	ch.setReplicationTargets(targets)
	r.leaderLanes.SetReplicationTargets(ch.key, targets)
}
