package node

import (
	"context"
	"errors"
	"fmt"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// ReadOwnerRouteCalls reads exactly one page. Continuation and explicit old-peer
// fallback belong to the caller, so uncertain errors never repeat owner work.
func (c *Client) ReadOwnerRouteCalls(ctx context.Context, owner uint64, calls []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
	if c == nil || c.node == nil || owner == 0 || !presence.ValidOwnerRecoveryCalls(calls, start) {
		return presence.OwnerRouteCallPage{}, authority.ErrRouteNotReady
	}
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{
		Op: presenceOpReadOwnerRouteCalls, RecoveryCalls: calls, RecoveryStart: start,
	})
	if err != nil {
		return presence.OwnerRouteCallPage{}, err
	}
	reply, err := c.node.CallRPC(ctx, owner, PresenceOwnerRPCServiceID, body)
	if err != nil {
		if ownerRouteCallsUnsupported(err) {
			return presence.OwnerRouteCallPage{}, presence.ErrOwnerRouteCallsUnsupported
		}
		return presence.OwnerRouteCallPage{}, err
	}
	return decodeOwnerRouteCalls(reply, calls, start)
}

func (a *Adapter) handleOwnerRouteCalls(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	if a == nil || !presence.ValidOwnerRecoveryCalls(req.RecoveryCalls, req.RecoveryStart) {
		return nil, authority.ErrRouteNotReady
	}
	reader, ok := a.ownerRoutes.(presence.OwnerRouteCallPageReader)
	if !ok {
		return nil, authority.ErrRouteNotReady
	}
	page, err := reader.ReadOwnerRouteCalls(ctx, req.RecoveryCalls, req.RecoveryStart)
	if err != nil {
		return nil, err
	}
	return encodeOwnerRouteCalls(page, req.RecoveryCalls, req.RecoveryStart)
}

func ownerRouteCallsUnsupported(err error) bool {
	match := func(remote transport.RemoteError) bool {
		return remote.Code == transport.RemoteErrorCodeGeneric && remote.Message ==
			fmt.Sprintf("internal/access/node: unknown presence op id %d", presenceOpReadOwnerRouteCallsID)
	}
	var remote transport.RemoteError
	if errors.As(err, &remote) {
		return match(remote)
	}
	var pointer *transport.RemoteError
	return errors.As(err, &pointer) && pointer != nil && match(*pointer)
}

// PresenceAuthorityReadOnly verifies the wire-v2 header before selecting only
// authority reads. The handler still validates every operation's complete body.
func PresenceAuthorityReadOnly(payload []byte) bool {
	if len(payload) < len(presenceRPCRequestMagic)+1 || !isPresenceRPCRequestBinary(payload) {
		return false
	}
	op := payload[len(presenceRPCRequestMagic)]
	return op == presenceOpEndpointsByUIDID || op == presenceOpEndpointsByTargetsID
}

// PresenceOwnerReadOnly verifies the wire-v2 header before selecting only owner
// reads. Unknown versions and operations retain mutation cancellation semantics.
func PresenceOwnerReadOnly(payload []byte) bool {
	if len(payload) < len(presenceRPCRequestMagic)+1 || !isPresenceRPCRequestBinary(payload) {
		return false
	}
	op := payload[len(presenceRPCRequestMagic)]
	return op == presenceOpReadOwnerRoutesID || op == presenceOpReadOwnerRoutesByTargetsID || op == presenceOpReadOwnerRouteCallsID
}
