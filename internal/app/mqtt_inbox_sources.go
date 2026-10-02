package app

import (
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTInboxSources binds owner-independent inbox source preparation to real
// Slot commits and replicated Channel protection. Directory/first-append
// ordering and complete inbox projection remain the caller's responsibility.
func newMQTTInboxSources(node *cluster.Node, ids interface{ Next() uint64 }) (*sessioncase.InboxSources, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	protector, err := clusterinfra.NewMQTTSourceProtector(clusterinfra.MQTTSourceProtectorOptions{Node: node, MessageIDs: ids})
	if err != nil {
		return nil, err
	}
	return sessioncase.NewInboxSources(sessioncase.InboxSourceOptions{Store: node, Sources: protector})
}
