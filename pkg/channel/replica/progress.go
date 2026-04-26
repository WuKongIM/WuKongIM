package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) ApplyFollowerCursor(ctx context.Context, req channel.ReplicaFollowerCursorUpdate) error {
	return r.submitLoopCommand(ctx, machineCursorCommand{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		ReplicaID:   req.ReplicaID,
		MatchOffset: req.MatchOffset,
		OffsetEpoch: req.OffsetEpoch,
	}).Err
}

func (r *replica) ApplyProgressAck(ctx context.Context, req channel.ReplicaProgressAckRequest) error {
	return r.ApplyFollowerCursor(ctx, channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		ReplicaID:   req.ReplicaID,
		MatchOffset: req.MatchOffset,
	})
}
