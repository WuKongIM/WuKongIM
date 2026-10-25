package app

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// channelAppendAuthorityLocal admits RPC-forwarded sends to the local authority reactor.
type channelAppendAuthorityLocal struct {
	group *channelappend.Group
}

func (l channelAppendAuthorityLocal) SubmitForAuthority(ctx context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	if l.group == nil {
		return channelAppendErrorResults(len(items), channelappend.ErrRouteNotReady)
	}
	submitSpan := rpctrace.Begin(ctx, rpctrace.StageChannelSubmit)
	future, err := l.group.SubmitLocal(ctx, target, items)
	submitSpan.End(rpctrace.ResultFor(err))
	if err != nil {
		return channelAppendErrorResults(len(items), err)
	}
	waitSpan := rpctrace.Begin(ctx, rpctrace.StageChannelFutureWait)
	results, err := future.Wait(ctx)
	waitSpan.End(rpctrace.ResultFor(err))
	if err != nil {
		return channelAppendErrorResults(len(items), err)
	}
	if len(results) != len(items) {
		return channelAppendErrorResults(len(items), channelappend.ErrAppendResultMissing)
	}
	return results
}

// channelAppendSubscriberSource pages current Slot-leader subscribers. Versioned
// snapshot reuse belongs to channelappend, never the benchmark setup cache.
type channelAppendSubscriberSource struct {
	node recipientSubscriberNode
}

func (s channelAppendSubscriberSource) NextSubscriberPage(ctx context.Context, req channelappend.SubscriberPageRequest) (channelappend.SubscriberPage, error) {
	if s.node == nil {
		return channelappend.SubscriberPage{Done: true}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1
	}
	uids, cursor, done, err := s.node.ListChannelSubscribersAuthoritative(ctx, req.ChannelID.ID, int64(req.ChannelID.Type), req.Cursor, limit)
	if err != nil {
		return channelappend.SubscriberPage{}, err
	}
	recipients := make([]channelappend.Recipient, 0, len(uids))
	for _, uid := range uids {
		if uid != "" {
			recipients = append(recipients, channelappend.Recipient{UID: uid})
		}
	}
	return channelappend.SubscriberPage{Recipients: recipients, Cursor: cursor, Done: done}, nil
}

func channelAppendErrorResults(n int, err error) []channelappend.SendBatchItemResult {
	results := make([]channelappend.SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}
