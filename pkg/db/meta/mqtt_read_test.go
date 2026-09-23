package meta

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/stretchr/testify/require"
)

func TestMQTTReadSnapshotKeepsIndexAndPrimaryAtOneRevision(t *testing.T) {
	ctx := context.Background()
	s := openTestMetaStore(t)
	defer s.close(t)
	r := mqttSessionFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, r, 0).Status)
	snapshot, err := s.db.engine.NewSnapshot()
	require.NoError(t, err)
	defer snapshot.Close()
	view := &Shard{db: s.db, hashSlot: 7, readSnapshot: snapshot}
	r.Revision++
	r.State, r.LeaseUntilMS, r.OfflineExpiresAtMS = MQTTSessionOffline, 0, 9000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, r, 1).Status)
	old, found, err := view.GetMQTTSession(ctx, r.Namespace, r.ClientID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(1), old.Revision)
	rows, _, done, err := view.ListMQTTSessionDeadlines(ctx, MQTTSessionDeadlineCursor{}, 1)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []MQTTSession{old}, rows, "index and row must come from the same snapshot")
	current, err := s.db.ReadMQTTState(ctx, 7, MQTTRead{Kind: MQTTReadSession, Namespace: r.Namespace, ClientID: r.ClientID})
	require.NoError(t, err)
	require.Equal(t, &r, current.Session)
}

func TestMQTTReadScopedPagesAndClosedShapes(t *testing.T) {
	ctx := context.Background()
	s := openTestMetaStore(t)
	defer s.close(t)
	session := mqttSessionFixture()
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, session, 0).Status)
	for _, topic := range []string{"a", "b"} {
		sub := mqttSubscriptionFixture()
		sub.Topic, sub.Generation = topic, session.Revision+1
		require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, s.db, mqttSubscriptionMutation(session, sub)).Status)
		session.Revision++
	}
	q := MQTTRead{Kind: MQTTReadSubscriptions, Namespace: session.Namespace, ClientID: session.ClientID, SessionGeneration: 1, Limit: 1}
	first, err := s.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, err)
	require.False(t, first.Done)
	require.Equal(t, "a", first.Subscriptions[0].Topic)
	require.Equal(t, session.Revision, first.Session.Revision)
	q.After = first.After
	second, err := s.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, err)
	require.True(t, second.Done)
	require.Equal(t, "b", second.Subscriptions[0].Topic)
	missing, err := s.db.ReadMQTTState(ctx, 8, q)
	require.NoError(t, err)
	require.Nil(t, missing.Session)
	require.Empty(t, missing.Subscriptions)
	for _, bad := range []MQTTRead{
		{}, {Kind: MQTTReadSession, Namespace: "main", ClientID: "client", Limit: 1},
		{Kind: MQTTReadSession, Namespace: "main", ClientID: "client", Topic: "ignored"},
		{Kind: MQTTReadSubscriptions, Namespace: "main", ClientID: "client", SessionGeneration: 1, Limit: 65},
		{Kind: MQTTReadWillRecovery, Limit: 17},
		{Kind: MQTTReadSubscriptions, Namespace: "main", ClientID: "client", SessionGeneration: 1, Limit: 1, After: MQTTReadCursor{Inflight: MQTTInflightCursor{DeliveryOrder: 1, PacketID: 1}}},
	} {
		_, err := s.db.ReadMQTTState(ctx, 7, bad)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument, "%+v", bad)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.db.ReadMQTTState(canceled, 7, q)
	require.ErrorIs(t, err, context.Canceled)
}
