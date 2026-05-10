package node

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemUIDCacheRPCAppliesAddAndRemoveOnRemoteNode(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	node1 := network.cluster(1)
	node2 := network.cluster(2)
	cache := &recordingSystemUIDCache{}
	New(Options{Cluster: node2, SystemUIDCache: cache})

	client := NewClient(node1)
	require.NoError(t, client.AddSystemUIDsToCache(context.Background(), 2, []string{"sys1", "sys2"}))
	require.NoError(t, client.RemoveSystemUIDsFromCache(context.Background(), 2, []string{"sys1"}))

	require.Equal(t, [][]string{{"sys1", "sys2"}}, cache.adds)
	require.Equal(t, [][]string{{"sys1"}}, cache.removes)
}

type recordingSystemUIDCache struct {
	adds    [][]string
	removes [][]string
}

func (r *recordingSystemUIDCache) AddSystemUIDsToCache(uids []string) error {
	r.adds = append(r.adds, append([]string(nil), uids...))
	return nil
}

func (r *recordingSystemUIDCache) RemoveSystemUIDsFromCache(uids []string) error {
	r.removes = append(r.removes, append([]string(nil), uids...))
	return nil
}
