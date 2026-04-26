package node

import (
	"testing"

	channelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestLeaderRepairConvertPreservesAllFields(t *testing.T) {
	accessReq := ChannelLeaderRepairRequest{
		ChannelID:            channel.ChannelID{ID: "repair-convert", Type: 4},
		ObservedChannelEpoch: 12,
		ObservedLeaderEpoch:  9,
		Reason:               "leader_dead",
	}
	runtimeReq := channelmeta.LeaderRepairRequest{
		ChannelID:            accessReq.ChannelID,
		ObservedChannelEpoch: accessReq.ObservedChannelEpoch,
		ObservedLeaderEpoch:  accessReq.ObservedLeaderEpoch,
		Reason:               accessReq.Reason,
	}
	require.Equal(t, runtimeReq, toChannelmetaLeaderRepairRequest(accessReq))
	require.Equal(t, accessReq, fromChannelmetaLeaderRepairRequest(runtimeReq))

	accessResult := ChannelLeaderRepairResult{
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID:    "repair-convert-result",
			ChannelType:  4,
			ChannelEpoch: 22,
			LeaderEpoch:  10,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{2, 3},
			Leader:       3,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
			Features:     5,
			LeaseUntilMS: 4444,
		},
		Changed: true,
	}
	runtimeResult := channelmeta.LeaderRepairResult{
		Meta:    accessResult.Meta,
		Changed: accessResult.Changed,
	}
	require.Equal(t, runtimeResult, toChannelmetaLeaderRepairResult(accessResult))
	require.Equal(t, accessResult, fromChannelmetaLeaderRepairResult(runtimeResult))
}

func TestLeaderEvaluateConvertPreservesAllFields(t *testing.T) {
	accessReq := ChannelLeaderEvaluateRequest{
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID:    "evaluate-convert",
			ChannelType:  5,
			ChannelEpoch: 31,
			LeaderEpoch:  14,
			Replicas:     []uint64{7, 8, 9},
			ISR:          []uint64{7, 9},
			Leader:       7,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
			Features:     6,
			LeaseUntilMS: 5555,
		},
	}
	runtimeReq := channelmeta.LeaderEvaluateRequest{Meta: accessReq.Meta}
	require.Equal(t, runtimeReq, toChannelmetaLeaderEvaluateRequest(accessReq))
	require.Equal(t, accessReq, fromChannelmetaLeaderEvaluateRequest(runtimeReq))

	accessReport := ChannelLeaderPromotionReport{
		NodeID:              9,
		Exists:              true,
		ChannelEpoch:        31,
		LocalLEO:            90,
		LocalCheckpointHW:   80,
		LocalOffsetEpoch:    6,
		CommitReadyNow:      true,
		ProjectedSafeHW:     88,
		ProjectedTruncateTo: 87,
		CanLead:             true,
		Reason:              "safe",
	}
	runtimeReport := channelmeta.LeaderPromotionReport{
		NodeID:              accessReport.NodeID,
		Exists:              accessReport.Exists,
		ChannelEpoch:        accessReport.ChannelEpoch,
		LocalLEO:            accessReport.LocalLEO,
		LocalCheckpointHW:   accessReport.LocalCheckpointHW,
		LocalOffsetEpoch:    accessReport.LocalOffsetEpoch,
		CommitReadyNow:      accessReport.CommitReadyNow,
		ProjectedSafeHW:     accessReport.ProjectedSafeHW,
		ProjectedTruncateTo: accessReport.ProjectedTruncateTo,
		CanLead:             accessReport.CanLead,
		Reason:              accessReport.Reason,
	}
	require.Equal(t, runtimeReport, toChannelmetaLeaderPromotionReport(accessReport))
	require.Equal(t, accessReport, fromChannelmetaLeaderPromotionReport(runtimeReport))
}
