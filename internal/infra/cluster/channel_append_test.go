package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestChannelAppendClientMapsResolvedMetaToAuthorityTarget(t *testing.T) {
	channelID := channelappend.ChannelID{ID: "room", Type: 2}
	node := &channelAppendNodeForTest{
		nodeID: 1,
		meta: channelruntime.Meta{
			ID:          channelruntime.ChannelID{ID: channelID.ID, Type: channelID.Type},
			Key:         "2:room",
			Leader:      3,
			Epoch:       11,
			LeaderEpoch: 7,
		},
		channel: metadb.Channel{
			ChannelID:                 channelID.ID,
			ChannelType:               int64(channelID.Type),
			Large:                     1,
			SubscriberMutationVersion: 19,
		},
	}
	client := NewChannelAppendClient(node, nil, nil)

	target, err := client.ResolveAppendAuthority(context.Background(), channelID)
	if err != nil {
		t.Fatalf("ResolveAppendAuthority() error = %v", err)
	}
	if node.calls != 1 || node.lastID.ID != "room" || node.lastID.Type != 2 {
		t.Fatalf("node calls/id = %d/%#v, want one canonical resolve", node.calls, node.lastID)
	}
	if target.ChannelID != channelID || target.ChannelKey != "2:room" || target.LeaderNodeID != 3 || target.Epoch != 11 || target.LeaderEpoch != 7 ||
		!target.Large || target.SubscriberMutationVersion != 19 {
		t.Fatalf("target = %#v, want mapped authority fields", target)
	}
}

func TestChannelAppendClientCachesRecipientMetadata(t *testing.T) {
	channelID := channelappend.ChannelID{ID: "room", Type: 2}
	node := &channelAppendNodeForTest{
		nodeID: 1,
		meta: channelruntime.Meta{
			ID:          channelruntime.ChannelID{ID: channelID.ID, Type: channelID.Type},
			Key:         "2:room",
			Leader:      3,
			Epoch:       11,
			LeaderEpoch: 7,
		},
		channel: metadb.Channel{
			ChannelID:                 channelID.ID,
			ChannelType:               int64(channelID.Type),
			Large:                     1,
			SubscriberMutationVersion: 19,
		},
	}
	cache := NewChannelAppendMetadataCache()
	client := NewChannelAppendClient(node, nil, cache)

	first, err := client.ResolveAppendAuthority(context.Background(), channelID)
	if err != nil {
		t.Fatalf("first ResolveAppendAuthority() error = %v", err)
	}
	node.channel = metadb.Channel{
		ChannelID:                 channelID.ID,
		ChannelType:               int64(channelID.Type),
		Large:                     0,
		SubscriberMutationVersion: 20,
	}
	second, err := client.ResolveAppendAuthority(context.Background(), channelID)
	if err != nil {
		t.Fatalf("second ResolveAppendAuthority() error = %v", err)
	}

	if node.metadataCalls != 1 {
		t.Fatalf("GetChannelMetadata calls = %d, want 1 cached lookup", node.metadataCalls)
	}
	if !first.Large || first.SubscriberMutationVersion != 19 || !second.Large || second.SubscriberMutationVersion != 19 {
		t.Fatalf("targets = %#v %#v, want cached recipient metadata", first, second)
	}
}

func TestChannelAppendClientAllowsMissingChannelMetadata(t *testing.T) {
	channelID := channelappend.ChannelID{ID: "room", Type: 2}
	client := NewChannelAppendClient(&channelAppendNodeForTest{
		meta: channelruntime.Meta{
			ID:          channelruntime.ChannelID{ID: channelID.ID, Type: channelID.Type},
			Key:         "2:room",
			Leader:      3,
			Epoch:       11,
			LeaderEpoch: 7,
		},
	}, nil, nil)

	target, err := client.ResolveAppendAuthority(context.Background(), channelID)
	if err != nil {
		t.Fatalf("ResolveAppendAuthority() error = %v, want missing metadata tolerated", err)
	}
	if target.Large || target.SubscriberMutationVersion != 0 {
		t.Fatalf("target metadata = large:%v version:%d, want zero metadata defaults", target.Large, target.SubscriberMutationVersion)
	}
}

func TestChannelAppendClientMapsRouteErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		want      error
		unchanged bool
	}{
		{name: "channel not leader", err: channelruntime.ErrNotLeader, want: channelappend.ErrNotChannelAuthority},
		{name: "slot propose not leader", err: propose.ErrNotLeader, want: channelappend.ErrNotChannelAuthority},
		{name: "stale meta", err: channelruntime.ErrStaleMeta, want: channelappend.ErrStaleRoute},
		{name: "channel not ready", err: channelruntime.ErrNotReady, want: channelappend.ErrRouteNotReady},
		{name: "cluster route not ready", err: cluster.ErrRouteNotReady, want: channelappend.ErrRouteNotReady},
		{name: "cluster no slot leader", err: cluster.ErrNoSlotLeader, want: channelappend.ErrRouteNotReady},
		{name: "cluster not started", err: cluster.ErrNotStarted, want: channelappend.ErrRouteNotReady},
		{name: "cluster stopping", err: cluster.ErrStopping, want: channelappend.ErrRouteNotReady},
		{name: "channel placement candidates unavailable", err: fmt.Errorf("%w: channel replica candidates 2 below replica count 3", channelruntime.ErrInvalidConfig), want: channelappend.ErrRouteNotReady},
		{name: "context canceled", err: context.Canceled, want: context.Canceled, unchanged: true},
		{name: "context deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded, unchanged: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := NewChannelAppendClient(&channelAppendNodeForTest{err: tc.err}, nil, nil)
			_, err := client.ResolveAppendAuthority(context.Background(), channelappend.ChannelID{ID: "room", Type: 2})
			if !errors.Is(err, tc.want) {
				t.Fatalf("ResolveAppendAuthority() error = %v, want %v", err, tc.want)
			}
			if tc.unchanged && err != tc.err {
				t.Fatalf("ResolveAppendAuthority() error = %v, want unchanged %v", err, tc.err)
			}
		})
	}
}

func TestChannelAppendClientForwardsRemoteResultsWithoutInterpretation(t *testing.T) {
	target := channelappend.AuthorityTarget{ChannelID: channelappend.ChannelID{ID: "remote", Type: 2}, ChannelKey: "2:remote", LeaderNodeID: 8}
	remote := &channelAppendRemoteForTest{results: []channelappend.SendBatchItemResult{
		{Result: channelappend.SendResult{Reason: channelappend.ReasonUnsupported}},
		{Err: channelappend.ErrStaleRoute},
	}}
	client := NewChannelAppendClient(&channelAppendNodeForTest{nodeID: 1}, remote, nil)
	items := []channelappend.SendBatchItem{
		{Context: context.Background(), Command: channelappend.SendCommand{FromUID: "u1", ChannelID: "remote", ChannelType: 2}},
		{Context: context.Background(), Command: channelappend.SendCommand{FromUID: "u2", ChannelID: "remote", ChannelType: 2}},
	}

	results := client.ForwardSendBatch(context.Background(), target, items)
	if remote.calls != 1 || remote.target != target || len(remote.items) != 2 {
		t.Fatalf("remote calls/target/items = %d/%#v/%d, want forwarded batch", remote.calls, remote.target, len(remote.items))
	}
	if len(results) != 2 || results[0].Result.Reason != channelappend.ReasonUnsupported || !errors.Is(results[1].Err, channelappend.ErrStaleRoute) {
		t.Fatalf("results = %#v, want remote item-aligned results unchanged", results)
	}
}

type channelAppendNodeForTest struct {
	nodeID        uint64
	meta          channelruntime.Meta
	channel       metadb.Channel
	err           error
	lastID        channelruntime.ChannelID
	calls         int
	metadataCalls int
}

func (n *channelAppendNodeForTest) NodeID() uint64 {
	return n.nodeID
}

func (n *channelAppendNodeForTest) ResolveChannelAppendAuthority(_ context.Context, id channelruntime.ChannelID) (channelruntime.Meta, error) {
	n.calls++
	n.lastID = id
	if n.err != nil {
		return channelruntime.Meta{}, n.err
	}
	return n.meta, nil
}

func (n *channelAppendNodeForTest) GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error) {
	n.metadataCalls++
	if n.err != nil {
		return metadb.Channel{}, n.err
	}
	if n.channel.ChannelID == "" {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return n.channel, nil
}

type channelAppendRemoteForTest struct {
	results []channelappend.SendBatchItemResult
	target  channelappend.AuthorityTarget
	items   []channelappend.SendBatchItem
	calls   int
}

func (r *channelAppendRemoteForTest) ForwardSendBatch(_ context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	r.calls++
	r.target = target
	r.items = append(r.items, items...)
	return r.results
}
