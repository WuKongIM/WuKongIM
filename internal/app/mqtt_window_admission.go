package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// newMQTTWindowAdmission binds original-content preparation and atomic window
// admission to foreground Node authority and the exact local execution owner.
func newMQTTWindowAdmission(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer) (*sessioncase.WindowAdmission, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewWindowAdmission(sessioncase.WindowAdmissionOptions{Store: node, Owners: owners, Metadata: channels.NewSlotMetaSource(node), Channels: node, Authorization: authorization})
}
