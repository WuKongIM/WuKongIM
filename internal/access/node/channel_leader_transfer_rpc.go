package node

import (
	"context"
	"errors"
	"fmt"

	channelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// ChannelLeaderTransferRequest describes one authoritative leader transfer attempt.
type ChannelLeaderTransferRequest struct {
	// ChannelID identifies the channel to transfer.
	ChannelID channel.ChannelID `json:"channel_id"`
	// ObservedChannelEpoch carries the caller's last observed channel epoch.
	ObservedChannelEpoch uint64 `json:"observed_channel_epoch,omitempty"`
	// ObservedLeaderEpoch carries the caller's last observed leader epoch.
	ObservedLeaderEpoch uint64 `json:"observed_leader_epoch,omitempty"`
	// TargetNodeID is the requested new leader.
	TargetNodeID uint64 `json:"target_node_id"`
}

// ChannelLeaderTransferResult returns the authoritative runtime meta after transfer validation.
type ChannelLeaderTransferResult struct {
	// Meta is authoritative runtime metadata after transfer or validation.
	Meta metadb.ChannelRuntimeMeta `json:"meta"`
	// Changed reports whether transfer persisted changed metadata.
	Changed bool `json:"changed"`
}

type channelLeaderTransferResponse struct {
	Status   string                       `json:"status"`
	LeaderID uint64                       `json:"leader_id,omitempty"`
	Result   *ChannelLeaderTransferResult `json:"result,omitempty"`
}

func (r channelLeaderTransferResponse) rpcStatus() string {
	return r.Status
}

func (r channelLeaderTransferResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (a *Adapter) handleChannelLeaderTransferRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeChannelLeaderTransferRequest(body)
	if err != nil {
		return nil, err
	}
	slotID := multiraft.SlotID(0)
	if a != nil && a.cluster != nil {
		slotID = a.cluster.SlotForKey(req.ChannelID.ID)
	}
	if status, leaderID, handled := a.authoritativeRPCStatus(slotID); handled {
		return encodeChannelLeaderTransferResponse(channelLeaderTransferResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}
	if a == nil || a.channelLeaderTransfer == nil {
		return nil, fmt.Errorf("access/node: channel leader transfer not configured")
	}
	result, err := a.channelLeaderTransfer.TransferChannelLeaderAuthoritative(ctx, toChannelmetaLeaderTransferRequest(req))
	if errors.Is(err, channel.ErrNoSafeChannelLeader) {
		return encodeChannelLeaderTransferResponse(channelLeaderTransferResponse{Status: rpcStatusNoSafeCandidate})
	}
	if err != nil {
		return nil, err
	}
	accessResult := fromChannelmetaLeaderTransferResult(result)
	return encodeChannelLeaderTransferResponse(channelLeaderTransferResponse{
		Status: rpcStatusOK,
		Result: &accessResult,
	})
}

// TransferChannelLeader asks the current slot leader to transfer the channel leader.
func (c *Client) TransferChannelLeader(ctx context.Context, req channelmeta.LeaderTransferRequest) (channelmeta.LeaderTransferResult, error) {
	if c == nil || c.cluster == nil {
		return channelmeta.LeaderTransferResult{}, fmt.Errorf("access/node: cluster not configured")
	}
	slotID := c.cluster.SlotForKey(req.ChannelID.ID)
	body, err := encodeChannelLeaderTransferRequestBinary(fromChannelmetaLeaderTransferRequest(req))
	if err != nil {
		return channelmeta.LeaderTransferResult{}, err
	}

	peers := c.cluster.PeersForSlot(slotID)
	if len(peers) == 0 {
		return channelmeta.LeaderTransferResult{}, raftcluster.ErrSlotNotFound
	}

	tried := make(map[multiraft.NodeID]struct{}, len(peers))
	candidates := append([]multiraft.NodeID(nil), peers...)
	var lastErr error

	for len(candidates) > 0 {
		peer := candidates[0]
		candidates = candidates[1:]
		if _, ok := tried[peer]; ok {
			continue
		}
		tried[peer] = struct{}{}

		respBody, err := c.cluster.RPCService(ctx, peer, slotID, channelLeaderTransferRPCServiceID, body)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := decodeChannelLeaderTransferResponse(respBody)
		if err != nil {
			lastErr = err
			continue
		}

		switch resp.Status {
		case rpcStatusOK:
			if resp.Result == nil {
				return channelmeta.LeaderTransferResult{}, fmt.Errorf("access/node: missing channel leader transfer result")
			}
			return toChannelmetaLeaderTransferResult(*resp.Result), nil
		case rpcStatusNotLeader:
			if leaderID := multiraft.NodeID(resp.LeaderID); leaderID != 0 {
				if _, ok := tried[leaderID]; !ok {
					candidates = append([]multiraft.NodeID{leaderID}, candidates...)
				}
				continue
			}
			lastErr = channel.ErrNotLeader
		case rpcStatusNoLeader:
			lastErr = raftcluster.ErrNoLeader
		case rpcStatusNoSlot:
			lastErr = raftcluster.ErrSlotNotFound
		case rpcStatusNoSafeCandidate:
			return channelmeta.LeaderTransferResult{}, channel.ErrNoSafeChannelLeader
		default:
			lastErr = fmt.Errorf("access/node: unexpected channel leader transfer status %q", resp.Status)
		}
	}

	if lastErr != nil {
		return channelmeta.LeaderTransferResult{}, lastErr
	}
	return channelmeta.LeaderTransferResult{}, raftcluster.ErrNoLeader
}

func encodeChannelLeaderTransferResponse(resp channelLeaderTransferResponse) ([]byte, error) {
	return encodeChannelLeaderTransferResponseBinary(resp)
}

func decodeChannelLeaderTransferResponse(body []byte) (channelLeaderTransferResponse, error) {
	return decodeChannelLeaderTransferResponseBinary(body)
}

func toChannelmetaLeaderTransferRequest(req ChannelLeaderTransferRequest) channelmeta.LeaderTransferRequest {
	return channelmeta.LeaderTransferRequest{
		ChannelID:            req.ChannelID,
		ObservedChannelEpoch: req.ObservedChannelEpoch,
		ObservedLeaderEpoch:  req.ObservedLeaderEpoch,
		TargetNodeID:         req.TargetNodeID,
	}
}

func fromChannelmetaLeaderTransferRequest(req channelmeta.LeaderTransferRequest) ChannelLeaderTransferRequest {
	return ChannelLeaderTransferRequest{
		ChannelID:            req.ChannelID,
		ObservedChannelEpoch: req.ObservedChannelEpoch,
		ObservedLeaderEpoch:  req.ObservedLeaderEpoch,
		TargetNodeID:         req.TargetNodeID,
	}
}

func toChannelmetaLeaderTransferResult(result ChannelLeaderTransferResult) channelmeta.LeaderTransferResult {
	return channelmeta.LeaderTransferResult{
		Meta:    result.Meta,
		Changed: result.Changed,
	}
}

func fromChannelmetaLeaderTransferResult(result channelmeta.LeaderTransferResult) ChannelLeaderTransferResult {
	return ChannelLeaderTransferResult{
		Meta:    result.Meta,
		Changed: result.Changed,
	}
}
