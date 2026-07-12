package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ManagerLatestMessagesRPCServiceID is the node-local latest-message RPC service.
const ManagerLatestMessagesRPCServiceID uint8 = clusternet.RPCManagerLatestMessages

// HandleManagerLatestMessagesRPC handles one node-local latest-message read.
func (a *Adapter) HandleManagerLatestMessagesRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerLatestMessagesRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.managerLatestMessages == nil {
		return encodeManagerLatestMessagesResponse(managerLatestMessagesRPCResponse{Status: rpcStatusRejected})
	}
	page, err := a.managerLatestMessages.ListLocalLatestMessages(ctx, req.BeforeMessageID, req.Limit)
	return encodeManagerLatestMessagesResponse(managerLatestMessagesRPCResponse{
		Status: managerLatestMessagesStatusForError(err),
		Page:   page,
	})
}

// ListManagerLatestMessages reads one peer node's local latest-message page.
func (c *Client) ListManagerLatestMessages(ctx context.Context, nodeID, beforeMessageID uint64, limit int) (managementusecase.ListMessagesResponse, error) {
	if c == nil || c.node == nil {
		return managementusecase.ListMessagesResponse{}, managementusecase.ErrLatestMessagesUnavailable
	}
	body, err := encodeManagerLatestMessagesRequest(managerLatestMessagesRPCRequest{BeforeMessageID: beforeMessageID, Limit: limit})
	if err != nil {
		return managementusecase.ListMessagesResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerLatestMessagesRPCServiceID, body)
	if err != nil {
		return managementusecase.ListMessagesResponse{}, err
	}
	resp, err := decodeManagerLatestMessagesResponse(respBody)
	if err != nil {
		return managementusecase.ListMessagesResponse{}, err
	}
	if err := managerLatestMessagesErrorForStatus(resp.Status); err != nil {
		return managementusecase.ListMessagesResponse{}, err
	}
	return resp.Page, nil
}

func managerLatestMessagesStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusInvalidArgument
	default:
		return rpcStatusRejected
	}
}

func managerLatestMessagesErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusInvalidArgument:
		return metadb.ErrInvalidArgument
	case rpcStatusRejected:
		return managementusecase.ErrLatestMessagesUnavailable
	default:
		return fmt.Errorf("internal/access/node: unknown manager latest messages rpc status %q", status)
	}
}
