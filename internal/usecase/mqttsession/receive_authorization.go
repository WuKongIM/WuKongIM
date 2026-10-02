package mqttsession

import (
	"context"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ReceiveMetadata must read a coherent snapshot at current Slot authority after
// a fresh apply barrier. Replica caches and separate channel/member reads cannot
// implement this port; production composition uses the foreground-gated Node.
type ReceiveMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
}

type ReceiveAuthorizationOptions struct {
	Store ReceiveMetadata
	// Timeout bounds an authority call; default five seconds, maximum thirty.
	// The dependency must obey cancellation; no detached worker is created.
	Timeout time.Duration
}

// ReceiveAuthorization returns the current ordinary membership incarnation.
// It owns no cached grants, membership mutations or SEND privilege decisions.
type ReceiveAuthorization struct{ options ReceiveAuthorizationOptions }

var _ SubscriptionAuthorizer = (*ReceiveAuthorization)(nil)

func NewReceiveAuthorization(o ReceiveAuthorizationOptions) (*ReceiveAuthorization, error) {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Timeout <= 0 || o.Timeout > 30*time.Second {
		return nil, ErrInvalid
	}
	return &ReceiveAuthorization{options: o}, nil
}

// AuthorizeSubscription orders a group decision at one fresh snapshot. An
// overlapping removal may occur after that decision; callers recheck immediately
// before delivery and compare the returned incarnation with persisted intent.
// Only definitive absence/disband returns denial. Unavailability retains debt.
func (a *ReceiveAuthorization) AuthorizeSubscription(ctx context.Context, uid string, r SubscriptionRequest) (version uint64, err error) {
	defer func() {
		if recover() != nil {
			version, err = 0, ErrSubscriptionCallback
		}
	}()
	if a == nil || ctx == nil || !contract.ValidIdentity(uid, 1024) || !validSubscriptionRequest(r) {
		return 0, ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	if r.TargetKind == meta.MQTTSubscriptionUserInbox {
		if r.TargetID != uid {
			return 0, ErrSubscriptionDenied
		}
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, a.options.Timeout)
	defer cancel()
	key := meta.SubscriberKey{ChannelID: r.TargetID, ChannelType: 2, UID: uid}
	result, err := a.options.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadMembership, MembershipKey: key})
	if canceled := ctx.Err(); canceled != nil {
		return 0, canceled
	}
	if err != nil {
		return 0, err
	}
	if !result.Done || result.After != (meta.MQTTReadCursor{}) || result.Session != nil || result.Accounting != nil {
		return 0, ErrEvidence
	}
	if len(result.Sessions)+len(result.Subscriptions)+len(result.DeliveryCursors)+len(result.Inflight)+len(result.Bindings)+len(result.Wills)+len(result.SourceOwners) != 0 || meta.ValidateMQTTMembershipView(key, result.Membership) != nil {
		return 0, ErrEvidence
	}
	view := result.Membership
	if view.Channel == nil || view.Channel.Disband != 0 || view.Member == nil {
		return 0, ErrSubscriptionDenied
	}
	return view.Member.Incarnation, nil
}
