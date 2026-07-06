package propose

import (
	"context"
	"errors"
	"strings"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// NetworkForwardClient forwards Slot proposals over cluster typed RPC.
type NetworkForwardClient struct {
	caller clusternet.Caller
}

// NewNetworkForwardClient creates a ForwardClient backed by caller.
func NewNetworkForwardClient(caller clusternet.Caller) *NetworkForwardClient {
	return &NetworkForwardClient{caller: caller}
}

// ForwardPropose encodes req and sends it to nodeID.
func (c *NetworkForwardClient) ForwardPropose(ctx context.Context, nodeID uint64, req ForwardRequest) error {
	_, err := c.ForwardProposeResult(ctx, nodeID, req)
	return err
}

// ForwardProposeResult encodes req, sends it to nodeID, and returns remote apply bytes.
func (c *NetworkForwardClient) ForwardProposeResult(ctx context.Context, nodeID uint64, req ForwardRequest) ([]byte, error) {
	payload, err := EncodeForwardRequest(req)
	if err != nil {
		return nil, err
	}
	result, err := clusternet.CallOwnedPayload(ctx, c.caller, nodeID, clusternet.RPCSlotForwardPropose, payload)
	if err != nil {
		return nil, mapForwardError(err)
	}
	return result, nil
}

// ForwardHandler handles remote Slot proposal requests on the target node.
type ForwardHandler struct {
	slots SlotRuntime
}

// NewForwardHandler creates a ForwardHandler for slots.
func NewForwardHandler(slots SlotRuntime) *ForwardHandler { return &ForwardHandler{slots: slots} }

// HandleRPC decodes and applies one forwarded Slot proposal.
func (h *ForwardHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := DecodeForwardRequest(payload)
	if err != nil {
		return nil, err
	}
	if h == nil || h.slots == nil || !h.slots.IsLocalLeader(req.SlotID) {
		return nil, ErrNotLeader
	}
	ctx = WithProposalClass(ctx, req.Class)
	if slots, ok := h.slots.(ResultSlotRuntime); ok {
		return slots.ProposeResult(ctx, req.SlotID, req.Payload)
	}
	if err := h.slots.Propose(ctx, req.SlotID, req.Payload); err != nil {
		return nil, err
	}
	return nil, nil
}

var _ ForwardClient = (*NetworkForwardClient)(nil)
var _ ResultForwardClient = (*NetworkForwardClient)(nil)

func mapForwardError(err error) error {
	if err == nil {
		return nil
	}
	var remoteErr transport.RemoteError
	if errors.As(err, &remoteErr) && strings.Contains(remoteErr.Message, ErrNotLeader.Error()) {
		return ErrNotLeader
	}
	return err
}
