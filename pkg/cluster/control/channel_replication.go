package control

import (
	"context"
	"encoding/json"
	"errors"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

const (
	channelReplicationControlVersion = 1
	channelReplicationOpCapabilities = "capabilities"
)

var errChannelReplicationControl = errors.New("control: invalid channel replication control message")

type channelReplicationControlRequest struct {
	Version   int    `json:"version"`
	Operation string `json:"operation"`
}

type channelReplicationControlResponse struct {
	Version    int                                     `json:"version"`
	Capability controller.ChannelReplicationCapability `json:"capability"`
}

// NewChannelReplicationControlHandler answers the Channel replication
// capability probe from the exact running process. Unknown versions,
// operations and fields are rejected, never answered with a guess.
func NewChannelReplicationControlHandler(capability func() controller.ChannelReplicationCapability) clusternet.Handler {
	return channelReplicationControlHandler{capability: capability}
}

type channelReplicationControlHandler struct {
	capability func() controller.ChannelReplicationCapability
}

func (h channelReplicationControlHandler) HandleRPC(_ context.Context, payload []byte) ([]byte, error) {
	var req channelReplicationControlRequest
	if h.capability == nil || decodePresenceControl(payload, &req) != nil ||
		req.Version != channelReplicationControlVersion || req.Operation != channelReplicationOpCapabilities {
		return nil, errChannelReplicationControl
	}
	return json.Marshal(channelReplicationControlResponse{Version: channelReplicationControlVersion, Capability: h.capability()})
}

// ChannelReplicationControlClient probes one node's Channel replication capability.
type ChannelReplicationControlClient struct {
	caller clusternet.Caller
}

// NewChannelReplicationControlClient creates a capability probe client.
func NewChannelReplicationControlClient(caller clusternet.Caller) *ChannelReplicationControlClient {
	return &ChannelReplicationControlClient{caller: caller}
}

// Capabilities returns nodeID's answer. Any transport error, unknown service
// (older node), malformed reply or foreign identity is an error, so callers
// treat the node as unsupported.
func (c *ChannelReplicationControlClient) Capabilities(ctx context.Context, nodeID uint64) (controller.ChannelReplicationCapability, error) {
	if c == nil || c.caller == nil || nodeID == 0 {
		return controller.ChannelReplicationCapability{}, errChannelReplicationControl
	}
	payload, err := json.Marshal(channelReplicationControlRequest{Version: channelReplicationControlVersion, Operation: channelReplicationOpCapabilities})
	if err != nil {
		return controller.ChannelReplicationCapability{}, err
	}
	response, err := clusternet.CallOwnedPayload(ctx, c.caller, nodeID, clusternet.RPCChannelReplicationControl, payload)
	if err != nil {
		return controller.ChannelReplicationCapability{}, err
	}
	var out channelReplicationControlResponse
	if err := decodePresenceControl(response, &out); err != nil {
		return controller.ChannelReplicationCapability{}, err
	}
	if out.Version != channelReplicationControlVersion || out.Capability.NodeID != nodeID {
		return controller.ChannelReplicationCapability{}, errChannelReplicationControl
	}
	return out.Capability, nil
}

// EnsureChannelAuthorityPromise delegates one-way activation to the hosted
// Controller runtime; only the Controller leader can commit it.
func (r *Runtime) EnsureChannelAuthorityPromise(ctx context.Context) (bool, error) {
	if r == nil || r.backend == nil {
		return false, controller.ErrNotStarted
	}
	return r.backend.EnsureChannelAuthorityPromise(ctx)
}
