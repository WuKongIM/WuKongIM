package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// newMQTTExchangeRecovery obtains old exchange content through fresh Node
// authority and exact Owners. It starts no sender or recovery scheduler.
func newMQTTExchangeRecovery(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer) (*sessioncase.ExchangeRecovery, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewExchangeRecovery(sessioncase.ExchangeRecoveryOptions{Store: node, Owners: owners, Metadata: channels.NewSlotMetaSource(node), Channels: node, Authorization: authorization})
}
