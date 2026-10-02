package channel

import (
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTReplayRequestRequiresBoundedExactAuthority(t *testing.T) {
	req := MQTTReplayRequest{ChannelID: ChannelID{ID: "room", Type: 2}, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1,
		Range: MQTTReplayRange{Generation: quorumlog.MQTTSourceGeneration(CommandID{1}), From: 1, Through: math.MaxUint64, Limit: 256, MaxBytes: 16 << 20}}
	require.True(t, req.Valid())
	for _, change := range []func(*MQTTReplayRequest){
		func(r *MQTTReplayRequest) { r.ChannelID.ID = "" }, func(r *MQTTReplayRequest) { r.ChannelID.Type = 0 },
		func(r *MQTTReplayRequest) { r.ExpectedChannelEpoch = 0 }, func(r *MQTTReplayRequest) { r.ExpectedLeaderEpoch = 0 }, func(r *MQTTReplayRequest) { r.ExpectedRouteGeneration = 0 },
		func(r *MQTTReplayRequest) { r.Range.From = 0 }, func(r *MQTTReplayRequest) { r.Range.Through = 0 }, func(r *MQTTReplayRequest) { r.Range.Limit = 0 },
		func(r *MQTTReplayRequest) { r.Range.Limit = 257 }, func(r *MQTTReplayRequest) { r.Range.MaxBytes = 0 }, func(r *MQTTReplayRequest) { r.Range.MaxBytes++ },
		func(r *MQTTReplayRequest) { r.Range.Generation = "legacy" }, func(r *MQTTReplayRequest) { r.Range.Generation = quorumlog.MQTTSourceGeneration(CommandID{}) },
	} {
		bad := req
		change(&bad)
		require.False(t, bad.Valid())
	}
}
