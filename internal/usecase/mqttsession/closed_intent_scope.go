package mqttsession

import (
	"context"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// closedIntentScope separates live execution from cleanup of durable closed
// intent. A background scope grants no Owner operation or network capability.
type closedIntentScope struct {
	live *subscriptionOperation
	uid  string
}

func (s *closedIntentScope) UID() string { return s.uid }
func (s *closedIntentScope) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.live != nil {
		return s.live.Check()
	}
	return nil
}

func (op *closedIntentScope) checkSession(ctx context.Context, guard *Subscriptions, o contract.Owner, row *meta.MQTTSession) error {
	if op.live != nil {
		return guard.checkSession(ctx, op.live, o, row)
	}
	if err := op.check(ctx); err != nil {
		return err
	}
	if row == nil {
		return ErrFenced
	}
	if meta.ValidateMQTTSession(*row) != nil {
		return ErrEvidence
	}
	if sessionOwner(*row) != o || row.UID != op.uid || row.State == meta.MQTTSessionEnded {
		return ErrFenced
	}
	now, err := guard.now()
	if err != nil {
		return err
	}
	if now.UnixMilli() < row.UpdatedAtMS {
		return ErrClock
	}
	return nil
}
