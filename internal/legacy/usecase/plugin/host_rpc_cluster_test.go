package plugin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestHostRPCClusterConfigMapsSnapshotDeterministically(t *testing.T) {
	reader := &recordingClusterReader{snapshot: ClusterSnapshot{
		Nodes: []ClusterNode{
			{ID: 2, ClusterAddr: "127.0.0.1:7002", APIServerAddr: "http://127.0.0.1:5002", Online: false},
			{ID: 1, ClusterAddr: "127.0.0.1:7001", APIServerAddr: "http://127.0.0.1:5001", Online: true},
		},
		Slots: []ClusterSlot{
			{ID: 9, Leader: 2, Term: 3, Replicas: []uint64{2, 1}},
			{ID: 7, Leader: 1, Term: 5, Replicas: []uint64{1, 2}},
		},
	}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), ClusterReader: reader})

	resp, err := app.ClusterConfig(context.Background(), "plugin.cluster")

	if err != nil {
		t.Fatalf("ClusterConfig returned error: %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("cluster reader calls = %d, want 1", reader.calls)
	}
	nodes := resp.GetNodes()
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	if nodes[0].GetId() != 1 || nodes[0].GetClusterAddr() != "127.0.0.1:7001" || nodes[0].GetApiServerAddr() != "http://127.0.0.1:5001" || !nodes[0].GetOnline() {
		t.Fatalf("first node = %#v", nodes[0])
	}
	if nodes[1].GetId() != 2 || nodes[1].GetClusterAddr() != "127.0.0.1:7002" || nodes[1].GetApiServerAddr() != "http://127.0.0.1:5002" || nodes[1].GetOnline() {
		t.Fatalf("second node = %#v", nodes[1])
	}
	slots := resp.GetSlots()
	if len(slots) != 2 {
		t.Fatalf("slots = %d, want 2", len(slots))
	}
	if slots[0].GetId() != 7 || slots[0].GetLeader() != 1 || slots[0].GetTerm() != 5 || !reflect.DeepEqual(slots[0].GetReplicas(), []uint64{1, 2}) {
		t.Fatalf("first slot = %#v", slots[0])
	}
	if slots[1].GetId() != 9 || slots[1].GetLeader() != 2 || slots[1].GetTerm() != 3 || !reflect.DeepEqual(slots[1].GetReplicas(), []uint64{2, 1}) {
		t.Fatalf("second slot = %#v", slots[1])
	}
}

func TestHostRPCClusterConfigRequiresReader(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore()})

	_, err := app.ClusterConfig(context.Background(), "plugin.cluster")

	assertErrorIs(t, err, ErrClusterReaderRequired)
}

func TestHostRPCClusterChannelsBelongNodeGroupsByOwnerStableOrder(t *testing.T) {
	owners := &recordingChannelOwnerReader{owners: map[channel.ChannelID]uint64{
		{ID: "g2", Type: frame.ChannelTypeGroup}:  2,
		{ID: "p1", Type: frame.ChannelTypePerson}: 1,
		{ID: "g1", Type: frame.ChannelTypeGroup}:  2,
	}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), ChannelOwners: owners, NodeID: 99})

	resp, err := app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{
		{ChannelId: "g2", ChannelType: uint32(frame.ChannelTypeGroup)},
		{ChannelId: "p1", ChannelType: uint32(frame.ChannelTypePerson)},
		{ChannelId: "g1", ChannelType: uint32(frame.ChannelTypeGroup)},
	}}, "plugin.cluster")

	if err != nil {
		t.Fatalf("ClusterChannelsBelongNode returned error: %v", err)
	}
	wantCalls := []channel.ChannelID{{ID: "g2", Type: frame.ChannelTypeGroup}, {ID: "p1", Type: frame.ChannelTypePerson}, {ID: "g1", Type: frame.ChannelTypeGroup}}
	if !reflect.DeepEqual(owners.calls, wantCalls) {
		t.Fatalf("owner lookups = %#v, want %#v", owners.calls, wantCalls)
	}
	groups := resp.GetClusterChannelBelongNodeResps()
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].GetNodeId() != 1 || len(groups[0].GetChannels()) != 1 || groups[0].GetChannels()[0].GetChannelId() != "p1" {
		t.Fatalf("first group = %#v", groups[0])
	}
	if groups[1].GetNodeId() != 2 {
		t.Fatalf("second group node = %d, want 2", groups[1].GetNodeId())
	}
	channels := groups[1].GetChannels()
	if len(channels) != 2 || channels[0].GetChannelId() != "g2" || channels[1].GetChannelId() != "g1" {
		t.Fatalf("second group channels = %#v, want request order", channels)
	}
}

func TestHostRPCClusterChannelsBelongNodeRequiresLookupAndRejectsUnknownOwner(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore()})
	_, err := app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}}}, "plugin.cluster")
	assertErrorIs(t, err, ErrChannelOwnerReaderRequired)

	app = mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), ChannelOwners: &recordingChannelOwnerReader{}})
	_, err = app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}}}, "plugin.cluster")
	assertErrorIs(t, err, ErrChannelOwnerUnknown)

	owners := &recordingChannelOwnerReader{err: errors.New("lookup failed")}
	app = mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), ChannelOwners: owners})
	_, err = app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}}}, "plugin.cluster")
	if err == nil || err.Error() != "lookup failed" {
		t.Fatalf("error = %v, want lookup failed", err)
	}
}

func TestHostRPCClusterChannelsBelongNodeRejectsEmptyChannels(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), ChannelOwners: &recordingChannelOwnerReader{}})

	_, err := app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{}, "plugin.cluster")
	assertErrorIs(t, err, ErrChannelRequired)

	_, err = app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{{ChannelType: 2}}}, "plugin.cluster")
	assertErrorIs(t, err, ErrChannelRequired)
}

type recordingClusterReader struct {
	calls    int
	snapshot ClusterSnapshot
	err      error
}

func (r *recordingClusterReader) ClusterSnapshot(_ context.Context) (ClusterSnapshot, error) {
	r.calls++
	if r.err != nil {
		return ClusterSnapshot{}, r.err
	}
	return r.snapshot, nil
}

type recordingChannelOwnerReader struct {
	calls  []channel.ChannelID
	owners map[channel.ChannelID]uint64
	err    error
}

func (r *recordingChannelOwnerReader) ChannelOwnerNode(_ context.Context, id channel.ChannelID) (uint64, error) {
	r.calls = append(r.calls, id)
	if r.err != nil {
		return 0, r.err
	}
	return r.owners[id], nil
}
