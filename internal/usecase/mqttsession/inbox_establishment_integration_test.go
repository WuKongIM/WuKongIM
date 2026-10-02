//go:build integration

package mqttsession_test

import (
	"context"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTInboxEstablishmentDeadlineRetainsUnconfirmedCandidate(t *testing.T) {
	f := setupInboxEstablishment(t)
	f.directory(t, meta.ChannelKey{ChannelID: f.channel.ID, ChannelType: 1})
	f.options.Timeout = 300 * time.Millisecond
	confirmed := false
	f.replay = func(ctx context.Context, _ meta.MQTTBindingOwner, _ uint64) error {
		confirmed = true
		<-ctx.Done()
		return nil // A late success cannot authorize progress or activation.
	}
	p, err := app.NewInboxEstablishment(f.options)
	require.NoError(t, err)
	got, err := p.Establish(context.Background(), f.request())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, got)
	require.True(t, confirmed)
	q := f.readQualification(t)
	require.Equal(t, meta.MQTTBindingPreparing, q.Stage)
	require.Empty(t, q.DiscoveryAfterChannelID)
	require.False(t, q.DiscoveryDone)
	require.Zero(t, f.owners.Snapshot().Operations)
}
