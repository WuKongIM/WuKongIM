package workload

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	benchwkproto "github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	gatewayBalanceRoundRobin = "round_robin"
	defaultHeartbeatTimeout  = 5 * time.Second
)

// ReconnectConfig reserves reconnect policy shape for future workload phases.
type ReconnectConfig = model.ReconnectConfig

// ConnectionClient is the minimal WKProto client contract managed by workers.
type ConnectionClient interface {
	// Connect opens the client session for the supplied identity.
	Connect(ctx context.Context, uid, deviceID string) error
	// Close releases the underlying gateway connection.
	Close() error
}

// HeartbeatClient is implemented by clients that can send gateway heartbeat frames.
type HeartbeatClient interface {
	// Ping sends one heartbeat frame on the active session.
	Ping(ctx context.Context) error
}

// ConnectionUser identifies one simulated online WKProto session.
type ConnectionUser struct {
	// UID is the benchmark user identifier used in the WKProto connect packet.
	UID string
	// DeviceID is the benchmark device identifier used in the WKProto connect packet.
	DeviceID string
	// Token is the optional connect token used for this user.
	Token string
}

// ConnectionSession stores the active client session for one UID.
type ConnectionSession struct {
	// UID is the benchmark user identifier for this connection.
	UID string
	// DeviceID is the benchmark device identifier for this connection.
	DeviceID string
	// Token is the effective connect token used for this session.
	Token string
	// GatewayAddr is the selected gateway TCP address for this connection.
	GatewayAddr string
	// ConnectedAt records when the connect handshake completed.
	ConnectedAt time.Time
	// Client is the live WKProto client bound to this session.
	Client ConnectionClient

	cancelHeartbeat context.CancelFunc
}

// ConnectionManagerConfig controls benchmark WKProto connection creation.
type ConnectionManagerConfig struct {
	// Target optionally provides gateway addresses from the worker assignment.
	Target model.Target
	// GatewayAddrs overrides Target.Gateway.TCP.Addrs when non-empty.
	GatewayAddrs []string
	// GatewayBalance selects the gateway assignment algorithm; empty means round_robin.
	GatewayBalance string
	// ConnectRate limits connection attempts in operations per second; zero means unlimited.
	ConnectRate model.Rate
	// Reconnect is retained for future reconnect support; it must remain disabled in v1.
	Reconnect ReconnectConfig
	// Heartbeat controls optional client heartbeat pings for idle online sessions.
	Heartbeat model.HeartbeatConfig
	// Token is the default connect token applied when a user does not specify one.
	Token string
	// OperationTimeout is passed to production WKProto clients.
	OperationTimeout time.Duration
	// AckTimeout is passed to production WKProto clients for internal SENDACK tracking.
	AckTimeout time.Duration
	// Client optionally overrides the production WKProto client's per-session capacities.
	Client *model.WorkerClientConfig
	// TCPSource optionally supplies a finite local IPv4 address and port pool to the default client factory.
	TCPSource *model.TCPSourceConfig
	// ClientFactory overrides production WKProto client creation for tests.
	ClientFactory func(user ConnectionUser, addr string) (ConnectionClient, error)

	sleep   func(context.Context, time.Duration) error
	tcpDial tcpDialFunc
}

// ConnectionManager owns the UID-to-session map for worker gateway connections.
type ConnectionManager struct {
	cfg          ConnectionManagerConfig
	gatewayAddrs []string
	factory      func(user ConnectionUser, addr string) (ConnectionClient, error)
	sleep        func(context.Context, time.Duration) error
	rateLimiter  *connectRateLimiter
	sourceDialer *tcpSourceDialer

	mu          sync.Mutex
	nextGateway int
	sessions    map[string]*ConnectionSession
	metrics     *metrics.Registry
}

// NewConnectionManager validates config and creates an empty connection manager.
func NewConnectionManager(cfg ConnectionManagerConfig) (*ConnectionManager, error) {
	gatewayAddrs := normalizeGatewayAddrs(cfg.GatewayAddrs)
	if len(gatewayAddrs) == 0 {
		gatewayAddrs = normalizeGatewayAddrs(cfg.Target.Gateway.TCP.Addrs)
	}
	if len(gatewayAddrs) == 0 {
		return nil, fmt.Errorf("connection manager: at least one gateway tcp address is required")
	}
	balance := strings.TrimSpace(cfg.GatewayBalance)
	if balance == "" {
		balance = gatewayBalanceRoundRobin
	}
	if balance != gatewayBalanceRoundRobin {
		return nil, fmt.Errorf("connection manager: unsupported gateway balance %q", cfg.GatewayBalance)
	}
	if cfg.Reconnect.Enabled {
		return nil, fmt.Errorf("connection manager: reconnect is not implemented yet")
	}
	if cfg.Heartbeat.Enabled && cfg.Heartbeat.Interval <= 0 {
		return nil, fmt.Errorf("connection manager: heartbeat interval must be greater than zero")
	}
	if err := validateWorkerClientConfig(cfg.Client); err != nil {
		return nil, err
	}
	sourceDialer, err := newTCPSourceDialer(cfg.TCPSource, cfg.tcpDial)
	if err != nil {
		return nil, fmt.Errorf("connection manager: %w", err)
	}
	factory := cfg.ClientFactory
	if factory == nil {
		factory = func(user ConnectionUser, addr string) (ConnectionClient, error) {
			return benchwkproto.NewClient(defaultWKProtoClientConfig(cfg, user, addr, sourceDialer))
		}
	}
	sleep := cfg.sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &ConnectionManager{
		cfg:          cfg,
		gatewayAddrs: gatewayAddrs,
		factory:      factory,
		sleep:        sleep,
		rateLimiter:  newConnectRateLimiter(cfg.ConnectRate, sleep),
		sourceDialer: sourceDialer,
		sessions:     make(map[string]*ConnectionSession),
		metrics:      metrics.NewRegistry(),
	}, nil
}

func validateWorkerClientConfig(cfg *model.WorkerClientConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.SendQueueCapacity <= 0 || cfg.MaxInflight <= 0 || cfg.ReadBufferSize <= 0 || cfg.FrameBufferSize <= 0 {
		return fmt.Errorf("connection manager: configured client capacities must all be greater than zero")
	}
	return nil
}

func defaultWKProtoClientConfig(cfg ConnectionManagerConfig, user ConnectionUser, addr string, sourceDialer *tcpSourceDialer) benchwkproto.ClientConfig {
	clientCfg := benchwkproto.ClientConfig{
		Addr:             addr,
		Token:            user.Token,
		OperationTimeout: cfg.OperationTimeout,
		AckTimeout:       cfg.AckTimeout,
	}
	if sourceDialer != nil {
		clientCfg.Dialer = sourceDialer
	}
	if cfg.Client != nil {
		clientCfg.SendQueueCapacity = cfg.Client.SendQueueCapacity
		clientCfg.MaxInflight = cfg.Client.MaxInflight
		clientCfg.ReadBufferSize = cfg.Client.ReadBufferSize
		clientCfg.FrameBufferSize = cfg.Client.FrameBufferSize
	}
	return clientCfg
}

// Connect opens sessions for all users in order, honoring the configured connect rate.
func (m *ConnectionManager) Connect(ctx context.Context, users []ConnectionUser) error {
	for _, user := range users {
		if _, err := m.ConnectUser(ctx, user); err != nil {
			return err
		}
	}
	return nil
}

// ConnectUser opens or returns the existing session for one UID.
func (m *ConnectionManager) ConnectUser(ctx context.Context, user ConnectionUser) (*ConnectionSession, error) {
	if m == nil {
		return nil, fmt.Errorf("connection manager: nil manager")
	}
	normalized, err := m.normalizedUser(user)
	if err != nil {
		return nil, err
	}
	if session, ok := m.Session(normalized.UID); ok {
		return session, nil
	}
	if err := m.rateLimiter.Wait(ctx); err != nil {
		m.recordConnectError(err)
		return nil, err
	}
	m.recordConnectAttempt()
	addr := m.selectGateway()
	client, err := m.factory(normalized, addr)
	if err != nil {
		m.recordConnectError(err)
		return nil, err
	}
	if err := client.Connect(ctx, normalized.UID, normalized.DeviceID); err != nil {
		_ = client.Close()
		m.recordConnectError(err)
		return nil, err
	}
	session := &ConnectionSession{
		UID:         normalized.UID,
		DeviceID:    normalized.DeviceID,
		Token:       normalized.Token,
		GatewayAddr: addr,
		ConnectedAt: time.Now(),
		Client:      client,
	}
	m.startHeartbeat(session)
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[normalized.UID]; existing != nil {
		if session.cancelHeartbeat != nil {
			session.cancelHeartbeat()
		}
		_ = client.Close()
		return existing, nil
	}
	m.sessions[normalized.UID] = session
	m.recordConnectSuccess()
	return session, nil
}

// ReconnectUsers closes and reopens sessions for the supplied users.
func (m *ConnectionManager) ReconnectUsers(ctx context.Context, users []ConnectionUser) error {
	for _, user := range users {
		if _, err := m.ReconnectUser(ctx, user); err != nil {
			return err
		}
	}
	return nil
}

// ReconnectUser replaces the active session for one UID with a new connection.
func (m *ConnectionManager) ReconnectUser(ctx context.Context, user ConnectionUser) (*ConnectionSession, error) {
	if m == nil {
		return nil, fmt.Errorf("connection manager: nil manager")
	}
	normalized, err := m.normalizedUser(user)
	if err != nil {
		return nil, err
	}
	old := m.removeSession(normalized.UID)
	closeSession(old)
	return m.ConnectUser(ctx, normalized)
}

// Sessions returns a stable copy of all active sessions.
func (m *ConnectionManager) Sessions() []*ConnectionSession {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sessions := make([]*ConnectionSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}

// Session returns the active session for a UID, when connected.
func (m *ConnectionManager) Session(uid string) (*ConnectionSession, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[strings.TrimSpace(uid)]
	return session, ok
}

// ActiveCount returns the current number of connected UID sessions.
func (m *ConnectionManager) ActiveCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// MetricsSnapshot returns connection lifecycle counters recorded by the manager.
func (m *ConnectionManager) MetricsSnapshot() metrics.SnapshotData {
	if m == nil || m.metrics == nil {
		return metrics.SnapshotData{Counters: map[string]uint64{}, Gauges: map[string]float64{}, Histograms: map[string]metrics.HistogramSummary{}}
	}
	return m.metrics.Collect()
}

func (m *ConnectionManager) recordConnectAttempt() {
	if m != nil && m.metrics != nil {
		m.metrics.IncCounter("connect_attempt_total", nil)
	}
}

func (m *ConnectionManager) recordConnectSuccess() {
	if m != nil && m.metrics != nil {
		m.metrics.IncCounter("connect_success_total", nil)
	}
}

func (m *ConnectionManager) recordConnectError(err error) {
	if m != nil && m.metrics != nil {
		m.metrics.IncCounter("connect_error_total", nil)
		m.metrics.RecordErrorSample("connect_error", err)
	}
}

func (m *ConnectionManager) startHeartbeat(session *ConnectionSession) {
	if m == nil || session == nil || !m.cfg.Heartbeat.Enabled {
		return
	}
	client, ok := session.Client.(HeartbeatClient)
	if !ok || client == nil {
		return
	}
	interval := m.cfg.Heartbeat.Interval
	if interval <= 0 {
		return
	}
	timeout := m.cfg.Heartbeat.Timeout
	if timeout <= 0 {
		timeout = m.cfg.OperationTimeout
	}
	if timeout <= 0 {
		timeout = defaultHeartbeatTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.cancelHeartbeat = cancel
	go m.heartbeatLoop(ctx, client, interval, timeout)
}

func (m *ConnectionManager) heartbeatLoop(ctx context.Context, client HeartbeatClient, interval, timeout time.Duration) {
	for {
		if err := m.sleep(ctx, interval); err != nil {
			return
		}
		pingCtx, cancel := context.WithTimeout(ctx, timeout)
		err := client.Ping(pingCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.metrics.IncCounter("heartbeat_error_total", nil)
			m.metrics.RecordErrorSample("heartbeat_error", err)
			continue
		}
		m.metrics.IncCounter("heartbeat_success_total", nil)
	}
}

func (m *ConnectionManager) normalizedUser(user ConnectionUser) (ConnectionUser, error) {
	user.UID = strings.TrimSpace(user.UID)
	user.DeviceID = strings.TrimSpace(user.DeviceID)
	user.Token = strings.TrimSpace(user.Token)
	if user.UID == "" {
		return ConnectionUser{}, fmt.Errorf("connection manager: uid is required")
	}
	if user.DeviceID == "" {
		return ConnectionUser{}, fmt.Errorf("connection manager: device id is required")
	}
	if user.Token == "" {
		user.Token = strings.TrimSpace(m.cfg.Token)
	}
	return user, nil
}

func (m *ConnectionManager) removeSession(uid string) *ConnectionSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[strings.TrimSpace(uid)]
	delete(m.sessions, strings.TrimSpace(uid))
	return session
}

// Close closes all active client sessions and clears the session map.
func (m *ConnectionManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	sessions := make([]*ConnectionSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*ConnectionSession)
	m.mu.Unlock()

	var closeErr error
	for _, session := range sessions {
		if err := closeSession(session); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func closeSession(session *ConnectionSession) error {
	if session == nil {
		return nil
	}
	if session.cancelHeartbeat != nil {
		session.cancelHeartbeat()
		session.cancelHeartbeat = nil
	}
	if session.Client == nil {
		return nil
	}
	return session.Client.Close()
}

func (m *ConnectionManager) selectGateway() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	addr := m.gatewayAddrs[m.nextGateway%len(m.gatewayAddrs)]
	m.nextGateway++
	return addr
}

func normalizeGatewayAddrs(addrs []string) []string {
	out := make([]string, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	for _, addr := range addrs {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}

type connectRateLimiter struct {
	interval time.Duration
	sleep    func(context.Context, time.Duration) error
	mu       sync.Mutex
	started  bool
}

func newConnectRateLimiter(rate model.Rate, sleep func(context.Context, time.Duration) error) *connectRateLimiter {
	limiter := &connectRateLimiter{sleep: sleep}
	if rate.PerSecond > 0 {
		limiter.interval = time.Duration(float64(time.Second) / rate.PerSecond)
	}
	return limiter
}

func (l *connectRateLimiter) Wait(ctx context.Context) error {
	if l == nil || l.interval <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started {
		l.started = true
		return nil
	}
	return l.sleep(ctx, l.interval)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
