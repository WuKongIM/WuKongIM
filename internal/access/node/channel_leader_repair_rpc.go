package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// ChannelLeaderRepairRequest describes one authoritative leader repair attempt.
type ChannelLeaderRepairRequest struct {
	// ChannelID identifies the channel to repair.
	ChannelID channel.ChannelID `json:"channel_id"`
	// ObservedChannelEpoch carries the caller's last observed channel epoch.
	ObservedChannelEpoch uint64 `json:"observed_channel_epoch,omitempty"`
	// ObservedLeaderEpoch carries the caller's last observed leader epoch.
	ObservedLeaderEpoch uint64 `json:"observed_leader_epoch,omitempty"`
	// Reason records why the caller asked for repair.
	Reason string `json:"reason,omitempty"`
}

// ChannelLeaderRepairResult returns the authoritative runtime meta after repair.
type ChannelLeaderRepairResult struct {
	// Meta is the authoritative runtime metadata after the repair attempt.
	Meta metadb.ChannelRuntimeMeta `json:"meta"`
	// Changed reports whether the repair persisted a new channel leader.
	Changed bool `json:"changed"`
}

type channelLeaderRepairResponse struct {
	Status   string                     `json:"status"`
	LeaderID uint64                     `json:"leader_id,omitempty"`
	Result   *ChannelLeaderRepairResult `json:"result,omitempty"`
}

func (r channelLeaderRepairResponse) rpcStatus() string {
	return r.Status
}

func (r channelLeaderRepairResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (a *Adapter) handleChannelLeaderRepairRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req ChannelLeaderRepairRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	slotID := multiraft.SlotID(0)
	if a != nil && a.cluster != nil {
		slotID = a.cluster.SlotForKey(req.ChannelID.ID)
	}
	if status, leaderID, handled := a.authoritativeRPCStatus(slotID); handled {
		return encodeChannelLeaderRepairResponse(channelLeaderRepairResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}
	if a == nil || a.channelLeaderRepair == nil {
		return nil, fmt.Errorf("access/node: channel leader repair not configured")
	}
	result, err := a.channelLeaderRepair.RepairChannelLeaderAuthoritative(ctx, req)
	if errors.Is(err, channel.ErrNoSafeChannelLeader) {
		return encodeChannelLeaderRepairResponse(channelLeaderRepairResponse{Status: rpcStatusNoSafeCandidate})
	}
	if err != nil {
		return nil, err
	}
	return encodeChannelLeaderRepairResponse(channelLeaderRepairResponse{
		Status: rpcStatusOK,
		Result: &result,
	})
}

// RepairChannelLeader asks the current slot leader to repair the channel leader.
func (c *Client) RepairChannelLeader(ctx context.Context, req ChannelLeaderRepairRequest) (ChannelLeaderRepairResult, error) {
	if c == nil || c.cluster == nil {
		return ChannelLeaderRepairResult{}, fmt.Errorf("access/node: cluster not configured")
	}
	slotID := c.cluster.SlotForKey(req.ChannelID.ID)
	body, err := json.Marshal(req)
	if err != nil {
		return ChannelLeaderRepairResult{}, err
	}

	peers := c.cluster.PeersForSlot(slotID)
	if len(peers) == 0 {
		return ChannelLeaderRepairResult{}, raftcluster.ErrSlotNotFound
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

		respBody, err := c.cluster.RPCService(ctx, peer, slotID, channelLeaderRepairRPCServiceID, body)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := decodeChannelLeaderRepairResponse(respBody)
		if err != nil {
			lastErr = err
			continue
		}

		switch resp.Status {
		case rpcStatusOK:
			if resp.Result == nil {
				return ChannelLeaderRepairResult{}, fmt.Errorf("access/node: missing channel leader repair result")
			}
			return *resp.Result, nil
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
			return ChannelLeaderRepairResult{}, channel.ErrNoSafeChannelLeader
		default:
			lastErr = fmt.Errorf("access/node: unexpected channel leader repair status %q", resp.Status)
		}
	}

	if lastErr != nil {
		return ChannelLeaderRepairResult{}, lastErr
	}
	return ChannelLeaderRepairResult{}, raftcluster.ErrNoLeader
}

func encodeChannelLeaderRepairResponse(resp channelLeaderRepairResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeChannelLeaderRepairResponse(body []byte) (channelLeaderRepairResponse, error) {
	var resp channelLeaderRepairResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}
