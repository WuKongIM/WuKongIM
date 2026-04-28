package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestChannelMessagesRPCReturnsMaxMessageSeq(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store := engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	require.NoError(t, store.StoreCheckpoint(channel.Checkpoint{Epoch: 1, HW: 42}))

	adapter := New(Options{
		LocalNodeID:  2,
		ChannelLogDB: engine,
		ChannelMeta: &stubNodeMetaRefresher{
			meta: channel.Meta{
				ID:     id,
				Leader: 2,
			},
		},
	})

	body := mustMarshal(t, channelMessagesRequest{
		Query: ChannelMessagesQuery{
			ChannelID:  id,
			MaxSeqOnly: true,
		},
	})
	respBody, err := adapter.handleChannelMessagesRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeChannelMessagesResponse(respBody)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, uint64(42), resp.Page.MaxMessageSeq)
	require.Empty(t, resp.Page.Messages)
	require.False(t, resp.Page.HasMore)
}
