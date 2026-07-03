package cluster

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// ChannelIdempotencyNode is the cluster local idempotency lookup surface used by internal.
type ChannelIdempotencyNode interface {
	LookupChannelIdempotency(context.Context, channelruntime.ChannelID, string, string) (channelstore.IdempotencyHit, bool, error)
}

// ChannelIdempotencyStore adapts cluster committed idempotency lookups to channelappend.
type ChannelIdempotencyStore struct {
	node ChannelIdempotencyNode
}

// NewChannelIdempotencyStore creates a ChannelIdempotencyStore.
func NewChannelIdempotencyStore(node ChannelIdempotencyNode) *ChannelIdempotencyStore {
	return &ChannelIdempotencyStore{node: node}
}

// LookupSend returns a prior successful send only when the payload hash matches.
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
