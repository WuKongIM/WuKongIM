package app

import (
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// newMQTTInboxAdmission composes bounded checkpoint advancement with real source
// preparation and native directory evidence. The product append hook must still
// bind the returned incarnation to its append; this factory starts no workers.
func newMQTTInboxAdmission(node *cluster.Node, ids interface{ Next() uint64 }) (*sessioncase.InboxAdmission, error) {
	sources, err := newMQTTInboxSources(node, ids)
	if err != nil {
		return nil, err
	}
	return sessioncase.NewInboxAdmission(sessioncase.InboxAdmissionOptions{Store: node, Sources: sources})
}
