package cluster

import (
	"context"
	"errors"
	"testing"

	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestPluginConversationReaderMapsActiveRows(t *testing.T) {
	node := &recordingPluginConversationNode{rows: []metadb.ConversationState{
		{ChannelID: "g1", ChannelType: 2},
		{ChannelID: "p1", ChannelType: 1},
	}}
	reader := NewPluginConversationReader(node)

	channels, err := reader.ConversationChannels(context.Background(), "u1", 1000)

	require.NoError(t, err)
	require.Equal(t, 1, node.calls)
	require.Equal(t, metadb.ConversationKindNormal, node.kind)
	require.Equal(t, "u1", node.uid)
	require.Equal(t, 1000, node.limit)
	require.Len(t, channels, 2)
	require.Equal(t, "g1", channels[0].ID)
	require.Equal(t, uint8(2), channels[0].Type)
	require.Equal(t, "p1", channels[1].ID)
	require.Equal(t, uint8(1), channels[1].Type)
}

func TestPluginConversationReaderPropagatesNodeError(t *testing.T) {
	wantErr := errors.New("active rows unavailable")
	reader := NewPluginConversationReader(&recordingPluginConversationNode{err: wantErr})

	_, err := reader.ConversationChannels(context.Background(), "u1", 1000)

	require.ErrorIs(t, err, wantErr)
}

func TestPluginConversationReaderRejectsInvalidChannelType(t *testing.T) {
	reader := NewPluginConversationReader(&recordingPluginConversationNode{rows: []metadb.ConversationState{
		{ChannelID: "bad", ChannelType: 256},
	}})

	_, err := reader.ConversationChannels(context.Background(), "u1", 1000)

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid conversation channel type")
}

func TestPluginConversationReaderRequiresNode(t *testing.T) {
	reader := NewPluginConversationReader(nil)

	_, err := reader.ConversationChannels(context.Background(), "u1", 1000)

	require.ErrorIs(t, err, pluginusecase.ErrConversationReaderRequired)
}

type recordingPluginConversationNode struct {
	calls int
	kind  metadb.ConversationKind
	uid   string
	limit int
	rows  []metadb.ConversationState
	err   error
}

func (n *recordingPluginConversationNode) ListConversationActivePage(_ context.Context, kind metadb.ConversationKind, uid string, _ metadb.ConversationActiveCursor, limit int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error) {
	n.calls++
	n.kind = kind
	n.uid = uid
	n.limit = limit
	if n.err != nil {
		return nil, metadb.ConversationActiveCursor{}, false, n.err
	}
	return append([]metadb.ConversationState(nil), n.rows...), metadb.ConversationActiveCursor{}, true, nil
}
