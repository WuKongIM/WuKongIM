package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTSubscriptionRequests binds bounded request completion to real group
// protection/replay and Session authority. Product admission also needs the
// still-pending inbox projection and its future-source admission contract.
func newMQTTSubscriptionRequests(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer, ids interface{ Next() uint64 }, maxSubscriptions int) (*sessioncase.SubscriptionRequests, error) {
	projection, err := newMQTTGroupProjection(node, owners, authorization, ids)
	if err != nil {
		return nil, err
	}
	subscriptions, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: node, Owners: owners, Authorization: authorization, Projection: projection, MaxSubscriptions: maxSubscriptions})
	if err != nil {
		return nil, err
	}
	return sessioncase.NewSubscriptionRequests(sessioncase.SubscriptionRequestOptions{Subscriptions: subscriptions})
}
