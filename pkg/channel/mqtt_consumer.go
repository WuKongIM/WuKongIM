package channel

import "context"

// MQTTReplayConsumerRequest selects a bounded immutable page covered by an exact
// committed anchor. It confers no Session permission or consumer progress.
type MQTTReplayConsumerRequest struct {
	Request        MQTTReplayRequest
	AnchorPosition uint64
}

func (q MQTTReplayConsumerRequest) Valid() bool {
	r := q.Request
	return r.Valid() && q.AnchorPosition > r.Range.Through && (MQTTReplayPlanRequest{ChannelID: r.ChannelID, ExpectedChannelEpoch: r.ExpectedChannelEpoch, ExpectedLeaderEpoch: r.ExpectedLeaderEpoch, ExpectedRouteGeneration: r.ExpectedRouteGeneration, Generation: r.Range.Generation}).Valid()
}

// MQTTReplayConsumerReader reads through current cluster authority, never using
// ordinary history or a replica-local success as a routing substitute.
type MQTTReplayConsumerReader interface {
	ReadMQTTReplay(context.Context, MQTTReplayConsumerRequest) (MQTTReplayPage, error)
}
