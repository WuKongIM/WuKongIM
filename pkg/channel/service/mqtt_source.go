package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

var _ ch.MQTTSourceActivator = (*cluster)(nil)

// EnsureMQTTSource uses the normal Channel sequencer for first activation and
// confirms durable protection under current reactor fences before returning.
// Concurrent first requests may append duplicate controls; their first identity
// wins. Once active, subscription admissions require no further control append.
func (c *cluster) EnsureMQTTSource(ctx context.Context, req ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !req.Valid() {
		return ch.MQTTSourceSnapshot{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	key := ch.ChannelKeyForID(req.ChannelID)
	release, err := c.group.ReserveAppend(key)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	defer release()
	source, found, err := c.confirmMQTTSource(ctx, req)
	if err != nil || found {
		return source, err
	}
	result, err := c.appendBatch(ctx, ch.AppendBatchRequest{
		ChannelID: req.ChannelID, ExpectedChannelEpoch: req.ExpectedChannelEpoch, ExpectedLeaderEpoch: req.ExpectedLeaderEpoch,
		CommitMode: ch.CommitModeQuorum, OmitResultPayload: true, ServerAllocatedMessageIDs: true,
		Messages: []ch.Message{{MessageID: req.MessageID, ServerTimestampMS: req.ServerTimestampMS,
			SyncOnce: true, Payload: []byte(quorumlog.MQTTSourceActivationPayload)}},
	}, &req)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if len(result.Items) != 1 {
		return ch.MQTTSourceSnapshot{}, ch.ErrLogConflict
	}
	if err := result.Items[0].Err; err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if result.Items[0].MessageSeq == 0 {
		return ch.MQTTSourceSnapshot{}, ch.ErrLogConflict
	}
	source, found, err = c.confirmMQTTSource(ctx, req)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if !found || source.CommittedThrough < result.Items[0].MessageSeq {
		return ch.MQTTSourceSnapshot{}, ch.ErrLogConflict
	}
	return source, nil
}

func (c *cluster) confirmMQTTSource(ctx context.Context, req ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return ch.MQTTSourceSnapshot{}, false, err
	}
	key := ch.ChannelKeyForID(req.ChannelID)
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventMQTTSource, Key: key, Context: ctx, MQTTSource: req})
	if err != nil {
		return ch.MQTTSourceSnapshot{}, false, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, false, err
	}
	// Unlike ordinary appends, an already completed query must not escape a
	// synchronously canceled admission attempt. No durability is rolled back.
	if err := ctx.Err(); err != nil {
		return ch.MQTTSourceSnapshot{}, false, err
	}
	return result.MQTTSource, result.MQTTSourceFound, nil
}
