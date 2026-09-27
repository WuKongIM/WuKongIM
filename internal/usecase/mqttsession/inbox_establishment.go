package mqttsession

import (
	"context"
	"time"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// InboxEstablishmentMetadata reads through current Slot authority and commits UID
// qualifications. Local replicas cannot prove absence or directory completion.
type InboxEstablishmentMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	CompareAndSwapMQTTSourceBinding(context.Context, uint64, meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error)
}

type InboxEstablishmentOptions struct {
	Store         InboxEstablishmentMetadata
	Owners        *runtime.Owners
	Authorization SubscriptionAuthorizer
	Sources       InboxAdmissionSources
	Replay        GroupReplayConfirmation
	// PageSize bounds one turn including non-person candidates; default 8, maximum 64.
	PageSize int
	// Timeout bounds a whole page; Now shares the owner's monotonic clock.
	Timeout time.Duration
	Now     func() time.Time
}

// InboxEstablishment implements initial UID discovery and the Establish half of
// SubscriptionProjection. App must also compose safe removal and future append
// admission before exposing a complete product subscription service.
type InboxEstablishment struct {
	options InboxEstablishmentOptions
	guard   *Subscriptions
}

func NewInboxEstablishment(o InboxEstablishmentOptions) (*InboxEstablishment, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.PageSize == 0 {
		o.PageSize = 8
	}
	if o.Store == nil || o.Owners == nil || o.Authorization == nil || o.Sources == nil || o.Replay == nil || o.PageSize < 1 || o.PageSize > 64 || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	p := &InboxEstablishment{options: o, guard: &Subscriptions{options: SubscriptionOptions{Owners: o.Owners, Authorization: o.Authorization, Now: o.Now, Timeout: o.Timeout}}}
	if _, err := p.guard.now(); err != nil {
		return nil, err
	}
	return p, nil
}

// Establish commits qualification before scanning, then advances only confirmed
// source boundaries. Pending pages and lost replies retain their durable cursor.
func (p *InboxEstablishment) Establish(parent context.Context, r SubscriptionProjectionRequest) (out SubscriptionProjectionReceipt, err error) {
	if p == nil || meta.ValidateMQTTSubscription(r.Subscription) != nil || r.Subscription.TargetKind != meta.MQTTSubscriptionUserInbox {
		return out, ErrInvalid
	}
	if r.Subscription.Stage != meta.MQTTSubscriptionPreparing {
		return out, ErrConflict
	}
	op, ctx, cancel, err := p.guard.begin(parent, r.Owner)
	if err != nil {
		return out, err
	}
	defer func() {
		defer op.Done()
		defer cancel()
		if recover() != nil {
			err = ErrSubscriptionCallback
		}
		if stopped := checkSubscriptionScope(ctx, op); err == nil && stopped != nil {
			err = stopped
		}
		if err != nil {
			out = SubscriptionProjectionReceipt{}
		}
	}()
	if r.UID != op.UID() || r.Subscription.TargetID != r.UID || r.Subscription.AuthorizationVersion != 0 {
		return out, ErrEvidence
	}
	t := inboxEstablishmentTurn{p: p, ctx: ctx, op: op, request: r}
	if err = t.current(); err != nil {
		return out, err
	}
	row, found, err := t.qualification()
	if err != nil {
		return out, err
	}
	if !found {
		row = meta.MQTTSourceBinding{Key: t.key(), UID: r.UID, Topic: r.Subscription.Topic, OperationID: r.Subscription.OperationID, Stage: meta.MQTTBindingPreparing}
		row, err = t.write(row)
		if err != nil {
			return out, err
		}
	}
	if !row.DiscoveryDone {
		query := meta.MQTTRead{Kind: meta.MQTTReadInboxDirectory, Owner: row.Key.Owner, Limit: p.options.PageSize, After: meta.MQTTReadCursor{Directory: meta.ChannelKey{ChannelID: row.DiscoveryAfterChannelID, ChannelType: int64(row.DiscoveryAfterChannelType)}}}
		page, e := t.read(query)
		if e != nil {
			return out, e
		}
		if err = validateInboxEstablishmentPage(query, page); err != nil {
			return out, err
		}
		for _, candidate := range page.Directory {
			if candidate.ChannelType == 1 {
				left, right, e := channelid.DecodePersonChannel(candidate.ChannelID)
				if e != nil || channelid.EncodePersonChannel(left, right) != candidate.ChannelID || left != r.UID && right != r.UID {
					return out, ErrEvidence
				}
				if err = t.current(); err != nil {
					return out, err
				}
				ch := SourceChannel{ID: candidate.ChannelID, Type: 1}
				prepared, e := p.options.Sources.Prepare(ctx, row.Key, ch)
				if e != nil {
					return out, e
				}
				if !prepared.Needed || !validInboxAdmissionPreparation(row, ch, prepared) {
					return out, ErrEvidence
				}
				if err = t.current(); err != nil {
					return out, err
				}
				if err = p.options.Replay.Confirm(ctx, prepared.Binding.Key.Owner, prepared.Binding.StartAfter); err != nil {
					return out, err
				}
			}
			row.DiscoveryAfterChannelID, row.DiscoveryAfterChannelType = candidate.ChannelID, uint8(candidate.ChannelType)
			row, err = t.write(row)
			if err != nil {
				return out, err
			}
		}
		if !page.Done {
			return out, ErrReplayPending
		}
		row.DiscoveryDone = true
	}
	if row.Stage != meta.MQTTBindingActive || row.IntentRevision != r.Subscription.Revision {
		row.Stage = meta.MQTTBindingActive
		row, err = t.write(row)
		if err != nil {
			return out, err
		}
	}
	current, found, err := t.qualification()
	if err != nil {
		return out, err
	}
	if !found || current != row || !current.DiscoveryDone || current.Stage != meta.MQTTBindingActive {
		return out, ErrEvidence
	}
	if err = t.current(); err != nil {
		return out, err
	}
	sub := r.Subscription
	return SubscriptionProjectionReceipt{Namespace: sub.Namespace, ClientID: sub.ClientID, Topic: sub.Topic, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, IntentRevision: sub.Revision, OperationID: sub.OperationID}, nil
}
