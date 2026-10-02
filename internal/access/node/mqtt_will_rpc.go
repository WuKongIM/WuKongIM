package node

import (
	"context"
	"errors"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

// MQTTWillRPCServiceID seals exact local dispatch attempts, never Slot ownership.
const MQTTWillRPCServiceID = clusternet.RPCMQTTWillDispatch

var errMQTTWillProof = errors.New("node: MQTT Will dispatch proof unavailable")

// MQTTWillAttempts grants nil only for a durable sealed non-dispatch. Cleanup
// cannot grant dispatch, and callers must supply the complete original tuple.
type MQTTWillAttempts interface {
	SealUndispatched(context.Context, contract.WillAttempt) error
	ReleaseAttempt(context.Context, contract.WillAttempt) error
}

// MQTTWillRPC owns only bounded decoding and status mapping. The composed port
// checks its own node, generation lock and exact persisted transition.
type MQTTWillRPC struct{ Attempts MQTTWillAttempts }

func (r MQTTWillRPC) HandleRPC(ctx context.Context, body []byte) ([]byte, error) {
	a, op, requestStatus, err := decodeMQTTWillFrame(body, "WKWF")
	if err != nil || op < 1 || op > 2 || requestStatus != 0 {
		return nil, errMQTTWillProof
	}
	status := byte(2)
	if r.Attempts != nil {
		if op == 1 {
			err = r.Attempts.SealUndispatched(ctx, a)
		} else {
			err = r.Attempts.ReleaseAttempt(ctx, a)
		}
		switch {
		case err == nil:
			status = 1
		case errors.Is(err, context.Canceled):
			status = 3
		case errors.Is(err, context.DeadlineExceeded):
			status = 4
		}
	}
	return encodeMQTTWillFrame("WKwf", op, status, a)
}

// MQTTWillClient uses one exact-node call through the existing transport. It
// never follows leaders or treats missing capability/transport loss as proof.
type MQTTWillClient struct{ node MQTTOwnerRPCNode }

func NewMQTTWillClient(node MQTTOwnerRPCNode) *MQTTWillClient { return &MQTTWillClient{node: node} }

func (c *MQTTWillClient) SealUndispatched(ctx context.Context, a contract.WillAttempt) error {
	return c.call(ctx, a, 1)
}
func (c *MQTTWillClient) ReleaseAttempt(ctx context.Context, a contract.WillAttempt) error {
	return c.call(ctx, a, 2)
}

func (c *MQTTWillClient) call(ctx context.Context, a contract.WillAttempt, op byte) error {
	if c == nil || c.node == nil || ctx == nil {
		return errMQTTWillProof
	}
	body, err := encodeMQTTWillFrame("WKWF", op, 0, a)
	if err != nil {
		return err
	}
	response, err := c.node.CallRPC(ctx, a.NodeID, MQTTWillRPCServiceID, body)
	if err != nil {
		return err
	}
	echo, echoOp, status, err := decodeMQTTWillFrame(response, "WKwf")
	if err != nil || echo != a || echoOp != op {
		return errMQTTWillProof
	}
	switch status {
	case 1:
		return nil
	case 3:
		return context.Canceled
	case 4:
		return context.DeadlineExceeded
	default:
		return errMQTTWillProof
	}
}

func encodeMQTTWillFrame(magic string, op, status byte, a contract.WillAttempt) ([]byte, error) {
	identity, err := a.MarshalBinary()
	if err != nil {
		return nil, errMQTTWillProof
	}
	b := append([]byte(magic), 1, op, status)
	return append(b, identity...), nil
}

func decodeMQTTWillFrame(b []byte, magic string) (contract.WillAttempt, byte, byte, error) {
	if len(b) < 8 || len(b) > 7+contract.MaxWillAttemptBytes || string(b[:4]) != magic || b[4] != 1 {
		return contract.WillAttempt{}, 0, 0, errMQTTWillProof
	}
	a, err := contract.DecodeWillAttempt(b[7:])
	if err != nil {
		return a, 0, 0, errMQTTWillProof
	}
	return a, b[5], b[6], nil
}
