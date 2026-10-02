package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func mqttOfflineDrainSession(t *testing.T, db *MetaDB) MQTTSession {
	t.Helper()
	s, found, err := db.HashSlot(7).GetMQTTSession(context.Background(), "main", "client")
	require.NoError(t, err)
	require.True(t, found)
	return s
}

func mqttOfflineDrainCloseIntent(t *testing.T, db *MetaDB, replacement bool) {
	t.Helper()
	s := mqttOfflineDrainSession(t, db)
	sub, found, err := db.HashSlot(7).GetMQTTSubscription(context.Background(), "main", "client", 1, mqttSubscriptionFixture().Topic)
	require.NoError(t, err)
	require.True(t, found)
	sub.Stage, sub.RecoveryAtMS = MQTTSubscriptionRemoving, 1000
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, db, mqttSubscriptionMutation(s, sub)).Status)
	if replacement {
		s = mqttOfflineDrainSession(t, db)
		sub.Stage, sub.RecoveryAtMS = MQTTSubscriptionRemoved, 0
		require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, db, mqttSubscriptionMutation(s, sub)).Status)
		s = mqttOfflineDrainSession(t, db)
		sub.Stage, sub.RecoveryAtMS = MQTTSubscriptionPreparing, 1000
		sub.Generation, sub.OperationID, sub.AuthorizationVersion = s.Revision+1, "new-operation", 77
		require.Equal(t, MQTTSessionCASApplied, writeMQTTSubscription(t, db, mqttSubscriptionMutation(s, sub)).Status)
	}
}

func mqttOfflineDrainDisconnect(t *testing.T, db *MetaDB) MQTTSession {
	t.Helper()
	s := mqttOfflineDrainSession(t, db)
	previous := s.Revision
	s.State, s.LeaseUntilMS, s.OfflineExpiresAtMS = MQTTSessionOffline, 0, 9000
	s.Revision++
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, db, s, previous).Status)
	return s
}

func TestMQTTOfflineDrainPreservesExchangesAndQualifiedCharges(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		s := openTestMetaStore(t)
		m := prepareMQTTQualified(t, s.db)
		require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
		admit := mqttWindowMutation(t, s.db, MQTTWindowAdmit)
		admit.Publication = mqttPublication(101)
		admit.Publication.Bytes = 30
		x := writeMQTTWindow(t, s.db, admit)
		require.Equal(t, MQTTWindowApplied, x.Status)
		before, found, err := s.db.HashSlot(7).GetMQTTInflight(context.Background(), "main", "client", 1, MQTTOutbound, x.PacketID)
		require.NoError(t, err)
		require.True(t, found)
		mqttOfflineDrainCloseIntent(t, s.db, replacement)
		session := mqttOfflineDrainDisconnect(t, s.db)
		advance := mqttWindowMutation(t, s.db, MQTTWindowAdvance)
		advance.Through, advance.ReleasedMessages, advance.ReleasedBytes = 104, 1, 50
		wrong := advance
		wrong.ReleasedBytes--
		require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, wrong).Status)
		require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, advance).Status)
		require.Equal(t, MQTTWindowUnchanged, writeMQTTWindow(t, s.db, advance).Status)
		r := readMQTTAccounting(t, s.db, m.Key)
		require.Nil(t, r.Accounting)
		require.EqualValues(t, 1, r.Session.PendingMessages)
		require.EqualValues(t, 30, r.Session.PendingBytes)
		require.EqualValues(t, 1, r.Session.OutboundInflight)
		require.EqualValues(t, 100, r.DeliveryCursors[0].CompletedThrough)
		require.EqualValues(t, 104, r.DeliveryCursors[0].WindowThrough)
		require.Equal(t, session.OfflineExpiresAtMS, r.Session.OfflineExpiresAtMS)
		require.Equal(t, MQTTSessionOffline, r.Session.State)
		require.Equal(t, session.WillGeneration, r.Session.WillGeneration)
		ack := mqttWindowMutation(t, s.db, MQTTWindowAck)
		ack.PacketID, ack.DeliveryOrder = x.PacketID, x.DeliveryOrder
		require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, ack).Status)
		after, found, err := s.db.HashSlot(7).GetMQTTInflight(context.Background(), "main", "client", 1, MQTTOutbound, x.PacketID)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, before, after)
		s.close(t)
	}
}

func TestMQTTOfflineDrainRejectsLiveIntentAndStaleAuthority(t *testing.T) {
	for _, mode := range []string{"active-intent", "owner", "node", "boot", "connection", "revision", "lifetime", "future-subscription", "ended", "admit"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			key := prepareMQTTWindow(t, s.db, 3, 64, 1)
			if mode != "active-intent" {
				mqttOfflineDrainCloseIntent(t, s.db, false)
			}
			session := mqttOfflineDrainDisconnect(t, s.db)
			if mode == "ended" {
				previous := session.Revision
				session.Revision++
				session.State, session.OfflineExpiresAtMS, session.TerminationReason = MQTTSessionEnded, 0, MQTTSessionExpired
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, session, previous).Status)
			}
			m := mqttWindowMutation(t, s.db, MQTTWindowAdvance)
			m.Through, m.ReleasedMessages, m.ReleasedBytes = 110, 4, 400
			switch mode {
			case "owner":
				m.OwnerGeneration++
			case "node":
				m.OwnerNodeID++
			case "boot":
				m.OwnerBootID = "wrong"
			case "connection":
				m.ConnectionID++
			case "revision":
				m.ExpectedRevision--
			case "lifetime":
				m.Key.SessionGeneration++
			case "future-subscription":
				m.Key.SubscriptionGeneration++
			case "admit":
				m.Op, m.Through, m.ReleasedMessages, m.ReleasedBytes, m.Publication = MQTTWindowAdmit, 0, 0, 0, mqttPublication(101)
			}
			before := readMQTTAccounting(t, s.db, key)
			require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, m).Status)
			require.Equal(t, before, readMQTTAccounting(t, s.db, key))
		})
	}
}

func TestMQTTOfflineDrainKeepsOneAccountingRangePerMutation(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	m := prepareMQTTQualified(t, s.db)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	m.ExpectedRevision, m.Through, m.AddedMessages, m.AddedBytes = 5, 108, 1, 70
	m.Qualified.From, m.Qualified.Items = 105, []MQTTAccountingItem{{Position: 107, Bytes: 70}}
	require.Equal(t, MQTTSessionCASApplied, writeMQTTCursor(t, s.db, m).Status)
	mqttOfflineDrainCloseIntent(t, s.db, false)
	mqttOfflineDrainDisconnect(t, s.db)
	advance := mqttWindowMutation(t, s.db, MQTTWindowAdvance)
	advance.Through, advance.ReleasedMessages, advance.ReleasedBytes = 108, 3, 150
	before := readMQTTAccounting(t, s.db, m.Key)
	require.Equal(t, MQTTWindowConflict, writeMQTTWindow(t, s.db, advance).Status)
	require.Equal(t, before, readMQTTAccounting(t, s.db, m.Key))
	advance.Through, advance.ReleasedMessages, advance.ReleasedBytes = 104, 2, 80
	require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, advance).Status)
	middle := readMQTTAccounting(t, s.db, m.Key)
	require.EqualValues(t, 105, middle.Accounting.From)
	require.EqualValues(t, 1, middle.Session.PendingMessages)
	advance = mqttWindowMutation(t, s.db, MQTTWindowAdvance)
	advance.Through, advance.ReleasedMessages, advance.ReleasedBytes = 108, 1, 70
	require.Equal(t, MQTTWindowApplied, writeMQTTWindow(t, s.db, advance).Status)
	end := readMQTTAccounting(t, s.db, m.Key)
	require.Nil(t, end.Accounting)
	require.Zero(t, end.Session.PendingMessages)
	require.Zero(t, end.Session.PendingBytes)
	require.EqualValues(t, 108, end.DeliveryCursors[0].CompletedThrough)
}

func TestMQTTOfflineCancellationInitOnlyForClosedIntent(t *testing.T) {
	for _, mode := range []string{"preparing", "removing", "replacement", "authorization", "owner", "revision", "ended"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			prepareMQTTCursorSession(t, s.db)
			if mode != "preparing" {
				mqttOfflineDrainCloseIntent(t, s.db, mode == "replacement")
			}
			session := mqttOfflineDrainDisconnect(t, s.db)
			if mode == "ended" {
				previous := session.Revision
				session.Revision++
				session.State, session.OfflineExpiresAtMS, session.TerminationReason = MQTTSessionEnded, 0, MQTTSessionExpired
				require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, session, previous).Status)
			}
			m := mqttCursorMutation(session)
			m.Op = MQTTCursorCancelInit
			switch mode {
			case "authorization":
				m.AuthorizationVersion++
			case "owner":
				m.OwnerGeneration++
			case "revision":
				m.ExpectedRevision--
			}
			r := writeMQTTCursor(t, s.db, m)
			if mode != "removing" && mode != "replacement" {
				require.Equal(t, MQTTSessionCASConflict, r.Status)
				_, found, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), m.Key)
				require.NoError(t, err)
				require.False(t, found)
				require.Equal(t, session, mqttOfflineDrainSession(t, s.db))
				return
			}
			require.Equal(t, MQTTSessionCASApplied, r.Status)
			require.Equal(t, MQTTSessionOffline, r.SessionState)
			require.Equal(t, MQTTSessionCASUnchanged, writeMQTTCursor(t, s.db, m).Status)
			c, found, err := s.db.HashSlot(7).GetMQTTDeliveryCursor(context.Background(), m.Key)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, c.StartAfter, c.CompletedThrough)
			require.Zero(t, c.PendingMessages)
			m.ExpectedRevision, m.Through = r.CurrentRevision, m.Through+1
			require.Equal(t, MQTTSessionCASConflict, writeMQTTCursor(t, s.db, m).Status)
			require.Equal(t, session.OfflineExpiresAtMS, mqttOfflineDrainSession(t, s.db).OfflineExpiresAtMS)
		})
	}
}
