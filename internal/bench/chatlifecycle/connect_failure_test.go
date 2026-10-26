package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// connectDiagnosticTestError exercises the public structural observation seam
// without requiring new production declarations in the baseline RED run.
type connectDiagnosticTestError struct {
	phase, kind string
	reason      frame.ReasonCode
	known       bool
	fingerprint [16]byte
}

func (e connectDiagnosticTestError) Error() string        { return "private-uid-token-connect-error" }
func (e connectDiagnosticTestError) ConnectPhase() string { return e.phase }
func (e connectDiagnosticTestError) ConnectKind() string  { return e.kind }
func (e connectDiagnosticTestError) ConnackReason() (frame.ReasonCode, bool) {
	return e.reason, e.known
}
func (e connectDiagnosticTestError) ConnectFingerprint() [16]byte { return e.fingerprint }

type panickingConnectDiagnosticTestError struct{ connectDiagnosticTestError }

func (panickingConnectDiagnosticTestError) ConnectPhase() string { panic("private-observer-panic") }

func TestSessionConnectFailureRetainsClosedHandshakeEvidence(t *testing.T) {
	fingerprint := [16]byte{17, 23, 31, 47}
	tests := []struct {
		name        string
		failure     error
		code        FailureCode
		value       uint64
		fingerprint [16]byte
	}{
		{"system_rejection", connectDiagnosticTestError{"rejected", "rejected", frame.ReasonSystemError, true, fingerprint}, 52, uint64(frame.ReasonSystemError), fingerprint},
		{"unknown_rejection_reason", connectDiagnosticTestError{"rejected", "rejected", 255, false, fingerprint}, 52, 0, fingerprint},
		{"dial_timeout", connectDiagnosticTestError{phase: "dial", kind: "timeout"}, 48, 3, [16]byte{}},
		{"write_network", connectDiagnosticTestError{phase: "connect_write", kind: "network", fingerprint: fingerprint}, 49, 5, fingerprint},
		{"read_eof", connectDiagnosticTestError{phase: "connack_read", kind: "eof", fingerprint: fingerprint}, 50, 4, fingerprint},
		{"wrong_frame", connectDiagnosticTestError{phase: "frame", kind: "protocol", fingerprint: fingerprint}, 51, 8, fingerprint},
		{"crypto", connectDiagnosticTestError{phase: "crypto", kind: "crypto", fingerprint: fingerprint}, 53, 10, fingerprint},
		{"local_closed", connectDiagnosticTestError{phase: "local", kind: "closed"}, 47, 6, [16]byte{}},
		{"unknown_kind", connectDiagnosticTestError{phase: "connack_read", kind: "private-kind", fingerprint: fingerprint}, 50, 0, fingerprint},
		{"foreign_phase", connectDiagnosticTestError{phase: "private-phase", kind: "protocol", fingerprint: fingerprint}, 30, 0, [16]byte{}},
		{"contradictory_success", connectDiagnosticTestError{"rejected", "rejected", frame.ReasonSuccess, true, fingerprint}, 30, 0, [16]byte{}},
		{"legacy_cause", errors.New("private-uid-token-connect-error"), 30, 0, [16]byte{}},
		{"wrapped_cause", fmt.Errorf("private-wrapper: %w", connectDiagnosticTestError{"rejected", "rejected", frame.ReasonSystemError, true, fingerprint}), 52, uint64(frame.ReasonSystemError), fingerprint},
		{"broken_observer", panickingConnectDiagnosticTestError{}, 30, 0, [16]byte{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSessionTestFixture(t)
			fixture.factory.connectErr = test.failure
			uid := fixture.identity.UID(19)
			_, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 19, LoginOrdinal: 1})
			failure, ok := LoginSyncFailureOf(err)
			if !ok || failure.Stage != LoginSyncStageConnect || failure.Reason != LoginSyncReasonTransport || failure.Classification != SyncClassificationHarnessInvalid {
				t.Fatalf("original login verdict changed: %+v/%v", failure, ok)
			}
			snapshot := fixture.verifier.evidence.Snapshot()
			if len(snapshot.Classes) != 1 || snapshot.Classes[0].Count != 1 || len(snapshot.Classes[0].First) != 1 {
				t.Fatalf("expected exactly one bounded failure: %+v", snapshot)
			}
			got := snapshot.Classes[0].First[0]
			if got.Stage != EvidenceStageConnect || got.Code != test.code || got.Value != test.value || got.Fingerprint != test.fingerprint || got.SampleIndex != 0 {
				t.Fatalf("closed CONNECT evidence=%+v, want code=%d value=%d fingerprint=%x", got, test.code, test.value, test.fingerprint)
			}
			body, marshalErr := json.Marshal(snapshot)
			if marshalErr != nil || strings.Contains(string(body), "private-") || strings.Contains(string(body), uid) {
				t.Fatalf("retained evidence leaked caller data or failed to encode: %v", marshalErr)
			}
			counts := fixture.pool.Snapshot()
			if counts.ConnectFailed != 1 || counts.ConnectCanceled != 0 || counts.Online != 0 {
				t.Fatalf("original connection accounting changed: %+v", counts)
			}
		})
	}
}

func TestSessionConnectFailureCancellationRetainsOriginalGate(t *testing.T) {
	fixture := newSessionTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.factory.onConnect = cancel
	fixture.factory.connectErr = connectDiagnosticTestError{phase: "connack_read", kind: "canceled", fingerprint: [16]byte{1}}
	uid := fixture.identity.UID(19)
	_, err := fixture.pool.Login(ctx, SessionLogin{UID: uid, UserIndex: 19, LoginOrdinal: 1})
	failure, ok := LoginSyncFailureOf(err)
	if !ok || failure.Reason != LoginSyncReasonCanceled {
		t.Fatalf("planned cancellation reclassified: %+v/%v", failure, ok)
	}
	if snapshot := fixture.verifier.evidence.Snapshot(); len(snapshot.Classes) != 0 {
		t.Fatalf("cancellation created harness failure: %+v", snapshot)
	}
	counts := fixture.pool.Snapshot()
	if counts.ConnectCanceled != 1 || counts.ConnectFailed != 0 {
		t.Fatalf("cancellation counters changed: %+v", counts)
	}
}
