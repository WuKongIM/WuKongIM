package workload

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	benchwkproto "github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
)

const gatewayBalanceRoundRobin = "round_robin"

// ReconnectConfig reserves reconnect policy shape for future workload phases.
type ReconnectConfig = model.ReconnectConfig

// ConnectionClient is the minimal WKProto client contract managed by workers.
type ConnectionClient interface {
	// Connect opens the client session for the supplied identity.
	Connect(ctx context.Context, uid, deviceID string) error
	// Close releases the underlying gateway connection.
	Close() error
}

// ConnectionUser identifies one simulated online WKProto session.
type ConnectionUser struct {
	// UID is the benchmark user identifier used in the WKProto connect packet.
	UID string
	// DeviceID is the benchmark device identifier used in the WKProto connect packet.
	DeviceID string
}

// ConnectionSession stores the active client session for one UID.
type ConnectionSession struct {
	// UID is the benchmark user identifier for this connection.
	UID string
	// DeviceID is the benchmark device identifier for this connection.
	DeviceID string
	// GatewayAddr is the selected gateway TCP address for this connection.
	GatewayAddr string
	// ConnectedAt records when the connect handshake completed.
	ConnectedAt time.Time
	// Client is the live WKProto client bound to this session.
	Client ConnectionClient
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
	// OperationTimeout is passed to production WKProto clients.
	OperationTimeout time.Duration
	// ClientFactory overrides production WKProto client creation for tests.
	ClientFactory func(addr string) (ConnectionClient, error)

	sleep func(context.Context, time.Duration) error
}

// ConnectionManager owns the UID-to-session map for worker gateway connections.
type ConnectionManager struct {
	cfg          ConnectionManagerConfig
	gatewayAddrs []string
	factory      func(addr string) (ConnectionClient, error)
	sleep        func(context.Context, time.Duration) error
	rateLimiter  *connectRateLimiter

	mu          sync.Mutex
	nextGateway int
	sessions    map[string]*ConnectionSession
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
	factory := cfg.ClientFactory
	if factory == nil {
		factory = func(addr string) (ConnectionClient, error) {
			return benchwkproto.NewClient(benchwkproto.ClientConfig{Addr: addr, OperationTimeout: cfg.OperationTimeout})
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
		sessions:     make(map[string]*ConnectionSession),
	}, nil
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
	user.UID = strings.TrimSpace(user.UID)
	user.DeviceID = strings.TrimSpace(user.DeviceID)
	if user.UID == "" {
		return nil, fmt.Errorf("connection manager: uid is required")
	}
	if user.DeviceID == "" {
		return nil, fmt.Errorf("connection manager: device id is required")
	}
	if session, ok := m.Session(user.UID); ok {
		return session, nil
	}
	if err := m.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	addr := m.selectGateway()
	client, err := m.factory(addr)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(ctx, user.UID, user.DeviceID); err != nil {
		_ = client.Close()
		return nil, err
	}
	session := &ConnectionSession{
		UID:         user.UID,
		DeviceID:    user.DeviceID,
		GatewayAddr: addr,
		ConnectedAt: time.Now(),
		Client:      client,
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[user.UID]; existing != nil {
		_ = client.Close()
		return existing, nil
	}
	m.sessions[user.UID] = session
	return session, nil
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
		if session.Client == nil {
			continue
		}
		if err := session.Client.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
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
