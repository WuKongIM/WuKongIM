package channelmeta

import (
	"encoding/json"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestLeaderRepairDTOJSONRoundTrip(t *testing.T) {
	want := LeaderRepairRequest{
		ChannelID:            channel.ChannelID{ID: "dto-repair", Type: 2},
		ObservedChannelEpoch: 11,
		ObservedLeaderEpoch:  7,
		Reason:               "leader_dead",
	}

	body, err := json.Marshal(want)
	require.NoError(t, err)

	var got LeaderRepairRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, want, got)
}

func TestLeaderRepairResultDTOJSONRoundTrip(t *testing.T) {
	want := LeaderRepairResult{
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID:    "dto-repair-result",
			ChannelType:  2,
			ChannelEpoch: 13,
			LeaderEpoch:  5,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{1, 3},
			Leader:       3,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
			Features:     4,
			LeaseUntilMS: 123456789,
		},
		Changed: true,
	}

	body, err := json.Marshal(want)
	require.NoError(t, err)

	var got LeaderRepairResult
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, want, got)
}

func TestLeaderEvaluateDTOJSONRoundTrip(t *testing.T) {
	wantReq := LeaderEvaluateRequest{
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID:    "dto-evaluate",
			ChannelType:  3,
			ChannelEpoch: 21,
			LeaderEpoch:  8,
			Replicas:     []uint64{4, 5, 6},
			ISR:          []uint64{4, 6},
			Leader:       4,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
			Features:     9,
			LeaseUntilMS: 987654321,
		},
	}
	wantReport := LeaderPromotionReport{
		NodeID:              6,
		Exists:              true,
		ChannelEpoch:        21,
		LocalLEO:            44,
		LocalCheckpointHW:   33,
		LocalOffsetEpoch:    7,
		CommitReadyNow:      true,
		ProjectedSafeHW:     40,
		ProjectedTruncateTo: 39,
		CanLead:             true,
		Reason:              "safe",
	}

	reqBody, err := json.Marshal(wantReq)
	require.NoError(t, err)
	reportBody, err := json.Marshal(wantReport)
	require.NoError(t, err)

	var gotReq LeaderEvaluateRequest
	require.NoError(t, json.Unmarshal(reqBody, &gotReq))
	require.Equal(t, wantReq, gotReq)

	var gotReport LeaderPromotionReport
	require.NoError(t, json.Unmarshal(reportBody, &gotReport))
	require.Equal(t, wantReport, gotReport)
}
