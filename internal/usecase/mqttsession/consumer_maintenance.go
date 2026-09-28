package mqttsession

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ConsumerMaintenanceOptions composes authoritative, bounded usecases. This
// maintenance grants no network-write or inflight-admission capability.
type ConsumerMaintenanceOptions struct {
	Store      SourceProgressMetadata
	Accounting *Accounting
	Drain      *SourceDrain
	Progress   *SourceProgress
	Removal    *SourceRemoval
	// Retirement is optional; when set, Removed tombstones of ended lifetimes
	// are deleted behind the source Slot's closed-lifetime fence.
	Retirement *SourceRetirement
	Ender      SessionEnder
	// Timeout bounds one complete binding turn, including exact-owner cleanup.
	Timeout time.Duration
}

// ConsumerMaintenanceResult reports proved outcomes of this turn. Retried End
// confirmations may repeat; these are not unique Session termination counts.
type ConsumerMaintenanceResult struct {
	Accounted, Projected, Removed, QuotaEnded, RevokedEnded bool
	// QualificationRemoved counts a UID tombstone, never a Channel release.
	QualificationRemoved bool
	// Retired reports a deleted Removed tombstone of an ended lifetime.
	Retired bool
}
type ConsumerMaintenance struct{ options ConsumerMaintenanceOptions }

func NewConsumerMaintenance(o ConsumerMaintenanceOptions) (*ConsumerMaintenance, error) {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Accounting == nil || o.Drain == nil || o.Progress == nil || o.Removal == nil || o.Ender == nil || o.Timeout <= 0 || o.Timeout > 5*time.Second {
		return nil, ErrInvalid
	}
	return &ConsumerMaintenance{options: o}, nil
}

// Maintain accounts one Channel content page independently of sockets/windows,
// then projects completion or removal. UID keys project explicit lifetime ending
// without accounting or Channel release. Exact generations fence every turn.
func (c *ConsumerMaintenance) Maintain(parent context.Context, k meta.MQTTSourceBindingKey) (out ConsumerMaintenanceResult, err error) {
	q := meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: k}
	if c == nil || parent == nil || meta.ValidateMQTTRead(q) != nil {
		return out, ErrInvalid
	}
	defer func() {
		if recover() != nil {
			err = ErrSubscriptionCallback
		}
	}()
	ctx, cancel := context.WithTimeout(parent, c.options.Timeout)
	defer cancel()
	if k.Owner.Kind == meta.MQTTBindingUID {
		// UID work never accounts content, acquires an owner scope or grants GC.
		progress, e := c.options.Progress.Reconcile(ctx, k)
		if e != nil {
			return out, e
		}
		if progress.NeedsRemoval {
			removed, e := c.options.Removal.Reconcile(ctx, k)
			if e != nil {
				return out, e
			}
			out.QualificationRemoved = removed.Changed && removed.Binding.Stage == meta.MQTTBindingRemoved
		}
		if err = c.retire(ctx, k, &out); err != nil {
			return out, err
		}
		return out, ctx.Err()
	}
	r, err := c.read(ctx, q)
	if err != nil {
		return out, err
	}
	if r.Session != nil || len(r.Subscriptions) != 0 || len(r.Bindings) > 1 {
		return out, ErrEvidence
	}
	if len(r.Bindings) == 0 {
		// Only retirement deletes binding rows; its fence keeps the key closed.
		return out, nil
	}
	b := r.Bindings[0]
	if meta.ValidateMQTTSourceBinding(b) != nil || b.Key != k {
		return out, ErrEvidence
	}
	if b.Stage == meta.MQTTBindingRemoved {
		return out, c.retire(ctx, k, &out)
	}
	r, err = c.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, Topic: b.Topic})
	if err != nil {
		return out, err
	}
	s := r.Session
	if len(r.Bindings) != 0 || len(r.Subscriptions) > 1 || s == nil || meta.ValidateMQTTSession(*s) != nil || s.Namespace != k.Namespace || s.ClientID != k.ClientID || s.UID != b.UID || s.Generation < k.SessionGeneration {
		return out, ErrEvidence
	}
	if s.Generation == k.SessionGeneration && s.State != meta.MQTTSessionEnded {
		if len(r.Subscriptions) != 1 || !validSubscriptionEvidence(r.Subscriptions[0], *s) || r.Subscriptions[0].Topic != b.Topic || r.Subscriptions[0].Generation < k.SubscriptionGeneration {
			return out, ErrEvidence
		}
		sub := r.Subscriptions[0]
		if sub.Generation > k.SubscriptionGeneration || sub.Stage >= meta.MQTTSubscriptionRemoving {
			_, e := c.options.Drain.ReconcileClosed(ctx, k)
			if errors.Is(e, ErrSourceDrainPending) {
				// The existing binding index retains the next bounded range turn.
				return out, ctx.Err()
			}
			if e != nil {
				return out, e
			}
		}
	}
	reason := meta.MQTTSessionEndReason(0)
	if s.Generation == k.SessionGeneration && s.State == meta.MQTTSessionEnded {
		if s.TerminationReason == meta.MQTTSessionQuota || s.TerminationReason == meta.MQTTSessionRevoked {
			reason = s.TerminationReason
		}
	} else if s.Generation == k.SessionGeneration && b.Stage == meta.MQTTBindingActive {
		if len(r.Subscriptions) != 1 {
			return out, ErrEvidence
		}
		sub := r.Subscriptions[0]
		if !validSubscriptionEvidence(sub, *s) || sub.Topic != b.Topic || sub.Generation < k.SubscriptionGeneration {
			return out, ErrEvidence
		}
		if sub.Generation == k.SubscriptionGeneration && sub.Stage == meta.MQTTSubscriptionActive {
			if sub.OperationID != b.OperationID || sub.AuthorizationVersion != b.AuthorizationVersion {
				return out, ErrEvidence
			}
			key := meta.MQTTDeliveryCursorKey{Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, SubscriptionGeneration: k.SubscriptionGeneration, SourceKind: meta.MQTTSourceChannel, SourceID: k.Owner.ID, SourceGeneration: k.Owner.Generation}
			a, e := c.options.Accounting.Account(ctx, key)
			if e != nil {
				if !errors.Is(e, ErrSubscriptionDenied) && !errors.Is(e, ErrSubscriptionRevoked) {
					return out, e
				}
				reason = meta.MQTTSessionRevoked
			} else {
				if a.Owner != sessionOwner(*s) {
					return out, ErrFenced
				}
				out.Accounted = a.Changed
				if a.Ended {
					reason = meta.MQTTSessionQuota
				}
			}
		}
	}
	if reason != 0 {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if err = c.options.Ender.End(ctx, EndCommand{Owner: sessionOwner(*s), Reason: reason}); err != nil {
			return out, err
		}
		if err = ctx.Err(); err != nil {
			return out, err
		}
		out.QuotaEnded = reason == meta.MQTTSessionQuota
		out.RevokedEnded = reason == meta.MQTTSessionRevoked
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	progress, err := c.options.Progress.Reconcile(ctx, k)
	if err != nil {
		return out, err
	}
	out.Projected = progress.Changed
	if progress.NeedsRemoval {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		removed, e := c.options.Removal.Reconcile(ctx, k)
		if e != nil {
			return out, e
		}
		out.Removed = removed.Changed && removed.Binding.Stage == meta.MQTTBindingRemoved
	}
	return out, ctx.Err()
}

// retire runs one optional tombstone retirement turn. A lost race is benign:
// the next discovery pass rereads the binding.
func (c *ConsumerMaintenance) retire(ctx context.Context, k meta.MQTTSourceBindingKey, out *ConsumerMaintenanceResult) error {
	if c.options.Retirement == nil {
		return nil
	}
	done, err := c.options.Retirement.Reconcile(ctx, k)
	if errors.Is(err, ErrConflict) {
		return nil
	}
	out.Retired = done
	return err
}

func (c *ConsumerMaintenance) read(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := ctx.Err(); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := c.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return r, err
	}
	if err = ctx.Err(); err != nil {
		return meta.MQTTReadResult{}, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Runtime != nil || r.Admission != nil || r.Membership != nil || r.Accounting != nil || len(r.Directory)+len(r.SourceOwners)+len(r.Sessions)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Wills) != 0 {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}
