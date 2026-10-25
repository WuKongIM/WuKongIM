package gateway

import (
	"context"
	"errors"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"testing"
)

func TestHandlerOnSessionActivatePreservesWrappedPhaseAndFailureClass(t *testing.T) {
	cause := presence.WrapActivationPhase(presence.ActivationPhaseRegister, presence.WrapActivationPhase(presence.ActivationPhaseRecovery, authority.ErrRouteNotReady))
	sess := session.New(session.Config{ID: 101})
	sess.SetValue(coregateway.SessionValueUID, "private-uid")
	handler := New(Options{Presence: &recordingPresence{activateErr: cause}})
	connack, err := handler.OnSessionActivate(&coregateway.Context{Session: sess, RequestContext: context.Background()})
	if connack != nil || !errors.Is(err, authority.ErrRouteNotReady) || err.Error() != cause.Error() {
		t.Fatalf("activation result = %+v/%v", connack, err)
	}
	classified, ok := err.(interface{ GatewayAuthFailure() string })
	if !ok || classified.GatewayAuthFailure() != "activation_route_not_ready" {
		t.Fatalf("changed gateway auth class: %T/%v", err, err)
	}
}
