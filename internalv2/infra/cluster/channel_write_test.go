package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestChannelWriteClientMapsResolvedMetaToAuthorityTarget(t *testing.T) {
	channelID := channelwrite.ChannelID{ID: "room", Type: 2}
	node := &channelWriteNodeForTest{
		nodeID: 1,
		meta: channelv2.Meta{
			ID:          channelv2.ChannelID{ID: channelID.ID, Type: channelID.Type},
			Key:         "2:room",
			Leader:      3,
			Epoch:       11,
			LeaderEpoch: 7,
		},
	}
	client := NewChannelWriteClient(node, nil)

	target, err := client.ResolveAppendAuthority(context.Background(), channelID)
	if err != nil {
		t.Fatalf("ResolveAppendAuthority() error = %v", err)
	}
	if node.calls != 1 || node.lastID.ID != "room" || node.lastID.Type != 2 {
		t.Fatalf("node calls/id = %d/%#v, want one canonical resolve", node.calls, node.lastID)
	}
	if target.ChannelID != channelID || target.ChannelKey != "2:room" || target.LeaderNodeID != 3 || target.Epoch != 11 || target.LeaderEpoch != 7 {
		t.Fatalf("target = %#v, want mapped authority fields", target)
	}
}

func TestChannelWriteClientMapsRouteErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		want      error
		unchanged bool
	}{
		{name: "channel not leader", err: channelv2.ErrNotLeader, want: channelwrite.ErrNotChannelAuthority},
		{name: "stale meta", err: channelv2.ErrStaleMeta, want: channelwrite.ErrStaleRoute},
		{name: "channel not ready", err: channelv2.ErrNotReady, want: channelwrite.ErrRouteNotReady},
		{name: "cluster route not ready", err: clusterv2.ErrRouteNotReady, want: channelwrite.ErrRouteNotReady},
		{name: "cluster no slot leader", err: clusterv2.ErrNoSlotLeader, want: channelwrite.ErrRouteNotReady},
		{name: "cluster not started", err: clusterv2.ErrNotStarted, want: channelwrite.ErrRouteNotReady},
		{name: "cluster stopping", err: clusterv2.ErrStopping, want: channelwrite.ErrRouteNotReady},
		{name: "context canceled", err: context.Canceled, want: context.Canceled, unchanged: true},
		{name: "context deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded, unchanged: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := NewChannelWriteClient(&channelWriteNodeForTest{err: tc.err}, nil)
			_, err := client.ResolveAppendAuthority(context.Background(), channelwrite.ChannelID{ID: "room", Type: 2})
			if !errors.Is(err, tc.want) {
				t.Fatalf("ResolveAppendAuthority() error = %v, want %v", err, tc.want)
			}
			if tc.unchanged && err != tc.err {
				t.Fatalf("ResolveAppendAuthority() error = %v, want unchanged %v", err, tc.err)
			}
		})
	}
}

func TestChannelWriteClientForwardsRemoteResultsWithoutInterpretation(t *testing.T) {
	target := channelwrite.AuthorityTarget{ChannelID: channelwrite.ChannelID{ID: "remote", Type: 2}, ChannelKey: "2:remote", LeaderNodeID: 8}
	remote := &channelWriteRemoteForTest{results: []channelwrite.SendBatchItemResult{
		{Result: channelwrite.SendResult{Reason: channelwrite.ReasonUnsupported}},
		{Err: channelwrite.ErrStaleRoute},
	}}
	client := NewChannelWriteClient(&channelWriteNodeForTest{nodeID: 1}, remote)
	items := []channelwrite.SendBatchItem{
		{Context: context.Background(), Command: channelwrite.SendCommand{FromUID: "u1", ChannelID: "remote", ChannelType: 2}},
		{Context: context.Background(), Command: channelwrite.SendCommand{FromUID: "u2", ChannelID: "remote", ChannelType: 2}},
	}

	results := client.ForwardSendBatch(context.Background(), target, items)
	if remote.calls != 1 || remote.target != target || len(remote.items) != 2 {
		t.Fatalf("remote calls/target/items = %d/%#v/%d, want forwarded batch", remote.calls, remote.target, len(remote.items))
	}
	if len(results) != 2 || results[0].Result.Reason != channelwrite.ReasonUnsupported || !errors.Is(results[1].Err, channelwrite.ErrStaleRoute) {
		t.Fatalf("results = %#v, want remote item-aligned results unchanged", results)
	}
}

type channelWriteNodeForTest struct {
	nodeID uint64
	meta   channelv2.Meta
	err    error
	lastID channelv2.ChannelID
	calls  int
}

func (n *channelWriteNodeForTest) NodeID() uint64 {
	return n.nodeID
}

func (n *channelWriteNodeForTest) ResolveChannelAppendAuthority(_ context.Context, id channelv2.ChannelID) (channelv2.Meta, error) {
	n.calls++
	n.lastID = id
	if n.err != nil {
		return channelv2.Meta{}, n.err
	}
	return n.meta, nil
}

type channelWriteRemoteForTest struct {
	results []channelwrite.SendBatchItemResult
	target  channelwrite.AuthorityTarget
	items   []channelwrite.SendBatchItem
	calls   int
}

func (r *channelWriteRemoteForTest) ForwardSendBatch(_ context.Context, target channelwrite.AuthorityTarget, items []channelwrite.SendBatchItem) []channelwrite.SendBatchItemResult {
	r.calls++
	r.target = target
	r.items = append(r.items, items...)
	return r.results
}
