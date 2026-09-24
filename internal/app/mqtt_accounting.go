package app

import (
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// newMQTTAccounting composes bounded maintenance on fresh metadata and anchored
// original-content reads. It starts no worker and grants no network-send authority.
func newMQTTAccounting(node *cluster.Node, authorization sessioncase.SubscriptionAuthorizer) (*sessioncase.Accounting, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewAccounting(sessioncase.AccountingOptions{Store: node, Metadata: channels.NewSlotMetaSource(node), Channels: node, Authorization: authorization})
}
