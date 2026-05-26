package cluster

import (
	"context"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

const rpcServiceForward uint8 = 1

func (c *Cluster) forwardToLeader(ctx context.Context, leaderID multiraft.NodeID, slotID multiraft.SlotID, cmd []byte) error {
	_, err := c.forwardToLeaderResult(ctx, leaderID, slotID, cmd)
	return err
}

func (c *Cluster) forwardToLeaderResult(ctx context.Context, leaderID multiraft.NodeID, slotID multiraft.SlotID, cmd []byte) ([]byte, error) {
	payload := encodeForwardPayload(uint64(slotID), cmd)
	resp, err := c.fwdClient.RPCService(ctx, uint64(leaderID), uint64(slotID), rpcServiceForward, payload)
	if err != nil {
		return nil, err
	}
	errCode, data, decodeErr := decodeForwardResp(resp)
	if decodeErr != nil {
		return nil, fmt.Errorf("decode forward response: %w", decodeErr)
	}
	switch errCode {
	case errCodeOK:
		if isHashSlotFencedResult(data) {
			return nil, ErrHashSlotFenced
		}
		if isStaleMetaResult(data) {
			return nil, metadb.ErrStaleMeta
		}
		return data, nil
	case errCodeNotLeader:
		return nil, ErrNotLeader
	case errCodeTimeout:
		return nil, transport.ErrTimeout
	case errCodeNoSlot:
		return nil, ErrSlotNotFound
	default:
		return nil, fmt.Errorf("unknown forward error code: %d", errCode)
	}
}

// handleForwardRPC is the server-side RPC handler for forwarded proposals.
func (c *Cluster) handleForwardRPC(ctx context.Context, body []byte) ([]byte, error) {
	slotID, cmd, err := decodeForwardPayload(body)
	if err != nil {
		return encodeForwardResp(errCodeNoSlot, nil), nil
	}
	if c.stopped.Load() {
		return encodeForwardResp(errCodeTimeout, nil), nil
	}
	_, err = c.runtime.Status(multiraft.SlotID(slotID))
	if err != nil {
		return encodeForwardResp(errCodeNoSlot, nil), nil
	}
	future, err := c.runtime.Propose(ctx, multiraft.SlotID(slotID), cmd)
	if err != nil {
		return encodeForwardResp(errCodeNotLeader, nil), nil
	}
	result, err := future.Wait(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return encodeForwardResp(errCodeTimeout, nil), nil
		}
		return encodeForwardResp(errCodeNotLeader, nil), nil
	}
	return encodeForwardResp(errCodeOK, result.Data), nil
}
