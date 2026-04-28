package app

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerMessageReaderMaxMessageSeqReadsLocalCommittedHW(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store := engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	require.NoError(t, store.StoreCheckpoint(channel.Checkpoint{Epoch: 1, HW: 55}))

	reader := managerMessageReader{
		localNodeID: 1,
		channelLog:  engine,
		metas: managerMessageMetasFake{
			meta: metadb.ChannelRuntimeMeta{
				ChannelID:   id.ID,
				ChannelType: int64(id.Type),
				Leader:      1,
			},
		},
	}

	got, err := reader.MaxMessageSeq(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, uint64(55), got)
}

type managerMessageMetasFake struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

func (f managerMessageMetasFake) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return f.meta, f.err
}
