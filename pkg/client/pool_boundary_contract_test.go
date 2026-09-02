package client

import (
	"context"
	"errors"
	"testing"
)

func TestNewPoolValidatesRoutingConfiguration(t *testing.T) {
	if _, err := NewPool(PoolConfig{}); !errors.Is(err, ErrMissingAddr) {
		t.Fatalf("NewPool(empty) error = %v, want %v", err, ErrMissingAddr)
	}
	if _, err := NewPool(PoolConfig{Addrs: []string{"gateway"}, Balance: "random"}); err == nil {
		t.Fatal("NewPool(unsupported balance) error = nil")
	}
	p, err := NewPool(PoolConfig{Client: Config{Addr: "gateway"}})
	if err != nil {
		t.Fatalf("NewPool(client address fallback) error = %v", err)
	}
	if p.cfg.Balance != defaultPoolBalanceRoundRobin {
		t.Fatalf("default balance = %q, want %q", p.cfg.Balance, defaultPoolBalanceRoundRobin)
	}
	if err := p.Connect(nil, nil); err != nil {
		t.Fatalf("Connect(empty identities) error = %v", err)
	}
}

func TestPoolConnectRejectsLifecycleAndAddressErrorsBeforeDial(t *testing.T) {
	var nilPool *Pool
	if err := nilPool.Connect(context.Background(), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil Pool.Connect() = %v, want %v", err, ErrClosed)
	}

	p := &Pool{clients: make(map[string]poolClient)}
	if err := p.Connect(context.Background(), nil); !errors.Is(err, ErrMissingAddr) {
		t.Fatalf("Pool.Connect(missing address) = %v, want %v", err, ErrMissingAddr)
	}

	p.cfg.Client.Addr = "gateway"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Connect(ctx, []Identity{{UID: "u1"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Pool.Connect(canceled) = %v, want %v", err, context.Canceled)
	}
}

func TestPoolSendBatchFailsBeforeLosingInputOrderEvidence(t *testing.T) {
	var nilPool *Pool
	if _, err := nilPool.SendBatch(context.Background(), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil Pool.SendBatch() = %v, want %v", err, ErrClosed)
	}

	p := &Pool{clients: make(map[string]poolClient)}
	if results, err := p.SendBatch(nil, nil); err != nil || results != nil {
		t.Fatalf("Pool.SendBatch(empty) = %#v, %v; want nil, nil", results, err)
	}
	if _, err := p.SendBatch(context.Background(), []RoutedMessage{{UID: "missing"}}); err == nil {
		t.Fatal("Pool.SendBatch(missing UID) error = nil")
	}

	p.clients["u1"] = &contractPoolClient{}
	results, err := p.SendBatch(context.Background(), []RoutedMessage{{UID: "u1"}})
	if err == nil || len(results) != 1 {
		t.Fatalf("Pool.SendBatch(result count mismatch) = %#v, %v", results, err)
	}

	sendErr := errors.New("send failed")
	p.clients["u1"] = &contractPoolClient{sendErr: sendErr}
	results, err = p.SendBatch(context.Background(), []RoutedMessage{{UID: "u1"}})
	if !errors.Is(err, sendErr) || len(results) != 1 {
		t.Fatalf("Pool.SendBatch(send failure) = %#v, %v; want retained ordered result storage", results, err)
	}
}

func TestPoolCloseReportsClientFailureAndRejectsNonConcreteLookup(t *testing.T) {
	var nilPool *Pool
	if err := nilPool.Close(); err != nil {
		t.Fatalf("nil Pool.Close() error = %v", err)
	}
	if client, ok := nilPool.Client("u1"); ok || client != nil {
		t.Fatalf("nil Pool.Client() = %#v, %t", client, ok)
	}

	closeErr := errors.New("close failed")
	owned := &contractPoolClient{closeErr: closeErr}
	p := &Pool{clients: map[string]poolClient{"u1": owned}}
	if client, ok := p.Client("u1"); ok || client != nil {
		t.Fatalf("Pool.Client(non-concrete) = %#v, %t", client, ok)
	}
	if err := p.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Pool.Close() error = %v, want %v", err, closeErr)
	}
	if !owned.closed {
		t.Fatal("Pool.Close() did not attempt to close owned client")
	}
}

type contractPoolClient struct {
	results  []SendResult
	sendErr  error
	closeErr error
	closed   bool
}

func (c *contractPoolClient) SendBatch(context.Context, []Message) ([]SendResult, error) {
	return c.results, c.sendErr
}

func (c *contractPoolClient) Close() error {
	c.closed = true
	return c.closeErr
}
