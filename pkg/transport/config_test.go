package transport

import (
	"errors"
	"testing"
	"time"
)

type testDiscovery map[NodeID]string

func (d testDiscovery) Resolve(nodeID NodeID) (string, error) {
	addr, ok := d[nodeID]
	if !ok {
		return "", ErrNodeNotFound
	}
	return addr, nil
}

func TestClientConfigDefaultsAndRequiresDiscovery(t *testing.T) {
	_, err := NewClient(ClientConfig{NodeID: 1})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewClient() error = %v, want ErrInvalidConfig", err)
	}

	client, err := NewClient(ClientConfig{
		NodeID:    1,
		Discovery: testDiscovery{2: "127.0.0.1:1"},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Stop()

	if client.cfg.PoolSize != DefaultPoolSize {
		t.Fatalf("PoolSize = %d, want %d", client.cfg.PoolSize, DefaultPoolSize)
	}
	if client.cfg.DialTimeout != DefaultDialTimeout {
		t.Fatalf("DialTimeout = %s, want %s", client.cfg.DialTimeout, DefaultDialTimeout)
	}
	if client.cfg.Limits.MaxFrameBodyBytes != DefaultLimits().MaxFrameBodyBytes {
		t.Fatalf("MaxFrameBodyBytes = %d, want default", client.cfg.Limits.MaxFrameBodyBytes)
	}
}

func TestClientConfigRejectsInvalidLimitsAndPoolSize(t *testing.T) {
	_, err := NewClient(ClientConfig{
		Discovery: testDiscovery{2: "127.0.0.1:1"},
		PoolSize:  -1,
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewClient() pool error = %v, want ErrInvalidConfig", err)
	}

	_, err = NewClient(ClientConfig{
		Discovery: testDiscovery{2: "127.0.0.1:1"},
		Limits:    Limits{MaxFrameBodyBytes: -1},
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewClient() limits error = %v, want ErrInvalidConfig", err)
	}
}

func TestLimitsValidateAcceptsDefaultsAndRejectsNegativeValues(t *testing.T) {
	limits := DefaultLimits()
	if err := limits.Validate(); err != nil {
		t.Fatalf("DefaultLimits().Validate() error = %v", err)
	}

	limits.MaxBatchBytes = 0
	if !errors.Is(limits.Validate(), ErrInvalidConfig) {
		t.Fatalf("zero MaxBatchBytes should return ErrInvalidConfig")
	}
}

func TestServiceOptionsValidateRequiresConcurrency(t *testing.T) {
	err := (ServiceOptions{Concurrency: 0, QueueSize: 1, MaxQueueBytes: 1024, Timeout: time.Second}).Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestServerConfigDefaultsLoggerAndLimits(t *testing.T) {
	server, err := NewServer(ServerConfig{NodeID: 1})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer server.Stop()

	if server.cfg.Logger == nil {
		t.Fatal("expected default logger")
	}
	if server.cfg.Limits.MaxFrameBodyBytes != DefaultLimits().MaxFrameBodyBytes {
		t.Fatalf("MaxFrameBodyBytes = %d, want default", server.cfg.Limits.MaxFrameBodyBytes)
	}
}

func TestNilObserverStaysNilAfterConfigNormalization(t *testing.T) {
	client, err := NewClient(ClientConfig{
		NodeID:    1,
		Discovery: testDiscovery{2: "127.0.0.1:1"},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Stop()
	if client.cfg.Observer != nil {
		t.Fatalf("client cfg observer = %#v, want nil", client.cfg.Observer)
	}

	server, err := NewServer(ServerConfig{NodeID: 1})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer server.Stop()
	if server.cfg.Observer != nil {
		t.Fatalf("server cfg observer = %#v, want nil", server.cfg.Observer)
	}
}

func TestClientConfigPreservesExplicitTimeout(t *testing.T) {
	client, err := NewClient(ClientConfig{
		NodeID:      1,
		Discovery:   testDiscovery{2: "127.0.0.1:1"},
		PoolSize:    2,
		DialTimeout: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Stop()

	if client.cfg.PoolSize != 2 {
		t.Fatalf("PoolSize = %d, want 2", client.cfg.PoolSize)
	}
	if client.cfg.DialTimeout != 250*time.Millisecond {
		t.Fatalf("DialTimeout = %s, want 250ms", client.cfg.DialTimeout)
	}
}

func TestLimitsValidationRejectsImpossibleBatch(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxBatchBytes = limits.MaxFrameBodyBytes + 1
	err := limits.Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want ErrInvalidConfig", err)
	}
}
