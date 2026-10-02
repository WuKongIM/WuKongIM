package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
)

// CommitMQTTReplayAnchor retains ordinary append reservation, ordering and
// cancellation ownership while returning only the verified durable control proof.
func (c *cluster) CommitMQTTReplayAnchor(ctx context.Context, q ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	anchor, err := q.Anchor()
	if err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	// Validate bounded slices before allocating retained copies.
	q = q.Clone()
	body, err := anchor.MarshalBinary()
	if err != nil {
		return ch.MQTTReplayAnchorProof{}, ch.ErrInvalidConfig
	}
	key := ch.ChannelKeyForID(q.Meta.ID)
	release, err := c.group.ReserveAppend(key)
	if err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	defer release()
	op := c.group.NextOpID()
	request := ch.AppendBatchRequest{ChannelID: q.Meta.ID, ExpectedChannelEpoch: q.Meta.Epoch, ExpectedLeaderEpoch: q.Meta.LeaderEpoch, CommitMode: ch.CommitModeQuorum, OmitResultPayload: true, ServerAllocatedMessageIDs: true,
		Messages: []ch.Message{{MessageID: q.MessageID, ServerTimestampMS: q.ServerTimestampMS, SyncOnce: true, Payload: body}}}
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventAppend, Key: key, Context: ctx, OpID: op, Append: request, MQTTAnchor: &q})
	if err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	result, completed := awaitAppendFuture(ctx, future)
	if completed {
		if result.Err != nil {
			return ch.MQTTReplayAnchorProof{}, result.Err
		}
		if err := ctx.Err(); err != nil {
			return ch.MQTTReplayAnchorProof{}, err
		}
		return result.MQTTAnchor, nil
	}
	c.cancelAppendObservation(key, op, future, ctx.Err())
	return ch.MQTTReplayAnchorProof{}, ctx.Err()
}

var _ ch.MQTTReplayAnchorCommitter = (*cluster)(nil)
