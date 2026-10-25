//go:build integration

package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type admissionDiagnosticPermissionPort struct {
	err   error
	calls int
}

func (p *admissionDiagnosticPermissionPort) ReadPermissionsBatch(_ context.Context, reads []message.PermissionRead) []message.PermissionReadResult {
	p.calls++
	out := make([]message.PermissionReadResult, len(reads))
	for i := range out {
		out[i].Err = p.err
	}
	return out
}
func (p *admissionDiagnosticPermissionPort) GetChannelForPermission(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, p.err
}
func (p *admissionDiagnosticPermissionPort) ContainsChannelSubscriber(context.Context, string, int64, string) (bool, error) {
	return false, p.err
}
func (p *admissionDiagnosticPermissionPort) HasChannelSubscribers(context.Context, string, int64) (bool, error) {
	return false, p.err
}

// This exercises the gateway batch entry and real message permission policy;
// only the authority port is controlled. No private logging helper is called.
func TestGatewayPermissionAdmissionDiagnosticsPreserveBusyACK(t *testing.T) {
	for _, tc := range []struct {
		name     string
		observed bool
		snapshot message.PermissionAdmissionRejection
	}{
		{"measured", true, message.PermissionAdmissionRejection{Reasons: 6, ActiveCohorts: 16, OwnedCalls: 1024, OwnedBytes: 16776000, RequestBytes: 1282}},
		{"observed-zero", true, message.PermissionAdmissionRejection{}},
		{"unobserved-busy", false, message.PermissionAdmissionRejection{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := fmt.Errorf("%w: %w", message.ErrRouteNotReady, message.ErrBackpressured)
			err := error(cause)
			if tc.observed {
				input := tc.snapshot
				err = message.WithPermissionAdmissionRejection(err, input)
				input.OwnedCalls = -1
			}
			if err.Error() != cause.Error() || !errors.Is(err, cause) {
				t.Fatal("diagnostic attachment changed the original error")
			}
			err = fmt.Errorf("entry adapter: %w", fmt.Errorf("authority mapping: %w", err))
			permissions := &admissionDiagnosticPermissionPort{err: err}
			logger := newRecordingLogger("internal.access.gateway")
			var writes []frame.Frame
			sess := newTestSession(t, &writes)
			sess.SetValue(coregateway.SessionValueUID, "sender")
			handler := New(Options{Logger: logger, SendTimeout: time.Second, Messages: message.New(message.Options{PermissionStore: permissions, PermissionBatchStore: permissions})})
			if callErr := handler.OnSendBatch([]coregateway.SendBatchItem{{
				Context: coregateway.Context{Session: sess, RequestContext: context.Background()},
				Frame:   &frame.SendPacket{ClientSeq: 1, ClientMsgNo: "permission-rejected", ChannelID: "group", ChannelType: 2, Payload: []byte("one")},
			}}); callErr != nil {
				t.Fatal(callErr)
			}
			if permissions.calls != 1 {
				t.Fatalf("real batched authority path calls = %d", permissions.calls)
			}
			if len(writes) != 1 || requireSendack(t, writes, 0).ReasonCode != frame.ReasonSystemBusy {
				t.Fatal("permission backpressure ACK changed")
			}
			entry := requireLogEntry(t, logger, "WARN", "internal.access.gateway.frame", "internal.access.gateway.send_failed")
			logged := requireFieldValue[error](t, entry, "error")
			if logged.Error() != err.Error() || !errors.Is(logged, cause) || !errors.Is(logged, message.ErrBackpressured) || !errors.Is(logged, message.ErrRouteNotReady) {
				t.Fatalf("warning changed error identity/text: %v", logged)
			}
			if tc.observed {
				if !requireFieldValue[bool](t, entry, "permissionAdmissionPresent") {
					t.Fatal("observed admission snapshot absent")
				}
				for key, want := range map[string]int64{
					"permissionAdmissionReasons":       int64(tc.snapshot.Reasons),
					"permissionAdmissionActiveCohorts": int64(tc.snapshot.ActiveCohorts),
					"permissionAdmissionOwnedCalls":    int64(tc.snapshot.OwnedCalls),
					"permissionAdmissionOwnedBytes":    int64(tc.snapshot.OwnedBytes),
					"permissionAdmissionRequestBytes":  int64(tc.snapshot.RequestBytes),
				} {
					if got := requireFieldValue[int64](t, entry, key); got != want {
						t.Errorf("%s = %d, want %d", key, got, want)
					}
				}
			} else {
				for _, field := range entry.fields {
					switch field.Key {
					case "permissionAdmissionPresent", "permissionAdmissionReasons", "permissionAdmissionActiveCohorts", "permissionAdmissionOwnedCalls", "permissionAdmissionOwnedBytes", "permissionAdmissionRequestBytes":
						t.Errorf("unobserved admission reported: %s", field.Key)
					}
				}
			}
		})
	}
}
