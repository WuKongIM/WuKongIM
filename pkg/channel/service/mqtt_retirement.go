package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
)

// CommitMQTTReplayRetirement retains ordinary append reservation, ordering and
// cancellation ownership while returning only the verified durable control proof.
func (c *cluster) CommitMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	retirement, err := q.Retirement()
	if err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	// Validate bounded slices before allocating retained copies.
	q = q.Clone()
	body, err := retirement.MarshalBinary()
	if err != nil {
		return ch.MQTTReplayRetirementProof{}, ch.ErrInvalidConfig
	}
	key := ch.ChannelKeyForID(q.Meta.ID)
	release, err := c.group.ReserveAppend(key)
	if err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	defer release()
	op := c.group.NextOpID()
	request := ch.AppendBatchRequest{ChannelID: q.Meta.ID, ExpectedChannelEpoch: q.Meta.Epoch, ExpectedLeaderEpoch: q.Meta.LeaderEpoch, CommitMode: ch.CommitModeQuorum, OmitResultPayload: true, ServerAllocatedMessageIDs: true,
		Messages: []ch.Message{{MessageID: q.MessageID, ServerTimestampMS: q.ServerTimestampMS, SyncOnce: true, Payload: body}}}
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventAppend, Key: key, Context: ctx, OpID: op, Append: request, MQTTRetirement: &q})
	if err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	result, completed := awaitAppendFuture(ctx, future)
	if completed {
		if result.Err != nil {
			return ch.MQTTReplayRetirementProof{}, result.Err
		}
		if err := ctx.Err(); err != nil {
			return ch.MQTTReplayRetirementProof{}, err
		}
		return result.MQTTRetirement, nil
	}
	c.cancelAppendObservation(key, op, future, ctx.Err())
	return ch.MQTTReplayRetirementProof{}, ctx.Err()
}

var _ ch.MQTTReplayRetirementCommitter = (*cluster)(nil)
