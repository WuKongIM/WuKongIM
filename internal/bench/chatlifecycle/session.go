package chatlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	errSessionConfig  = errors.New("chat lifecycle session: configuration is incomplete")
	errSessionOnline  = errors.New("chat lifecycle session: UID is already online")
	errSessionOffline = errors.New("chat lifecycle session: UID is offline")
)

const defaultSessionHeartbeatInterval = 30 * time.Second

// SessionClock is the narrow time source used for login latency and expiration.
// Unit workers can advance it without wall-clock sleeps.
type SessionClock interface {
	Now() time.Time
}

// SessionClient is one fresh WKProto connection. Implementations adapt the
// production client without moving protocol or synchronization rules here.
type SessionClient interface {
	Connect(context.Context, string, string) error
	ReadFrame(context.Context) (frame.Frame, error)
	// TrySend must not wait for local queue or inflight capacity. Implementations
	// report transient local pressure with client.ErrSendQueueFull.
	TrySend(context.Context, *frame.SendPacket) error
	Ping(context.Context) error
	AckRecv(context.Context, *frame.RecvackPacket) error
	Close() error
	// QueueSnapshot must stop waiting when its context is canceled.
	QueueSnapshot(context.Context) (SessionQueueSnapshot, error)
	ReadErrorInfo(error) (wkproto.ReadErrorInfo, bool)
}

// sessionFrameObserver is the optional precision seam implemented by the
// production adapter. Generic test or alternate clients may omit it; the pool
// then timestamps the frame when its sole drain consumes it.
type sessionFrameObserver interface {
	ReadFrameObserved(context.Context) (frame.Frame, time.Time, error)
}

// SessionFrameTiming carries identity-free, process-local SEND boundaries.
type SessionFrameTiming struct {
	PendingStartedAt time.Time
	WriteStartedAt   time.Time
	ObservedAt       time.Time
}

type sessionFrameTimingObserver interface {
	ReadFrameTiming(context.Context) (frame.Frame, SessionFrameTiming, error)
}

// SessionClientFactory constructs a client with the deterministic per-UID
// token already installed in its CONNECT configuration.
type SessionClientFactory interface {
	NewSession(context.Context, string, string) (SessionClient, error)
}

// WKProtoSessionAdapter binds the existing production benchmark client to the
// session runtime without recreating framing, crypto, or queue semantics.
type WKProtoSessionAdapter struct {
	client *wkproto.Client
}

// NewWKProtoSessionAdapter rejects nil rather than deferring a panic to login.
func NewWKProtoSessionAdapter(client *wkproto.Client) (*WKProtoSessionAdapter, error) {
	if client == nil {
		return nil, errSessionConfig
	}
	return &WKProtoSessionAdapter{client: client}, nil
}

func (a *WKProtoSessionAdapter) Connect(ctx context.Context, uid, deviceID string) error {
	return a.client.Connect(ctx, uid, deviceID)
}

func (a *WKProtoSessionAdapter) ReadFrame(ctx context.Context) (frame.Frame, error) {
	return a.client.ReadFrame(ctx)
}

func (a *WKProtoSessionAdapter) ReadFrameObserved(ctx context.Context) (frame.Frame, time.Time, error) {
	return a.client.ReadFrameObserved(ctx)
}

func (a *WKProtoSessionAdapter) ReadFrameTiming(ctx context.Context) (frame.Frame, SessionFrameTiming, error) {
	packet, timing, err := a.client.ReadFrameTiming(ctx)
	return packet, SessionFrameTiming{
		PendingStartedAt: timing.PendingStartedAt,
		WriteStartedAt:   timing.WriteStartedAt,
		ObservedAt:       timing.ObservedAt,
	}, err
}

func (a *WKProtoSessionAdapter) TrySend(ctx context.Context, packet *frame.SendPacket) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return a.client.TrySend(packet)
}

func (a *WKProtoSessionAdapter) Ping(ctx context.Context) error {
	return a.client.Ping(ctx)
}

func (a *WKProtoSessionAdapter) AckRecv(ctx context.Context, ack *frame.RecvackPacket) error {
	return a.client.RecvAck(ctx, ack.MessageID, ack.MessageSeq)
}

func (a *WKProtoSessionAdapter) Close() error { return a.client.Close() }

func (a *WKProtoSessionAdapter) QueueSnapshot(ctx context.Context) (SessionQueueSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionQueueSnapshot{}, err
	}
	snapshot := a.client.QueueSnapshot()
	if err := ctx.Err(); err != nil {
		return SessionQueueSnapshot{}, err
	}
	return SessionQueueSnapshot{
		Depth:    snapshot.InnerRecvDepth + snapshot.AdapterDepth,
		Capacity: snapshot.InnerRecvCapacity + snapshot.AdapterCapacity,
		Inflight: snapshot.PublicationCurrent,
	}, nil
}

func (a *WKProtoSessionAdapter) ReadErrorInfo(err error) (wkproto.ReadErrorInfo, bool) {
	return wkproto.ReadErrorInfoOf(err)
}

var _ SessionClient = (*WKProtoSessionAdapter)(nil)
var _ sessionFrameObserver = (*WKProtoSessionAdapter)(nil)
var _ sessionFrameTimingObserver = (*WKProtoSessionAdapter)(nil)

// SessionQueueSnapshot is the bounded common projection needed by the engine.
type SessionQueueSnapshot struct {
	Depth    int
	Capacity int
	Inflight int
}

// SessionPoolConfig wires only public target sync and WKProto transport seams.
type SessionPoolConfig struct {
	Identity *IdentitySpace
	Schedule ScheduleModel
	Catalog  GroupCatalog
	Factory  SessionClientFactory
	Syncer   ConversationSyncer
	Verifier *Verifier
	Clock    SessionClock
	DeviceID string
	// SyncLatency fixes exact threshold counters, including the 3s formal p99.9
	// boundary that cannot be reconstructed from the fixed 2s/5s histogram buckets.
	SyncLatency   LatencyLimit
	SingleAnomaly time.Duration
	// HeartbeatInterval keeps every traffic-ready session present in the
	// authority routing directory. Zero selects the real-client 30s cadence.
	HeartbeatInterval time.Duration
	// StartingCapacity bounds concurrent CONNECT plus full-sync operations.
	StartingCapacity int

	// OnSendack runs inline after verifier processing and may apply bounded
	// backpressure; the receive drain never drops a completion to avoid waiting.
	OnSendack func(uid string, ack *frame.SendackPacket, verificationErr error)
	// OnAsyncSendError transfers one non-terminal result-queue error to the
	// engine that owns retry state. The raw transport error is never exposed.
	OnAsyncSendError func(uid string, clientSeq uint64, clientMsgNo string)
}

// SessionLogin binds a reconstructed identity to one global login ordinal.
type SessionLogin struct {
	UID          string
	UserIndex    uint64
	LoginOrdinal uint64
	// NewIdentity keeps relationship publication pending until the engine has
	// observed this first successful synchronized login.
	NewIdentity bool
}

// SessionSnapshot is an identity-safe in-process view used only by the owner.
// Worker snapshots aggregate this state and never enumerate it.
type SessionSnapshot struct {
	UID                       string
	UserIndex                 uint64
	LoginOrdinal              uint64
	Deadline                  time.Time
	TrafficReady              bool
	GatewayConnectLatency     time.Duration
	ConversationSyncLatency   time.Duration
	SynchronizedConversations int
}

// SessionPoolSnapshot contains only bounded aggregate ownership, real startup
// outcomes, fixed latency histograms, and queue data.
type SessionPoolSnapshot struct {
	Online                     int
	Starting                   int
	Closing                    int
	TrafficReady               int
	QueueDepth                 int
	QueueCapacity              int
	TransportInflight          int
	TransportAdmissionRejected uint64
	ReadErrors                 uint64
	VerificationErrors         uint64
	FactoryFailed              uint64
	FactoryCanceled            uint64
	ConnectStarted             uint64
	ConnectCompleted           uint64
	ConnectFailed              uint64
	ConnectCanceled            uint64
	SyncStarted                uint64
	SyncCompleted              uint64
	SyncFailed                 uint64
	SyncCanceled               uint64
	// CloseReasons attributes every teardown to a fixed, identity-free
	// initiator and separately counts transport-close failures.
	CloseReasons               SessionCloseReasonSnapshot
	GatewayConnectLatency      WorkerHistogramSnapshot
	ConversationSyncLatency    WorkerHistogramSnapshot
	ConversationSyncThresholds LatencyThresholdCounters
	SendPendingToWriteLatency  WorkerHistogramSnapshot
	SendWriteToAckLatency      WorkerHistogramSnapshot
}

// SessionCloseReasonSnapshot is the bounded connection-teardown vocabulary.
// The first six counters are mutually exclusive initiators;
// TransportCloseFailed is an additional cleanup outcome.
type SessionCloseReasonSnapshot struct {
	Expired              uint64 `json:"expired"`
	HeartbeatFailed      uint64 `json:"heartbeat_failed"`
	RemoteTerminal       uint64 `json:"remote_terminal"`
	ReadFailed           uint64 `json:"read_failed"`
	GenerationStop       uint64 `json:"generation_stop"`
	ExplicitLogout       uint64 `json:"explicit_logout"`
	TransportCloseFailed uint64 `json:"transport_close_failed"`
}

type sessionCloseReason uint32

const (
	sessionCloseReasonNone sessionCloseReason = iota
	sessionCloseReasonExpired
	sessionCloseReasonHeartbeatFailed
	sessionCloseReasonRemoteTerminal
	sessionCloseReasonReadFailed
	sessionCloseReasonGenerationStop
	sessionCloseReasonExplicitLogout
)

// SessionCounts is the O(1) ownership projection used by the scheduler.
type SessionCounts struct {
	Online   int
	Starting int
	Closing  int
}

type onlineSession struct {
	snapshot              SessionSnapshot
	client                SessionClient
	cancel                context.CancelFunc
	done                  chan struct{}
	heartbeatDone         chan struct{}
	groupIndex            int
	groupPosition         int
	relationshipsObserved bool
	// closeInitiator is guarded by SessionPool.mu and claimed exactly once.
	closeInitiator sessionCloseReason
}

// SessionPool owns only currently online clients. A UID has exactly one
// receive drain and one heartbeat loop; logout joins both before releasing
// verifier state.
type SessionPool struct {
	identity          *IdentitySpace
	schedule          ScheduleModel
	catalog           GroupCatalog
	factory           SessionClientFactory
	syncer            ConversationSyncer
	verifier          *Verifier
	clock             SessionClock
	deviceID          string
	startingCapacity  int
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	heartbeatSleep    func(context.Context, time.Duration) error
	onSendack         func(string, *frame.SendackPacket, error)
	onAsyncSendError  func(string, uint64, string)

	mu                 sync.RWMutex
	online             map[string]*onlineSession
	onlineByIndex      map[uint64]*onlineSession
	onlineGroupMembers [][]*onlineSession
	starting           map[string]struct{}
	closing            map[string]*onlineSession
	// sendLeases keeps an admitted logical SEND's session routable until its
	// final ACK, terminal result, or cancellation releases ownership.
	sendLeases map[string]uint32
	// correlationLeases binds each sampled logical SEND to one exact online
	// recipient. correlationLeaseCounts lets expiry test one UID in O(1)
	// without retaining message history after the verifier closes the sample.
	correlationLeases         map[string]string
	correlationLeaseCounts    map[string]uint32
	readErrors                uint64
	verificationErrors        uint64
	factoryFailed             uint64
	factoryCanceled           uint64
	connectStarted            uint64
	connectCompleted          uint64
	connectFailed             uint64
	connectCanceled           uint64
	syncStarted               uint64
	syncCompleted             uint64
	syncFailed                uint64
	syncCanceled              uint64
	closeReasons              SessionCloseReasonSnapshot
	gatewayLatency            WorkerHistogramSnapshot
	syncLatency               WorkerHistogramSnapshot
	syncThresholds            LatencyThresholdCounters
	sendPendingToWriteLatency WorkerHistogramSnapshot
	sendWriteToAckLatency     WorkerHistogramSnapshot
	// transportAdmissionRejected survives individual session churn for the generation.
	transportAdmissionRejected atomic.Uint64

	// ownershipCapacity is the generation's fixed online target. While nonzero,
	// online, starting, and closing ownership may never exceed this bound, which
	// permanently reserves one cleanup-queue entry for every routable session.
	// It is guarded by mu.
	ownershipCapacity int
	// cleanupMu serializes generation cleanup-loop lifecycle and reservations.
	// cleanupPending equals queued plus in-progress cleanup entries; the worker
	// decrements it before removing the matching closing tombstone.
	cleanupMu      sync.Mutex
	cleanupQueue   chan *onlineSession
	cleanupDone    chan struct{}
	cleanupPending int
}

// NewSessionPool validates all lifecycle seams before any client is created.
func NewSessionPool(config SessionPoolConfig) (*SessionPool, error) {
	if config.SyncLatency == (LatencyLimit{}) && config.SingleAnomaly == 0 {
		config.SyncLatency = LatencyLimit{P99: time.Second, P999: 3 * time.Second}
		config.SingleAnomaly = 10 * time.Second
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = defaultSessionHeartbeatInterval
	}
	if config.Identity == nil || config.Schedule.identity != config.Identity || config.Catalog.identity != config.Identity || config.Factory == nil ||
		config.Syncer == nil || config.Verifier == nil || config.Clock == nil || config.DeviceID == "" ||
		config.StartingCapacity <= 0 || config.StartingCapacity > maxVerifierCapacity ||
		config.SyncLatency.P99 <= 0 || config.SyncLatency.P999 < config.SyncLatency.P99 || config.SingleAnomaly < config.SyncLatency.P999 ||
		config.HeartbeatInterval <= 0 {
		return nil, errSessionConfig
	}
	return &SessionPool{
		identity: config.Identity, schedule: config.Schedule, catalog: config.Catalog, factory: config.Factory,
		syncer: config.Syncer, verifier: config.Verifier, clock: config.Clock,
		deviceID: config.DeviceID, startingCapacity: config.StartingCapacity,
		heartbeatInterval: config.HeartbeatInterval, heartbeatTimeout: config.SingleAnomaly, heartbeatSleep: sleepSessionHeartbeat,
		onSendack:                 config.OnSendack,
		onAsyncSendError:          config.OnAsyncSendError,
		online:                    make(map[string]*onlineSession),
		onlineByIndex:             make(map[uint64]*onlineSession),
		onlineGroupMembers:        make([][]*onlineSession, config.Catalog.Count()),
		starting:                  make(map[string]struct{}),
		closing:                   make(map[string]*onlineSession),
		sendLeases:                make(map[string]uint32),
		correlationLeases:         make(map[string]string),
		correlationLeaseCounts:    make(map[string]uint32),
		gatewayLatency:            newWorkerHistogramSnapshot(),
		syncLatency:               newWorkerHistogramSnapshot(),
		sendPendingToWriteLatency: newWorkerHistogramSnapshot(),
		sendWriteToAckLatency:     newWorkerHistogramSnapshot(),
		syncThresholds: LatencyThresholdCounters{
			P99Limit: config.SyncLatency.P99, P999Limit: config.SyncLatency.P999,
		},
	}, nil
}

// Login creates a fresh connection, completes CONNECT then zero-coverage full
// sync through RunLoginSync, and starts the ordered receive drain plus heartbeat
// only after the session is traffic-ready.
func (p *SessionPool) Login(ctx context.Context, login SessionLogin) (SessionSnapshot, error) {
	return p.login(ctx, context.Background(), login)
}

// login binds the long-lived receive drain to drainParent while startup uses
// the separately cancelable caller context. Engine generations use this seam
// so stopping a generation cancels both admission and every ordered drain.
func (p *SessionPool) login(ctx, drainParent context.Context, login SessionLogin) (SessionSnapshot, error) {
	if p == nil || login.UID == "" || p.identity.UID(login.UserIndex) != login.UID {
		return SessionSnapshot{}, errSessionConfig
	}
	if ctx == nil || drainParent == nil {
		return SessionSnapshot{}, errSessionConfig
	}
	if err := p.reserveLogin(login.UID); err != nil {
		return SessionSnapshot{}, err
	}
	return p.loginReserved(ctx, drainParent, login)
}

func (p *SessionPool) reserveLogin(uid string) error {
	p.mu.Lock()
	_, online := p.online[uid]
	_, starting := p.starting[uid]
	_, closing := p.closing[uid]
	if online || starting || closing {
		p.mu.Unlock()
		return errSessionOnline
	}
	if p.ownershipCapacity > 0 && len(p.online)+len(p.starting)+len(p.closing) >= p.ownershipCapacity {
		capacity := p.ownershipCapacity
		p.mu.Unlock()
		_ = p.verifier.evidence.Record(EvidenceEvent{
			Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: FailureCodeSessionLoginSaturated,
			Value: uint64(capacity),
		})
		return &RuntimeError{code: RuntimeFailureLoginSaturated}
	}
	if len(p.starting) >= p.startingCapacity {
		p.mu.Unlock()
		_ = p.verifier.evidence.Record(EvidenceEvent{
			Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: FailureCodeSessionLoginSaturated,
			Value: uint64(p.startingCapacity),
		})
		return &RuntimeError{code: RuntimeFailureLoginSaturated}
	}
	p.starting[uid] = struct{}{}
	p.mu.Unlock()
	return nil
}

func (p *SessionPool) loginReserved(ctx, drainParent context.Context, login SessionLogin) (SessionSnapshot, error) {
	defer func() {
		p.mu.Lock()
		delete(p.starting, login.UID)
		p.mu.Unlock()
	}()

	schedule, err := p.schedule.Login(login.LoginOrdinal)
	if err != nil {
		return SessionSnapshot{}, err
	}
	client, err := p.factory.NewSession(ctx, login.UID, p.tokenForUID(login.UID))
	if err != nil {
		reason := LoginSyncReasonTransport
		if ctx.Err() != nil {
			reason = LoginSyncReasonCanceled
		}
		operationErr := newLoginSyncOperationError(LoginSyncStageFactory, reason)
		p.recordFactoryOutcome(operationErr)
		return SessionSnapshot{}, operationErr
	}
	connector := sessionLoginConnector{client: client, deviceID: p.deviceID}
	result, err := RunLoginSync(ctx, login.UID, connector, p.syncer, p.clock.Now)
	p.recordLoginSyncOutcome(result, err)
	if err != nil {
		_ = client.Close()
		return SessionSnapshot{}, err
	}

	readyAt := p.clock.Now()
	snapshot := SessionSnapshot{
		UID: login.UID, UserIndex: login.UserIndex, LoginOrdinal: login.LoginOrdinal,
		Deadline: readyAt.Add(schedule.SessionDuration), TrafficReady: result.TrafficReady,
		GatewayConnectLatency:     result.GatewayConnectLatency,
		ConversationSyncLatency:   result.ConversationSyncLatency,
		SynchronizedConversations: len(result.Conversations),
	}
	drainCtx, cancel := context.WithCancel(drainParent)
	session := &onlineSession{
		snapshot: snapshot, client: client, cancel: cancel, done: make(chan struct{}), heartbeatDone: make(chan struct{}),
		groupIndex: -1, groupPosition: -1, relationshipsObserved: !login.NewIdentity,
	}
	if group, _, member, groupErr := p.catalog.GroupForMemberIndex(login.UserIndex); groupErr != nil {
		cancel()
		_ = client.Close()
		return SessionSnapshot{}, groupErr
	} else if member {
		session.groupIndex = int(group.Index)
	}
	p.mu.Lock()
	delete(p.starting, login.UID)
	p.online[login.UID] = session
	p.onlineByIndex[login.UserIndex] = session
	if session.groupIndex >= 0 {
		members := p.onlineGroupMembers[session.groupIndex]
		session.groupPosition = len(members)
		p.onlineGroupMembers[session.groupIndex] = append(members, session)
	}
	p.mu.Unlock()
	go p.drain(drainCtx, session)
	go p.heartbeat(drainCtx, session)
	return snapshot, nil
}

func (p *SessionPool) recordFactoryOutcome(err error) {
	failure, _ := LoginSyncFailureOf(err)
	p.mu.Lock()
	if failure.Reason == LoginSyncReasonCanceled {
		incrementSessionOutcome(&p.factoryCanceled)
	} else {
		incrementSessionOutcome(&p.factoryFailed)
	}
	p.mu.Unlock()
	p.recordLoginSyncEvidence(err)
}

func (p *SessionPool) recordLoginSyncOutcome(result LoginSyncResult, err error) {
	failure, failed := LoginSyncFailureOf(err)
	p.mu.Lock()
	if result.ConnectStarted {
		incrementSessionOutcome(&p.connectStarted)
		recordWorkerLatency(&p.gatewayLatency, result.GatewayConnectLatency)
	}
	if result.ConnectCompleted {
		incrementSessionOutcome(&p.connectCompleted)
	} else if result.ConnectStarted {
		if failed && failure.Reason == LoginSyncReasonCanceled {
			incrementSessionOutcome(&p.connectCanceled)
		} else {
			incrementSessionOutcome(&p.connectFailed)
		}
	}
	if result.SyncStarted {
		incrementSessionOutcome(&p.syncStarted)
		recordWorkerLatency(&p.syncLatency, result.ConversationSyncLatency)
		recordLatencyThresholdCounters(&p.syncThresholds, result.ConversationSyncLatency)
	}
	if result.SyncCompleted {
		incrementSessionOutcome(&p.syncCompleted)
	} else if result.SyncStarted {
		if failed && failure.Reason == LoginSyncReasonCanceled {
			incrementSessionOutcome(&p.syncCanceled)
		} else {
			incrementSessionOutcome(&p.syncFailed)
		}
	}
	p.mu.Unlock()
	if err != nil {
		p.recordLoginSyncEvidence(err)
	}
}

func (p *SessionPool) recordLoginSyncEvidence(err error) {
	failure, ok := LoginSyncFailureOf(err)
	if !ok || failure.Reason == LoginSyncReasonCanceled {
		return
	}
	event := EvidenceEvent{Class: FailureClassHarness}
	switch failure.Stage {
	case LoginSyncStageFactory:
		event.Stage, event.Code = EvidenceStageSessionFactory, FailureCodeSessionFactoryFailed
	case LoginSyncStageConnect:
		event.Stage, event.Code = EvidenceStageConnect, FailureCodeSessionConnectFailed
	case LoginSyncStageSync:
		event.Stage = EvidenceStageSync
		var validation *ConversationSyncValidationError
		if errors.As(err, &validation) {
			event.Code = FailureCodeSessionSyncValidation
			if failure.Classification == SyncClassificationProductFailure {
				event.Class = FailureClassReceive
			}
		} else {
			event.Code = FailureCodeSessionSyncFailed
		}
	default:
		return
	}
	_ = p.verifier.evidence.Record(event)
}

func incrementSessionOutcome(counter *uint64) {
	if *counter != ^uint64(0) {
		(*counter)++
	}
}

// IsOnline reports current owned connection state without retaining history.
func (p *SessionPool) IsOnline(uid string) bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.online[uid]
	return ok
}

// sendEligibleAt reports whether an owned session may accept a new logical
// SEND at the owner clock boundary. A deadline-expired session can remain
// online while an earlier admitted SEND drains, but it must not receive a new
// SEND lease.
func (p *SessionPool) sendEligibleAt(uid string, at time.Time) bool {
	if p == nil || uid == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	session := p.online[uid]
	return sessionSendEligibleAt(session, p.sendLeases[uid], at)
}

func sessionSendEligibleAt(session *onlineSession, sendLeases uint32, at time.Time) bool {
	return session != nil && session.snapshot.TrafficReady && session.snapshot.Deadline.After(at) && sendLeases != ^uint32(0)
}

// Counts reports current ownership map sizes without touching client gauges.
func (p *SessionPool) Counts() SessionCounts {
	if p == nil {
		return SessionCounts{}
	}
	p.mu.RLock()
	counts := SessionCounts{Online: len(p.online), Starting: len(p.starting), Closing: len(p.closing)}
	p.mu.RUnlock()
	return counts
}

func (p *SessionPool) isOwned(uid string) bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, online := p.online[uid]
	_, starting := p.starting[uid]
	_, closing := p.closing[uid]
	return online || starting || closing
}

// CanActivate enforces the relationship contract at the point work is admitted.
func (p *SessionPool) CanActivate(edge RelationshipEdge) bool {
	if p == nil || edge.OwnerUID == "" || edge.PeerUID == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	owner := p.online[edge.OwnerUID]
	peer := p.online[edge.PeerUID]
	return owner != nil && peer != nil && owner.snapshot.TrafficReady && peer.snapshot.TrafficReady
}

// relationshipObservation reports whether one traffic-ready identity is
// online and whether its initial new-user relationship publication completed.
func (p *SessionPool) relationshipObservation(userIndex uint64) (online, observed bool) {
	if p == nil {
		return false, false
	}
	p.mu.RLock()
	session := p.onlineByIndex[userIndex]
	if session != nil {
		online = session.snapshot.TrafficReady
		observed = online && session.relationshipsObserved
	}
	p.mu.RUnlock()
	return online, observed
}

// markRelationshipsObserved completes the bounded per-online-session
// publication fence. It returns false for an offline or already observed UID.
func (p *SessionPool) markRelationshipsObserved(userIndex uint64) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	session := p.onlineByIndex[userIndex]
	if session == nil || !session.snapshot.TrafficReady || session.relationshipsObserved {
		return false
	}
	session.relationshipsObserved = true
	return true
}

// TrySend performs non-waiting local admission through the owned connection.
func (p *SessionPool) TrySend(ctx context.Context, uid string, packet *frame.SendPacket) error {
	p.mu.RLock()
	session := p.online[uid]
	p.mu.RUnlock()
	if session == nil {
		return errSessionOffline
	}
	err := session.client.TrySend(ctx, packet)
	if errors.Is(err, wkclient.ErrSendQueueFull) {
		recordSaturatingAtomic(&p.transportAdmissionRejected)
	}
	return err
}

func recordSaturatingAtomic(counter *atomic.Uint64) {
	for {
		current := counter.Load()
		if current == ^uint64(0) || counter.CompareAndSwap(current, current+1) {
			return
		}
	}
}

// Expire logs out every due session. Sorting makes simultaneous fake-clock
// expiration replayable while retaining only the current online UID slice.
func (p *SessionPool) Expire(now time.Time) int {
	if p == nil {
		return 0
	}
	sessions := p.detachExpired(now)
	p.finishDetachedSessions(sessions)
	return len(sessions)
}

// startExpiryCleanup starts the one bounded generation-owned cleanup loop.
// Capacity is the worker's online target, so queued closing ownership cannot
// grow with elapsed run history.
func (p *SessionPool) startExpiryCleanup(capacity int) error {
	if p == nil || capacity <= 0 || capacity > maxVerifierCapacity {
		return errSessionConfig
	}
	p.cleanupMu.Lock()
	defer p.cleanupMu.Unlock()
	if p.cleanupQueue != nil || p.cleanupDone != nil || p.cleanupPending != 0 {
		return errSessionOnline
	}
	p.mu.Lock()
	if len(p.online) != 0 || len(p.starting) != 0 || len(p.closing) != 0 || p.ownershipCapacity != 0 {
		p.mu.Unlock()
		return errSessionOnline
	}
	p.ownershipCapacity = capacity
	p.mu.Unlock()
	p.cleanupQueue = make(chan *onlineSession, capacity)
	p.cleanupDone = make(chan struct{})
	go p.runExpiryCleanup(p.cleanupQueue, p.cleanupDone)
	return nil
}

// stopExpiryCleanup joins every detached session before a generation can
// reset SessionPool ownership or publish terminal cleanup completion.
func (p *SessionPool) stopExpiryCleanup() {
	if p == nil {
		return
	}
	p.cleanupMu.Lock()
	queue, done := p.cleanupQueue, p.cleanupDone
	if queue == nil {
		p.cleanupMu.Unlock()
		return
	}
	close(queue)
	p.cleanupQueue = nil
	p.cleanupDone = nil
	p.cleanupMu.Unlock()
	<-done
	p.cleanupMu.Lock()
	p.cleanupPending = 0
	p.cleanupMu.Unlock()
}

// expireAsync atomically reserves bounded cleanup ownership before detaching
// every due session. The generation ownership invariant makes queue exhaustion
// unreachable; violating it is a terminal configuration error, never a reason
// to leave an expired session routable.
func (p *SessionPool) expireAsync(now time.Time) (int, error) {
	if p == nil {
		return 0, errSessionConfig
	}
	p.cleanupMu.Lock()
	defer p.cleanupMu.Unlock()
	if p.cleanupQueue == nil || p.cleanupPending > cap(p.cleanupQueue) {
		return 0, errSessionConfig
	}
	sessions, ok := p.detachExpiredWithin(now, cap(p.cleanupQueue)-p.cleanupPending)
	if !ok {
		return 0, errSessionConfig
	}
	p.cleanupPending += len(sessions)
	for _, session := range sessions {
		p.cleanupQueue <- session
	}
	return len(sessions), nil
}

func (p *SessionPool) acquireSendLease(uid string) bool {
	if p == nil || uid == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	session := p.online[uid]
	if session == nil || !session.snapshot.TrafficReady || !session.snapshot.Deadline.After(p.clock.Now()) {
		return false
	}
	current := p.sendLeases[uid]
	if current == ^uint32(0) {
		return false
	}
	p.sendLeases[uid] = current + 1
	return true
}

// acquireSendAndCorrelationLease atomically protects the sender until its
// final SEND result and, for a sampled message, one exact recipient until the
// verifier closes that correlation. The second lease is deliberately sparse:
// exact one-percent sampling bounds both maps independently of total traffic.
func (p *SessionPool) acquireSendAndCorrelationLease(intent TrafficIntent, at time.Time) bool {
	if p == nil || intent.Logical.Sender == "" ||
		(intent.correlationRecipient != "" && intent.Logical.ClientMsgNo == "") {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	sender := p.online[intent.Logical.Sender]
	if !sessionSendEligibleAt(sender, p.sendLeases[intent.Logical.Sender], at) {
		return false
	}
	if p.sendLeases[intent.Logical.Sender] == ^uint32(0) {
		return false
	}
	if intent.correlationRecipient != "" {
		if intent.correlationRecipient == intent.Logical.Sender || p.correlationLeases[intent.Logical.ClientMsgNo] != "" ||
			p.correlationLeaseCounts[intent.correlationRecipient] == ^uint32(0) {
			return false
		}
		recipient := p.online[intent.correlationRecipient]
		if recipient == nil || !recipient.snapshot.TrafficReady || !recipient.snapshot.Deadline.After(at) {
			return false
		}
	}
	p.sendLeases[intent.Logical.Sender]++
	if intent.correlationRecipient != "" {
		p.correlationLeases[intent.Logical.ClientMsgNo] = intent.correlationRecipient
		p.correlationLeaseCounts[intent.correlationRecipient]++
	}
	return true
}

func (p *SessionPool) releaseSendLease(uid string) bool {
	if p == nil || uid == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.sendLeases[uid]
	if current == 0 {
		return false
	}
	if current == 1 {
		delete(p.sendLeases, uid)
	} else {
		p.sendLeases[uid] = current - 1
	}
	return true
}

func (p *SessionPool) releaseCorrelationLease(clientMsgNo string) bool {
	if p == nil || clientMsgNo == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	uid := p.correlationLeases[clientMsgNo]
	if uid == "" {
		return false
	}
	current := p.correlationLeaseCounts[uid]
	if current == 0 {
		return false
	}
	delete(p.correlationLeases, clientMsgNo)
	if current == 1 {
		delete(p.correlationLeaseCounts, uid)
	} else {
		p.correlationLeaseCounts[uid] = current - 1
	}
	return true
}

// releaseAllCorrelationLeases clears only generation-local observation
// ownership after every session drain has joined. A successful SENDACK can
// outlive inflight SEND ownership while its sampled RECV is still pending, so
// Engine.Stop must not leave that bounded lease in the next generation.
func (p *SessionPool) releaseAllCorrelationLeases() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.correlationLeases = make(map[string]string)
	p.correlationLeaseCounts = make(map[string]uint32)
	p.mu.Unlock()
}

// runExpiryCleanup owns all asynchronous transport teardown for one Engine
// generation. It releases the queue reservation before the closing tombstone,
// so replacement admission can never overbook cleanup capacity.
func (p *SessionPool) runExpiryCleanup(queue <-chan *onlineSession, done chan<- struct{}) {
	defer close(done)
	for session := range queue {
		_ = p.finishDetachedTransport(session)
		p.cleanupMu.Lock()
		p.cleanupPending--
		p.cleanupMu.Unlock()
		p.finishClosing(session.snapshot.UID, session)
	}
}

// detachExpiredWithin moves every currently due session out of routing only
// when the caller has reserved enough cleanup entries for the complete set.
func (p *SessionPool) detachExpiredWithin(now time.Time, capacity int) ([]*onlineSession, bool) {
	p.mu.Lock()
	due := make([]string, 0)
	for uid, session := range p.online {
		if !session.snapshot.Deadline.After(now) && p.sendLeases[uid] == 0 && p.correlationLeaseCounts[uid] == 0 {
			due = append(due, uid)
		}
	}
	if len(due) > capacity {
		p.mu.Unlock()
		return nil, false
	}
	sort.Strings(due)
	sessions := make([]*onlineSession, 0, len(due))
	for _, uid := range due {
		session := p.online[uid]
		p.removeOnlineLocked(uid, session.snapshot.UserIndex)
		p.closing[uid] = session
		p.claimCloseReasonLocked(session, sessionCloseReasonExpired)
		sessions = append(sessions, session)
	}
	p.mu.Unlock()
	for _, session := range sessions {
		session.cancel()
	}
	return sessions, true
}

// detachExpired removes due sessions from online routing and cancels their
// drains without waiting for transport cleanup. The closing tombstone keeps
// each UID owned until finishDetachedSessions completes.
func (p *SessionPool) detachExpired(now time.Time) []*onlineSession {
	if p == nil {
		return nil
	}
	due := p.dueSessionUIDs(now)
	sessions := make([]*onlineSession, 0, len(due))
	for _, uid := range due {
		if session := p.detachSession(uid, sessionCloseReasonExpired); session != nil {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func (p *SessionPool) dueSessionUIDs(now time.Time) []string {
	p.mu.RLock()
	due := make([]string, 0)
	for uid, session := range p.online {
		if !session.snapshot.Deadline.After(now) && p.sendLeases[uid] == 0 && p.correlationLeaseCounts[uid] == 0 {
			due = append(due, uid)
		}
	}
	p.mu.RUnlock()
	sort.Strings(due)
	return due
}

// Logout removes online admission, cancels and closes the socket, joins its
// ordered drain and heartbeat, and only then releases recipient monotonic state.
func (p *SessionPool) Logout(uid string) error {
	return p.logout(uid, sessionCloseReasonExplicitLogout)
}

func (p *SessionPool) logout(uid string, reason sessionCloseReason) error {
	if p == nil {
		return errSessionOffline
	}
	session := p.detachSession(uid, reason)
	if session == nil {
		return errSessionOffline
	}
	return p.finishDetachedSession(session)
}

// detachSession moves one traffic-ready UID into its closing tombstone before
// cancellation, so neither routing nor replacement can overlap old cleanup.
func (p *SessionPool) detachSession(uid string, reason sessionCloseReason) *onlineSession {
	p.mu.Lock()
	session := p.online[uid]
	if session != nil {
		p.removeOnlineLocked(uid, session.snapshot.UserIndex)
		p.closing[uid] = session
		p.claimCloseReasonLocked(session, reason)
	}
	p.mu.Unlock()
	if session == nil {
		return nil
	}
	session.cancel()
	return session
}

// finishDetachedSessions preserves deterministic direct Expire and Logout
// semantics without using the generation's asynchronous cleanup loop.
func (p *SessionPool) finishDetachedSessions(sessions []*onlineSession) {
	for _, session := range sessions {
		_ = p.finishDetachedSession(session)
	}
}

// finishDetachedSession closes transport, joins both per-session loops,
// releases verifier state, and only then removes the closing tombstone.
func (p *SessionPool) finishDetachedSession(session *onlineSession) error {
	closeErr := p.finishDetachedTransport(session)
	p.finishClosing(session.snapshot.UID, session)
	return closeErr
}

// finishDetachedTransport joins transport-owned work and releases verifier
// state while the caller retains the session's closing tombstone.
func (p *SessionPool) finishDetachedTransport(session *onlineSession) error {
	closeErr := session.client.Close()
	if closeErr != nil {
		p.recordTransportCloseFailure()
	}
	<-session.done
	<-session.heartbeatDone
	p.verifier.ReleaseRecipient(session.snapshot.UID)
	return closeErr
}

// CloseAll performs the same joined logout boundary for every online session.
func (p *SessionPool) CloseAll() error {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	uids := make([]string, 0, len(p.online))
	for uid := range p.online {
		uids = append(uids, uid)
	}
	p.mu.RUnlock()
	sort.Strings(uids)
	var result error
	for _, uid := range uids {
		if err := p.logout(uid, sessionCloseReasonGenerationStop); err != nil && !errors.Is(err, errSessionOffline) {
			result = errors.Join(result, err)
		}
	}
	p.mu.RLock()
	closing := make([]<-chan struct{}, 0, len(p.closing))
	for _, session := range p.closing {
		closing = append(closing, session.done)
	}
	p.mu.RUnlock()
	for _, done := range closing {
		<-done
	}
	return result
}

// Snapshot aggregates queue gauges without exposing online identities.
func (p *SessionPool) Snapshot() SessionPoolSnapshot {
	snapshot, _ := p.SnapshotContext(context.Background())
	return snapshot
}

// SnapshotContext is the cancelable aggregate used by Engine control calls.
func (p *SessionPool) SnapshotContext(ctx context.Context) (SessionPoolSnapshot, error) {
	if p == nil {
		return SessionPoolSnapshot{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionPoolSnapshot{}, err
	}
	p.mu.RLock()
	snapshot := SessionPoolSnapshot{
		Online: len(p.online), Starting: len(p.starting), Closing: len(p.closing), ReadErrors: p.readErrors, VerificationErrors: p.verificationErrors,
		FactoryFailed: p.factoryFailed, FactoryCanceled: p.factoryCanceled,
		ConnectStarted: p.connectStarted, ConnectCompleted: p.connectCompleted, ConnectFailed: p.connectFailed, ConnectCanceled: p.connectCanceled,
		SyncStarted: p.syncStarted, SyncCompleted: p.syncCompleted, SyncFailed: p.syncFailed, SyncCanceled: p.syncCanceled,
		CloseReasons:          p.closeReasons,
		GatewayConnectLatency: p.gatewayLatency, ConversationSyncLatency: p.syncLatency,
		ConversationSyncThresholds: p.syncThresholds,
		SendPendingToWriteLatency:  p.sendPendingToWriteLatency,
		SendWriteToAckLatency:      p.sendWriteToAckLatency,
	}
	clients := make([]SessionClient, 0, len(p.online))
	for _, session := range p.online {
		if session.snapshot.TrafficReady {
			snapshot.TrafficReady++
		}
		clients = append(clients, session.client)
	}
	p.mu.RUnlock()
	for _, client := range clients {
		queue, err := client.QueueSnapshot(ctx)
		if err != nil {
			return SessionPoolSnapshot{}, err
		}
		snapshot.QueueDepth += queue.Depth
		snapshot.QueueCapacity += queue.Capacity
		snapshot.TransportInflight += queue.Inflight
	}
	snapshot.TransportAdmissionRejected = p.transportAdmissionRejected.Load()
	return snapshot, nil
}

// setEngineObservers is wired once before sessions start. Keeping it private
// prevents control-plane callers from replacing retry or scheduler ownership.
func (p *SessionPool) setEngineObservers(
	sendack func(string, *frame.SendackPacket, error),
	asyncSendError func(string, uint64, string),
) error {
	if p == nil || sendack == nil || asyncSendError == nil {
		return errSessionConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.online) != 0 || p.onSendack != nil || p.onAsyncSendError != nil {
		return errSessionConfig
	}
	p.onSendack = sendack
	p.onAsyncSendError = asyncSendError
	return nil
}

func (p *SessionPool) resetRuntime() error {
	if p == nil {
		return errSessionConfig
	}
	p.cleanupMu.Lock()
	cleanupStopped := p.cleanupQueue == nil && p.cleanupDone == nil && p.cleanupPending == 0
	p.cleanupMu.Unlock()
	if !cleanupStopped {
		return errSessionOnline
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.online) != 0 || len(p.starting) != 0 || len(p.closing) != 0 || len(p.sendLeases) != 0 ||
		len(p.correlationLeases) != 0 || len(p.correlationLeaseCounts) != 0 {
		return errSessionOnline
	}
	p.ownershipCapacity = 0
	p.readErrors = 0
	p.verificationErrors = 0
	p.factoryFailed = 0
	p.factoryCanceled = 0
	p.connectStarted = 0
	p.connectCompleted = 0
	p.connectFailed = 0
	p.connectCanceled = 0
	p.syncStarted = 0
	p.syncCompleted = 0
	p.syncFailed = 0
	p.syncCanceled = 0
	p.closeReasons = SessionCloseReasonSnapshot{}
	p.gatewayLatency = newWorkerHistogramSnapshot()
	p.syncLatency = newWorkerHistogramSnapshot()
	p.sendPendingToWriteLatency = newWorkerHistogramSnapshot()
	p.sendWriteToAckLatency = newWorkerHistogramSnapshot()
	p.transportAdmissionRejected.Store(0)
	p.onlineByIndex = make(map[uint64]*onlineSession)
	p.onlineGroupMembers = make([][]*onlineSession, p.catalog.Count())
	p.sendLeases = make(map[string]uint32)
	p.correlationLeases = make(map[string]string)
	p.correlationLeaseCounts = make(map[string]uint32)
	return nil
}

func (p *SessionPool) drain(ctx context.Context, session *onlineSession) {
	defer close(session.done)
	for {
		packet, timing, err := readSessionFrame(ctx, session.client)
		if err != nil {
			info, classified := session.client.ReadErrorInfo(err)
			if classified && info.Kind == wkproto.ReadErrorNonTerminal {
				if info.ClientMsgNo == "" {
					_ = p.verifier.evidence.Record(EvidenceEvent{
						Class: FailureClassHarness, Stage: EvidenceStageReceive, Code: FailureCodeSessionReadFailed,
					})
				} else if info.ClientSeq == 0 {
					_ = p.verifier.evidence.Record(EvidenceEvent{
						Class: FailureClassHarness, Stage: EvidenceStageReceive, Code: FailureCodeSessionReadFailed,
					})
				} else if p.onAsyncSendError != nil {
					p.onAsyncSendError(session.snapshot.UID, info.ClientSeq, info.ClientMsgNo)
				}
				continue
			}
			if ctx.Err() == nil {
				p.detachUnexpected(session, classified && info.Kind == wkproto.ReadErrorTerminal)
			}
			return
		}
		switch packet := packet.(type) {
		case *frame.SendackPacket:
			observedAt := timing.ObservedAt
			if observedAt.IsZero() {
				observedAt = p.clock.Now()
			}
			p.recordSendTiming(timing)
			verificationErr := p.verifier.HandleSendackAt(packet, observedAt)
			if !p.verifier.correlationOutstanding(packet.ClientMsgNo) {
				p.releaseCorrelationLease(packet.ClientMsgNo)
			}
			if verificationErr != nil {
				p.mu.Lock()
				p.verificationErrors++
				p.mu.Unlock()
			}
			if p.onSendack != nil {
				p.onSendack(session.snapshot.UID, packet, verificationErr)
			}
		case *frame.RecvPacket:
			if err := p.verifier.HandleRecvAt(ctx, session.snapshot.UID, packet, session.client, p.clock.Now); err != nil {
				p.mu.Lock()
				p.verificationErrors++
				p.mu.Unlock()
			}
			if !p.verifier.correlationOutstanding(packet.ClientMsgNo) {
				p.releaseCorrelationLease(packet.ClientMsgNo)
			}
		}
	}
}

func readSessionFrame(ctx context.Context, client SessionClient) (frame.Frame, SessionFrameTiming, error) {
	if observer, ok := client.(sessionFrameTimingObserver); ok {
		return observer.ReadFrameTiming(ctx)
	}
	if observer, ok := client.(sessionFrameObserver); ok {
		packet, observedAt, err := observer.ReadFrameObserved(ctx)
		return packet, SessionFrameTiming{ObservedAt: observedAt}, err
	}
	packet, err := client.ReadFrame(ctx)
	return packet, SessionFrameTiming{}, err
}

func (p *SessionPool) recordSendTiming(timing SessionFrameTiming) {
	if p == nil || timing.PendingStartedAt.IsZero() || timing.WriteStartedAt.IsZero() || timing.ObservedAt.IsZero() ||
		timing.WriteStartedAt.Before(timing.PendingStartedAt) || timing.ObservedAt.Before(timing.WriteStartedAt) {
		return
	}
	p.mu.Lock()
	recordWorkerLatency(&p.sendPendingToWriteLatency, timing.WriteStartedAt.Sub(timing.PendingStartedAt))
	recordWorkerLatency(&p.sendWriteToAckLatency, timing.ObservedAt.Sub(timing.WriteStartedAt))
	p.mu.Unlock()
}

// heartbeat keeps an otherwise idle real client visible to the authority
// presence directory. A control-write failure closes the socket so the sole
// receive drain publishes the existing bounded unexpected-session evidence and
// the engine replaces the missing online session.
func (p *SessionPool) heartbeat(ctx context.Context, session *onlineSession) {
	defer close(session.heartbeatDone)
	for {
		if err := p.heartbeatSleep(ctx, p.heartbeatInterval); err != nil {
			return
		}
		pingCtx, cancel := context.WithTimeout(ctx, p.heartbeatTimeout)
		err := session.client.Ping(pingCtx)
		cancel()
		if err == nil {
			continue
		}
		if ctx.Err() == nil {
			p.mu.Lock()
			claimed := p.online[session.snapshot.UID] == session &&
				p.claimCloseReasonLocked(session, sessionCloseReasonHeartbeatFailed)
			p.mu.Unlock()
			if claimed {
				if err := session.client.Close(); err != nil {
					p.recordTransportCloseFailure()
					p.detachHeartbeatCloseFailure(session)
				}
			}
		}
		return
	}
}

// detachHeartbeatCloseFailure completes ownership cleanup when a failed PING
// is followed by a transport Close error. It runs on the heartbeat goroutine,
// so it joins only the read drain; the caller's deferred close publishes the
// heartbeatDone boundary after this method returns.
func (p *SessionPool) detachHeartbeatCloseFailure(session *onlineSession) {
	p.mu.Lock()
	if p.online[session.snapshot.UID] != session {
		p.mu.Unlock()
		return
	}
	p.removeOnlineLocked(session.snapshot.UID, session.snapshot.UserIndex)
	p.closing[session.snapshot.UID] = session
	p.mu.Unlock()

	session.cancel()
	<-session.done
	p.verifier.ReleaseRecipient(session.snapshot.UID)
	p.finishClosing(session.snapshot.UID, session)
}

func (p *SessionPool) detachUnexpected(session *onlineSession, remoteTerminal bool) {
	class := FailureClassHarness
	code := FailureCodeSessionReadFailed
	p.mu.Lock()
	if p.online[session.snapshot.UID] != session {
		p.mu.Unlock()
		return
	}
	reason := session.closeInitiator
	if reason == sessionCloseReasonNone {
		reason = sessionCloseReasonReadFailed
		if remoteTerminal {
			reason = sessionCloseReasonRemoteTerminal
			class = FailureClassReceive
			code = FailureCodeSessionRemoteTerminal
		}
		p.claimCloseReasonLocked(session, reason)
	}
	// Evidence is bounded in-process state. Recording it while ownership is
	// locked makes the terminal transition observable in one direction only:
	// callers may see evidence before removal, but never offline before evidence.
	_ = p.verifier.evidence.Record(EvidenceEvent{Class: class, Stage: EvidenceStageReceive, Code: code})
	p.removeOnlineLocked(session.snapshot.UID, session.snapshot.UserIndex)
	p.closing[session.snapshot.UID] = session
	p.readErrors++
	p.mu.Unlock()

	session.cancel()
	if err := session.client.Close(); err != nil {
		p.recordTransportCloseFailure()
	}
	<-session.heartbeatDone
	p.verifier.ReleaseRecipient(session.snapshot.UID)
	p.finishClosing(session.snapshot.UID, session)
}

func (p *SessionPool) claimCloseReasonLocked(session *onlineSession, reason sessionCloseReason) bool {
	if session == nil || reason == sessionCloseReasonNone || session.closeInitiator != sessionCloseReasonNone {
		return false
	}
	session.closeInitiator = reason
	p.recordCloseReasonLocked(reason)
	return true
}

func (p *SessionPool) recordTransportCloseFailure() {
	p.mu.Lock()
	incrementSessionOutcome(&p.closeReasons.TransportCloseFailed)
	p.mu.Unlock()
}

func (p *SessionPool) recordCloseReasonLocked(reason sessionCloseReason) {
	switch reason {
	case sessionCloseReasonExpired:
		incrementSessionOutcome(&p.closeReasons.Expired)
	case sessionCloseReasonHeartbeatFailed:
		incrementSessionOutcome(&p.closeReasons.HeartbeatFailed)
	case sessionCloseReasonRemoteTerminal:
		incrementSessionOutcome(&p.closeReasons.RemoteTerminal)
	case sessionCloseReasonReadFailed:
		incrementSessionOutcome(&p.closeReasons.ReadFailed)
	case sessionCloseReasonGenerationStop:
		incrementSessionOutcome(&p.closeReasons.GenerationStop)
	case sessionCloseReasonExplicitLogout:
		incrementSessionOutcome(&p.closeReasons.ExplicitLogout)
	}
}

func (p *SessionPool) finishClosing(uid string, session *onlineSession) {
	p.mu.Lock()
	if p.closing[uid] == session {
		delete(p.closing, uid)
	}
	p.mu.Unlock()
}

func (p *SessionPool) removeOnlineLocked(uid string, userIndex uint64) {
	session := p.online[uid]
	if session != nil && session.groupIndex >= 0 {
		members := p.onlineGroupMembers[session.groupIndex]
		position := session.groupPosition
		if position >= 0 && position < len(members) && members[position] == session {
			last := len(members) - 1
			if position != last {
				moved := members[last]
				members[position] = moved
				moved.groupPosition = position
			}
			members[last] = nil
			p.onlineGroupMembers[session.groupIndex] = members[:last]
		}
		session.groupPosition = -1
	}
	delete(p.online, uid)
	delete(p.onlineByIndex, userIndex)
}

func (p *SessionPool) onlineGroupMember(group Group, ordinal uint64, requireRecipient bool) (SessionLogin, bool) {
	return p.onlineGroupMemberExcluding(group, ordinal, requireRecipient, time.Time{}, nil)
}

func (p *SessionPool) onlineGroupMemberExcluding(group Group, ordinal uint64, requireRecipient bool, sendEligibleAt time.Time, excluded func(string) bool) (SessionLogin, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if group.Index >= uint64(len(p.onlineGroupMembers)) {
		return SessionLogin{}, false
	}
	members := p.onlineGroupMembers[group.Index]
	needed := 1
	if requireRecipient {
		needed = 2
	}
	if len(members) < needed {
		return SessionLogin{}, false
	}
	start := ordinal % uint64(len(members))
	for offset := 0; offset < len(members); offset++ {
		session := members[(start+uint64(offset))%uint64(len(members))]
		if !sendEligibleAt.IsZero() && !sessionSendEligibleAt(session, p.sendLeases[session.snapshot.UID], sendEligibleAt) {
			continue
		}
		if excluded != nil && excluded(session.snapshot.UID) {
			continue
		}
		return SessionLogin{
			UID: session.snapshot.UID, UserIndex: session.snapshot.UserIndex, LoginOrdinal: session.snapshot.LoginOrdinal,
		}, true
	}
	return SessionLogin{}, false
}

// onlineGroupCorrelationRecipient returns one deterministic local recipient
// distinct from sender. The later atomic lease acquisition revalidates this
// snapshot before the SEND enters the engine work heap.
func (p *SessionPool) onlineGroupCorrelationRecipient(group Group, sender string, ordinal uint64, at time.Time) (string, bool) {
	if p == nil || sender == "" || group.Index >= uint64(len(p.onlineGroupMembers)) {
		return "", false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	members := p.onlineGroupMembers[group.Index]
	if len(members) < 2 {
		return "", false
	}
	start := ordinal % uint64(len(members))
	for offset := 0; offset < len(members); offset++ {
		session := members[(start+uint64(offset))%uint64(len(members))]
		if session.snapshot.UID == sender || !session.snapshot.TrafficReady || !session.snapshot.Deadline.After(at) {
			continue
		}
		return session.snapshot.UID, true
	}
	return "", false
}

func (p *SessionPool) onlineGroupMemberInCategory(category GroupCategory, ordinal uint64, requireRecipient bool, owner uint64) (SessionLogin, uint64, bool) {
	return p.onlineGroupMemberInCategoryExcluding(category, ordinal, requireRecipient, owner, time.Time{}, nil)
}

func (p *SessionPool) onlineGroupMemberInCategoryExcluding(category GroupCategory, ordinal uint64, requireRecipient bool, owner uint64, sendEligibleAt time.Time, excluded func(string) bool) (SessionLogin, uint64, bool) {
	start, count, ok := p.catalog.categoryRange(category)
	if !ok {
		return SessionLogin{}, 0, false
	}
	needed := 1
	if requireRecipient {
		needed = 2
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	first := ordinal % uint64(count)
	for offset := 0; offset < count; offset++ {
		groupIndex := start + (first+uint64(offset))%uint64(count)
		groupOwner, err := p.catalog.GroupOwner(groupIndex)
		if err != nil || groupOwner != owner {
			continue
		}
		members := p.onlineGroupMembers[groupIndex]
		if len(members) < needed {
			continue
		}
		memberStart := ordinal % uint64(len(members))
		for memberOffset := 0; memberOffset < len(members); memberOffset++ {
			session := members[(memberStart+uint64(memberOffset))%uint64(len(members))]
			if !sendEligibleAt.IsZero() && !sessionSendEligibleAt(session, p.sendLeases[session.snapshot.UID], sendEligibleAt) {
				continue
			}
			if excluded != nil && excluded(session.snapshot.UID) {
				continue
			}
			return SessionLogin{
				UID: session.snapshot.UID, UserIndex: session.snapshot.UserIndex, LoginOrdinal: session.snapshot.LoginOrdinal,
			}, groupIndex, true
		}
	}
	return SessionLogin{}, 0, false
}

func (p *SessionPool) tokenForUID(uid string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/chat-lifecycle/connect-token/v1"))
	_, _ = h.Write(p.identity.rootKey[:])
	_, _ = h.Write([]byte(uid))
	sum := h.Sum(nil)
	return "wkt-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:20])
}

type sessionLoginConnector struct {
	client   SessionClient
	deviceID string
}

func (c sessionLoginConnector) Connect(ctx context.Context, uid string) error {
	return c.client.Connect(ctx, uid, c.deviceID)
}

func sleepSessionHeartbeat(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
