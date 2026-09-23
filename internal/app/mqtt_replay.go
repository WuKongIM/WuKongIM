package app

import (
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// newMQTTReplayCoordinator wires fresh Slot metadata and foreground Node ports.
// It owns no background worker and does not enable product MQTT admission.
func newMQTTReplayCoordinator(node *cluster.Node, ids interface{ Next() uint64 }) (*sessioncase.ReplayCoordinator, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewReplayCoordinator(sessioncase.ReplayCoordinatorOptions{
		Metadata: channels.NewSlotMetaSource(node), Channels: node, MessageIDs: ids,
	})
}
