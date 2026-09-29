package mqttsession_test

import (
	"context"
	"testing"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// anchorThrough moves the fixture's latest committed anchor to through.
func anchorThrough(f *accountingFixture, through uint64) {
	f.plan.Anchor.Anchor.Through = through
	f.plan.Anchor.Manifest.BaseOffset, f.plan.Anchor.Manifest.LastOffset, f.plan.Anchor.Manifest.PreviousIndex = through, through+1, through
}

// A fresh subscription starts at its protected boundary, which may be newer
// than the latest anchor. That is "not ready yet", not invalid evidence; once
// accounting has advanced past an anchor, a regressed anchor stays evidence.
func TestAccountingWaitsForAnchorBehindUnaccountedCursor(t *testing.T) {
	ctx := context.Background()
	f, a := setupAccounting(t)
	anchorThrough(f, 9)
	got, err := a.Account(ctx, f.key)
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.NotErrorIs(t, err, app.ErrEvidence)
	require.Zero(t, got)
	require.Zero(t, f.writes)

	f.setMessages([]ch.MQTTReplayPublication{{Message: ch.Message{Payload: []byte("native"), FromUID: "alice"}}})
	_, err = a.Account(ctx, f.key)
	require.NoError(t, err)
	writes := f.writes
	anchorThrough(f, 9)
	_, err = a.Account(ctx, f.key)
	require.ErrorIs(t, err, app.ErrEvidence)
	require.Equal(t, writes, f.writes)
}

// Before the first accounting receipt there is nothing to admit: the window
// is idle, not a concurrent lifecycle conflict.
func TestWindowAdmissionIdlesBeforeFirstAccounting(t *testing.T) {
	f, _, w := setupWindow(t)
	got, err := w.Prepare(context.Background(), f.connection.Owner, f.key)
	require.NoError(t, err)
	require.True(t, got.Idle)
	require.Nil(t, got.Delivery)
}
