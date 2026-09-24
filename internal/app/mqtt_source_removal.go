package app

import (
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTSourceRemoval binds per-consumer release to foreground Slot authority.
// Aggregate source protection and shared-content retirement retain their ports.
func newMQTTSourceRemoval(node *cluster.Node) (*sessioncase.SourceRemoval, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewSourceRemoval(sessioncase.SourceRemovalOptions{Store: node})
}
