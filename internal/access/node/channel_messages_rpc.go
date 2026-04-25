package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// ChannelMessagesQuery describes one leader-authoritative channel message page request.
type ChannelMessagesQuery struct {
	// ChannelID identifies the channel to scan.
	ChannelID channel.ChannelID `json:"channel_id"`
	// SyncMode enables legacy-compatible range sync instead of manager descending query.
	SyncMode bool `json:"sync_mode,omitempty"`
	// BeforeSeq is the exclusive upper sequence bound for pagination.
	BeforeSeq uint64 `json:"before_seq,omitempty"`
	// StartSeq is the inclusive starting sequence boundary for sync mode.
	StartSeq uint64 `json:"start_seq,omitempty"`
	// EndSeq is the exclusive ending sequence boundary for sync mode.
	EndSeq uint64 `json:"end_seq,omitempty"`
	// Limit is the maximum number of matched messages to return.
	Limit int `json:"limit"`
	// PullMode selects the legacy sync direction when sync mode is enabled.
	PullMode uint8 `json:"pull_mode,omitempty"`
	// MessageID filters the result to matching durable message identifiers when set.
	MessageID uint64 `json:"message_id,omitempty"`
	// ClientMsgNo filters the result to matching client message numbers when set.
	ClientMsgNo string `json:"client_msg_no,omitempty"`
}

// ChannelMessagesPage is one channel message page in the order selected by the query mode.
type ChannelMessagesPage struct {
	// Messages contains matched messages in query order.
	Messages []channel.Message `json:"messages,omitempty"`
	// HasMore reports whether another matched page exists.
	HasMore bool `json:"has_more"`
	// NextBeforeSeq is the exclusive upper sequence bound for the next page.
	NextBeforeSeq uint64 `json:"next_before_seq,omitempty"`
}

type channelMessagesRequest struct {
	Query ChannelMessagesQuery `json:"query"`
}

type channelMessagesResponse struct {
	Status   string              `json:"status"`
	LeaderID uint64              `json:"leader_id,omitempty"`
	Page     ChannelMessagesPage `json:"page,omitempty"`
}

func (r channelMessagesResponse) rpcStatus() string {
	return r.Status
}

func (r channelMessagesResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (a *Adapter) handleChannelMessagesRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req channelMessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if a.channelLogDB == nil {
		return nil, fmt.Errorf("access/node: channel log db not configured")
	}

	meta, err := a.refreshMessageQueryMeta(ctx, req.Query.ChannelID)
	switch {
	case errors.Is(err, raftcluster.ErrNoLeader):
		return encodeChannelMessagesResponse(channelMessagesResponse{Status: rpcStatusNoLeader})
	case err != nil:
		return nil, err
	}
	if meta.Leader == 0 {
		return encodeChannelMessagesResponse(channelMessagesResponse{Status: rpcStatusNoLeader})
	}
	if uint64(meta.Leader) != a.localNodeID {
		return encodeChannelMessagesResponse(channelMessagesResponse{
			Status:   rpcStatusNotLeader,
			LeaderID: uint64(meta.Leader),
		})
	}

	committedHW, err := channelhandler.LoadCommittedHW(a.channelLogDB, req.Query.ChannelID)
	if err != nil {
		return nil, err
	}
	if req.Query.SyncMode {
		page, err := channelhandler.SyncMessages(a.channelLogDB, committedHW, channelhandler.SyncMessagesRequest{
			ChannelID: req.Query.ChannelID,
			StartSeq:  req.Query.StartSeq,
			EndSeq:    req.Query.EndSeq,
			Limit:     req.Query.Limit,
			PullMode:  channelhandler.SyncPullMode(req.Query.PullMode),
		})
		if err != nil {
			return nil, err
		}
		return encodeChannelMessagesResponse(channelMessagesResponse{
			Status: rpcStatusOK,
			Page: ChannelMessagesPage{
				Messages: append([]channel.Message(nil), page.Messages...),
				HasMore:  page.HasMore,
			},
		})
	}
	page, err := channelhandler.QueryMessages(a.channelLogDB, committedHW, channelhandler.QueryMessagesRequest{
		ChannelID:   req.Query.ChannelID,
		BeforeSeq:   req.Query.BeforeSeq,
		Limit:       req.Query.Limit,
		MessageID:   req.Query.MessageID,
		ClientMsgNo: req.Query.ClientMsgNo,
	})
	if err != nil {
		return nil, err
	}
	return encodeChannelMessagesResponse(channelMessagesResponse{
		Status: rpcStatusOK,
		Page: ChannelMessagesPage{
			Messages:      append([]channel.Message(nil), page.Messages...),
			HasMore:       page.HasMore,
			NextBeforeSeq: page.NextBeforeSeq,
		},
	})
}

func (a *Adapter) refreshMessageQueryMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error) {
	if a == nil || a.channelMeta == nil {
		return channel.Meta{}, channel.ErrStaleMeta
	}
	return a.channelMeta.RefreshChannelMeta(ctx, id)
}

func (c *Client) QueryChannelMessages(ctx context.Context, nodeID uint64, req ChannelMessagesQuery) (ChannelMessagesPage, error) {
	if c.cluster == nil {
		return ChannelMessagesPage{}, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return ChannelMessagesPage{}, channel.ErrNotLeader
	}

	tried := make(map[uint64]struct{}, 2)
	candidates := []uint64{nodeID}
	var lastErr error

	for len(candidates) > 0 {
		target := candidates[0]
		candidates = candidates[1:]
		if target == 0 {
			continue
		}
		if _, ok := tried[target]; ok {
			continue
		}
		tried[target] = struct{}{}

		resp, err := callDirectRPC(ctx, c, target, channelMessagesRPCServiceID, channelMessagesRequest{
			Query: req,
		}, decodeChannelMessagesResponse)
		if err != nil {
			lastErr = normalizeChannelMessagesRPCError(err)
			continue
		}

		switch resp.Status {
		case rpcStatusOK:
			return resp.Page, nil
		case rpcStatusNotLeader:
			lastErr = channel.ErrNotLeader
			if resp.LeaderID != 0 {
				candidates = append([]uint64{resp.LeaderID}, candidates...)
			}
		case rpcStatusNoLeader:
			lastErr = raftcluster.ErrNoLeader
		default:
			lastErr = fmt.Errorf("access/node: unexpected channel message status %q", resp.Status)
		}
	}

	if lastErr != nil {
		return ChannelMessagesPage{}, lastErr
	}
	return ChannelMessagesPage{}, channel.ErrNotLeader
}

func encodeChannelMessagesResponse(resp channelMessagesResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeChannelMessagesResponse(body []byte) (channelMessagesResponse, error) {
	var resp channelMessagesResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}

func normalizeChannelMessagesRPCError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrStaleMeta) || errors.Is(err, raftcluster.ErrNoLeader) {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, channel.ErrNotLeader.Error()):
		return channel.ErrNotLeader
	case strings.Contains(msg, channel.ErrStaleMeta.Error()):
		return channel.ErrStaleMeta
	case strings.Contains(msg, raftcluster.ErrNoLeader.Error()):
		return raftcluster.ErrNoLeader
	default:
		return err
	}
}
