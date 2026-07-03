package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnsureChannelStoresGenerationByChannelKey(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-gen")

	require.NoError(t, rt.EnsureChannel(meta))
	store, ok := rt.generationStore.(*fakeGenerationStore)
	require.True(t, ok)
	require.Equal(t, uint64(1), store.stored[meta.Key])

	factory, ok := rt.replicaFactory.(*fakeReplicaFactory)
	require.True(t, ok)
	require.Equal(t, uint64(1), factory.created[0].Generation)
}
