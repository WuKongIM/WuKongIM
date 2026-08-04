package chatlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	errSessionConfig  = errors.New("chat lifecycle session: configuration is incomplete")
	errSessionOnline  = errors.New("chat lifecycle session: UID is already online")
	errSessionOffline = errors.New("chat lifecycle session: UID is offline")
)

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
	Send(context.Context, *frame.SendPacket) error
	AckRecv(context.Context, *frame.RecvackPacket) error
	Close() error
	QueueSnapshot() SessionQueueSnapshot
	ReadErrorInfo(error) (wkproto.ReadErrorInfo, bool)
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

func (a *WKProtoSessionAdapter) Send(ctx context.Context, packet *frame.SendPacket) error {
	return a.client.Send(ctx, packet)
}

func (a *WKProtoSessionAdapter) AckRecv(ctx context.Context, ack *frame.RecvackPacket) error {
	return a.client.RecvAck(ctx, ack.MessageID, ack.MessageSeq)
}

func (a *WKProtoSessionAdapter) Close() error { return a.client.Close() }

func (a *WKProtoSessionAdapter) QueueSnapshot() SessionQueueSnapshot {
	snapshot := a.client.QueueSnapshot()
	return SessionQueueSnapshot{
		Depth:    snapshot.InnerRecvDepth + snapshot.AdapterDepth,
		Capacity: snapshot.InnerRecvCapacity + snapshot.AdapterCapacity,
		Inflight: snapshot.PublicationCurrent,
	}
}

func (a *WKProtoSessionAdapter) ReadErrorInfo(err error) (wkproto.ReadErrorInfo, bool) {
	return wkproto.ReadErrorInfoOf(err)
}

var _ SessionClient = (*WKProtoSessionAdapter)(nil)

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
	// StartingCapacity bounds concurrent CONNECT plus full-sync operations.
	StartingCapacity int

	// OnSendack runs inline after verifier processing and may apply bounded
	// backpressure; the receive drain never drops a completion to avoid waiting.
	OnSendack func(uid string, ack *frame.SendackPacket, verificationErr error)
	// OnAsyncSendError transfers one non-terminal result-queue error to the
	// engine that owns retry state. The raw transport error is never exposed.
	OnAsyncSendError func(uid, clientMsgNo string)
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

// SessionPoolSnapshot contains only bounded aggregate ownership and queue data.
type SessionPoolSnapshot struct {
	Online             int
	Starting           int
	Closing            int
	TrafficReady       int
	QueueDepth         int
	QueueCapacity      int
	TransportInflight  int
	ReadErrors         uint64
	VerificationErrors uint64
}

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
	groupIndex            int
	groupPosition         int
	relationshipsObserved bool
}

// SessionPool owns only currently online clients. A UID has exactly one
// receive drain, and logout joins that drain before releasing verifier state.
type SessionPool struct {
	identity         *IdentitySpace
	schedule         ScheduleModel
	catalog          GroupCatalog
	factory          SessionClientFactory
	syncer           ConversationSyncer
	verifier         *Verifier
	clock            SessionClock
	deviceID         string
	startingCapacity int
	onSendack        func(string, *frame.SendackPacket, error)
	onAsyncSendError func(string, string)

	mu                 sync.RWMutex
	online             map[string]*onlineSession
	onlineByIndex      map[uint64]*onlineSession
	onlineGroupMembers [][]*onlineSession
	starting           map[string]struct{}
	closing            map[string]*onlineSession
	readErrors         uint64
	verificationErrors uint64
}

// NewSessionPool validates all lifecycle seams before any client is created.
func NewSessionPool(config SessionPoolConfig) (*SessionPool, error) {
	if config.Identity == nil || config.Schedule.identity != config.Identity || config.Catalog.identity != config.Identity || config.Factory == nil ||
		config.Syncer == nil || config.Verifier == nil || config.Clock == nil || config.DeviceID == "" ||
		config.StartingCapacity <= 0 || config.StartingCapacity > maxVerifierCapacity {
		return nil, errSessionConfig
	}
	return &SessionPool{
		identity: config.Identity, schedule: config.Schedule, catalog: config.Catalog, factory: config.Factory,
		syncer: config.Syncer, verifier: config.Verifier, clock: config.Clock,
		deviceID: config.DeviceID, startingCapacity: config.StartingCapacity, onSendack: config.OnSendack,
		onAsyncSendError:   config.OnAsyncSendError,
		online:             make(map[string]*onlineSession),
		onlineByIndex:      make(map[uint64]*onlineSession),
		onlineGroupMembers: make([][]*onlineSession, config.Catalog.Count()),
		starting:           make(map[string]struct{}),
		closing:            make(map[string]*onlineSession),
	}, nil
}

// Login creates a fresh connection, completes CONNECT then version-zero full
// sync through RunLoginSync, and starts the sole ordered receive drain only
// after the session is traffic-ready.
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
		return SessionSnapshot{}, err
	}
	connector := sessionLoginConnector{client: client, deviceID: p.deviceID}
	result, err := RunLoginSync(ctx, login.UID, connector, p.syncer, p.clock.Now)
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
		snapshot: snapshot, client: client, cancel: cancel, done: make(chan struct{}),
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
	return snapshot, nil
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

// Send writes through the currently owned WKProto connection only.
func (p *SessionPool) Send(ctx context.Context, uid string, packet *frame.SendPacket) error {
	p.mu.RLock()
	session := p.online[uid]
	p.mu.RUnlock()
	if session == nil {
		return errSessionOffline
	}
	return session.client.Send(ctx, packet)
}

// Expire logs out every due session. Sorting makes simultaneous fake-clock
// expiration replayable while retaining only the current online UID slice.
func (p *SessionPool) Expire(now time.Time) int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	due := make([]string, 0)
	for uid, session := range p.online {
		if !session.snapshot.Deadline.After(now) {
			due = append(due, uid)
		}
	}
	p.mu.RUnlock()
	sort.Strings(due)
	for _, uid := range due {
		_ = p.Logout(uid)
	}
	return len(due)
}

// Logout removes online admission, cancels and closes the socket, joins its
// sole ordered drain, and only then releases recipient monotonic state.
func (p *SessionPool) Logout(uid string) error {
	if p == nil {
		return errSessionOffline
	}
	p.mu.Lock()
	session := p.online[uid]
	if session != nil {
		p.removeOnlineLocked(uid, session.snapshot.UserIndex)
		p.closing[uid] = session
	}
	p.mu.Unlock()
	if session == nil {
		return errSessionOffline
	}
	session.cancel()
	closeErr := session.client.Close()
	<-session.done
	p.verifier.ReleaseRecipient(uid)
	p.finishClosing(uid, session)
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
		if err := p.Logout(uid); err != nil && !errors.Is(err, errSessionOffline) {
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
	if p == nil {
		return SessionPoolSnapshot{}
	}
	p.mu.RLock()
	snapshot := SessionPoolSnapshot{
		Online: len(p.online), Starting: len(p.starting), Closing: len(p.closing), ReadErrors: p.readErrors, VerificationErrors: p.verificationErrors,
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
		queue := client.QueueSnapshot()
		snapshot.QueueDepth += queue.Depth
		snapshot.QueueCapacity += queue.Capacity
		snapshot.TransportInflight += queue.Inflight
	}
	return snapshot
}

// setEngineObservers is wired once before sessions start. Keeping it private
// prevents control-plane callers from replacing retry or scheduler ownership.
func (p *SessionPool) setEngineObservers(
	sendack func(string, *frame.SendackPacket, error),
	asyncSendError func(string, string),
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
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.online) != 0 || len(p.starting) != 0 || len(p.closing) != 0 {
		return errSessionOnline
	}
	p.readErrors = 0
	p.verificationErrors = 0
	p.onlineByIndex = make(map[uint64]*onlineSession)
	p.onlineGroupMembers = make([][]*onlineSession, p.catalog.Count())
	return nil
}

func (p *SessionPool) drain(ctx context.Context, session *onlineSession) {
	defer close(session.done)
	for {
		packet, err := session.client.ReadFrame(ctx)
		if err != nil {
			info, classified := session.client.ReadErrorInfo(err)
			if classified && info.Kind == wkproto.ReadErrorNonTerminal {
				if info.ClientMsgNo == "" {
					_ = p.verifier.evidence.Record(EvidenceEvent{
						Class: FailureClassHarness, Stage: EvidenceStageReceive, Code: FailureCodeSessionReadFailed,
					})
				} else if p.onAsyncSendError != nil {
					p.onAsyncSendError(session.snapshot.UID, info.ClientMsgNo)
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
			verificationErr := p.verifier.HandleSendack(packet)
			if verificationErr != nil {
				p.mu.Lock()
				p.verificationErrors++
				p.mu.Unlock()
			}
			if p.onSendack != nil {
				p.onSendack(session.snapshot.UID, packet, verificationErr)
			}
		case *frame.RecvPacket:
			if err := p.verifier.HandleRecv(ctx, session.snapshot.UID, packet, session.client); err != nil {
				p.mu.Lock()
				p.verificationErrors++
				p.mu.Unlock()
			}
		}
	}
}

func (p *SessionPool) detachUnexpected(session *onlineSession, remoteTerminal bool) {
	class := FailureClassHarness
	code := FailureCodeSessionReadFailed
	if remoteTerminal {
		class = FailureClassReceive
		code = FailureCodeSessionRemoteTerminal
	}
	p.mu.Lock()
	if p.online[session.snapshot.UID] != session {
		p.mu.Unlock()
		return
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
	_ = session.client.Close()
	p.verifier.ReleaseRecipient(session.snapshot.UID)
	p.finishClosing(session.snapshot.UID, session)
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
	session := members[ordinal%uint64(len(members))]
	return SessionLogin{
		UID: session.snapshot.UID, UserIndex: session.snapshot.UserIndex, LoginOrdinal: session.snapshot.LoginOrdinal,
	}, true
}

func (p *SessionPool) onlineGroupMemberInCategory(category GroupCategory, ordinal uint64, requireRecipient bool, owner uint64) (SessionLogin, uint64, bool) {
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
		session := members[ordinal%uint64(len(members))]
		return SessionLogin{
			UID: session.snapshot.UID, UserIndex: session.snapshot.UserIndex, LoginOrdinal: session.snapshot.LoginOrdinal,
		}, groupIndex, true
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
