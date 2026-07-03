package node

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	cmdSyncOpSync       = "sync"
	cmdSyncOpSyncAck    = "sync_ack"
	cmdSyncOpPushIntent = "push_intent"
	rpcStatusStaleOwner = "stale_owner"
)

type cmdSyncRPCRequest struct {
	Op     string                     `json:"op"`
	Query  cmdsync.SyncQuery          `json:"query,omitempty"`
	Ack    cmdsync.SyncAckCommand     `json:"ack,omitempty"`
	Intent cmdsync.ConversationIntent `json:"intent,omitempty"`
}

type cmdSyncRPCResponse struct {
	Status   string            `json:"status"`
	Error    string            `json:"error,omitempty"`
	Messages []channel.Message `json:"messages,omitempty"`
}

func (a *Adapter) handleCMDSyncRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeCMDSyncRequest(body)
	if err != nil {
		return nil, err
	}

	switch req.Op {
	case cmdSyncOpSync:
		if a == nil || a.cmdSync == nil {
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: "access/node: cmd sync usecase not configured"})
		}
		result, err := a.cmdSync.Sync(ctx, req.Query)
		if err != nil {
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusOK, Messages: append([]channel.Message(nil), result.Messages...)})
	case cmdSyncOpSyncAck:
		if a == nil || a.cmdSync == nil {
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: "access/node: cmd sync usecase not configured"})
		}
		if err := a.cmdSync.SyncAck(ctx, req.Ack); err != nil {
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusOK})
	case cmdSyncOpPushIntent:
		if a == nil || a.cmdConversationIntents == nil {
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: "access/node: cmd conversation intent sink not configured"})
		}
		if err := validateCMDConversationIntent(req.Intent); err != nil {
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		if err := a.cmdConversationIntents.PushIntent(ctx, req.Intent); err != nil {
			if errors.Is(err, cmdsync.ErrConversationIntentStaleOwner) {
				return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusStaleOwner, Error: err.Error()})
			}
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusOK})
	default:
		return nil, fmt.Errorf("access/node: unknown cmd sync op %q", req.Op)
	}
}

func (c *Client) SyncCMD(ctx context.Context, nodeID uint64, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	resp, err := c.callCMDSync(ctx, nodeID, cmdSyncRPCRequest{Op: cmdSyncOpSync, Query: query})
	if err != nil {
		return cmdsync.SyncResult{}, err
	}
	return cmdsync.SyncResult{Messages: append([]channel.Message(nil), resp.Messages...)}, nil
}

func (c *Client) SyncAckCMD(ctx context.Context, nodeID uint64, cmd cmdsync.SyncAckCommand) error {
	_, err := c.callCMDSync(ctx, nodeID, cmdSyncRPCRequest{Op: cmdSyncOpSyncAck, Ack: cmd})
	return err
}

func (c *Client) PushCMDConversationIntent(ctx context.Context, nodeID uint64, intent cmdsync.ConversationIntent) error {
	_, err := c.callCMDSync(ctx, nodeID, cmdSyncRPCRequest{Op: cmdSyncOpPushIntent, Intent: intent})
	return err
}

func (c *Client) callCMDSync(ctx context.Context, nodeID uint64, req cmdSyncRPCRequest) (cmdSyncRPCResponse, error) {
	if c == nil || c.cluster == nil {
		return cmdSyncRPCResponse{}, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return cmdSyncRPCResponse{}, fmt.Errorf("access/node: node id required")
	}
	body, err := encodeCMDSyncRequestBinary(req)
	if err != nil {
		return cmdSyncRPCResponse{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, cmdSyncRPCServiceID, body)
	if err != nil {
		return cmdSyncRPCResponse{}, err
	}
	resp, err := decodeCMDSyncResponse(respBody)
	if err != nil {
		return cmdSyncRPCResponse{}, err
	}
	if resp.Status != rpcStatusOK {
		if resp.Status == rpcStatusStaleOwner {
			return cmdSyncRPCResponse{}, cmdSyncStaleOwnerError{message: resp.Error}
		}
		if resp.Error != "" {
			return cmdSyncRPCResponse{}, fmt.Errorf("access/node: cmd sync rpc %s: %s", resp.Status, resp.Error)
		}
		return cmdSyncRPCResponse{}, fmt.Errorf("access/node: cmd sync rpc status %s", resp.Status)
	}
	return resp, nil
}

func validateCMDConversationIntent(intent cmdsync.ConversationIntent) error {
	commandChannelID := strings.TrimSpace(intent.CommandChannelID)
	if commandChannelID == "" || !runtimechannelid.IsCommandChannel(commandChannelID) {
		return fmt.Errorf("access/node: cmd conversation intent command channel required")
	}
	if intent.ChannelType == 0 {
		return fmt.Errorf("access/node: cmd conversation intent channel type required")
	}
	if intent.MessageSeq == 0 {
		return fmt.Errorf("access/node: cmd conversation intent message seq required")
	}
	nonEmptyUIDs := 0
	for uid := range intent.UserReadSeqs {
		if strings.TrimSpace(uid) == "" {
			return fmt.Errorf("access/node: cmd conversation intent uid required")
		}
		if intent.UserReadSeqs[uid] > intent.MessageSeq {
			return fmt.Errorf("access/node: cmd conversation intent read seq exceeds message seq")
		}
		nonEmptyUIDs++
	}
	if nonEmptyUIDs == 0 {
		return fmt.Errorf("access/node: cmd conversation intent uid read seqs required")
	}
	return nil
}

type cmdSyncStaleOwnerError struct {
	message string
}

func (e cmdSyncStaleOwnerError) Error() string {
	if e.message == "" {
		return cmdsync.ErrConversationIntentStaleOwner.Error()
	}
	return fmt.Sprintf("access/node: cmd sync rpc %s: %s", rpcStatusStaleOwner, e.message)
}

func (e cmdSyncStaleOwnerError) Unwrap() error {
	return cmdsync.ErrConversationIntentStaleOwner
}
