package cluster

import (
	"bytes"
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// ChannelIdempotencyNode locates a durable key and proves its current committed visibility.
type ChannelIdempotencyNode interface {
	LookupChannelIdempotency(context.Context, channelruntime.ChannelID, string, string) (channelstore.IdempotencyHit, bool, error)
	ReadChannelCommittedBatch(context.Context, []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error)
}

// ChannelIdempotencyStore adapts cluster committed idempotency lookups to channelappend.
type ChannelIdempotencyStore struct {
	node ChannelIdempotencyNode
}

// NewChannelIdempotencyStore creates a ChannelIdempotencyStore.
func NewChannelIdempotencyStore(node ChannelIdempotencyNode) *ChannelIdempotencyStore {
	return &ChannelIdempotencyStore{node: node}
}

// LookupSend proves that a matching durable key is visible in current committed history.
func (s *ChannelIdempotencyStore) LookupSend(ctx context.Context, query channelappend.IdempotencyQuery) (channelappend.SendResult, bool, error) {
	if s == nil || s.node == nil || query.FromUID == "" || query.ClientMsgNo == "" || query.ChannelID == "" || query.ChannelType == 0 {
		return channelappend.SendResult{}, false, nil
	}
	hit, ok, err := s.node.LookupChannelIdempotency(ctx, channelruntime.ChannelID{ID: query.ChannelID, Type: query.ChannelType}, query.FromUID, query.ClientMsgNo)
	if err != nil || !ok {
		if channelIdempotencyLookupMissError(err) {
			return channelappend.SendResult{}, false, nil
		}
		return channelappend.SendResult{}, ok, mapAppendError(err)
	}
	if query.PayloadHash != 0 && hit.PayloadHash != query.PayloadHash {
		return channelappend.SendResult{}, false, nil
	}
	// A failed quorum attempt may leave an exact local index entry. Only a
	// point read through the current Channel Leader can authorize SENDACK.
	if hit.Message.MessageSeq == 0 || hit.Message.MessageID == 0 {
		return channelappend.SendResult{}, false, nil
	}
	reads, err := s.node.ReadChannelCommittedBatch(ctx, []clusterchannels.CommittedRead{{
		ChannelID: channelruntime.ChannelID{ID: query.ChannelID, Type: query.ChannelType},
		Request: channelstore.ReadCommittedRequest{
			FromSeq: hit.Message.MessageSeq, MinSeq: hit.Message.MessageSeq,
			MaxSeq: hit.Message.MessageSeq, Limit: 1, MaxBytes: max(1, len(hit.Message.Payload)),
		},
	}})
	if err != nil {
		return channelappend.SendResult{}, false, mapAppendError(err)
	}
	if len(reads) != 1 {
		return channelappend.SendResult{}, false, channelappend.ErrAppendFailed
	}
	if reads[0].Err != nil {
		return channelappend.SendResult{}, false, mapAppendError(reads[0].Err)
	}
	if len(reads[0].Read.Messages) != 1 {
		return channelappend.SendResult{}, false, nil
	}
	committed := reads[0].Read.Messages[0]
	if committed.MessageID != hit.Message.MessageID || committed.MessageSeq != hit.Message.MessageSeq ||
		committed.FromUID != query.FromUID || committed.ClientMsgNo != query.ClientMsgNo ||
		!bytes.Equal(committed.Payload, hit.Message.Payload) {
		return channelappend.SendResult{}, false, nil
	}
	return channelappend.SendResult{
		MessageID:  hit.Message.MessageID,
		MessageSeq: hit.Message.MessageSeq,
		Reason:     channelappend.ReasonSuccess,
	}, true, nil
}

func channelIdempotencyLookupMissError(err error) bool {
	return errors.Is(err, cluster.ErrNotStarted) ||
		errors.Is(err, cluster.ErrRouteNotReady) ||
		errors.Is(err, cluster.ErrNoSlotLeader) ||
		appendErrorMatches(err, channelruntime.ErrNotReady) ||
		appendErrorMatches(err, channelruntime.ErrChannelNotFound) ||
		appendErrorMatches(err, channelruntime.ErrInvalidConfig)
}
