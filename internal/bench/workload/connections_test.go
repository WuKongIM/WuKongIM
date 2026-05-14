package workload

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
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

func TestConnectionManagerRejectsInvalidConfig(t *testing.T) {
	if _, err := NewConnectionManager(ConnectionManagerConfig{}); err == nil {
		t.Fatal("NewConnectionManager() error = nil, want missing gateway error")
	}
	if _, err := NewConnectionManager(ConnectionManagerConfig{GatewayAddrs: []string{"gw"}, Reconnect: ReconnectConfig{Enabled: true}}); err == nil {
		t.Fatal("NewConnectionManager() error = nil, want reconnect disabled error")
	}
}

type recordingClientFactory struct {
	addrs   []string
	clients []*recordingClient
}

func (f *recordingClientFactory) newClient(addr string) (ConnectionClient, error) {
	client := &recordingClient{addr: addr}
	f.addrs = append(f.addrs, addr)
	f.clients = append(f.clients, client)
	return client, nil
}

type recordingClient struct {
	addr      string
	connected []ConnectionUser
	closed    bool
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
