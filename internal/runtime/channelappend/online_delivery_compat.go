package channelappend

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

// OnlineDeliveryEnqueuer accepts canonical plans during the delivery-runtime migration.
type OnlineDeliveryEnqueuer interface {
	// EnqueueRecipientDeliveryPlan transfers one bounded plan to Online Delivery.
	EnqueueRecipientDeliveryPlan(context.Context, onlinedelivery.RecipientDeliveryPlan) error
}

// onlineDeliveryCompatibilityEnqueuer keeps recipient selection and batching on
// the established channelappend path while changing only the handoff contract.
type onlineDeliveryCompatibilityEnqueuer struct {
	// mode labels the canonical handoff without changing recipient selection.
	mode onlinedelivery.Mode
	// next is the converged Online Delivery admission boundary.
	next OnlineDeliveryEnqueuer
}

func (e onlineDeliveryCompatibilityEnqueuer) EnqueueRecipientBatch(ctx context.Context, target RecipientAuthorityTarget, batch RecipientBatch) error {
	return e.next.EnqueueRecipientDeliveryPlan(ctx, onlinedelivery.RecipientDeliveryPlan{
		Mode:  e.mode,
		Event: batch.Event,
		Targets: []onlinedelivery.RecipientTargetBatch{{
			Target:     target,
			Recipients: batch.Recipients,
		}},
	})
}

func (e onlineDeliveryCompatibilityEnqueuer) EnqueueRecipientDeliveryPlan(ctx context.Context, plan RecipientDeliveryPlan) error {
	targets := make([]onlinedelivery.RecipientTargetBatch, len(plan.Targets))
	for i := range plan.Targets {
		targets[i] = onlinedelivery.RecipientTargetBatch{
			Target:     plan.Targets[i].Target,
			Recipients: plan.Targets[i].Recipients,
		}
	}
	return e.next.EnqueueRecipientDeliveryPlan(ctx, onlinedelivery.RecipientDeliveryPlan{
		Mode:    e.mode,
		Event:   plan.Event,
		Targets: targets,
	})
}

func deliveryEnqueuerFromOptions(opts Options) RecipientDeliveryEnqueuer {
	if opts.OnlineDeliveryEnqueuer != nil {
		return onlineDeliveryCompatibilityEnqueuer{
			mode: onlinedelivery.ModeDurable,
			next: opts.OnlineDeliveryEnqueuer,
		}
	}
	return opts.RecipientDeliveryEnqueuer
}

// transientDeliveryEnqueuer changes only canonical adapters. Legacy delivery
// retains its pre-convergence behavior because its contract has no mode field.
func transientDeliveryEnqueuer(enqueuer RecipientDeliveryEnqueuer) RecipientDeliveryEnqueuer {
	adapter, ok := enqueuer.(onlineDeliveryCompatibilityEnqueuer)
	if !ok {
		return enqueuer
	}
	adapter.mode = onlinedelivery.ModeTransient
	return adapter
}
