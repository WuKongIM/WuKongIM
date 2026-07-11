package workload

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestConnectionManagerConnectsUsersRoundRobinAndTracksActiveSessions(t *testing.T) {
	factory := &recordingClientFactory{}
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100", "gw-b:5100"},
		ClientFactory: factory.newClient,
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()

	users := []ConnectionUser{
		{UID: "u1", DeviceID: "d1"},
		{UID: "u2", DeviceID: "d2"},
		{UID: "u3", DeviceID: "d3"},
	}
	if err := manager.Connect(context.Background(), users); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if got, want := manager.ActiveCount(), 3; got != want {
		t.Fatalf("ActiveCount() = %d, want %d", got, want)
	}
	if got, want := factory.addrs, []string{"gw-a:5100", "gw-b:5100", "gw-a:5100"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("gateway addrs = %v, want %v", got, want)
	}
	for _, user := range users {
		session, ok := manager.Session(user.UID)
		if !ok {
			t.Fatalf("Session(%q) missing", user.UID)
		}
		if session.UID != user.UID || session.DeviceID != user.DeviceID || session.Client == nil || session.GatewayAddr == "" {
			t.Fatalf("session(%q) = %#v", user.UID, session)
		}
	}
}

func TestConnectionManagerAppliesDefaultAndPerUserTokens(t *testing.T) {
	factory := &recordingClientFactory{}
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100"},
		Token:         "default-token",
		ClientFactory: factory.newClient,
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()

	users := []ConnectionUser{
		{UID: "u1", DeviceID: "d1"},
		{UID: "u2", DeviceID: "d2", Token: "user-token"},
	}
	if err := manager.Connect(context.Background(), users); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if got, want := factory.users[0].Token, "default-token"; got != want {
		t.Fatalf("first factory token = %q, want %q", got, want)
	}
	if got, want := factory.users[1].Token, "user-token"; got != want {
		t.Fatalf("second factory token = %q, want %q", got, want)
	}
	if session, ok := manager.Session("u1"); !ok || session.Token != "default-token" {
		t.Fatalf("session u1 token = %#v, want default-token", session)
	}
	if session, ok := manager.Session("u2"); !ok || session.Token != "user-token" {
		t.Fatalf("session u2 token = %#v, want user-token", session)
	}
}

func TestConnectionManagerRateLimitsConnectAttempts(t *testing.T) {
	var sleeps []time.Duration
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100"},
		ConnectRate:   model.Rate{PerSecond: 10},
		ClientFactory: (&recordingClientFactory{}).newClient,
		sleep: func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()

	if err := manager.Connect(context.Background(), []ConnectionUser{{UID: "u1", DeviceID: "d1"}, {UID: "u2", DeviceID: "d2"}, {UID: "u3", DeviceID: "d3"}}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if got, want := sleeps, []time.Duration{100 * time.Millisecond, 100 * time.Millisecond}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rate-limit sleeps = %v, want %v", got, want)
	}
}

func TestConnectionManagerIsIdempotentByUID(t *testing.T) {
	factory := &recordingClientFactory{}
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100"},
		ClientFactory: factory.newClient,
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()

	first, err := manager.ConnectUser(context.Background(), ConnectionUser{UID: "u1", DeviceID: "d1"})
	if err != nil {
		t.Fatalf("ConnectUser(first) error = %v", err)
	}
	second, err := manager.ConnectUser(context.Background(), ConnectionUser{UID: "u1", DeviceID: "d1"})
	if err != nil {
		t.Fatalf("ConnectUser(second) error = %v", err)
	}
	if first != second {
		t.Fatalf("duplicate UID returned different sessions")
	}
	if got, want := len(factory.clients), 1; got != want {
		t.Fatalf("client creations = %d, want %d", got, want)
	}
	if got, want := manager.ActiveCount(), 1; got != want {
		t.Fatalf("ActiveCount() = %d, want %d", got, want)
	}
}

func TestConnectionManagerSessionsReturnsStableCopy(t *testing.T) {
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100"},
		ClientFactory: (&recordingClientFactory{}).newClient,
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()
	if err := manager.Connect(context.Background(), []ConnectionUser{{UID: "u1", DeviceID: "d1"}}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	sessions := manager.Sessions()
	sessions[0] = nil

	if got, want := manager.ActiveCount(), 1; got != want {
		t.Fatalf("ActiveCount() = %d, want %d", got, want)
	}
	if session, ok := manager.Session("u1"); !ok || session == nil {
		t.Fatalf("Session(u1) missing after mutating Sessions result")
	}
}

func TestConnectionManagerMetricsCountPartialConnectFailureByUser(t *testing.T) {
	factory := &recordingClientFactory{}
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100"},
		ClientFactory: factory.newClient,
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()

	err = manager.Connect(context.Background(), []ConnectionUser{{UID: "u1", DeviceID: "d1"}, {UID: "fail", DeviceID: "d2"}, {UID: "u3", DeviceID: "d3"}})
	if err == nil {
		t.Fatal("Connect() error = nil, want partial failure")
	}

	snap := manager.MetricsSnapshot()
	if got, want := snap.Counters["connect_attempt_total"], uint64(2); got != want {
		t.Fatalf("connect_attempt_total = %d, want %d", got, want)
	}
	if got, want := snap.Counters["connect_success_total"], uint64(1); got != want {
		t.Fatalf("connect_success_total = %d, want %d", got, want)
	}
	if got, want := snap.Counters["connect_error_total"], uint64(1); got != want {
		t.Fatalf("connect_error_total = %d, want %d", got, want)
	}
}

func TestConnectionManagerSendsHeartbeatPings(t *testing.T) {
	factory := &recordingClientFactory{}
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100"},
		Heartbeat:     model.HeartbeatConfig{Enabled: true, Interval: time.Millisecond, Timeout: time.Second},
		ClientFactory: factory.newClient,
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()

	if err := manager.Connect(context.Background(), []ConnectionUser{{UID: "u1", DeviceID: "d1"}}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if factory.clients[0].pings.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("heartbeat ping was not sent")
}

func TestConnectionManagerRejectsInvalidConfig(t *testing.T) {
	if _, err := NewConnectionManager(ConnectionManagerConfig{}); err == nil {
		t.Fatal("NewConnectionManager() error = nil, want missing gateway error")
	}
	if _, err := NewConnectionManager(ConnectionManagerConfig{GatewayAddrs: []string{"gw"}, Reconnect: ReconnectConfig{Enabled: true}}); err == nil {
		t.Fatal("NewConnectionManager() error = nil, want reconnect disabled error")
	}
	if _, err := NewConnectionManager(ConnectionManagerConfig{GatewayAddrs: []string{"gw"}, Heartbeat: model.HeartbeatConfig{Enabled: true}}); err == nil {
		t.Fatal("NewConnectionManager() error = nil, want heartbeat interval error")
	}
	if _, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs: []string{"gw"},
		Client:       &model.WorkerClientConfig{SendQueueCapacity: 16, MaxInflight: 1, ReadBufferSize: 1024},
	}); err == nil {
		t.Fatal("NewConnectionManager() error = nil, want incomplete client profile error")
	}
}

func TestDefaultWKProtoClientConfigUsesWorkerClientProfile(t *testing.T) {
	profile := &model.WorkerClientConfig{
		SendQueueCapacity: 16,
		MaxInflight:       1,
		ReadBufferSize:    1024,
		FrameBufferSize:   4,
	}
	cfg := defaultWKProtoClientConfig(ConnectionManagerConfig{Client: profile}, ConnectionUser{Token: "token-a"}, "gw-a:5100", nil)

	if cfg.Addr != "gw-a:5100" || cfg.Token != "token-a" {
		t.Fatalf("identity config = %#v", cfg)
	}
	if cfg.SendQueueCapacity != 16 || cfg.MaxInflight != 1 || cfg.ReadBufferSize != 1024 || cfg.FrameBufferSize != 4 {
		t.Fatalf("capacity config = %#v, want 16/1/1024/4", cfg)
	}
}

func TestNewConnectionManagerBuildsOneSharedTCPSourceDialerForDefaultFactory(t *testing.T) {
	pool := &model.TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1"}, PortMin: 2000, PortMax: 2001}
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs: []string{"gw-a:5100"},
		TCPSource:    pool,
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()

	if manager.sourceDialer == nil {
		t.Fatal("default connection manager source dialer = nil")
	}
	first := defaultWKProtoClientConfig(manager.cfg, ConnectionUser{Token: "a"}, "gw-a:5100", manager.sourceDialer)
	second := defaultWKProtoClientConfig(manager.cfg, ConnectionUser{Token: "b"}, "gw-a:5100", manager.sourceDialer)
	if first.Dialer != manager.sourceDialer || second.Dialer != manager.sourceDialer {
		t.Fatal("default client configs do not share the manager source dialer")
	}
}

func TestNewConnectionManagerOmitsSourceDialerForDefaultNetworkDialing(t *testing.T) {
	manager, err := NewConnectionManager(ConnectionManagerConfig{GatewayAddrs: []string{"gw-a:5100"}})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()

	if manager.sourceDialer != nil {
		t.Fatalf("default connection manager source dialer = %#v, want nil", manager.sourceDialer)
	}
	clientCfg := defaultWKProtoClientConfig(manager.cfg, ConnectionUser{}, "gw-a:5100", nil)
	if clientCfg.Dialer != nil {
		t.Fatalf("default WKProto dialer = %#v, want nil for ordinary net.Dialer behavior", clientCfg.Dialer)
	}
}

func TestDefaultConnectionManagerPropagatesTCPSourceErrorsThroughWKProtoClient(t *testing.T) {
	tests := []struct {
		name     string
		pool     *model.TCPSourceConfig
		dialErr  error
		wantKind TCPSourceErrorKind
		calls    int
	}{
		{
			name:     "unavailable",
			pool:     &model.TCPSourceConfig{IPv4Addrs: []string{"192.0.2.1"}, PortMin: 2000, PortMax: 2001},
			dialErr:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EADDRNOTAVAIL},
			wantKind: TCPSourceErrorUnavailable,
			calls:    1,
		},
		{
			name:     "exhausted",
			pool:     &model.TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1"}, PortMin: 2000, PortMax: 2001},
			dialErr:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EADDRINUSE},
			wantKind: TCPSourceErrorExhausted,
			calls:    2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			manager, err := NewConnectionManager(ConnectionManagerConfig{
				GatewayAddrs: []string{"127.0.0.1:5100"},
				TCPSource:    tt.pool,
				tcpDial: func(context.Context, *net.Dialer, string, string) (net.Conn, error) {
					calls++
					return nil, tt.dialErr
				},
			})
			if err != nil {
				t.Fatalf("NewConnectionManager() error = %v", err)
			}
			defer manager.Close()

			_, err = manager.ConnectUser(context.Background(), ConnectionUser{UID: "u1", DeviceID: "d1"})

			var sourceErr *TCPSourceError
			if !errors.As(err, &sourceErr) {
				t.Fatalf("ConnectUser() error = %T %v, want *TCPSourceError through default WKProto stack", err, err)
			}
			if sourceErr.Kind != tt.wantKind {
				t.Fatalf("ConnectUser() source kind = %q, want %q", sourceErr.Kind, tt.wantKind)
			}
			if calls != tt.calls {
				t.Fatalf("underlying dial calls = %d, want %d", calls, tt.calls)
			}
		})
	}
}

type recordingClientFactory struct {
	addrs   []string
	clients []*recordingClient
	users   []ConnectionUser
}

func (f *recordingClientFactory) newClient(user ConnectionUser, addr string) (ConnectionClient, error) {
	client := &recordingClient{addr: addr}
	f.users = append(f.users, user)
	f.addrs = append(f.addrs, addr)
	f.clients = append(f.clients, client)
	return client, nil
}

type recordingClient struct {
	addr      string
	connected []ConnectionUser
	closed    bool
	pings     atomic.Int32
}

func (c *recordingClient) Connect(ctx context.Context, uid, deviceID string) error {
	if uid == "fail" {
		return fmt.Errorf("connect failed")
	}
	c.connected = append(c.connected, ConnectionUser{UID: uid, DeviceID: deviceID})
	return nil
}

func (c *recordingClient) Close() error {
	c.closed = true
	return nil
}

func (c *recordingClient) Ping(context.Context) error {
	c.pings.Add(1)
	return nil
}
