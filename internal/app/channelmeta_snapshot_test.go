package app

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestChannelSnapshotApplierLoadSnapshotPayloadReportsMissingPayload(t *testing.T) {
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	store := engine.ForChannel(channel.ChannelKey("group-10"), channel.ChannelID{ID: "group-10", Type: 2})
	payload, err := channelSnapshotApplier{store: store}.LoadSnapshotPayload(context.Background())

	require.ErrorIs(t, err, channel.ErrEmptyState)
	require.Nil(t, payload)
}

func TestChannelSnapshotApplierLoadSnapshotPayloadReturnsStoredPayload(t *testing.T) {
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	store := engine.ForChannel(channel.ChannelKey("group-10"), channel.ChannelID{ID: "group-10", Type: 2})
	require.NoError(t, store.StoreSnapshotPayload([]byte("snap")))

	payload, err := channelSnapshotApplier{store: store}.LoadSnapshotPayload(context.Background())

	require.NoError(t, err)
	require.Equal(t, []byte("snap"), payload)
}
