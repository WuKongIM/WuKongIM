package mqttsession

import (
	"context"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SealGroup discovers the original source of one closed group intent, including
// preparation interrupted before its first binding commit. It cannot establish
// subscriptions or clear inflight work; final release remains SourceRemoval's job.
func (p *SourceDrain) SealGroup(parent context.Context, o contract.Owner, topic string) (out SourceDrainResult, err error) {
	if p == nil || p.options.Owners == nil || !validSubscriptionTopic(topic) {
		return out, ErrInvalid
	}
	live, ctx, cancel, err := p.guard.begin(parent, o)
	if err != nil {
		return out, err
	}
	defer finishSubscription(live, cancel, &err)
	op := &sourceDrainScope{live: live, uid: live.UID()}
	sub, err := p.closedGroup(ctx, op, o, topic)
	if err != nil {
		return out, err
	}
	r, err := p.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: sub.Namespace, ClientID: sub.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, Limit: 2})
	if err != nil {
		return out, err
	}
	if err = p.checkDrainSession(ctx, op, o, r.Session); err != nil {
		return out, err
	}
	if r.Session.Revision < sub.Revision || len(r.Bindings) != 0 || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) > 1 {
		return out, ErrEvidence
	}
	key := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:" + sub.TargetID}, Namespace: sub.Namespace, ClientID: sub.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation}
	if len(r.DeliveryCursors) == 1 {
		c := r.DeliveryCursors[0]
		if meta.ValidateMQTTDeliveryCursor(c) != nil || c.Key.Namespace != key.Namespace || c.Key.ClientID != key.ClientID || c.Key.SessionGeneration != key.SessionGeneration || c.Key.SubscriptionGeneration != key.SubscriptionGeneration || c.Key.SourceKind != meta.MQTTSourceChannel || c.Key.SourceID != key.Owner.ID || c.Topic != sub.Topic || c.AuthorizationVersion != sub.AuthorizationVersion || c.Revision > r.Session.Revision {
			return out, ErrEvidence
		}
		// A cursor is durable evidence of the original incarnation. Never resolve a
		// replacement source or create a missing binding behind this proven state.
		key.Owner.Generation = c.Key.SourceGeneration
	} else {
		if p.options.Sources == nil {
			return out, ErrEvidence
		}
		channel := SourceChannel{ID: sub.TargetID, Type: 2}
		if err = op.check(ctx); err != nil {
			return out, err
		}
		source, e := p.options.Sources.ProtectMQTTSource(ctx, channel)
		if e != nil {
			return out, e
		}
		if err = op.check(ctx); err != nil {
			return out, err
		}
		if source.Channel != channel || !contract.ValidIdentity(source.Generation, 128) || source.ProtectedAfter >= source.CommittedThrough {
			return out, ErrEvidence
		}
		key.Owner.Generation = source.Generation
		binding, e := p.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: key})
		if e != nil {
			return out, e
		}
		if binding.Session != nil || len(binding.Subscriptions) != 0 || len(binding.DeliveryCursors) != 0 || len(binding.Bindings) > 1 {
			return out, ErrEvidence
		}
		if len(binding.Bindings) == 0 {
			current, e := p.closedGroup(ctx, op, o, topic)
			if e != nil {
				return out, e
			}
			if current != sub {
				return out, ErrConflict
			}
			// Generation records the original Preparing revision. Using the current
			// Removing revision here would erase the later closure witness required by
			// the binding state machine. This registration grants no delivery authority.
			if sub.Generation >= sub.Revision {
				return out, ErrEvidence
			}
			row := meta.MQTTSourceBinding{Key: key, UID: op.UID(), Topic: sub.Topic, IntentRevision: sub.Generation, AuthorizationVersion: sub.AuthorizationVersion, OperationID: sub.OperationID, Stage: meta.MQTTBindingPreparing}
			if _, err = p.sealBinding(ctx, op, meta.MQTTSourceBinding{}, row); err != nil {
				return out, err
			}
		}
	}
	out, err = p.Seal(ctx, o, key)
	if err != nil {
		return SourceDrainResult{}, err
	}
	current, err := p.closedGroup(ctx, op, o, topic)
	if err != nil {
		return SourceDrainResult{}, err
	}
	if current != sub {
		return SourceDrainResult{}, ErrConflict
	}
	return out, nil
}

// closedGroup accepts only current closed intent; caller-supplied old snapshots
// cannot manufacture a registration for an already replaced subscription.
func (p *SourceDrain) closedGroup(ctx context.Context, op *sourceDrainScope, o contract.Owner, topic string) (meta.MQTTSubscription, error) {
	r, err := p.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Topic: topic})
	if err != nil {
		return meta.MQTTSubscription{}, err
	}
	if err = p.checkDrainSession(ctx, op, o, r.Session); err != nil {
		return meta.MQTTSubscription{}, err
	}
	if len(r.Bindings) != 0 || len(r.DeliveryCursors) != 0 || len(r.Subscriptions) != 1 {
		return meta.MQTTSubscription{}, ErrEvidence
	}
	sub := r.Subscriptions[0]
	if !validSubscriptionEvidence(sub, *r.Session) || sub.Topic != topic || sub.OperationID != subscriptionOperationID(sub) {
		return meta.MQTTSubscription{}, ErrEvidence
	}
	if sub.TargetKind != meta.MQTTSubscriptionGroup || sub.Stage < meta.MQTTSubscriptionRemoving {
		return meta.MQTTSubscription{}, ErrConflict
	}
	return sub, nil
}
