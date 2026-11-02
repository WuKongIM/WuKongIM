package channelappend

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

// Failure inventory: retry may replace the captured route, or batching may
// omit its fence. Every authority must match before a prepared item can append.
func TestPersonDirectoryAppendFenceSurvivesRouteRetry(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "a@b", Type: 1}, Epoch: 2, LeaderEpoch: 3, RouteGeneration: 4}
	cmd := SendCommand{ChannelID: "a@b", ChannelType: 1, PersonDirectoryFence: metadb.PersonDirectoryAppendFence{ChannelEpoch: 2, LeaderEpoch: 3, RouteGeneration: 4, DirectoryGeneration: 1}}
	require.True(t, preparedCommandMatchesTarget(target, cmd))
	req := appendRequest(target, []preparedSend{{Command: cmd}}, 1)
	require.Equal(t, uint64(4), req.ExpectedRouteGeneration)
	for _, mutate := range []func(*AuthorityTarget){func(t *AuthorityTarget) { t.Epoch++ }, func(t *AuthorityTarget) { t.LeaderEpoch++ }, func(t *AuthorityTarget) { t.RouteGeneration++ }} {
		next := target
		mutate(&next)
		require.False(t, preparedCommandMatchesTarget(next, cmd))
	}
}

// A queued new generation must never lend its route fence to older items.
func TestPersonDirectoryAppendMixedQueueRejectsOldGeneration(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "a@b", Type: 1}, Epoch: 2, LeaderEpoch: 3, RouteGeneration: 5}
	items := make([]preparedSend, 2)
	for i := range items {
		items[i] = preparedSend{Index: i, Command: SendCommand{ChannelID: "a@b", ChannelType: 1, MessageID: uint64(i + 1), Payload: []byte("body"), PersonDirectoryFence: metadb.PersonDirectoryAppendFence{ChannelEpoch: 2, LeaderEpoch: 3, RouteGeneration: uint64(4 + i), DirectoryGeneration: uint64(1 + i)}}}
	}
	appender := newRecordingAppenderForAppendTest()
	got := (appendEffect{target: target, items: items}).run(context.Background(), appendPorts{appender: appender})
	require.Len(t, got.items, 2)
	requests := appender.Requests()
	require.Len(t, requests, 1)
	require.Len(t, requests[0].Messages, 1)
	require.Equal(t, uint64(2), requests[0].Messages[0].MessageID)
	require.Equal(t, uint64(5), requests[0].ExpectedRouteGeneration)
	for _, item := range got.items {
		if item.item.Index == 0 {
			require.ErrorIs(t, item.result.Err, ErrStaleRoute)
		}
	}
}

// An older cached route is not a newer incarnation. Refresh it once while
// preserving the Ready proof; only a route newer than that proof is terminal.
func TestPersonDirectoryAppendRefreshesLaggingRoute(t *testing.T) {
	for _, count := range []int{1, 2} {
		target := routerTarget("a@b", 1, 7)
		target.RouteGeneration = 5
		old := target
		old.RouteGeneration = 4
		resolver := &routerResolverForTest{targets: []AuthorityTarget{old, target}}
		local := &routerLocalSubmitterForTest{results: make([]SendBatchItemResult, count)}
		router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local, MaxRouteAttempts: 3})
		items := make([]SendBatchItem, count)
		for i := range items {
			items[i] = routerItem("a", "a@b", 1)
			items[i].Command.PersonDirectoryFence = metadb.PersonDirectoryAppendFence{ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 5, DirectoryGeneration: 2}
		}
		results := router.SendBatch(items)
		for _, result := range results {
			require.NoError(t, result.Err)
		}
		require.Equal(t, 2, resolver.calls)
		require.Equal(t, 1, local.calls)
		require.Equal(t, uint64(5), local.items[0].Command.PersonDirectoryFence.RouteGeneration)
	}
}
