package app

import (
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTSourceProgress binds consumer completion to foreground Slot authority.
// It starts no scheduler and does not enable product MQTT admission or GC.
func newMQTTSourceProgress(node *cluster.Node) (*sessioncase.SourceProgress, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewSourceProgress(sessioncase.SourceProgressOptions{Store: node})
}
