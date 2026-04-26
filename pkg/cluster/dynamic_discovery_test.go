package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestDynamicDiscoveryResolveUsesSeedsThenNodes(t *testing.T) {
	d := NewDynamicDiscovery([]SeedConfig{{ID: 9001, Addr: "127.0.0.1:7001"}}, nil)

	addr, err := d.Resolve(9001)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:7001", addr)

	d.UpdateNodes([]NodeConfig{{NodeID: 1, Addr: "wk-node1:7000"}})
	addr, err = d.Resolve(1)
	require.NoError(t, err)
	require.Equal(t, "wk-node1:7000", addr)
}

func TestDynamicDiscoveryUpdateRemovesStaleNodes(t *testing.T) {
	d := NewDynamicDiscovery(nil, []NodeConfig{{NodeID: 1, Addr: "a"}, {NodeID: 2, Addr: "b"}})
	d.UpdateNodes([]NodeConfig{{NodeID: 2, Addr: "b2"}})

	_, err := d.Resolve(1)
	require.ErrorIs(t, err, transport.ErrNodeNotFound)
	addr, err := d.Resolve(2)
	require.NoError(t, err)
	require.Equal(t, "b2", addr)
}

func TestDynamicDiscoveryGetNodesMergesSeedsAndNodesSorted(t *testing.T) {
	d := NewDynamicDiscovery(
		[]SeedConfig{{ID: 3, Addr: "seed3"}, {ID: 1, Addr: "seed1"}},
		[]NodeConfig{{NodeID: 2, Addr: "node2"}, {NodeID: 3, Addr: "node3"}},
	)
	d.UpsertSeed(SeedConfig{ID: 4, Addr: "seed4"})

	require.Equal(t, []NodeInfo{
		{NodeID: 1, Addr: "seed1"},
		{NodeID: 2, Addr: "node2"},
		{NodeID: 3, Addr: "node3"},
		{NodeID: 4, Addr: "seed4"},
	}, d.GetNodes())
}

func TestDynamicDiscoveryInvokesSubscribersForChangedAndRemovedNodes(t *testing.T) {
	d := NewDynamicDiscovery(nil, []NodeConfig{{NodeID: 1, Addr: "a"}, {NodeID: 2, Addr: "b"}})
	defer d.Stop()

	var events []struct {
		nodeID  uint64
		oldAddr string
		newAddr string
	}
	cancel := d.OnAddressChange(func(nodeID uint64, oldAddr, newAddr string) {
		events = append(events, struct {
			nodeID  uint64
			oldAddr string
			newAddr string
		}{nodeID: nodeID, oldAddr: oldAddr, newAddr: newAddr})
	})
	defer cancel()

	changed := d.UpdateNodes([]NodeConfig{{NodeID: 1, Addr: "a2"}})

	require.ElementsMatch(t, []uint64{1, 2}, changed)
	require.ElementsMatch(t, []struct {
		nodeID  uint64
		oldAddr string
		newAddr string
	}{
		{nodeID: 1, oldAddr: "a", newAddr: "a2"},
		{nodeID: 2, oldAddr: "b", newAddr: ""},
	}, events)
}
