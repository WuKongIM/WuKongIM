package app

import (
	"context"
	"errors"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
)

// mqttConnectionControl translates lifecycle DTOs without owning scheduling,
// authentication or Session policy. Full product composition remains gated.
type mqttConnectionControl struct{ sessions *sessioncase.App }

func (c mqttConnectionControl) Renew(ctx context.Context, o contract.Owner) error {
	_, err := c.sessions.Renew(ctx, o)
	return err
}
func (c mqttConnectionControl) Disconnect(ctx context.Context, i runtime.DisconnectIntent) error {
	err := c.sessions.Disconnect(ctx, sessioncase.DisconnectCommand{Owner: i.Owner, Normal: i.Normal, SessionExpirySec: i.SessionExpirySec, ObservedAt: i.ObservedAt})
	if errors.Is(err, sessioncase.ErrFenced) {
		return runtime.ErrOwnerFenced
	}
	return err
}
