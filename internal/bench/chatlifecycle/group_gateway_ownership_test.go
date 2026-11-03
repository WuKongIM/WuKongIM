package chatlifecycle

import (
	"context"
	"errors"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Failure cases: a foreign/malformed/non-owned identity prepares a credential
// or creates a client; canceled callers still resolve/prepare; or direct
// single-endpoint adapters lose their original credential/cancellation behavior.
func TestDefaultWorkerGatewayRejectsUnownedIdentityBeforeTokenPreparation(t *testing.T) {
	cfg := LocalConfig()
	generation, err := NewEngineWorkerGenerationFactory("gateway-unit").New(WorkerAssignment{WorkerFence: WorkerFence{RunID: cfg.RunID, AssignmentID: "gateway-unit", Generation: 1}, WorkerID: 0, WorkerCount: 3, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	g := generation.(*engineWorkerGeneration)
	factory := g.engine.sessions.factory.(engineWorkerSessionFactory)
	calls := 0
	factory.tokens = target.NewClient(target.Config{APIAddrs: []string{"http://gateway-unit.test"}, HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"accepted":1}`)), Header: make(http.Header)}, nil
	})}})
	foreign, err := NewIdentitySpace("another-run", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"", "malformed-user", foreign.UID(0), g.engine.sessions.identity.UID(1)} {
		client, err := factory.NewSession(context.Background(), uid, "test-token")
		if client != nil {
			_ = client.Close()
		}
		if client != nil || err == nil || calls != 0 {
			t.Errorf("unowned gateway identity created_client=%v err=%v token_calls=%d", client != nil, err, calls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, err := factory.NewSession(ctx, g.engine.sessions.identity.UID(0), "test-token")
	if client != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled caller escaped")
	}
}
