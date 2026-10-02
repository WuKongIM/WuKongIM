package app

import (
	"context"
	"errors"
	"sync/atomic"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
)

// mqttConnectionControl translates lifecycle DTOs without owning scheduling,
// authentication or Session policy. Restore retires only the node-local runtime:
// metadata may already be fenced on another node and will be replaced or resumed.
type mqttConnectionControl struct {
	sessions *sessioncase.App
	owners   *runtime.Owners
	// restoring is generation-owned and terminal once maintenance starts. Ordinary
	// shutdown keeps durable Disconnect semantics; a successor gets a fresh flag.
	restoring *atomic.Bool
}

func (c mqttConnectionControl) Renew(ctx context.Context, o contract.Owner) error {
	if c.restoring != nil && c.restoring.Load() {
		return runtime.ErrOwnerFenced
	}
	_, err := c.sessions.Renew(ctx, o)
	return err
}
func (c mqttConnectionControl) Disconnect(ctx context.Context, i runtime.DisconnectIntent) error {
	if c.restoring != nil && c.restoring.Load() {
		// Require exact physical close and joined admitted effects even though the
		// local Connections worker already checks them. Unknown effects still block.
		return c.owners.Quiesce(ctx, i.Owner)
	}
	err := c.sessions.Disconnect(ctx, sessioncase.DisconnectCommand{Owner: i.Owner, Normal: i.Normal, SessionExpirySec: i.SessionExpirySec, ObservedAt: i.ObservedAt})
	if errors.Is(err, sessioncase.ErrFenced) {
		return runtime.ErrOwnerFenced
	}
	return err
}
