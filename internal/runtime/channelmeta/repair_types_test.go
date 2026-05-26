package channelmeta

import (
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestLeaderRepairDTOFieldsAreTransportNeutral(t *testing.T) {
	requireTransportNeutralFields(t, LeaderRepairRequest{}, []string{
		"ChannelID",
		"ObservedChannelEpoch",
		"ObservedLeaderEpoch",
		"Reason",
	})
	requireTransportNeutralFields(t, LeaderRepairResult{}, []string{
		"Meta",
		"Changed",
	})
}

func TestLeaderEvaluateDTOFieldsAreTransportNeutral(t *testing.T) {
	requireTransportNeutralFields(t, LeaderEvaluateRequest{}, []string{
		"Meta",
	})
	requireTransportNeutralFields(t, LeaderPromotionReport{}, []string{
		"NodeID",
		"Exists",
		"ChannelEpoch",
		"LocalLEO",
		"LocalCheckpointHW",
		"LocalOffsetEpoch",
		"CommitReadyNow",
		"ProjectedSafeHW",
		"ProjectedTruncateTo",
		"CanLead",
		"Reason",
	})
}

func TestLeaderTransferDTOFieldsAreTransportNeutral(t *testing.T) {
	requireTransportNeutralFields(t, LeaderTransferRequest{}, []string{
		"ChannelID",
		"ObservedChannelEpoch",
		"ObservedLeaderEpoch",
		"TargetNodeID",
	})
	requireTransportNeutralFields(t, LeaderTransferResult{}, []string{
		"Meta",
		"Changed",
	})

	req := LeaderTransferRequest{
		ChannelID:            channel.ChannelID{ID: "room-1", Type: 2},
		ObservedChannelEpoch: 7,
		ObservedLeaderEpoch:  3,
		TargetNodeID:         2,
	}
	result := LeaderTransferResult{
		Meta:    metadb.ChannelRuntimeMeta{ChannelID: "room-1", ChannelType: 2, Leader: 2},
		Changed: true,
	}

	require.Equal(t, uint64(2), req.TargetNodeID)
	require.True(t, result.Changed)
	require.Equal(t, uint64(2), result.Meta.Leader)
}

func requireTransportNeutralFields(t *testing.T, value any, want []string) {
	t.Helper()

	typ := reflect.TypeOf(value)
	require.Equal(t, reflect.Struct, typ.Kind())
	got := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		got = append(got, field.Name)
		require.Emptyf(t, field.Tag.Get("json"), "%s.%s should not define transport JSON tags", typ.Name(), field.Name)
	}
	require.Equal(t, want, got)
}
