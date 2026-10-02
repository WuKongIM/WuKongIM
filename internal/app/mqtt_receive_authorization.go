package app

import (
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTReceiveAuthorization shares current Slot receive authority across
// subscription, accounting, recovery and final send checks without caching grants.
func newMQTTReceiveAuthorization(node *cluster.Node) (*sessioncase.ReceiveAuthorization, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewReceiveAuthorization(sessioncase.ReceiveAuthorizationOptions{Store: node})
}
