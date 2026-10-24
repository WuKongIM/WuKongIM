//go:build integration

package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

// The authority port supplies a measured error contract; real admission and its
// reservation lifecycle are exercised by the proxy public-read integration suite.
type permissionDiagnosticAuthorityError struct {
	snapshot slotproxy.PermissionAdmissionRejection
}

func (*permissionDiagnosticAuthorityError) Error() string { return slotproxy.ErrPermissionBusy.Error() }
func (*permissionDiagnosticAuthorityError) Unwrap() error { return slotproxy.ErrPermissionBusy }
func (e *permissionDiagnosticAuthorityError) PermissionAdmissionRejection() slotproxy.PermissionAdmissionRejection {
	return e.snapshot
}

func TestPermissionAdmissionDiagnosticsCrossMetadataAdapter(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot slotproxy.PermissionAdmissionRejection
		want     messageusecase.PermissionAdmissionRejection
	}{
		{"measured", slotproxy.PermissionAdmissionRejection{Reasons: 7, ActiveCohorts: 64, OwnedCalls: 1024, OwnedBytes: 16776000, RequestBytes: 1282}, messageusecase.PermissionAdmissionRejection{Reasons: 7, ActiveCohorts: 64, OwnedCalls: 1024, OwnedBytes: 16776000, RequestBytes: 1282}},
		// Zero is a port-contract fixture, not a claim that real saturation can
		// occur with no resources. Presence must not depend on numeric values.
		{"observed-zero", slotproxy.PermissionAdmissionRejection{}, messageusecase.PermissionAdmissionRejection{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := &permissionDiagnosticAuthorityError{snapshot: tc.snapshot}
			node := &sendPolicyErrorNode{err: fmt.Errorf("authority: %w", cause)}
			store := NewChannelMetadataStore(node, nil, nil)
			out := store.ReadPermissionsBatch(context.Background(), []messageusecase.PermissionRead{{Kind: messageusecase.PermissionReadUserSendPolicy, UID: "sender"}})
			if len(out) != 1 || out[0].Err == nil {
				t.Fatalf("public batch result = %#v", out)
			}
			err := out[0].Err
			if err.Error() != "internal/message: route not ready: internal/message: backpressured: authority: permission read admission busy" {
				t.Fatalf("existing mapped error text changed: %v", err)
			}
			for _, identity := range []error{cause, slotproxy.ErrPermissionBusy, messageusecase.ErrRouteNotReady, messageusecase.ErrBackpressured} {
				if !errors.Is(err, identity) {
					t.Fatalf("mapped error lost identity %v", identity)
				}
			}
			wrapped := fmt.Errorf("entry: %w", fmt.Errorf("usecase: %w", err))
			got, ok := messageusecase.PermissionAdmissionRejectionFromError(wrapped)
			if !ok || got != tc.want {
				t.Fatalf("mapped snapshot = %+v present=%v, want %+v", got, ok, tc.want)
			}
			// Neither later authority state nor a consumer-owned copy can mutate
			// the value captured at the infrastructure boundary.
			cause.snapshot.OwnedCalls = -1
			got.OwnedCalls = -2
			again, ok := messageusecase.PermissionAdmissionRejectionFromError(wrapped)
			if !ok || again != tc.want {
				t.Fatalf("snapshot was not copied: %+v present=%v", again, ok)
			}
		})
	}
	for _, cause := range []error{nil, slotproxy.ErrPermissionBusy, context.Canceled, context.DeadlineExceeded} {
		store := NewChannelMetadataStore(&sendPolicyErrorNode{err: cause}, nil, nil)
		out := store.ReadPermissionsBatch(context.Background(), []messageusecase.PermissionRead{{Kind: messageusecase.PermissionReadUserSendPolicy, UID: "sender"}})
		if len(out) != 1 {
			t.Fatalf("result alignment changed: %d", len(out))
		}
		if _, ok := messageusecase.PermissionAdmissionRejectionFromError(out[0].Err); ok {
			t.Fatalf("unobserved error gained occupancy: %v", cause)
		}
		if cause != nil && !errors.Is(out[0].Err, cause) {
			t.Fatalf("unobserved cause changed: %v", out[0].Err)
		}
	}
}
