package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestClusterConfigMapsSnapshotDeterministically(t *testing.T) {
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
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, ClusterReader: reader})
	require.NoError(t, err)

	resp, err := app.ClusterConfig(context.Background(), "plugin.cluster")

	require.NoError(t, err)
	require.Equal(t, 1, reader.calls)
	nodes := resp.GetNodes()
	require.Len(t, nodes, 2)
	require.Equal(t, uint64(1), nodes[0].GetId())
	require.Equal(t, "127.0.0.1:7001", nodes[0].GetClusterAddr())
	require.Equal(t, "http://127.0.0.1:5001", nodes[0].GetApiServerAddr())
	require.True(t, nodes[0].GetOnline())
	require.Equal(t, uint64(2), nodes[1].GetId())
	require.False(t, nodes[1].GetOnline())
	slots := resp.GetSlots()
	require.Len(t, slots, 2)
	require.Equal(t, uint32(7), slots[0].GetId())
	require.Equal(t, uint64(1), slots[0].GetLeader())
	require.Equal(t, uint32(5), slots[0].GetTerm())
	require.Equal(t, []uint64{1, 2}, slots[0].GetReplicas())
	require.Equal(t, uint32(9), slots[1].GetId())
	require.Equal(t, []uint64{2, 1}, slots[1].GetReplicas())
	reader.snapshot.Slots[0].Replicas[0] = 99
	require.Equal(t, []uint64{2, 1}, slots[1].GetReplicas())
}

func TestClusterConfigRequiresReader(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	_, err = app.ClusterConfig(context.Background(), "plugin.cluster")

	require.ErrorIs(t, err, ErrClusterReaderRequired)
}

func TestClusterChannelsBelongNodeGroupsByOwnerStableOrder(t *testing.T) {
	owners := &recordingChannelOwnerReader{owners: map[message.ChannelID]uint64{
		{ID: "g2", Type: frame.ChannelTypeGroup}:  2,
		{ID: "p1", Type: frame.ChannelTypePerson}: 1,
		{ID: "g1", Type: frame.ChannelTypeGroup}:  2,
	}}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, ChannelOwners: owners})
	require.NoError(t, err)

	resp, err := app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{
		{ChannelId: "g2", ChannelType: uint32(frame.ChannelTypeGroup)},
		{ChannelId: "p1", ChannelType: uint32(frame.ChannelTypePerson)},
		{ChannelId: "g1", ChannelType: uint32(frame.ChannelTypeGroup)},
	}}, "plugin.cluster")

	require.NoError(t, err)
	require.Equal(t, []message.ChannelID{
		{ID: "g2", Type: frame.ChannelTypeGroup},
		{ID: "p1", Type: frame.ChannelTypePerson},
		{ID: "g1", Type: frame.ChannelTypeGroup},
	}, owners.calls)
	groups := resp.GetClusterChannelBelongNodeResps()
	require.Len(t, groups, 2)
	require.Equal(t, uint64(1), groups[0].GetNodeId())
	require.Len(t, groups[0].GetChannels(), 1)
	require.Equal(t, "p1", groups[0].GetChannels()[0].GetChannelId())
	require.Equal(t, uint64(2), groups[1].GetNodeId())
	require.Len(t, groups[1].GetChannels(), 2)
	require.Equal(t, "g2", groups[1].GetChannels()[0].GetChannelId())
	require.Equal(t, "g1", groups[1].GetChannels()[1].GetChannelId())
}

func TestClusterChannelsBelongNodeRequiresLookupAndRejectsUnknownOwner(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}})
	require.NoError(t, err)
	_, err = app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}}}, "plugin.cluster")
	require.ErrorIs(t, err, ErrChannelOwnerReaderRequired)

	app, err = NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, ChannelOwners: &recordingChannelOwnerReader{}})
	require.NoError(t, err)
	_, err = app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}}}, "plugin.cluster")
	require.ErrorIs(t, err, ErrChannelOwnerUnknown)

	wantErr := errors.New("lookup failed")
	owners := &recordingChannelOwnerReader{err: wantErr}
	app, err = NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, ChannelOwners: owners})
	require.NoError(t, err)
	_, err = app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}}}, "plugin.cluster")
	require.ErrorIs(t, err, wantErr)
}

func TestClusterChannelsBelongNodeRejectsEmptyChannels(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, ChannelOwners: &recordingChannelOwnerReader{}})
	require.NoError(t, err)

	_, err = app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{}, "plugin.cluster")
	require.ErrorIs(t, err, ErrChannelRequired)

	_, err = app.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{Channels: []*pluginproto.Channel{{ChannelType: 2}}}, "plugin.cluster")
	require.ErrorIs(t, err, ErrChannelRequired)
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
	calls  []message.ChannelID
	owners map[message.ChannelID]uint64
	err    error
}

func (r *recordingChannelOwnerReader) ChannelOwnerNode(_ context.Context, id message.ChannelID) (uint64, error) {
	r.calls = append(r.calls, id)
	if r.err != nil {
		return 0, r.err
	}
	return r.owners[id], nil
}
