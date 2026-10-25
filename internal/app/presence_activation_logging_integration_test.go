//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accessgateway "github.com/WuKongIM/WuKongIM/internal/access/gateway"
	applog "github.com/WuKongIM/WuKongIM/internal/log"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Empty membership is the controlled external boundary. The real recovery
// provider constructs owner_roster evidence; this test does not manufacture it.
type activationLogCluster struct {
	*fakePresenceCluster
	rpcCalls atomic.Int64
}

func (*activationLogCluster) LocalControllerSnapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{}, nil
}
func (c *activationLogCluster) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	c.rpcCalls.Add(1)
	return nil, errors.New("unexpected outbound RPC in local Register fixture")
}

type activationLogProtocol struct{ *testkit.FakeProtocol }

func (*activationLogProtocol) ConnectAuthenticationRequired(session.Session) (bool, bool) {
	return true, true
}

func TestGatewayAuthLogRetainsActualLocalRegisterRecoveryProviderEvidence(t *testing.T) {
	for _, format := range []string{"json", "console"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			logger, err := applog.NewLogger(applog.Config{Dir: dir, Level: "info", Format: format})
			if err != nil {
				t.Fatal(err)
			}
			cluster := &activationLogCluster{fakePresenceCluster: newFakePresenceCluster(3, nil)}
			a, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 3}}, WithCluster(cluster), WithLogger(logger))
			if err != nil {
				t.Fatal(err)
			}
			if a.presenceRecovery == nil || a.presenceAuthorityClient == nil {
				t.Fatal("real presence recovery was not composed")
			}
			target, err := a.presenceAuthorityClient.ResolveRouteTarget("fixture-private-uid")
			if err != nil {
				t.Fatal(err)
			}
			a.presenceDirectory.BecomeAuthority(target)
			handler := accessgateway.New(accessgateway.Options{Presence: a.gatewayPresenceUsecase(), Logger: logger.Named("access.gateway")})
			proto := &activationLogProtocol{FakeProtocol: testkit.NewFakeProtocol("wkproto")}
			proto.DecodedFrames = []frame.Frame{&frame.ConnectPacket{UID: "fixture-private-uid", DeviceID: "fixture-private-device"}}
			proto.EncodedBytes = []byte("connack-system")
			factory := testkit.NewFakeTransportFactory("activation-log-transport")
			registry := core.NewRegistry()
			if err := registry.RegisterProtocol(proto); err != nil {
				t.Fatal(err)
			}
			if err := registry.RegisterTransport(factory); err != nil {
				t.Fatal(err)
			}
			srv, err := core.NewServer(registry, &gateway.Options{
				Handler: handler, Logger: logger,
				Authenticator: gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
					return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}, SessionValues: map[string]any{gateway.SessionValueUID: "fixture-private-uid"}}, nil
				}),
				Listeners: []gateway.ListenerOptions{{Name: "activation", Network: "tcp", Address: "127.0.0.1:0", Transport: factory.Name(), Protocol: proto.Name()}},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := srv.Stop(); err != nil {
					t.Error(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := a.Stop(ctx); err != nil {
					t.Error(err)
				}
				if err := logger.Sync(); err != nil {
					t.Error(err)
				}
			})
			if err := srv.Start(); err != nil {
				t.Fatal(err)
			}
			conn := factory.MustOpen("activation", 101)
			factory.MustData("activation", 101, []byte("connect"))
			select {
			case <-conn.CloseCh():
			case <-time.After(3 * time.Second):
				t.Fatal("activation failure did not close")
			}
			if err := srv.Stop(); err != nil {
				t.Fatal(err)
			}
			if len(conn.Writes()) != 1 {
				t.Fatal("activation failure changed CONNACK cadence")
			}
			connack, ok := proto.LastEncodeFrame.(*frame.ConnackPacket)
			if !ok || connack.ReasonCode != frame.ReasonSystemError {
				t.Fatal("activation failure changed retryable CONNACK")
			}
			if cluster.rpcCalls.Load() != 0 {
				t.Fatal("local Register bypass proof unexpectedly used remote RPC")
			}
			if err := logger.Sync(); err != nil {
				t.Fatal(err)
			}
			logs, err := applog.NewAppLogReader(applog.AppLogReaderOptions{Dir: dir}).Entries(context.Background(), applog.AppLogEntriesRequest{Source: "app", Limit: 100, Levels: []string{"WARN"}})
			if err != nil {
				t.Fatal(err)
			}
			var auth []applog.AppLogEntry
			for _, entry := range logs.Items {
				if entry.Message == "gateway auth failed" {
					auth = append(auth, entry)
				}
				if entry.Fields["event"] == "internal.access.node.presence_activation_not_ready" {
					t.Fatal("local Register unexpectedly crossed node adapter")
				}
			}
			if len(auth) != 1 {
				t.Fatalf("auth logs=%d, want1", len(auth))
			}
			entry := auth[0]
			for key, want := range map[string]string{"failure": "activation_route_not_ready", "recovery_provider_scope": "physical_round", "recovery_provider_reason": "owner_roster", "recovery_provider_calls": "1"} {
				if fmt.Sprint(entry.Fields[key]) != want {
					t.Errorf("%s=%v, want%s", key, entry.Fields[key], want)
				}
			}
			if !strings.Contains(fmt.Sprint(entry.Fields["error"]), "presence activation recovery provider:") {
				t.Fatal("real local error chain lost inner phase")
			}
			closed := make(map[string]any)
			for key, value := range entry.Fields {
				if !strings.HasPrefix(key, "recovery_provider_") {
					continue
				}
				closed[key] = value
				for _, secret := range []string{"fixture-private-uid", "fixture-private-device", "unexpected outbound RPC"} {
					if strings.Contains(fmt.Sprint(value), secret) {
						t.Fatalf("new provider field contains private text: %s", key)
					}
				}
			}
			for _, absent := range []string{"recovery_provider_owner_node", "recovery_provider_owner_configured_ns", "recovery_provider_owner_effective_ns", "recovery_provider_owner_elapsed_ns", "recovery_provider_owner_error"} {
				if _, ok := closed[absent]; ok {
					t.Errorf("unknown owner stage was invented: %s", absent)
				}
			}
			artifact, err := json.Marshal(map[string]any{"schema": "activation-provider-log-fixture/v1", "format": format, "auth_failure_logs": len(auth), "outbound_RPCs": cluster.rpcCalls.Load(), "provider_fields": closed})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("redacted_fixture_artifact=%s", artifact)
		})
	}
}
