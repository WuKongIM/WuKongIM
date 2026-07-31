package channelappend

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

func TestOnlineDeliveryCompatibilityPortCarriesExplicitMode(t *testing.T) {
	for _, mode := range []onlinedelivery.Mode{onlinedelivery.ModeDurable, onlinedelivery.ModeTransient} {
		t.Run(modeNameForTest(mode), func(t *testing.T) {
			enqueuer := &recordingOnlineDeliveryEnqueuerForTest{}
			_, err := dispatchRecipientsForTarget(
				context.Background(),
				mode,
				AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, Large: true},
				CommittedEnvelope{MessageID: 7, MessageScopedUIDs: []string{"u1"}},
				subscriberCache{},
				commitPorts{
					recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 1},
					onlineDeliveryEnqueuer:     enqueuer,
					recipientBatchSize:         16,
				},
			)
			if err != nil {
				t.Fatalf("dispatchRecipientsForTarget() error = %v", err)
			}
			if len(enqueuer.plans) != 1 || enqueuer.plans[0].Mode != mode {
				t.Fatalf("plans = %#v, want one plan with mode %v", enqueuer.plans, mode)
			}
		})
	}
}

type recordingOnlineDeliveryEnqueuerForTest struct {
	plans []onlinedelivery.RecipientDeliveryPlan
}

func (e *recordingOnlineDeliveryEnqueuerForTest) EnqueueRecipientDeliveryPlan(_ context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	e.plans = append(e.plans, plan.Clone())
	return nil
}

func modeNameForTest(mode onlinedelivery.Mode) string {
	if mode == onlinedelivery.ModeTransient {
		return "transient"
	}
	return "durable"
}
