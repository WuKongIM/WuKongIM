package meta

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRuntimeMetadataDecodeOwnsFieldsAfterInputReuse(t *testing.T) {
	key := []byte("runtime-owned")
	want := ChannelRuntimeMeta{ChannelEpoch: 3, LeaderEpoch: 4, Leader: 1, MinISR: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, WriteFenceToken: "owned-token"}
	value := encodeChannelRuntimeMetaValue(key, want)
	got, err := decodeChannelRuntimeMetaValue(key, value)
	require.NoError(t, err)
	clear(value)
	require.Equal(t, want.WriteFenceToken, got.WriteFenceToken)
	require.Equal(t, want.Replicas, got.Replicas)
	require.Equal(t, want.ISR, got.ISR)
}
