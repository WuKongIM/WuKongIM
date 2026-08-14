package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/internal/usecase/benchterminal"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestAppWiresAuthenticatedTerminalFencePrepareToRealProductDrains(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t, Config{
		DataDir: shortAppTestDataDir(t),
		Cluster: clusterpkg.Config{NodeID: 1},
		API:     APIConfig{ListenAddr: "127.0.0.1:0"},
		Bench: BenchConfig{
			APIEnabled: true,
			APIToken:   "terminal-api-token",
		},
		Delivery: DeliveryConfig{Enabled: true},
		Gateway: GatewayConfig{
			Listeners: []gateway.ListenerOptions{{
				Name:      "terminal-test",
				Network:   "tcp",
				Address:   "127.0.0.1:0",
				Transport: "gnet",
				Protocol:  "wkproto",
			}},
			Runtime: gateway.RuntimeOptions{
				AsyncSendWorkers:       1,
				AsyncSendQueueCapacity: 32,
				AsyncAuthWorkers:       1,
				AsyncAuthQueueCapacity: 8,
			},
		},
	}, WithCluster(cluster), WithStartupConsole(io.Discard, false))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, ok := app.gateway.(*gateway.Gateway); !ok {
		t.Fatalf("gateway = %T, want real *gateway.Gateway", app.gateway)
	}
	if app.benchTerminal == nil {
		t.Fatal("bench terminal controller was not wired")
	}
	apiServer, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api = %T, want *accessapi.Server", app.api)
	}
	startTestApp(t, app)

	unauthorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/bench/v1/terminal-fence/prepare", strings.NewReader(`{
		"run_id":"run-a","assignment_id":"generation-a","expected_sessions":2500
	}`))
	request.Header.Set("Content-Type", "application/json")
	apiServer.Handler().ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}
	if status := app.benchTerminal.Status(); status.Stage != benchterminal.StageIdle {
		t.Fatalf("terminal stage before auth = %s, want idle", status.Stage)
	}

	authorized := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/bench/v1/terminal-fence/prepare", strings.NewReader(`{
		"run_id":"run-a","assignment_id":"generation-a","expected_sessions":2500
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer terminal-api-token")
	apiServer.Handler().ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d: %s", authorized.Code, http.StatusOK, authorized.Body.String())
	}
	var grant model.TerminalFenceGrant
	if err := json.Unmarshal(authorized.Body.Bytes(), &grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if grant.Version != model.TerminalFenceVersion || grant.RunID != "run-a" || grant.AssignmentID != "generation-a" || grant.ExpectedSessions != 2500 || grant.Epoch == 0 || grant.Capability == "" {
		t.Fatalf("grant = %#v, want exact prepared product-generation grant", grant)
	}
	var writes []frame.Frame
	gatewaySession := session.New(session.Config{
		ID: 101,
		WriteFrameFn: func(written frame.Frame, _ session.OutboundMeta) error {
			writes = append(writes, written)
			return nil
		},
	})
	nonce := frame.TerminalFenceNonce{1}
	event, err := frame.NewTerminalFenceRequest(frame.TerminalFenceGrant{Epoch: grant.Epoch, Capability: grant.Capability}, nonce)
	if err != nil {
		t.Fatalf("build terminal event: %v", err)
	}
	if err := app.handler.OnFrame(gateway.Context{Session: gatewaySession, RequestContext: context.Background()}, event); err != nil {
		t.Fatalf("gateway terminal event did not reuse prepared controller: %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("gateway terminal writes = %d, want one ACK", len(writes))
	}
	ack, err := frame.ParseTerminalFenceAck(writes[0].(*frame.EventPacket))
	if err != nil || !ack.Matches(grant.Epoch, nonce) {
		t.Fatalf("gateway terminal ACK = %#v, %v", ack, err)
	}
	status := app.benchTerminal.Status()
	if status.Stage != benchterminal.StageAwaitingSessions || status.ExpectedSessions != 2500 || status.SealedSessions != 1 || status.Epoch != grant.Epoch {
		t.Fatalf("terminal status = %#v, want awaiting exact session cut", status)
	}
}

func TestAppDoesNotAdvertiseTerminalFenceForPartialProductComposition(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t, Config{
		DataDir: shortAppTestDataDir(t),
		Cluster: clusterpkg.Config{NodeID: 1},
		API:     APIConfig{ListenAddr: "127.0.0.1:0"},
		Bench: BenchConfig{
			APIEnabled: true,
			APIToken:   "terminal-api-token",
		},
		Delivery: DeliveryConfig{Enabled: false},
		Gateway: GatewayConfig{Listeners: []gateway.ListenerOptions{{
			Name: "terminal-test", Network: "tcp", Address: "127.0.0.1:0", Transport: "gnet", Protocol: "wkproto",
		}}},
	}, WithCluster(cluster), WithStartupConsole(io.Discard, false))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.benchTerminal != nil {
		t.Fatalf("bench terminal controller = %T, want nil without Online Delivery", app.benchTerminal)
	}
	apiServer, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api = %T, want *accessapi.Server", app.api)
	}
	startTestApp(t, app)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer terminal-api-token")
	apiServer.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var capabilities struct {
		Supports struct {
			TerminalFencePrepare bool `json:"terminal_fence_prepare"`
		} `json:"supports"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &capabilities); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if capabilities.Supports.TerminalFencePrepare {
		t.Fatal("terminal_fence_prepare = true, want fail-closed partial composition")
	}
}
