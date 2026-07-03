package sync

import "context"

// GetStateRequest asks a Controller leader for a full cluster-state snapshot.
type GetStateRequest struct {
	ClusterID     string
	LocalRevision uint64
	LocalChecksum string
}

// GetStateResponse reports leader state availability or carries a full state file payload.
type GetStateResponse struct {
	NotLeader   bool
	NotReady    bool
	StaleLeader bool
	LeaderID    uint64
	NotModified bool
	Revision    uint64
	Checksum    string
	Payload     []byte
}

// Endpoint is an in-process or RPC adapter that serves ControllerV2 state.
type Endpoint interface {
	GetState(context.Context, GetStateRequest) (GetStateResponse, error)
}

// PeerPicker resolves Controller peer IDs to sync endpoints.
type PeerPicker interface {
	Endpoint(nodeID uint64) (Endpoint, bool)
	PeerIDs() []uint64
}
