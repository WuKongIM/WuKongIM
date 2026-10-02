package node

import (
	"context"
	"errors"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

// MQTTOwnerRPCServiceID isolates owner-local quiescence from WK conflict actions.
const MQTTOwnerRPCServiceID = clusternet.RPCMQTTOwner

// MQTTOwnerQuiescer returns nil only after exact-owner execution and transport
// have quiesced. Unsupported identities or uncertain cleanup must return errors.
type MQTTOwnerQuiescer interface {
	Quiesce(context.Context, contract.Owner) error
}

// MQTTOwnerRPC delegates a bounded exact-owner request without routing again.
// Product composition supplies a foreground-gated owner runtime when enabled.
type MQTTOwnerRPC struct{ Owners MQTTOwnerQuiescer }

func (a MQTTOwnerRPC) HandleRPC(ctx context.Context, body []byte) ([]byte, error) {
	owner, err := decodeMQTTOwnerRequest(body)
	if err != nil {
		return nil, err
	}
	status := byte(2)
	if a.Owners != nil {
		err = a.Owners.Quiesce(ctx, owner)
		switch {
		case err == nil:
			status = 1
		case errors.Is(err, context.Canceled):
			status = 4
		case errors.Is(err, context.DeadlineExceeded):
			status = 5
		case errors.Is(err, runtime.ErrOwnerClose):
			status = 3
		default:
			status = 2
		}
	}
	return encodeMQTTOwnerResponse(owner, status)
}

// MQTTOwnerRPCNode uses the existing node RPC transport, not a new connection pool.
type MQTTOwnerRPCNode interface {
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// MQTTOwnerClient requests isolation from the exact socket owner. It does not
// follow Slot leaders, try another node, or infer isolation from transport loss.
type MQTTOwnerClient struct{ node MQTTOwnerRPCNode }

func NewMQTTOwnerClient(node MQTTOwnerRPCNode) *MQTTOwnerClient { return &MQTTOwnerClient{node: node} }

func (c *MQTTOwnerClient) Quiesce(ctx context.Context, owner contract.Owner) error {
	body, err := encodeMQTTOwnerRequest(owner)
	if err != nil {
		return err
	}
	if c == nil || c.node == nil {
		return runtime.ErrOwnerUnknown
	}
	response, err := c.node.CallRPC(ctx, owner.NodeID, MQTTOwnerRPCServiceID, body)
	if err != nil {
		return err
	}
	echo, status, err := decodeMQTTOwnerResponse(response)
	if err != nil {
		return err
	}
	if echo != owner {
		return runtime.ErrOwnerUnknown
	}
	switch status {
	case 1:
		return nil
	case 2:
		return runtime.ErrOwnerUnknown
	case 3:
		return runtime.ErrOwnerClose
	case 4:
		return context.Canceled
	case 5:
		return context.DeadlineExceeded
	default:
		return errMQTTOwnerWire
	}
}
