package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTSourceDrain wires current Slot mutations and the replicated protector;
// interrupted preparation cannot use local source state as a substitute.
func newMQTTSourceDrain(node *cluster.Node, owners *runtime.Owners, sources sessioncase.SourceProtector) (*sessioncase.SourceDrain, error) {
	if node == nil || sources == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewSourceDrain(sessioncase.SourceDrainOptions{Store: node, Owners: owners, Sources: sources})
}
