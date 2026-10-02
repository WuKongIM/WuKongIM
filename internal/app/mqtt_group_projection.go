package app

import (
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTGroupProjection composes authoritative source, Session and shared replay
// ports. Inbox admission and product listener lifecycle remain separate wiring.
func newMQTTGroupProjection(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer, ids interface{ Next() uint64 }) (*sessioncase.GroupProjection, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	protector, err := clusterinfra.NewMQTTSourceProtector(clusterinfra.MQTTSourceProtectorOptions{Node: node, MessageIDs: ids})
	if err != nil {
		return nil, err
	}
	replay, err := newMQTTReplayCoordinator(node, ids)
	if err != nil {
		return nil, err
	}
	return sessioncase.NewGroupProjection(sessioncase.GroupProjectionOptions{Store: node, Owners: owners, Authorization: authorization, Sources: protector, Replay: replay})
}
