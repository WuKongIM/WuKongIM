package onlinedelivery

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

func TestRecipientDeliveryPlanCloneOwnsMutableStorage(t *testing.T) {
	plan := RecipientDeliveryPlan{
		Mode: ModeDurable,
		Event: channelappendcontract.CommittedEnvelope{
			MessageID: 1,
			Payload:   []byte("payload"),
		},
		Targets: []RecipientTargetBatch{{
			Target: authority.Target{SlotID: 7, LeaderNodeID: 2},
			Recipients: []channelappendcontract.Recipient{{
				UID:     "u1",
				JoinSeq: 3,
			}},
		}},
	}

	cloned := plan.Clone()
	plan.Event.Payload[0] = 'X'
	plan.Targets[0].Recipients[0].UID = "changed"

	if got := string(cloned.Event.Payload); got != "payload" {
		t.Fatalf("cloned payload = %q, want payload", got)
	}
	if got := cloned.Targets[0].Recipients[0].UID; got != "u1" {
		t.Fatalf("cloned recipient UID = %q, want u1", got)
	}
	if got := cloned.RecipientCount(); got != 1 {
		t.Fatalf("RecipientCount() = %d, want 1", got)
	}
	if !cloned.Mode.Valid() || Mode(0).Valid() {
		t.Fatalf("mode validity = %v/%v, want true/false", cloned.Mode.Valid(), Mode(0).Valid())
	}
}

func TestOwnerPushCloneOwnsMutableStorage(t *testing.T) {
	push := OwnerPush{
		OwnerNodeID: 2,
		Event: channelappendcontract.CommittedEnvelope{
			MessageID: 1,
			Payload:   []byte("payload"),
		},
		Routes: []Route{{UID: "u1", OwnerNodeID: 2, SessionID: 9}},
	}

	cloned := push.Clone()
	push.Event.Payload[0] = 'X'
	push.Routes[0].UID = "changed"

	if got := string(cloned.Event.Payload); got != "payload" {
		t.Fatalf("cloned payload = %q, want payload", got)
	}
	if got := cloned.Routes[0].UID; got != "u1" {
		t.Fatalf("cloned route UID = %q, want u1", got)
	}
}
