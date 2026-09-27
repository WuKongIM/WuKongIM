package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTInboxEstablishment composes initial UID discovery with authoritative
// source preparation and all-replica replay confirmation. Product composition
// must also require automatic person append admission and safe inbox removal.
func newMQTTInboxEstablishment(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer, ids interface{ Next() uint64 }) (*sessioncase.InboxEstablishment, error) {
	sources, err := newMQTTInboxSources(node, ids)
	if err != nil {
		return nil, err
	}
	replay, err := newMQTTReplayCoordinator(node, ids)
	if err != nil {
		return nil, err
	}
	return sessioncase.NewInboxEstablishment(sessioncase.InboxEstablishmentOptions{Store: node, Owners: owners, Authorization: authorization, Sources: sources, Replay: replay})
}
