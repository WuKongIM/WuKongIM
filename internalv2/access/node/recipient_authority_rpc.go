package node

import (
	"context"
	"errors"
	"fmt"

	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// RecipientAuthorityRPCServiceID is the clusterv2 RPC service for recipient UID authority work.
const RecipientAuthorityRPCServiceID uint8 = clusternet.RPCRecipientAuthority

// RecipientAuthority accepts post-commit work that is authoritative on this node.
type RecipientAuthority interface {
	Process(context.Context, recipientusecase.ProcessRequest) error
}

// HandleRecipientAuthorityRPC handles one encoded recipient authority RPC payload.
func (a *Adapter) HandleRecipientAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeRecipientAuthorityRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("recipient authority rpc decode failed",
			wklog.Event("internalv2.access.node.recipient_authority_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.recipientAuthority == nil {
		return encodeRecipientAuthorityResponse(recipientAuthorityResponse{Status: rpcStatusRejected})
	}
	err = a.recipientAuthority.Process(ctx, recipientusecase.ProcessRequest{
		Target:     req.Target,
		Event:      req.Event,
		Recipients: req.Recipients,
	})
	status := recipientAuthorityRPCStatusForError(err)
	if err != nil {
		a.rpcLogger().Warn("recipient authority rpc rejected",
			wklog.Event("internalv2.access.node.recipient_authority_rejected"),
			wklog.String("status", status),
			wklog.Error(err),
		)
	}
	return encodeRecipientAuthorityResponse(recipientAuthorityResponse{Status: status})
}

// ProcessRecipientAuthority forwards recipient-authority work to a target node.
func (c *Client) ProcessRecipientAuthority(ctx context.Context, nodeID uint64, req recipientusecase.ProcessRequest) error {
	if c == nil || c.node == nil {
		return fmt.Errorf("internalv2/access/node: recipient authority rpc client not configured")
	}
	body, err := encodeRecipientAuthorityRequest(recipientAuthorityRequest{
		Target:     req.Target,
		Event:      req.Event,
		Recipients: req.Recipients,
	})
	if err != nil {
		return err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, RecipientAuthorityRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodeRecipientAuthorityResponse(respBody)
	if err != nil {
		return err
	}
	return recipientAuthorityRPCErrorForStatus(resp.Status)
}

func recipientAuthorityRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, recipientusecase.ErrNotLeader):
		return rpcStatusNotLeader
	case errors.Is(err, recipientusecase.ErrStaleRoute):
		return rpcStatusStaleRoute
	case errors.Is(err, recipientusecase.ErrRouteNotReady):
		return rpcStatusRouteNotReady
	default:
		return rpcStatusRejected
	}
}

func recipientAuthorityRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusNotLeader:
		return recipientusecase.ErrNotLeader
	case rpcStatusStaleRoute:
		return recipientusecase.ErrStaleRoute
	case rpcStatusRouteNotReady:
		return recipientusecase.ErrRouteNotReady
	case rpcStatusRejected:
		return fmt.Errorf("internalv2/access/node: recipient authority rpc rejected")
	default:
		return fmt.Errorf("internalv2/access/node: unknown recipient authority rpc status %q", status)
	}
}
