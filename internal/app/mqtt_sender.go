package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// newMQTTSender binds connection turns to authoritative Node originals, window
// state and exact-owner termination. App separately owns sink and scheduling.
func newMQTTSender(node *cluster.Node, owners *runtime.Owners, authorization sessioncase.SubscriptionAuthorizer, sessions *sessioncase.App) (*sessioncase.Sender, error) {
	if node == nil || sessions == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewSender(sessioncase.SenderOptions{Window: sessioncase.WindowAdmissionOptions{Store: node, Owners: owners, Metadata: channels.NewSlotMetaSource(node), Channels: node, Authorization: authorization}, Ender: sessions})
}
