package conn

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

// startReceiveTiming leaves identity validation outside the measured boundary.
// Disabled, unsampled and unnegotiated frames have no extra receive clock or
// timing allocation. No frame can create an independent observation owner.
func (c *Conn) startReceiveTiming(header wire.Header) *rpc.ReceiveTiming {
	if !c.cfg.ReceiveTracing || header.RequestID&rpcTraceSampleBit == 0 ||
		header.ServiceID == wire.CapabilityServiceID || header.ServiceID == wire.TraceCapabilityServiceID {
		return nil
	}
	switch header.Kind {
	case core.FrameKindRPCRequest, core.FrameKindRPCBudgetRequest, core.FrameKindRPCResponse:
	default:
		return nil
	}
	if _, ok := c.TraceIdentity(header.RequestID); !ok {
		return nil
	}
	return &rpc.ReceiveTiming{ServiceID: header.ServiceID, FrameReady: time.Now()}
}

// recordResponseReceive runs only on an actual response receive branch. A
// timeout, synthetic shutdown result or mismatched wire service remains unknown.
// ResultOK describes completed receipt, not the remote business result.
func recordResponseReceive(outbound *Outbound, response *rpc.Response) {
	timing := response.ReceiveTiming
	if outbound.Trace == nil || timing == nil || timing.ServiceID != outbound.ServiceID {
		return
	}
	resumed := time.Now()
	outbound.Trace.RecordResponseReceive(timing.FrameReady, timing.ObserverDone, timing.Notified, resumed, rpctrace.ResultOK)
}
