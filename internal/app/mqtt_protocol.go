package app

import (
	adapter "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/mqtt"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// newMQTTProtocol preserves inbound admission while reserving output headroom
// for six server identities, a subscription identifier and native expiry. Packet
// and peer bounds are unchanged; oversized properties fail without truncation.
func newMQTTProtocol(inbound wire.Limits) *adapter.Adapter {
	outbound := inbound
	outbound.MaxProperties = publication.MaxProperties + 8
	outbound.MaxPropertyBytes = 64 << 10
	return adapter.NewWithOutboundLimits(inbound, outbound)
}
