package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestActivationFailureProjectsOriginalRouteHashAfterRollback(t *testing.T) {
	for _, boot := range []uint64{99, 0} {
		t.Run(fmt.Sprint(boot), func(t *testing.T) {
			local := &routeHashLocal{}
			app := presence.New(presence.Options{Local: local, Authority: routeHashAuthority{}, OwnerNodeID: 9, OwnerBootID: boot})
			sess := session.New(session.Config{ID: 11})
			sess.SetValue(coregateway.SessionValueUID, "private-uid")
			handler := New(Options{Presence: app})
			_, err := handler.OnSessionActivate(&coregateway.Context{Session: sess})
			if !errors.Is(err, authority.ErrRouteNotReady) || !local.rolledBack {
				t.Fatalf("original error/rollback changed: %v/%t", err, local.rolledBack)
			}
			var metadata interface{ GatewayAuthFailureLogFields() []wklog.Field }
			if !errors.As(err, &metadata) {
				t.Fatal("failed activation has no metadata projection")
			}
			got := ""
			for _, f := range metadata.GatewayAuthFailureLogFields() {
				if f.Key == "activation_route_hash" {
					got = fmt.Sprint(f.Value)
				}
			}
			want := "7718ef07734da84ac7c13fc5b43b11de"
			if boot == 0 {
				want = ""
			}
			if got != want {
				t.Fatalf("original route hash=%q want%q", got, want)
			}
		})
	}
}

type routeHashLocal struct {
	presence.LocalRegistry
	rolledBack bool
}

func (*routeHashLocal) RegisterPending(presence.LocalSession) error { return nil }
func (l *routeHashLocal) MarkClosingAndUnregister(uint64) (presence.OwnerRoute, bool) {
	l.rolledBack = true
	return presence.OwnerRoute{}, true
}

type routeHashAuthority struct {
	presence.AuthorityClient
	err error
}

func (a routeHashAuthority) RegisterRoute(context.Context, presence.Route) (presence.RegisterResult, error) {
	if a.err != nil {
		return presence.RegisterResult{}, a.err
	}
	return presence.RegisterResult{}, authority.ErrRouteNotReady
}

func TestActivationFailureProjectionRemainsWithin26Fields(t *testing.T) {
	data := presence.RecoveryProviderFailureEvidence{Reason: presence.RecoveryProviderOwnerUnavailable, Calls: 16,
		Observed:      presence.RecoveryProviderObservedOwnerBudget | presence.RecoveryProviderObservedOwnerDispatch | presence.RecoveryProviderObservedOwnerElapsed | presence.RecoveryProviderObservedOwnerError | presence.RecoveryProviderObservedPageNext,
		OwnersPlanned: 3, OwnersCompleted: 1, OwnerNodeID: 2, OwnerMode: presence.RecoveryProviderOwnerRemoteCalls,
		OwnerRPCTrace: rpctrace.TerminalIdentity{State: rpctrace.TerminalTraceBound, TraceID: "01000000000000000000000000000000/1"}}
	for s := presence.RecoveryProviderStage(0); s < presence.RecoveryProviderStageCount; s++ {
		data.Observed |= s.Mask()
	}
	cause := presence.WithRecoveryProviderFailure(authority.ErrRouteNotReady, data)
	app := presence.New(presence.Options{Local: &routeHashLocal{}, Authority: routeHashAuthority{err: cause}, OwnerNodeID: 9, OwnerBootID: 99})
	sess := session.New(session.Config{ID: 11})
	sess.SetValue(coregateway.SessionValueUID, "private-uid")
	_, err := New(Options{Presence: app}).OnSessionActivate(&coregateway.Context{Session: sess})
	var metadata interface{ GatewayAuthFailureLogFields() []wklog.Field }
	if !errors.As(err, &metadata) {
		t.Fatal("failure lost optional evidence")
	}
	fields := metadata.GatewayAuthFailureLogFields()
	if len(fields) != 26 {
		t.Fatalf("full immutable failure projection=%d want26 (cap32)", len(fields))
	}
	got := map[string]any{}
	for _, f := range fields {
		got[f.Key] = f.Value
	}
	if got["recovery_provider_owner_trace_state"] != "bound" || got["recovery_provider_owner_trace_id"] != "01000000000000000000000000000000/1" || got["activation_route_hash"] != "7718ef07734da84ac7c13fc5b43b11de" {
		t.Fatalf("join fields=%v", got)
	}
}
