package mqttsession

import (
	"context"
	"time"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// GroupProjectionMetadata keeps both Session and source effects on authoritative
// Slot ports. The projection never opens replica-local storage.
type GroupProjectionMetadata interface {
	GroupSourceMetadata
	SourceDrainMetadata
}

// GroupReplayConfirmation proves shared recovery on every eligible replica for
// the prepared source and boundary. Nil must never mean only quorum copying or
// an enqueued repair; ReplayCoordinator supplies the concrete bounded protocol.
type GroupReplayConfirmation interface {
	Confirm(context.Context, meta.MQTTBindingOwner, uint64) error
}

type GroupProjectionOptions struct {
	Store         GroupProjectionMetadata
	Owners        *runtime.Owners
	Authorization SubscriptionAuthorizer
	Sources       SourceProtector
	Replay        GroupReplayConfirmation
	// Now shares owner monotonic time; Timeout bounds the entire projection.
	Now     func() time.Time
	Timeout time.Duration
}

// GroupProjection implements one-source group establishment/removal. It owns no
// workers, subscriptions or inbox admission; app supplies the complete ports.
type GroupProjection struct {
	sources *GroupSources
	drain   *SourceDrain
	replay  GroupReplayConfirmation
}

func NewGroupProjection(o GroupProjectionOptions) (*GroupProjection, error) {
	if o.Replay == nil {
		return nil, ErrInvalid
	}
	sources, err := NewGroupSources(GroupSourceOptions{Store: o.Store, Owners: o.Owners, Authorization: o.Authorization, Sources: o.Sources, Now: o.Now, Timeout: o.Timeout})
	if err != nil {
		return nil, err
	}
	drain, err := NewSourceDrain(SourceDrainOptions{Store: o.Store, Owners: o.Owners, Sources: o.Sources, Now: o.Now, Timeout: o.Timeout})
	if err != nil {
		return nil, err
	}
	return &GroupProjection{sources: sources, drain: drain, replay: o.Replay}, nil
}

// Establish returns a receipt only after protected preparation, shared recovery
// and renewed current-intent/permission checks. Pending work retains its start.
func (p *GroupProjection) Establish(ctx context.Context, r SubscriptionProjectionRequest) (SubscriptionProjectionReceipt, error) {
	return p.project(ctx, r, true)
}

// Remove seals current closed intent while preserving old exchanges and content.
// Receive revocation cannot prevent this bounded cleanup from completing.
func (p *GroupProjection) Remove(ctx context.Context, r SubscriptionProjectionRequest) (SubscriptionProjectionReceipt, error) {
	return p.project(ctx, r, false)
}

func (p *GroupProjection) project(parent context.Context, r SubscriptionProjectionRequest, establish bool) (out SubscriptionProjectionReceipt, err error) {
	if p == nil || meta.ValidateMQTTSubscription(r.Subscription) != nil || r.Subscription.TargetKind != meta.MQTTSubscriptionGroup {
		return out, ErrInvalid
	}
	expected := meta.MQTTSubscriptionRemoving
	if establish {
		expected = meta.MQTTSubscriptionPreparing
	}
	if r.Subscription.Stage != expected {
		return out, ErrConflict
	}
	guard := p.sources.guard
	op, ctx, cancel, err := guard.begin(parent, r.Owner)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	scope := &preparationScope{live: op, uid: op.UID()}
	if r.UID != op.UID() {
		return out, ErrEvidence
	}
	if _, err = p.sources.current(ctx, scope, r.Owner, r.Subscription); err != nil {
		return out, err
	}
	if establish {
		prepared, e := p.sources.Prepare(ctx, r.Owner, r.Subscription.Topic)
		if e != nil {
			return out, e
		}
		if err = checkSubscriptionScope(ctx, op); err != nil {
			return out, err
		}
		if err = p.replay.Confirm(ctx, prepared.Binding.Key.Owner, prepared.Binding.StartAfter); err != nil {
			return out, err
		}
	} else {
		if _, err = p.drain.SealGroup(ctx, r.Owner, r.Subscription.Topic); err != nil {
			return out, err
		}
	}
	if _, err = p.sources.current(ctx, scope, r.Owner, r.Subscription); err != nil {
		return out, err
	}
	if establish {
		if err = p.sources.authorize(ctx, scope, r.Subscription); err != nil {
			return out, err
		}
	}
	sub := r.Subscription
	return SubscriptionProjectionReceipt{Namespace: sub.Namespace, ClientID: sub.ClientID, Topic: sub.Topic, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, IntentRevision: sub.Revision, OperationID: sub.OperationID}, nil
}

var _ SubscriptionProjection = (*GroupProjection)(nil)

// EstablishOffline resumes one captured Preparing group after disconnect. It
// retains all-replica confirmation but cannot activate intent or emit SUBACK.
func (p *GroupProjection) EstablishOffline(parent context.Context, r SubscriptionProjectionRequest) (out SubscriptionProjectionReceipt, err error) {
	if p == nil || r.Subscription.TargetKind != meta.MQTTSubscriptionGroup {
		return out, ErrInvalid
	}
	scope, ctx, cancel, err := beginOfflinePreparation(parent, p.sources.guard, r)
	if err != nil {
		return out, err
	}
	defer cancel()
	defer finishPreparation(ctx, scope, &out, &err)
	prepared, err := p.sources.prepare(ctx, scope, r.Owner, r.Subscription)
	if err != nil {
		return out, err
	}
	if err = scope.check(ctx); err != nil {
		return out, err
	}
	if err = p.replay.Confirm(ctx, prepared.Binding.Key.Owner, prepared.Binding.StartAfter); err != nil {
		return out, err
	}
	if _, err = p.sources.current(ctx, scope, r.Owner, r.Subscription); err != nil {
		return out, err
	}
	if err = p.sources.authorize(ctx, scope, r.Subscription); err != nil {
		return out, err
	}
	sub := r.Subscription
	return SubscriptionProjectionReceipt{Namespace: sub.Namespace, ClientID: sub.ClientID, Topic: sub.Topic, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, IntentRevision: sub.Revision, OperationID: sub.OperationID}, nil
}
