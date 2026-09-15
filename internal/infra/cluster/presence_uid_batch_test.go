package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"
)

func TestPresenceUIDBatchUsesAuthorityGroups(t *testing.T) {
	local, remote := &fakePresenceAuthority{}, &fakePresenceAuthority{}
	n := &fakePresenceCluster{nodeID: 1, routesByUID: map[string]cluster.Route{}, rpcByNode: map[uint64]cluster.NodeRPCHandler{2: presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remote})}}}
	uids := make([]string, 128)
	for i := range uids {
		uids[i] = fmt.Sprintf("batch-%03d", i)
		route := cluster.Route{HashSlot: uint16(i), SlotID: 1, Leader: 2, LeaderTerm: 7, ConfigEpoch: 9, Revision: 11, AuthorityEpoch: 3}
		n.routesByUID[uids[i]] = route
		n.routeKeyResults = append(n.routeKeyResults, cluster.RouteKeyResult{Route: route})
	}
	got, e := NewPresenceAuthorityClient(n, local).EndpointsByUIDs(context.Background(), uids)
	require.NoError(t, e)
	require.Len(t, got, len(uids))
	require.Equal(t, 1, len(n.calls), "128 UIDs on one remote leader must use one batched RPC")
	require.Len(t, n.routeKeysCalls, 1)
	require.Zero(t, n.routeKeyCalls)
	for _, uid := range uids {
		require.Len(t, got[uid], 1)
		require.Equal(t, uid, got[uid][0].UID)
	}
}

// Route each bounded page from the observed snapshot, including pages with a
// different cardinality, rather than returning a fixed test result slice.
type pagedPresenceCluster struct{ *fakePresenceCluster }

func (n pagedPresenceCluster) RouteKeysPartial(uids []string) ([]cluster.RouteKeyResult, error) {
	n.routeKeysCalls = append(n.routeKeysCalls, append([]string(nil), uids...))
	result := make([]cluster.RouteKeyResult, len(uids))
	for i, uid := range uids {
		result[i].Route = n.routesByUID[uid]
	}
	return result, nil
}

func TestPresenceUIDBatchBoundsPagesAndRetainsCrossPageDuplicates(t *testing.T) {
	remote := &fakePresenceAuthority{}
	n := &fakePresenceCluster{nodeID: 1, routesByUID: map[string]cluster.Route{}, rpcByNode: map[uint64]cluster.NodeRPCHandler{2: presenceRPCHandler{adapter: accessnode.New(accessnode.Options{Authority: remote})}}}
	uids := make([]string, 513)
	for i := 0; i < 512; i++ {
		uids[i] = fmt.Sprintf("paged-%03d", i)
		n.routesByUID[uids[i]] = cluster.Route{HashSlot: uint16(i % 256), SlotID: 1, Leader: 2, LeaderTerm: 7, ConfigEpoch: 9, Revision: 11, AuthorityEpoch: 3}
	}
	uids[512] = uids[0]
	got, err := NewPresenceAuthorityClient(pagedPresenceCluster{n}, &fakePresenceAuthority{}).EndpointsByUIDs(context.Background(), uids)
	require.NoError(t, err)
	require.Len(t, got, 512)
	require.Len(t, got[uids[0]], 2)
	require.Len(t, n.calls, 3)
	require.Len(t, n.routeKeysCalls, 3)
	for _, page := range n.routeKeysCalls {
		require.LessOrEqual(t, len(page), 256)
	}
}

func TestPresenceUIDBatchRejectsIncompleteAuthorityEvidence(t *testing.T) {
	route := cluster.Route{HashSlot: 1, SlotID: 1, Leader: 1, LeaderTerm: 7, ConfigEpoch: 9, Revision: 11, AuthorityEpoch: 3}
	failure := errors.New("authority unavailable")
	for _, tc := range []struct {
		name      string
		result    presence.EndpointLookupResult
		wantError bool
	}{
		{name: "offline", result: presence.EndpointLookupResult{}},
		{name: "failed", result: presence.EndpointLookupResult{Err: failure}, wantError: true},
		{name: "wrong identity", result: presence.EndpointLookupResult{Routes: []presence.Route{{UID: "unrequested"}}}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := &fakePresenceCluster{nodeID: 1, routeKeyResults: []cluster.RouteKeyResult{{Route: route}}}
			local := &fakeTargetBatchPresenceAuthority{fakePresenceAuthority: &fakePresenceAuthority{}, results: []presence.EndpointLookupResult{tc.result}}
			got, err := NewPresenceAuthorityClient(n, local).EndpointsByUIDs(context.Background(), []string{"wanted"})
			if tc.wantError {
				require.Error(t, err)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Contains(t, got, "wanted")
				require.Empty(t, got["wanted"])
			}
		})
	}
}
