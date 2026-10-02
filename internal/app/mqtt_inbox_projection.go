package app

import (
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// mqttInboxProjection composes both halves without reimplementing their policy.
// Product admission must also gate future appends, maintenance and restore fences.
type mqttInboxProjection struct {
	*sessioncase.InboxEstablishment
	*sessioncase.InboxRemoval
}

func newMQTTInboxProjection(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer, ids interface{ Next() uint64 }) (*mqttInboxProjection, error) {
	establishment, err := newMQTTInboxEstablishment(node, owners, authorization, ids)
	if err != nil {
		return nil, err
	}
	protector, err := clusterinfra.NewMQTTSourceProtector(clusterinfra.MQTTSourceProtectorOptions{Node: node, MessageIDs: ids})
	if err != nil {
		return nil, err
	}
	drain, err := newMQTTSourceDrain(node, owners, protector)
	if err != nil {
		return nil, err
	}
	removal, err := sessioncase.NewInboxRemoval(sessioncase.InboxRemovalOptions{Store: node, Owners: owners, Drain: drain})
	if err != nil {
		return nil, err
	}
	return &mqttInboxProjection{InboxEstablishment: establishment, InboxRemoval: removal}, nil
}

var _ sessioncase.SubscriptionProjection = (*mqttInboxProjection)(nil)
