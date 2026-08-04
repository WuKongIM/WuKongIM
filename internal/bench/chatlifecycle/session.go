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
}

// SessionClientFactory constructs a client with the deterministic per-UID
// token already installed in its CONNECT configuration.
type SessionClientFactory interface {
	NewSession(uid, token string) (SessionClient, error)
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
}

// SessionLogin binds a reconstructed identity to one global login ordinal.
type SessionLogin struct {
	UID          string
	UserIndex    uint64
	LoginOrdinal uint64
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
	TrafficReady       int
	QueueDepth         int
	QueueCapacity      int
	TransportInflight  int
	ReadErrors         uint64
	VerificationErrors uint64
}

type onlineSession struct {
	snapshot SessionSnapshot
	client   SessionClient
	cancel   context.CancelFunc
	done     chan struct{}
}

// SessionPool owns only currently online clients. A UID has exactly one
// receive drain, and logout joins that drain before releasing verifier state.
type SessionPool struct {
	identity         *IdentitySpace
	schedule         ScheduleModel
	factory          SessionClientFactory
	syncer           ConversationSyncer
	verifier         *Verifier
	clock            SessionClock
	deviceID         string
	startingCapacity int
	onSendack        func(string, *frame.SendackPacket, error)

	mu                 sync.RWMutex
	online             map[string]*onlineSession
	starting           map[string]struct{}
	readErrors         uint64
	verificationErrors uint64
}

// NewSessionPool validates all lifecycle seams before any client is created.
func NewSessionPool(config SessionPoolConfig) (*SessionPool, error) {
	if config.Identity == nil || config.Schedule.identity != config.Identity || config.Factory == nil ||
		config.Syncer == nil || config.Verifier == nil || config.Clock == nil || config.DeviceID == "" ||
		config.StartingCapacity <= 0 || config.StartingCapacity > maxVerifierCapacity {
		return nil, errSessionConfig
	}
	return &SessionPool{
		identity: config.Identity, schedule: config.Schedule, factory: config.Factory,
		syncer: config.Syncer, verifier: config.Verifier, clock: config.Clock,
		deviceID: config.DeviceID, startingCapacity: config.StartingCapacity, onSendack: config.OnSendack,
		online: make(map[string]*onlineSession), starting: make(map[string]struct{}),
	}, nil
}

// Login creates a fresh connection, completes CONNECT then version-zero full
// sync through RunLoginSync, and starts the sole ordered receive drain only
// after the session is traffic-ready.
func (p *SessionPool) Login(ctx context.Context, login SessionLogin) (SessionSnapshot, error) {
	if p == nil || login.UID == "" || p.identity.UID(login.UserIndex) != login.UID {
		return SessionSnapshot{}, errSessionConfig
	}
	p.mu.Lock()
	_, online := p.online[login.UID]
	_, starting := p.starting[login.UID]
	if online || starting {
		p.mu.Unlock()
		return SessionSnapshot{}, errSessionOnline
	}
	if len(p.starting) >= p.startingCapacity {
		p.mu.Unlock()
		_ = p.verifier.evidence.Record(EvidenceEvent{
			Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: FailureCodeSessionLoginSaturated,
			Value: uint64(p.startingCapacity),
		})
		return SessionSnapshot{}, &RuntimeError{code: RuntimeFailureLoginSaturated}
	}
	p.starting[login.UID] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.starting, login.UID)
		p.mu.Unlock()
	}()

	schedule, err := p.schedule.Login(login.LoginOrdinal)
	if err != nil {
		return SessionSnapshot{}, err
	}
	client, err := p.factory.NewSession(login.UID, p.tokenForUID(login.UID))
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
	drainCtx, cancel := context.WithCancel(context.Background())
	session := &onlineSession{snapshot: snapshot, client: client, cancel: cancel, done: make(chan struct{})}
	p.mu.Lock()
	delete(p.starting, login.UID)
	p.online[login.UID] = session
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
		delete(p.online, uid)
	}
	p.mu.Unlock()
	if session == nil {
		return errSessionOffline
	}
	session.cancel()
	closeErr := session.client.Close()
	<-session.done
	p.verifier.ReleaseRecipient(uid)
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
	return result
}

// Snapshot aggregates queue gauges without exposing online identities.
func (p *SessionPool) Snapshot() SessionPoolSnapshot {
	if p == nil {
		return SessionPoolSnapshot{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	snapshot := SessionPoolSnapshot{
		Online: len(p.online), Starting: len(p.starting), ReadErrors: p.readErrors, VerificationErrors: p.verificationErrors,
	}
	for _, session := range p.online {
		if session.snapshot.TrafficReady {
			snapshot.TrafficReady++
		}
		queue := session.client.QueueSnapshot()
		snapshot.QueueDepth += queue.Depth
		snapshot.QueueCapacity += queue.Capacity
		snapshot.TransportInflight += queue.Inflight
	}
	return snapshot
}

// setSendackObserver is wired once by Engine before sessions start. Keeping it
// package-private prevents control-plane callers from replacing ownership mid-run.
func (p *SessionPool) setSendackObserver(observer func(string, *frame.SendackPacket, error)) error {
	if p == nil || observer == nil {
		return errSessionConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.online) != 0 || p.onSendack != nil {
		return errSessionConfig
	}
	p.onSendack = observer
	return nil
}

func (p *SessionPool) resetRuntime() error {
	if p == nil {
		return errSessionConfig
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.online) != 0 || len(p.starting) != 0 {
		return errSessionOnline
	}
	p.readErrors = 0
	p.verificationErrors = 0
	return nil
}

func (p *SessionPool) drain(ctx context.Context, session *onlineSession) {
	defer close(session.done)
	for {
		packet, err := session.client.ReadFrame(ctx)
		if err != nil {
			if ctx.Err() == nil {
				p.mu.Lock()
				p.readErrors++
				p.mu.Unlock()
				_ = p.verifier.evidence.Record(EvidenceEvent{
					Class: FailureClassHarness, Stage: EvidenceStageReceive, Code: FailureCodeSessionReadFailed,
				})
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
