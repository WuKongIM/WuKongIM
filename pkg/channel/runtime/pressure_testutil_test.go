package runtime

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replica"
)

func TestPressureConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("MULTIISR_STRESS", "")

	cfg, err := loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if cfg.groups != 256 || cfg.peers != 8 {
		t.Fatalf("defaults = %+v, want groups=256 peers=8", cfg)
	}

	t.Setenv("MULTIISR_STRESS_GROUPS", "512")
	t.Setenv("MULTIISR_STRESS_PEERS", "12")

	cfg, err = loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if cfg.groups != 512 || cfg.peers != 12 {
		t.Fatalf("overrides = %+v, want groups=512 peers=12", cfg)
	}
}

func TestPressureConfigRejectsInvalidDuration(t *testing.T) {
	t.Setenv("MULTIISR_STRESS", "1")
	t.Setenv("MULTIISR_STRESS_DURATION", "bad")

	if _, err := loadPressureConfig(); err == nil {
		t.Fatal("loadPressureConfig() error = nil, want invalid duration error")
	}
}

const (
	defaultPressureDuration                   = 10 * time.Second
	defaultPressureGroups                     = 256
	defaultPressurePeers                      = 8
	defaultPressureSeed                 int64 = 1
	defaultPressureSnapshotInterval           = 32
	defaultPressureBackpressureInterval       = 16
)

type pressureConfig struct {
	stressEnabled        bool
	duration             time.Duration
	groups               int
	peers                int
	seed                 int64
	snapshotInterval     int
	backpressureInterval int
}

func loadPressureConfig() (pressureConfig, error) {
	cfg := pressureConfig{
		duration:             defaultPressureDuration,
		groups:               defaultPressureGroups,
		peers:                defaultPressurePeers,
		seed:                 defaultPressureSeed,
		snapshotInterval:     defaultPressureSnapshotInterval,
		backpressureInterval: defaultPressureBackpressureInterval,
	}

	var err error
	if cfg.stressEnabled, err = loadPressureBool("MULTIISR_STRESS", false); err != nil {
		return pressureConfig{}, err
	}
	if cfg.duration, err = loadPressureDuration("MULTIISR_STRESS_DURATION", cfg.duration); err != nil {
		return pressureConfig{}, err
	}
	if cfg.groups, err = loadPressurePositiveInt("MULTIISR_STRESS_GROUPS", cfg.groups); err != nil {
		return pressureConfig{}, err
	}
	if cfg.peers, err = loadPressurePositiveInt("MULTIISR_STRESS_PEERS", cfg.peers); err != nil {
		return pressureConfig{}, err
	}
	if cfg.seed, err = loadPressureInt64("MULTIISR_STRESS_SEED", cfg.seed); err != nil {
		return pressureConfig{}, err
	}
	if cfg.snapshotInterval, err = loadPressurePositiveInt("MULTIISR_STRESS_SNAPSHOT_INTERVAL", cfg.snapshotInterval); err != nil {
		return pressureConfig{}, err
	}
	if cfg.backpressureInterval, err = loadPressurePositiveInt("MULTIISR_STRESS_BACKPRESSURE_INTERVAL", cfg.backpressureInterval); err != nil {
		return pressureConfig{}, err
	}
	return cfg, nil
}

func loadPressureBool(key string, defaultValue bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func loadPressureDuration(key string, defaultValue time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return defaultValue, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s: must be > 0", key)
	}
	return value, nil
}

func loadPressurePositiveInt(key string, defaultValue int) (int, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s: must be > 0", key)
	}
	return value, nil
}

func loadPressureInt64(key string, defaultValue int64) (int64, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

type pressureHarness struct {
	tb        testing.TB
	cfg       pressureConfig
	runtime   *runtime
	factory   *pressureReplicaFactory
	transport *pressureTransport
	sessions  *pressurePeerSessionManager
	clock     *snapshotManualClock
	throttle  *fakeSnapshotThrottle
	groupIDs  []uint64
	peerIDs   []core.NodeID

	queueHighWatermark           map[core.NodeID]int
	snapshotWaitingHighWatermark int
	schedulerRounds              int
}

func newPressureHarness(tb testing.TB, cfg pressureConfig, mutate func(*Config)) *pressureHarness {
	tb.Helper()

	clock := newSnapshotManualClock(time.Unix(1700000000, 0))
	generations := newSessionGenerationStore()
	factory := newPressureReplicaFactory()
	transport := &pressureTransport{}
	sessions := newPressurePeerSessionManager()

	rtCfg := Config{
		LocalNode:       1,
		ReplicaFactory:  factory,
		GenerationStore: generations,
		Transport:       transport,
		PeerSessions:    sessions,
		Limits: Limits{
			MaxSnapshotInflight: 1,
		},
		Tombstones: TombstonePolicy{
			TombstoneTTL: 30 * time.Second,
		},
		Now: clock.Now,
	}
	if mutate != nil {
		mutate(&rtCfg)
	}

	rt, err := New(rtCfg)
	if err != nil {
		tb.Fatalf("New() error = %v", err)
	}
	impl := rt.(*runtime)
	throttle := &fakeSnapshotThrottle{
		advance: clock.Advance,
		rate:    rtCfg.Limits.MaxRecoveryBytesPerSecond,
	}
	impl.snapshotThrottle = throttle

	h := &pressureHarness{
		tb:                           tb,
		cfg:                          cfg,
		runtime:                      impl,
		factory:                      factory,
		transport:                    transport,
		sessions:                     sessions,
		clock:                        clock,
		throttle:                     throttle,
		queueHighWatermark:           make(map[core.NodeID]int),
		snapshotWaitingHighWatermark: 0,
	}
	h.initPeers()
	h.initGroups()
	h.observeState()
	return h
}

func (h *pressureHarness) initPeers() {
	h.peerIDs = make([]core.NodeID, 0, h.cfg.peers)
	for i := 0; i < h.cfg.peers; i++ {
		h.peerIDs = append(h.peerIDs, core.NodeID(i+2))
	}
}

func (h *pressureHarness) initGroups() {
	replicas := make([]core.NodeID, 0, len(h.peerIDs)+1)
	replicas = append(replicas, 1)
	replicas = append(replicas, h.peerIDs...)

	h.groupIDs = make([]uint64, 0, h.cfg.groups)
	for i := 0; i < h.cfg.groups; i++ {
		groupID := uint64(i + 1)
		mustEnsurePressureChannel(h.tb, h.runtime, testMetaLocal(groupID, 1, 1, replicas))
		h.groupIDs = append(h.groupIDs, groupID)
	}
}

func (h *pressureHarness) peerFor(groupIndex, round int) core.NodeID {
	offset := int(h.cfg.seed % int64(len(h.peerIDs)))
	return h.peerIDs[(offset+groupIndex+round)%len(h.peerIDs)]
}

func (h *pressureHarness) enqueueReplication(groupIndex, round int) {
	groupID := h.groupIDs[groupIndex%len(h.groupIDs)]
	h.runtime.enqueueReplication(testChannelKey(groupID), h.peerFor(groupIndex, round))
}

func (h *pressureHarness) enqueueReplicationToPeer(groupIndex int, peer core.NodeID) {
	groupID := h.groupIDs[groupIndex%len(h.groupIDs)]
	h.runtime.enqueueReplication(testChannelKey(groupID), peer)
}

func (h *pressureHarness) enqueueReplicationRound(round int) {
	for groupIndex := range h.groupIDs {
		h.enqueueReplication(groupIndex, round)
	}
}

func (h *pressureHarness) queueSnapshot(groupIndex int, bytes int64) {
	groupID := h.groupIDs[groupIndex%len(h.groupIDs)]
	h.runtime.queueSnapshotChunk(testChannelKey(groupID), bytes)
}

func (h *pressureHarness) runSchedulingTicks(rounds int) {
	for i := 0; i < rounds; i++ {
		h.runtime.runScheduler()
		h.schedulerRounds++
		h.observeState()
	}
}

func (h *pressureHarness) observeState() {
	for _, peer := range h.peerIDs {
		queued := h.runtime.queuedPeerRequests(peer)
		if queued > h.queueHighWatermark[peer] {
			h.queueHighWatermark[peer] = queued
		}
	}
	if waiting := h.runtime.queuedSnapshotGroups(); waiting > h.snapshotWaitingHighWatermark {
		h.snapshotWaitingHighWatermark = waiting
	}
}

func (h *pressureHarness) deliverPeerFetchResponses(peer core.NodeID, limit int) int {
	session := h.sessions.session(peer)
	delivered := 0
	for limit <= 0 || delivered < limit {
		remaining := limit - delivered
		envs := session.popSent(remaining)
		if len(envs) == 0 {
			break
		}
		for _, env := range envs {
			h.transport.deliver(Envelope{
				Peer:       env.Peer,
				ChannelKey: env.ChannelKey,
				Generation: env.Generation,
				Epoch:      env.Epoch,
				Kind:       MessageKindFetchResponse,
			})
			delivered++
			if limit > 0 && delivered >= limit {
				break
			}
		}
	}
	h.observeState()
	return delivered
}

func (h *pressureHarness) deliverFetchResponses(limitPerPeer int) int {
	delivered := 0
	for _, peer := range h.peerIDs {
		delivered += h.deliverPeerFetchResponses(peer, limitPerPeer)
	}
	return delivered
}

func (h *pressureHarness) assertSomeGroupMadeProgress(tb testing.TB) {
	tb.Helper()

	for _, groupID := range h.groupIDs {
		if h.factory.progressCount(groupID) > 0 {
			return
		}
	}
	tb.Fatal("expected at least one group to make progress")
}

func (h *pressureHarness) assertAllGroupsMadeProgress(tb testing.TB) {
	tb.Helper()

	for _, groupID := range h.groupIDs {
		if h.factory.progressCount(groupID) == 0 {
			tb.Fatalf("group %d made no progress", groupID)
		}
	}
}

func (h *pressureHarness) assertPeerQueuesDrained(tb testing.TB) {
	tb.Helper()

	for _, peer := range h.peerIDs {
		if queued := h.runtime.queuedPeerRequests(peer); queued != 0 {
			tb.Fatalf("peer %d queue = %d, want 0", peer, queued)
		}
	}
}

func (h *pressureHarness) assertSnapshotQueueDrained(tb testing.TB) {
	tb.Helper()

	if waiting := h.runtime.queuedSnapshotGroups(); waiting != 0 {
		tb.Fatalf("snapshot waiting queue = %d, want 0", waiting)
	}
}

func (h *pressureHarness) maxQueuedPeerRequests() int {
	maxQueued := 0
	for _, peer := range h.peerIDs {
		if queued := h.queueHighWatermark[peer]; queued > maxQueued {
			maxQueued = queued
		}
	}
	return maxQueued
}

func (h *pressureHarness) setPeerBackpressure(peer core.NodeID, level BackpressureLevel) {
	h.sessions.session(peer).setBackpressure(BackpressureState{Level: level})
}

func (h *pressureHarness) clearPeerBackpressure() {
	for _, peer := range h.peerIDs {
		h.setPeerBackpressure(peer, BackpressureNone)
	}
}

func (h *pressureHarness) reserveSnapshotSlot(tb testing.TB) {
	tb.Helper()

	if !h.runtime.snapshots.begin(h.runtime.cfg.Limits.MaxSnapshotInflight) {
		tb.Fatal("expected to reserve snapshot slot")
	}
}

func (h *pressureHarness) releaseSnapshotSlot() {
	h.runtime.completeSnapshot("")
	h.observeState()
}

func (h *pressureHarness) deliverAvailableFetchResponses() int {
	delivered := 0
	for _, peer := range h.peerIDs {
		if state := h.sessions.session(peer).Backpressure(); state.Level == BackpressureHard {
			continue
		}
		delivered += h.deliverPeerFetchResponses(peer, 0)
	}
	return delivered
}

func mustEnsurePressureChannel(tb testing.TB, rt *runtime, meta core.Meta) {
	tb.Helper()
	if err := rt.EnsureChannel(meta); err != nil {
		tb.Fatalf("EnsureChannel(%q) error = %v", meta.Key, err)
	}
}

type pressureReplicaFactory struct {
	mu       sync.Mutex
	replicas map[core.ChannelKey]*pressureReplica
}

func newPressureReplicaFactory() *pressureReplicaFactory {
	return &pressureReplicaFactory{
		replicas: make(map[core.ChannelKey]*pressureReplica),
	}
}

func (f *pressureReplicaFactory) New(cfg ChannelConfig) (replica.Replica, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	replica := &pressureReplica{
		state: core.ReplicaState{
			ChannelKey: cfg.ChannelKey,
			Role:       core.ReplicaRoleLeader,
			Epoch:      cfg.Meta.Epoch,
			Leader:     cfg.Meta.Leader,
			CommitReady: true,
		},
	}
	f.replicas[cfg.ChannelKey] = replica
	return replica, nil
}

func (f *pressureReplicaFactory) progressCount(groupID uint64) int {
	f.mu.Lock()
	replica := f.replicas[testChannelKey(groupID)]
	f.mu.Unlock()
	if replica == nil {
		return 0
	}
	return replica.progressCount()
}

type pressureReplica struct {
	mu              sync.Mutex
	state           core.ReplicaState
	applyFetchCalls int
}

func (r *pressureReplica) ApplyMeta(meta core.Meta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Epoch = meta.Epoch
	r.state.Leader = meta.Leader
	return nil
}

func (r *pressureReplica) BecomeLeader(meta core.Meta) error   { return r.ApplyMeta(meta) }
func (r *pressureReplica) BecomeFollower(meta core.Meta) error { return r.ApplyMeta(meta) }

func (r *pressureReplica) Tombstone() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Role = core.ReplicaRoleTombstoned
	return nil
}

func (r *pressureReplica) Close() error {
	return nil
}

func (r *pressureReplica) InstallSnapshot(_ context.Context, _ core.Snapshot) error {
	return nil
}

func (r *pressureReplica) Append(_ context.Context, _ []core.Record) (core.CommitResult, error) {
	return core.CommitResult{}, nil
}

func (r *pressureReplica) Fetch(_ context.Context, _ core.ReplicaFetchRequest) (core.ReplicaFetchResult, error) {
	return core.ReplicaFetchResult{}, nil
}

func (r *pressureReplica) ApplyFetch(_ context.Context, _ core.ReplicaApplyFetchRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applyFetchCalls++
	return nil
}

func (r *pressureReplica) ApplyProgressAck(_ context.Context, _ core.ReplicaProgressAckRequest) error {
	return nil
}

func (r *pressureReplica) ApplyReconcileProof(_ context.Context, _ core.ReplicaReconcileProof) error {
	return nil
}

func (r *pressureReplica) Status() core.ReplicaState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *pressureReplica) progressCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applyFetchCalls
}

type pressureTransport struct {
	mu      sync.Mutex
	handler func(Envelope)
}

func (t *pressureTransport) Send(peer core.NodeID, env Envelope) error {
	return nil
}

func (t *pressureTransport) RegisterHandler(fn func(Envelope)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handler = fn
}

func (t *pressureTransport) deliver(env Envelope) {
	t.mu.Lock()
	handler := t.handler
	t.mu.Unlock()
	if handler != nil {
		handler(env)
	}
}

type pressurePeerSessionManager struct {
	mu    sync.Mutex
	cache map[core.NodeID]*pressurePeerSession
}

func newPressurePeerSessionManager() *pressurePeerSessionManager {
	return &pressurePeerSessionManager{
		cache: make(map[core.NodeID]*pressurePeerSession),
	}
}

func (m *pressurePeerSessionManager) Session(peer core.NodeID) PeerSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, ok := m.cache[peer]; ok {
		return session
	}
	session := &pressurePeerSession{}
	m.cache[peer] = session
	return session
}

func (m *pressurePeerSessionManager) session(peer core.NodeID) *pressurePeerSession {
	m.mu.Lock()
	session, ok := m.cache[peer]
	m.mu.Unlock()
	if ok {
		return session
	}
	return m.Session(peer).(*pressurePeerSession)
}

type pressurePeerSession struct {
	mu           sync.Mutex
	sends        int
	sent         []Envelope
	backpressure BackpressureState
}

func (s *pressurePeerSession) Send(env Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends++
	s.sent = append(s.sent, env)
	return nil
}

func (s *pressurePeerSession) TryBatch(env Envelope) bool {
	return false
}

func (s *pressurePeerSession) Flush() error {
	return nil
}

func (s *pressurePeerSession) Backpressure() BackpressureState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backpressure
}

func (s *pressurePeerSession) Close() error {
	return nil
}

func (s *pressurePeerSession) popSent(limit int) []Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.sent) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(s.sent) {
		limit = len(s.sent)
	}
	envs := append([]Envelope(nil), s.sent[:limit]...)
	if limit == len(s.sent) {
		s.sent = nil
	} else {
		s.sent = append([]Envelope(nil), s.sent[limit:]...)
	}
	return envs
}

func (s *pressurePeerSession) setBackpressure(state BackpressureState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backpressure = state
}
