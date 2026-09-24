package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTAcknowledgements binds exact exchange completion to the foreground Node
// and current local owner registry. Entry send/packet binding is composed later.
func newMQTTAcknowledgements(node *cluster.Node, owners *runtime.Owners) (*sessioncase.Acknowledgements, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewAcknowledgements(sessioncase.AcknowledgementOptions{Store: node, Owners: owners})
}
