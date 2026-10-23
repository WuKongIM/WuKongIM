package app

import (
	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// rpcDiagnosticsObserver retains already selected samples after the existing
// transport observation drain; no Store.Record call runs on a business RPC path.
type rpcDiagnosticsObserver struct {
	store  *obsdiagnostics.Store
	nodeID uint64
}

func (o *rpcDiagnosticsObserver) ObserveTransport(event transport.Event) {
	if o == nil || o.store == nil || event.Name != "rpc_trace" || event.RPCTrace == nil {
		return
	}
	sample := event.RPCTrace
	var stage obsdiagnostics.Stage
	var peer uint64
	switch sample.Side {
	case rpctrace.SideClient:
		stage = obsdiagnostics.StageRPCClient
		peer = uint64(event.NodeID)
	case rpctrace.SideServer:
		stage = obsdiagnostics.StageRPCServer
		// Inbound transport does not prove the peer identity; do not use local ID.
	default:
		return
	}
	o.store.Record(obsdiagnostics.Event{
		TraceID: sample.TraceID, Stage: stage, At: sample.At, Duration: sample.Duration,
		NodeID: o.nodeID, PeerNodeID: peer, Service: event.ServiceAlias,
		Result: rpcDiagnosticResult(sample.Result), RPC: sample,
	})
}

func rpcDiagnosticResult(result rpctrace.Result) obsdiagnostics.Result {
	switch result {
	case rpctrace.ResultOK:
		return obsdiagnostics.ResultOK
	case rpctrace.ResultTimeout:
		return obsdiagnostics.ResultTimeout
	case rpctrace.ResultCanceled:
		return obsdiagnostics.ResultCanceled
	case rpctrace.ResultUnfinished:
		return obsdiagnostics.ResultPartial
	default:
		return obsdiagnostics.ResultError
	}
}

var _ transport.Observer = (*rpcDiagnosticsObserver)(nil)
