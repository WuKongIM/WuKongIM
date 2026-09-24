package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTDeliveryCoordinator binds discovery, accounting and sending to the same
// foreground Node and receive policy. App owns accepted-connection registration
// and must join its delivery runtime before closing these dependencies.
func newMQTTDeliveryCoordinator(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer, sessions *sessioncase.App, maxSubscriptions int) (*sessioncase.DeliveryCoordinator, error) {
	sender, err := newMQTTSender(node, owners, authorization, sessions)
	if err != nil {
		return nil, err
	}
	accounting, err := newMQTTAccounting(node, authorization)
	if err != nil {
		return nil, err
	}
	return sessioncase.NewDeliveryCoordinator(sessioncase.DeliveryCoordinatorOptions{Sender: sender, Accounting: accounting, MaxSubscriptions: maxSubscriptions})
}
