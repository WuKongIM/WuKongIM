package mqttsession_test

import (
	"context"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestConsumerMaintenanceRetiresEndedLifetimeTombstone(t *testing.T) {
	ctx := context.Background()
	f, a := setupAccounting(t)
	p, e := app.NewSourceProgress(app.SourceProgressOptions{Store: f.store, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	r, e := app.NewSourceRemoval(app.SourceRemovalOptions{Store: f.store, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	ret, e := app.NewSourceRetirement(app.SourceRetirementOptions{Store: f.store})
	require.NoError(t, e)
	c, e := app.NewConsumerMaintenance(app.ConsumerMaintenanceOptions{Store: f.store, Accounting: a, Drain: backgroundDrain(t, &progressStore{groupSourceStore: f.store}, func() time.Time { return f.now }), Progress: p, Removal: r, Retirement: ret, Ender: f.service})
	require.NoError(t, e)
	k := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: f.key.SourceID, Generation: f.key.SourceGeneration}, Namespace: f.key.Namespace, ClientID: f.key.ClientID, SessionGeneration: f.key.SessionGeneration, SubscriptionGeneration: f.key.SubscriptionGeneration}

	row := f.row(t)
	row.Revision++
	row.QuotaMessages = 1
	_, e = f.store.CompareAndSwapMQTTSession(ctx, row.Revision-1, row)
	require.NoError(t, e)
	require.NoError(t, f.service.Disconnect(ctx, app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("one")}}, {Message: ch.Message{Payload: []byte("two")}}})
	retired := false
	for range 6 {
		out, e := c.Maintain(ctx, k)
		require.NoError(t, e)
		retired = retired || out.Retired
	}
	require.True(t, retired)
	got, e := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: k})
	require.NoError(t, e)
	require.Empty(t, got.Bindings, "ended-lifetime tombstone must be retired")
	// A later turn on the retired key is a quiet no-op.
	out, e := c.Maintain(ctx, k)
	require.NoError(t, e, "a retired key is completed work")
	require.False(t, out.Retired)
}
