//go:build integration

package core_test

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type authMetadataError struct {
	err    error
	fields []wklog.Field
	calls  *atomic.Int64
}

func (e authMetadataError) Error() string              { return e.err.Error() }
func (e authMetadataError) Unwrap() error              { return e.err }
func (e authMetadataError) GatewayAuthFailure() string { return "activation_route_not_ready" }
func (e authMetadataError) GatewayAuthFailureLogFields() []wklog.Field {
	e.calls.Add(1)
	return e.fields
}

type capturedAuthLog struct {
	message string
	fields  []wklog.Field
}
type authMetadataLogger struct {
	wklog.Logger
	entries chan capturedAuthLog
}

func (l *authMetadataLogger) Named(string) wklog.Logger { return l }
func (l *authMetadataLogger) Warn(message string, fields ...wklog.Field) {
	l.entries <- capturedAuthLog{message, append([]wklog.Field(nil), fields...)}
}

func TestServerAuthFailureMetadataIsActivationOnlyBoundedAndSingleLog(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fieldCount    int
		authenticator bool
		wantExtra     int
	}{
		{"one field", 1, false, 1}, {"exact32", 32, false, 32},
		{"oversize33 unknown", 33, false, 0}, {"authenticator error does not project", 1, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var metadataCalls atomic.Int64
			fields := make([]wklog.Field, tc.fieldCount)
			for i := range fields {
				fields[i] = wklog.Int(fmt.Sprintf("test_metadata_%02d", i), i)
			}
			base := errors.New("activation failure")
			failure := fmt.Errorf("wrapped: %w", authMetadataError{base, fields, &metadataCalls})
			handler := newTestHandler()
			handler.onActivate = func(*gateway.Context) (*frame.ConnackPacket, error) { return nil, failure }
			proto := newScriptedProtocol("wkproto")
			proto.encodedBytes = []byte("connack-system")
			proto.pushDecode(decodeResult{frames: []frame.Frame{&frame.ConnectPacket{UID: "fixture-user", DeviceID: "fixture-device", ClientKey: "opaque-client-key", ClientTimestamp: 1700000000123}}, consumed: 1})
			factory := testkit.NewFakeTransportFactory("auth-metadata-transport")
			registry := core.NewRegistry()
			if err := registry.RegisterProtocol(proto); err != nil {
				t.Fatal(err)
			}
			if err := registry.RegisterTransport(factory); err != nil {
				t.Fatal(err)
			}
			logger := &authMetadataLogger{Logger: wklog.NewNop(), entries: make(chan capturedAuthLog, 8)}
			authenticator := gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
				if tc.authenticator {
					return nil, failure
				}
				return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			})
			srv, err := core.NewServer(registry, &gateway.Options{
				Handler: handler, Authenticator: authenticator, Logger: logger,
				Listeners: []gateway.ListenerOptions{{Name: "auth", Network: "tcp", Address: "127.0.0.1:0", Transport: factory.Name(), Protocol: proto.Name()}},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = srv.Stop() })
			if err := srv.Start(); err != nil {
				t.Fatal(err)
			}
			conn := factory.MustOpen("auth", 1)
			factory.MustData("auth", 1, []byte("c"))
			select {
			case <-conn.CloseCh():
			case <-time.After(3 * time.Second):
				t.Fatal("failed auth did not close")
			}
			if err := srv.Stop(); err != nil {
				t.Fatal(err)
			}
			if len(conn.Writes()) != 1 {
				t.Fatal("failed auth changed CONNACK cadence")
			}
			if len(logger.entries) != 1 {
				t.Fatalf("auth failure logs=%d, want1", len(logger.entries))
			}
			entry := <-logger.entries
			if entry.message != "gateway auth failed" {
				t.Fatalf("changed existing message=%q", entry.message)
			}
			extra := 0
			errorSeen := false
			failureClass := ""
			fingerprint := ""
			for _, f := range entry.fields {
				if f.Key == "connect_fingerprint" {
					fingerprint, _ = f.Value.(string)
				}
				for i := 0; i < tc.fieldCount; i++ {
					if f.Key == fmt.Sprintf("test_metadata_%02d", i) {
						extra++
					}
				}
				if f.Key == "error" {
					errorSeen = true
					if !errors.Is(f.Value.(error), base) {
						t.Fatal("error identity changed")
					}
				}
				if f.Key == "failure" {
					failureClass, _ = f.Value.(string)
				}
			}
			if !errorSeen {
				t.Fatal("original error field was removed")
			}
			if fingerprint != "382d6d5418bf374c708ca41aa9b855d0" {
				t.Fatalf("CONNECT tuple fingerprint = %q", fingerprint)
			}
			wantClass := "activation_route_not_ready"
			if tc.authenticator {
				wantClass = "authenticator_error"
			}
			if failureClass != wantClass {
				t.Fatalf("failure=%q, want%q", failureClass, wantClass)
			}
			if extra != tc.wantExtra {
				t.Fatalf("metadata fields=%d, want%d", extra, tc.wantExtra)
			}
			wantCalls := int64(1)
			if tc.authenticator {
				wantCalls = 0
			}
			if metadataCalls.Load() != wantCalls {
				t.Fatalf("projection calls=%d, want%d", metadataCalls.Load(), wantCalls)
			}
		})
	}
}
