package node

import (
	"context"
	"errors"
	"fmt"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ConversationAuthorityRPCServiceID is the clusterv2 RPC service for UID conversation authority calls.
const ConversationAuthorityRPCServiceID uint8 = clusternet.RPCConversationAuthority

// HandleConversationAuthorityRPC handles one encoded conversation authority RPC payload.
func (a *Adapter) HandleConversationAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeConversationAuthorityRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("conversation authority rpc decode failed",
			wklog.Event("internalv2.access.node.conversation_authority_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.conversation == nil {
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusRejected})
	}
	switch req.Op {
	case conversationOpAdmitPatches:
		err = a.conversation.AdmitPatches(ctx, req.Target, req.Patches)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err)})
	case conversationOpList:
		page, err := a.conversation.ListUserConversationActiveViewForTarget(ctx, req.Target, req.UID, req.After, req.Limit)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err), Page: page})
	case conversationOpDrain:
		result, err := a.conversation.DrainAuthority(ctx, req.Target)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err), DrainResult: result})
	default:
		return nil, fmt.Errorf("internalv2/access/node: unknown conversation authority op %q", req.Op)
	}
}

// AdmitConversationPatches forwards active conversation patches to nodeID.
func (c *Client) AdmitConversationPatches(ctx context.Context, nodeID uint64, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{Op: conversationOpAdmitPatches, Target: target, Patches: patches})
	if err != nil {
		return err
	}
	return conversationRPCErrorForStatus(resp.Status)
}

// ListConversations reads the target-owned active conversation page from nodeID.
func (c *Client) ListConversations(ctx context.Context, nodeID uint64, target conversationusecase.RouteTarget, uid string, after metadb.UserConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{Op: conversationOpList, Target: target, UID: uid, After: after, Limit: limit})
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	if err := conversationRPCErrorForStatus(resp.Status); err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return resp.Page, nil
}

// DrainConversationAuthority asks nodeID to flush and drain one conversation authority target.
func (c *Client) DrainConversationAuthority(ctx context.Context, nodeID uint64, target conversationusecase.RouteTarget) (string, error) {
	resp, err := c.callConversationAuthority(ctx, nodeID, conversationAuthorityRequest{Op: conversationOpDrain, Target: target})
	if err != nil {
		return "", err
	}
	if err := conversationRPCErrorForStatus(resp.Status); err != nil {
		return "", err
	}
	return resp.DrainResult, nil
}

func (c *Client) callConversationAuthority(ctx context.Context, nodeID uint64, req conversationAuthorityRequest) (conversationAuthorityResponse, error) {
	if c == nil || c.node == nil {
		return conversationAuthorityResponse{}, fmt.Errorf("internalv2/access/node: conversation authority rpc client not configured")
	}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		return conversationAuthorityResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ConversationAuthorityRPCServiceID, body)
	if err != nil {
		return conversationAuthorityResponse{}, err
	}
	return decodeConversationAuthorityResponse(respBody)
}

func conversationRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return conversationRPCStatusOK
	case errors.Is(err, conversationusecase.ErrNotLeader):
		return conversationRPCStatusNotLeader
	case errors.Is(err, conversationusecase.ErrStaleRoute):
		return conversationRPCStatusStaleRoute
	case errors.Is(err, conversationusecase.ErrRouteNotReady):
		return conversationRPCStatusRouteNotReady
	case errors.Is(err, conversationusecase.ErrCachePressure):
		return conversationRPCStatusCachePressure
	default:
		return conversationRPCStatusRejected
	}
}

func conversationRPCErrorForStatus(status string) error {
	switch status {
	case conversationRPCStatusOK:
		return nil
	case conversationRPCStatusNotLeader:
		return conversationusecase.ErrNotLeader
	case conversationRPCStatusStaleRoute:
		return conversationusecase.ErrStaleRoute
	case conversationRPCStatusRouteNotReady:
		return conversationusecase.ErrRouteNotReady
	case conversationRPCStatusCachePressure:
		return conversationusecase.ErrCachePressure
	case conversationRPCStatusRejected:
		return fmt.Errorf("internalv2/access/node: conversation authority rpc rejected")
	default:
		return fmt.Errorf("internalv2/access/node: unknown conversation authority rpc status %q", status)
	}
}
