package channelmeta

import (
	"reflect"
	"testing"

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
