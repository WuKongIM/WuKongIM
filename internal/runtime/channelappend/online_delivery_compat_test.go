package channelappend

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

func TestOnlineDeliveryCompatibilityPortCarriesExplicitMode(t *testing.T) {
	enqueuer := &recordingOnlineDeliveryEnqueuerForTest{}
	legacy := &recordingRecipientDeliveryEnqueuerForRecipientTest{}
	ports := commitPortsFromOptions(Options{
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  legacy,
		OnlineDeliveryEnqueuer:     enqueuer,
		RecipientBatchSize:         16,
	})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, Large: true}
	_, err := dispatchCommittedRecipientsForTarget(
		context.Background(), target,
		CommittedEnvelope{MessageID: 7, MessageScopedUIDs: []string{"u1"}},
		subscriberCache{}, ports,
	)
	if err != nil {
		t.Fatalf("durable dispatch error = %v", err)
	}
	completion := (realtimeEffect{target: target}).runItem(context.Background(), preparedSend{
		Command: SendCommand{
			MessageID: 8, ChannelID: "room", ChannelType: 2,
			MessageScopedUIDs: []string{"u1"}, NoPersist: true,
		},
	}, ports)
	if completion.result.Err != nil {
		t.Fatalf("transient dispatch error = %v", completion.result.Err)
	}
	if len(enqueuer.plans) != 2 || enqueuer.plans[0].Mode != onlinedelivery.ModeDurable || enqueuer.plans[1].Mode != onlinedelivery.ModeTransient {
		t.Fatalf("plans = %#v, want durable then transient", enqueuer.plans)
	}
	wantTarget := recipientAuthorityTargetForTest(1, 1, 1)
	for _, plan := range enqueuer.plans {
		if len(plan.Targets) != 1 || plan.Targets[0].Target != wantTarget || len(plan.Targets[0].Recipients) != 1 || plan.Targets[0].Recipients[0].UID != "u1" {
			t.Fatalf("plan targets = %#v, want exact target with u1", plan.Targets)
		}
	}
	if got := legacy.callCount(); got != 0 {
		t.Fatalf("legacy enqueue calls = %d, want canonical precedence", got)
	}
}

type recordingOnlineDeliveryEnqueuerForTest struct {
	plans []onlinedelivery.RecipientDeliveryPlan
}

func (e *recordingOnlineDeliveryEnqueuerForTest) EnqueueRecipientDeliveryPlan(_ context.Context, plan onlinedelivery.RecipientDeliveryPlan) error {
	e.plans = append(e.plans, plan.Clone())
	return nil
}
