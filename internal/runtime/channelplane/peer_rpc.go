package channelplane

import "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"

const (
	// RemoteAppendStatusOK means the remote owner appended the batch successfully.
	RemoteAppendStatusOK = "ok"
	// RemoteAppendStatusNotLeader means the target node is not the channel leader.
	RemoteAppendStatusNotLeader = "not_leader"
	// RemoteAppendStatusStaleRoute means the route epoch does not match the local owner view.
	RemoteAppendStatusStaleRoute = "stale_route"
	// RemoteAppendStatusLeaseExpired means the local owner lease expired before append.
	RemoteAppendStatusLeaseExpired = "lease_expired"
	// RemoteAppendStatusWriteFenced means the channel is currently write fenced.
	RemoteAppendStatusWriteFenced = "write_fenced"
	// RemoteAppendStatusNotReady means the target owner is not ready to accept writes.
	RemoteAppendStatusNotReady = "not_ready"
	// RemoteAppendStatusBackpressure means the target owner rejected the append under load.
	RemoteAppendStatusBackpressure = "backpressure"
	// RemoteAppendStatusInvalid means the remote owner rejected an invalid append envelope.
	RemoteAppendStatusInvalid = "invalid"
)

// AppendBatchesRequest groups remote channel append envelopes for one peer RPC.
type AppendBatchesRequest struct {
	// Batches contains independent channel append envelopes in request order.
	Batches []AppendBatchEnvelope
}

// AppendBatchEnvelope carries one channel append batch and the route epoch it was scheduled under.
type AppendBatchEnvelope struct {
	// RouteEpoch fences the append against stale channel route projections.
	RouteEpoch RouteEpoch
	// Request is the channel append batch to execute on the remote owner.
	Request channel.AppendBatchRequest
}

// AppendBatchesResponse carries one result for each request envelope.
type AppendBatchesResponse struct {
	// Results are aligned with AppendBatchesRequest.Batches.
	Results []AppendBatchRemoteResult
}

// AppendBatchRemoteResult is the typed remote-owner outcome for one append batch.
type AppendBatchRemoteResult struct {
	// Status is one of the RemoteAppendStatus* constants.
	Status string
	// Leader optionally hints the owner that currently leads the channel.
	Leader channel.NodeID
	// Result contains append output when Status is ok.
	Result channel.AppendBatchResult
}
