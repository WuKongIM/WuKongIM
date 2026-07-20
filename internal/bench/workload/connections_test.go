package workload

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"
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
	now := time.Unix(0, 0)
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100"},
		ConnectRate:   model.Rate{PerSecond: 10},
		ClientFactory: (&recordingClientFactory{}).newClient,
		sleep: func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			now = now.Add(d)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	defer manager.Close()
	manager.rateLimiter.now = func() time.Time { return now }

	if err := manager.Connect(context.Background(), []ConnectionUser{{UID: "u1", DeviceID: "d1"}, {UID: "u2", DeviceID: "d2"}, {UID: "u3", DeviceID: "d3"}}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if got, want := sleeps, []time.Duration{100 * time.Millisecond, 100 * time.Millisecond}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rate-limit sleeps = %v, want %v", got, want)
	}
}

func TestConnectRateLimiterAccountsForConnectionWorkBetweenAttempts(t *testing.T) {
	var slept time.Duration
	now := time.Unix(0, 0)
	limiter := newConnectRateLimiter(model.Rate{PerSecond: 10}, func(_ context.Context, d time.Duration) error {
		slept = d
		now = now.Add(d)
		return nil
	})
	limiter.now = func() time.Time { return now }

	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("Wait(first) error = %v", err)
	}
	now = now.Add(30 * time.Millisecond)
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("Wait(second) error = %v", err)
	}

	if got, want := slept, 70*time.Millisecond; got != want {
		t.Fatalf("second pacing sleep = %v, want %v after deducting connection work", got, want)
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

func TestConnectionManagerCloseWaitsForHeartbeatUnblockedByClientClose(t *testing.T) {
	client := newCloseUnblocksHeartbeatClient()
	t.Cleanup(client.releasePing)
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs: []string{"gw-a:5100"},
		Heartbeat:    model.HeartbeatConfig{Enabled: true, Interval: time.Nanosecond, Timeout: time.Second},
		ClientFactory: func(ConnectionUser, string) (ConnectionClient, error) {
			return client, nil
		},
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	if _, err := manager.ConnectUser(context.Background(), ConnectionUser{UID: "u1", DeviceID: "d1"}); err != nil {
		t.Fatalf("ConnectUser() error = %v", err)
	}
	if !waitConnectionTestSignal(client.pingStarted, time.Second) {
		t.Fatal("heartbeat Ping did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close() }()
	if !waitConnectionTestSignal(client.closeCalled, time.Second) {
		t.Fatal("ConnectionManager.Close() did not close the client")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("ConnectionManager.Close() returned before the heartbeat exited: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	client.releasePing()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("ConnectionManager.Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ConnectionManager.Close() deadlocked after client Close unblocked Ping")
	}

	first := manager.MetricsSnapshot()
	second := manager.MetricsSnapshot()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("metrics changed after Close returned: first=%#v second=%#v", first, second)
	}
}

func TestConnectionManagerCloseClosesEveryClientBeforeJoiningHeartbeats(t *testing.T) {
	factory := &closeUnblocksHeartbeatFactory{}
	manager, err := NewConnectionManager(ConnectionManagerConfig{
		GatewayAddrs:  []string{"gw-a:5100"},
		Heartbeat:     model.HeartbeatConfig{Enabled: true, Interval: time.Nanosecond, Timeout: time.Second},
		ClientFactory: factory.newClient,
	})
	if err != nil {
		t.Fatalf("NewConnectionManager() error = %v", err)
	}
	users := []ConnectionUser{{UID: "u1", DeviceID: "d1"}, {UID: "u2", DeviceID: "d2"}}
	if err := manager.Connect(context.Background(), users); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	clients := []*closeUnblocksHeartbeatClient{factory.client(0), factory.client(1)}
	for index, client := range clients {
		t.Cleanup(client.releasePing)
		if !waitConnectionTestSignal(client.pingStarted, time.Second) {
			t.Fatalf("heartbeat Ping %d did not start", index)
		}
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close() }()
	for index, client := range clients {
		if !waitConnectionTestSignal(client.closeCalled, time.Second) {
			t.Fatalf("client %d was not closed before heartbeat joins", index)
		}
	}
	select {
	case err := <-closeDone:
		t.Fatalf("ConnectionManager.Close() returned before heartbeat release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	for _, client := range clients {
		client.releasePing()
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("ConnectionManager.Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ConnectionManager.Close() did not join every heartbeat")
	}
}

func TestConnectionManagerSessionReplacementWaitsForPreviousHeartbeatExit(t *testing.T) {
	tests := []struct {
		name    string
		replace func(*ConnectionManager) error
	}{
		{
			name: "reconnect",
			replace: func(manager *ConnectionManager) error {
				_, err := manager.ReconnectUser(context.Background(), ConnectionUser{UID: "u1", DeviceID: "d1"})
				return err
			},
		},
		{
			name: "replace uid",
			replace: func(manager *ConnectionManager) error {
				_, err := manager.ReplaceUser(context.Background(), "u1", ConnectionUser{UID: "u2", DeviceID: "d2"})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := &closeUnblocksHeartbeatFactory{}
			manager, err := NewConnectionManager(ConnectionManagerConfig{
				GatewayAddrs:  []string{"gw-a:5100"},
				Heartbeat:     model.HeartbeatConfig{Enabled: true, Interval: time.Nanosecond, Timeout: time.Second},
				ClientFactory: factory.newClient,
			})
			if err != nil {
				t.Fatalf("NewConnectionManager() error = %v", err)
			}
			if _, err := manager.ConnectUser(context.Background(), ConnectionUser{UID: "u1", DeviceID: "d1"}); err != nil {
				t.Fatalf("ConnectUser() error = %v", err)
			}
			first := factory.client(0)
			t.Cleanup(first.releasePing)
			if !waitConnectionTestSignal(first.pingStarted, time.Second) {
				t.Fatal("initial heartbeat Ping did not start")
			}

			replaceDone := make(chan error, 1)
			go func() { replaceDone <- tt.replace(manager) }()
			if !waitConnectionTestSignal(first.closeCalled, time.Second) {
				t.Fatal("session replacement did not close the previous client")
			}
			select {
			case err := <-replaceDone:
				t.Fatalf("session replacement returned before the previous heartbeat exited: %v", err)
			case <-time.After(20 * time.Millisecond):
			}

			first.releasePing()
			select {
			case err := <-replaceDone:
				if err != nil {
					t.Fatalf("session replacement error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("session replacement did not finish after the previous heartbeat exited")
			}

			second := factory.client(1)
			t.Cleanup(second.releasePing)
			if !waitConnectionTestSignal(second.pingStarted, time.Second) {
				t.Fatal("replacement heartbeat Ping did not start")
			}
			closeDone := make(chan error, 1)
			go func() { closeDone <- manager.Close() }()
			if !waitConnectionTestSignal(second.closeCalled, time.Second) {
				t.Fatal("manager close did not close the replacement client")
			}
			second.releasePing()
			select {
			case err := <-closeDone:
				if err != nil {
					t.Fatalf("ConnectionManager.Close() error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("ConnectionManager.Close() did not join the replacement heartbeat")
			}
		})
	}
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

type closeUnblocksHeartbeatClient struct {
	pingStarted chan struct{}
	closeCalled chan struct{}
	releaseC    chan struct{}
	pingOnce    sync.Once
	closeOnce   sync.Once
	releaseOnce sync.Once
}

type closeUnblocksHeartbeatFactory struct {
	mu      sync.Mutex
	clients []*closeUnblocksHeartbeatClient
}

func (f *closeUnblocksHeartbeatFactory) newClient(ConnectionUser, string) (ConnectionClient, error) {
	client := newCloseUnblocksHeartbeatClient()
	f.mu.Lock()
	f.clients = append(f.clients, client)
	f.mu.Unlock()
	return client, nil
}

func (f *closeUnblocksHeartbeatFactory) client(index int) *closeUnblocksHeartbeatClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clients[index]
}

func newCloseUnblocksHeartbeatClient() *closeUnblocksHeartbeatClient {
	return &closeUnblocksHeartbeatClient{
		pingStarted: make(chan struct{}),
		closeCalled: make(chan struct{}),
		releaseC:    make(chan struct{}),
	}
}

func (c *closeUnblocksHeartbeatClient) Connect(context.Context, string, string) error {
	return nil
}

func (c *closeUnblocksHeartbeatClient) Close() error {
	c.closeOnce.Do(func() { close(c.closeCalled) })
	return nil
}

func (c *closeUnblocksHeartbeatClient) Ping(context.Context) error {
	c.pingOnce.Do(func() { close(c.pingStarted) })
	<-c.closeCalled
	<-c.releaseC
	return nil
}

func (c *closeUnblocksHeartbeatClient) releasePing() {
	c.releaseOnce.Do(func() { close(c.releaseC) })
}

func waitConnectionTestSignal(signal <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-signal:
		return true
	case <-time.After(timeout):
		return false
	}
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
