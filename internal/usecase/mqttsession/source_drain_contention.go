package mqttsession

import (
	"context"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// pendingSeal yields only after a definite rejection and fresh proof that
// progress or another seal advanced the same closed obligation. It performs
// three point reads and no write; the ordinary next turn chooses no new start.
func (p *SourceDrain) pendingSeal(ctx context.Context, op *closedIntentScope, owner contract.Owner, before meta.MQTTSourceBinding, sub meta.MQTTSubscription, cursor meta.MQTTDeliveryCursor, minimum uint64) error {
	r, err := p.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: before.Key})
	if err != nil {
		return err
	}
	if r.Session != nil || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || len(r.Bindings) != 1 {
		return ErrEvidence
	}
	current := r.Bindings[0]
	if meta.ValidateMQTTSourceBinding(current) != nil {
		return ErrEvidence
	}
	if sub.Stage != meta.MQTTSubscriptionRemoving || sub.Generation != before.Key.SubscriptionGeneration || !before.BoundaryKnown || before.EndKnown || current.Revision <= before.Revision || current.UpdatedAtMS < before.UpdatedAtMS || current.RecoveryAtMS < before.RecoveryAtMS || current.CompletedThrough < before.CompletedThrough || current.ProgressRevision < before.ProgressRevision {
		return ErrConflict
	}
	// All other fields remain identical. Only SourceProgress's strict advance
	// or the exact Removing seal is eligible; release/retirement is independent.
	progress := current.Stage == before.Stage && current.IntentRevision == before.IntentRevision && !current.EndKnown && current.CompletedThrough > before.CompletedThrough && current.ProgressRevision > before.ProgressRevision
	sealed := current.Stage == meta.MQTTBindingRemoving && current.IntentRevision == sub.Revision && current.EndKnown && current.EndThrough == cursor.AccountedThrough
	if !progress && !sealed {
		return ErrConflict
	}
	normalized := current
	normalized.Revision, normalized.UpdatedAtMS, normalized.RecoveryAtMS = before.Revision, before.UpdatedAtMS, before.RecoveryAtMS
	normalized.CompletedThrough, normalized.ProgressRevision = before.CompletedThrough, before.ProgressRevision
	if sealed {
		normalized.Stage, normalized.IntentRevision = before.Stage, before.IntentRevision
		normalized.EndKnown, normalized.EndThrough = before.EndKnown, before.EndThrough
	}
	if normalized != before {
		return ErrConflict
	}
	session, intent, err := p.closedIntent(ctx, op, owner, current)
	if err != nil {
		return err
	}
	if intent != sub || session.Revision < minimum {
		return ErrConflict
	}
	_, latest, found, err := p.cursor(ctx, op, owner, current, session.Revision)
	if err != nil {
		return err
	}
	if !found || latest.Revision < cursor.Revision || latest.AccountingVersion != cursor.AccountingVersion || latest.AccountedThrough != cursor.AccountedThrough || latest.CompletedThrough < cursor.CompletedThrough || latest.WindowThrough < cursor.WindowThrough {
		return ErrEvidence
	}
	if err = op.check(ctx); err != nil {
		return err
	}
	return ErrSourceDrainPending
}
