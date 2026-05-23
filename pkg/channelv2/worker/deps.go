package worker

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// Deps are blocking dependencies used by worker tasks.
type Deps struct {
	// LocalNode is the node executing this worker task.
	LocalNode ch.NodeID
	// Stores opens channel-scoped storage from inside workers.
	Stores store.Factory
	// Transport sends replication RPCs from inside workers.
	Transport transport.Client
}
