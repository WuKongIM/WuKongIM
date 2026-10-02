package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/stretchr/testify/require"
)

func mqttLifecycleMutation(s MQTTSession, event MQTTLifecycleEvent, at int64) MQTTLifecycleMutation {
	next := s
	next.Revision++
	next.UpdatedAtMS = at
	next.WillGeneration = 0
	next.LastLifecycleDigest = ""
	return MQTTLifecycleMutation{ExpectedRevision: s.Revision, ExpectedGeneration: s.Generation, OwnerGeneration: s.OwnerGeneration, OwnerNodeID: s.OwnerNodeID, OwnerBootID: s.OwnerBootID, ConnectionID: s.ConnectionID, Event: event, Session: next}
}
func mqttLifecycleWill(m MQTTLifecycleMutation) *MQTTWill {
	w := mqttWillFixture()
	s := m.Session
	w.Key = MQTTWillKey{Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.Generation, WillGeneration: s.Revision}
	w.UID, w.OwnerGeneration, w.OwnerNodeID, w.OwnerBootID, w.ConnectionID = s.UID, s.OwnerGeneration, s.OwnerNodeID, s.OwnerBootID, s.ConnectionID
	w.DecisionRevision, w.UpdatedAtMS = s.Revision, s.UpdatedAtMS
	w.IdempotencyKey, _ = MQTTWillIdempotencyKey(w.Key)
	return &w
}
func writeMQTTLifecycle(t *testing.T, db *MetaDB, m MQTTLifecycleMutation) MQTTLifecycleResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	r, err := b.ApplyMQTTLifecycle(7, m)
	require.NoError(t, err)
	require.Equal(t, MQTTLifecycleResult{}, *r)
	require.NoError(t, b.Commit(context.Background()))
	return *r
}
func readMQTTLifecycleSession(t *testing.T, db *MetaDB) MQTTSession {
	t.Helper()
	s, found, err := db.HashSlot(7).GetMQTTSession(context.Background(), "main", "client")
	require.NoError(t, err)
	require.True(t, found)
	return s
}
func prepareMQTTLifecycle(t *testing.T, db *MetaDB) (MQTTSession, MQTTWill) {
	t.Helper()
	s := mqttSessionFixture()
	s.WillGeneration = 0
	writeMQTTSession(t, db, s, 0)
	m := mqttLifecycleMutation(s, MQTTLifecycleInstallWill, 1100)
	m.Will = mqttLifecycleWill(m)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTLifecycle(t, db, m).Status)
	return readMQTTLifecycleSession(t, db), *m.Will
}
func mqttLifecycleOffline(m *MQTTLifecycleMutation, expiry uint32) {
	m.Session.SessionExpirySec = expiry
	m.Session.LeaseUntilMS = 0
	if expiry == 0 {
		m.Session.State = MQTTSessionEnded
		m.Session.OfflineExpiresAtMS = 0
		m.Session.TerminationReason = MQTTSessionExpired
	} else {
		m.Session.State = MQTTSessionOffline
		m.Session.OfflineExpiresAtMS = m.Session.UpdatedAtMS + int64(expiry)*1000
		m.Session.TerminationReason = 0
	}
}
func mqttLifecycleReconnect(s MQTTSession, at int64, clean bool) MQTTLifecycleMutation {
	m := mqttLifecycleMutation(s, MQTTLifecycleConnect, at)
	m.CleanStart = clean
	m.Session.OwnerGeneration++
	m.Session.OwnerNodeID = 3
	m.Session.OwnerBootID = "new-boot"
	m.Session.ConnectionID++
	m.Session.State = MQTTSessionActive
	m.Session.LeaseUntilMS = at + 5000
	m.Session.OfflineExpiresAtMS = 0
	m.Session.TerminationReason = 0
	if clean || s.State == MQTTSessionEnded || s.State == MQTTSessionOffline && s.OfflineExpiresAtMS <= at || s.State == MQTTSessionActive && s.SessionExpirySec == 0 {
		m.Session.Generation++
		m.Session.PendingMessages = 0
		m.Session.PendingBytes = 0
		m.Session.OutboundInflight = 0
		m.Session.NextPacketID = 1
		m.Session.NextDeliveryOrder = 1
	}
	return m
}

func TestMQTTLifecycleInitialCreationInstallAndFencing(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	initial := MQTTLifecycleMutation{Event: MQTTLifecycleConnect, Session: mqttSessionFixture()}
	initial.Session.WillGeneration = 0
	initial.Will = mqttLifecycleWill(initial)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTLifecycle(t, s.db, initial).Status)
	require.Equal(t, MQTTSessionCASUnchanged, writeMQTTLifecycle(t, s.db, initial).Status)
	current := readMQTTLifecycleSession(t, s.db)
	require.EqualValues(t, 1, current.WillGeneration)
	require.Len(t, current.LastLifecycleDigest, 64)
	got, found, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), initial.Will.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, *initial.Will, got)
	for _, change := range []func(*MQTTLifecycleMutation){
		func(m *MQTTLifecycleMutation) { m.OwnerGeneration++ }, func(m *MQTTLifecycleMutation) { m.OwnerNodeID++ }, func(m *MQTTLifecycleMutation) { m.OwnerBootID = "stale" }, func(m *MQTTLifecycleMutation) { m.ConnectionID++ }, func(m *MQTTLifecycleMutation) { m.ExpectedGeneration++ },
	} {
		m := mqttLifecycleMutation(current, MQTTLifecycleNormalDisconnect, 2000)
		mqttLifecycleOffline(&m, 20)
		change(&m)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, s.db, m).Status)
	}
	changed := initial
	changed.Session.UpdatedAtMS++
	require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, s.db, changed).Status)
	// Heartbeat is allowed, but cannot pretend it was the previous lifecycle result.
	heartbeat := current
	heartbeat.Revision++
	heartbeat.LeaseUntilMS++
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, s.db, heartbeat, current.Revision).Status)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, s.db, initial).Status)
}

func TestMQTTLifecycleCloseResumeCleanStartAndDue(t *testing.T) {
	for _, mode := range []string{"normal", "delay", "short-expiry", "zero-expiry", "resume", "takeover", "clean-start", "expired-resume", "due", "early-due", "early-expiry"} {
		t.Run(mode, func(t *testing.T) {
			s := openTestMetaStore(t)
			defer s.close(t)
			session, will := prepareMQTTLifecycle(t, s.db)
			if mode == "takeover" || mode == "clean-start" {
				m := mqttLifecycleReconnect(session, 2000, mode == "clean-start")
				m.Will = mqttLifecycleWill(m)
				require.Equal(t, MQTTSessionCASApplied, writeMQTTLifecycle(t, s.db, m).Status)
				require.Equal(t, MQTTSessionCASUnchanged, writeMQTTLifecycle(t, s.db, m).Status)
				old, _, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), will.Key)
				require.NoError(t, err)
				want := MQTTWillCancelled
				if mode == "clean-start" {
					want = MQTTWillReady
				}
				require.Equal(t, want, old.Stage)
				current := readMQTTLifecycleSession(t, s.db)
				require.Equal(t, m.Will.Key.WillGeneration, current.WillGeneration)
				fresh, found, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), m.Will.Key)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, MQTTWillArmed, fresh.Stage)
				return
			}
			event := MQTTLifecycleDisconnectWithWill
			if mode == "normal" {
				event = MQTTLifecycleNormalDisconnect
			}
			m := mqttLifecycleMutation(session, event, 2000)
			expiry := uint32(20)
			if mode == "short-expiry" || mode == "expired-resume" {
				expiry = 2
			}
			if mode == "zero-expiry" {
				expiry = 0
			}
			mqttLifecycleOffline(&m, expiry)
			require.Equal(t, MQTTSessionCASApplied, writeMQTTLifecycle(t, s.db, m).Status)
			session = readMQTTLifecycleSession(t, s.db)
			old, _, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), will.Key)
			require.NoError(t, err)
			switch mode {
			case "normal":
				require.Equal(t, MQTTWillCancelled, old.Stage)
				require.Zero(t, session.WillGeneration)
			case "delay":
				require.Equal(t, MQTTWillWaiting, old.Stage)
				require.EqualValues(t, 7000, old.DueAtMS)
			case "short-expiry":
				require.Equal(t, MQTTWillWaiting, old.Stage)
				require.EqualValues(t, 4000, old.DueAtMS)
			case "zero-expiry":
				require.Equal(t, MQTTWillReady, old.Stage)
				require.EqualValues(t, 2000, old.DueAtMS)
				require.Zero(t, session.WillGeneration)
			case "resume", "expired-resume":
				at := int64(3000)
				want := MQTTWillCancelled
				if mode == "expired-resume" {
					at = 5000
					want = MQTTWillReady
				}
				reconnect := mqttLifecycleReconnect(session, at, false)
				reconnect.Will = mqttLifecycleWill(reconnect)
				require.Equal(t, MQTTSessionCASApplied, writeMQTTLifecycle(t, s.db, reconnect).Status)
				old, _, err = s.db.HashSlot(7).GetMQTTWill(context.Background(), will.Key)
				require.NoError(t, err)
				require.Equal(t, want, old.Stage)
				require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, s.db, m).Status, "late close from the old connection cannot alter the new owner")
			case "due", "early-due":
				at := int64(7000)
				want := MQTTSessionCASApplied
				if mode == "early-due" {
					at = 6000
					want = MQTTSessionCASConflict
				}
				due := mqttLifecycleMutation(session, MQTTLifecycleWillDue, at)
				require.Equal(t, want, writeMQTTLifecycle(t, s.db, due).Status)
				current := readMQTTLifecycleSession(t, s.db)
				if mode == "due" {
					require.Zero(t, current.WillGeneration)
					require.Equal(t, MQTTSessionOffline, current.State)
				} else {
					require.Equal(t, session, current)
				}
			case "early-expiry":
				end := mqttLifecycleMutation(session, MQTTLifecycleEnd, 3000)
				end.Session.State = MQTTSessionEnded
				end.Session.OfflineExpiresAtMS = 0
				end.Session.TerminationReason = MQTTSessionExpired
				require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, s.db, end).Status)
			}
		})
	}
}

func TestMQTTLifecyclePreventsBypassAndPartialCommit(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	session, will := prepareMQTTLifecycle(t, s.db)
	direct := will
	direct.Revision++
	direct.DecisionRevision++
	direct.Stage = MQTTWillCancelled
	direct.CancelReason = MQTTWillNormalDisconnect
	require.Equal(t, MQTTSessionCASConflict, writeMQTTWill(t, s.db, will.Revision, direct).Status)
	for _, change := range []func(*MQTTSession){
		func(r *MQTTSession) { r.WillGeneration = 0 }, func(r *MQTTSession) { r.OwnerGeneration++; r.ConnectionID++ },
		func(r *MQTTSession) {
			r.State = MQTTSessionEnded
			r.LeaseUntilMS = 0
			r.TerminationReason = MQTTSessionExplicit
		},
		func(r *MQTTSession) { r.LastLifecycleDigest = "" },
	} {
		bad := session
		bad.Revision++
		change(&bad)
		require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, s.db, bad, session.Revision).Status)
	}
	reconnect := mqttLifecycleReconnect(session, 2000, true)
	reconnect.Will = mqttLifecycleWill(reconnect)
	// A preexisting new key must not leave the old Will resolved or the owner advanced.
	require.Equal(t, MQTTSessionCASApplied, writeMQTTWill(t, s.db, 0, *reconnect.Will).Status)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, s.db, reconnect).Status)
	require.Equal(t, session, readMQTTLifecycleSession(t, s.db))
	old, _, err := s.db.HashSlot(7).GetMQTTWill(context.Background(), will.Key)
	require.NoError(t, err)
	require.Equal(t, will, old)
	end := mqttLifecycleMutation(session, MQTTLifecycleEnd, 2000)
	end.Session.State = MQTTSessionEnded
	end.Session.LeaseUntilMS = 0
	end.Session.TerminationReason = MQTTSessionRevoked
	b := s.db.NewBatch()
	defer b.Close()
	_, err = b.ApplyMQTTLifecycle(7, end)
	require.NoError(t, err)
	sentinel := errors.New("neighbor rejected")
	b.addOp(7, func(context.Context, *batchCommitState, *engine.Batch) error { return sentinel })
	require.ErrorIs(t, b.Commit(context.Background()), sentinel)
	require.Equal(t, session, readMQTTLifecycleSession(t, s.db))
	old, _, err = s.db.HashSlot(7).GetMQTTWill(context.Background(), will.Key)
	require.NoError(t, err)
	require.Equal(t, will, old)
	bad := end
	bad.Session.WillGeneration = 999
	b2 := s.db.NewBatch()
	defer b2.Close()
	_, err = b2.ApplyMQTTLifecycle(7, bad)
	require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
}

func TestMQTTLifecycleQuotaEndsWillInSameCommit(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "offline"}[offline], func(t *testing.T) {
			db := openTestMetaStore(t)
			defer db.close(t)
			session := prepareMQTTCursorSession(t, db.db)
			init := mqttCursorMutation(session)
			writeMQTTCursor(t, db.db, init)
			session = readMQTTLifecycleSession(t, db.db)
			install := mqttLifecycleMutation(session, MQTTLifecycleInstallWill, 1100)
			install.Will = mqttLifecycleWill(install)
			require.Equal(t, MQTTSessionCASApplied, writeMQTTLifecycle(t, db.db, install).Status)
			session = readMQTTLifecycleSession(t, db.db)
			if offline {
				close := mqttLifecycleMutation(session, MQTTLifecycleDisconnectWithWill, 1500)
				mqttLifecycleOffline(&close, 20)
				writeMQTTLifecycle(t, db.db, close)
				session = readMQTTLifecycleSession(t, db.db)
			}
			account := mqttCursorMutation(session)
			account.Op = MQTTCursorAccount
			account.Through = 101 + session.QuotaMessages
			account.AddedMessages = session.QuotaMessages + 1
			account.AddedBytes = 1
			account.UpdatedAtMS = 2000
			result := writeMQTTCursor(t, db.db, account)
			require.Equal(t, MQTTSessionCASApplied, result.Status)
			require.Equal(t, MQTTSessionQuota, result.TerminationReason)
			require.Equal(t, MQTTSessionCASUnchanged, writeMQTTCursor(t, db.db, account).Status)
			current := readMQTTLifecycleSession(t, db.db)
			require.Equal(t, MQTTSessionEnded, current.State)
			require.Zero(t, current.WillGeneration)
			will, found, err := db.db.HashSlot(7).GetMQTTWill(context.Background(), install.Will.Key)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, MQTTWillReady, will.Stage)
			require.EqualValues(t, 2000, will.DueAtMS)
			require.Equal(t, current.Revision, will.DecisionRevision)
		})
	}
}

func TestMQTTLifecycleSameBatchAndMissingWillFailure(t *testing.T) {
	db := openTestMetaStore(t)
	defer db.close(t)
	s := mqttSessionFixture()
	s.WillGeneration = 0
	writeMQTTSession(t, db.db, s, 0)
	install := mqttLifecycleMutation(s, MQTTLifecycleInstallWill, 1100)
	install.Will = mqttLifecycleWill(install)
	after := install.Session
	after.WillGeneration = install.Will.Key.WillGeneration
	close := mqttLifecycleMutation(after, MQTTLifecycleNormalDisconnect, 2000)
	mqttLifecycleOffline(&close, 20)
	b := db.db.NewBatch()
	defer b.Close()
	a, err := b.ApplyMQTTLifecycle(7, install)
	require.NoError(t, err)
	c, err := b.ApplyMQTTLifecycle(7, close)
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background()))
	require.Equal(t, MQTTSessionCASApplied, a.Status)
	require.Equal(t, MQTTSessionCASApplied, c.Status)
	require.Zero(t, readMQTTLifecycleSession(t, db.db).WillGeneration)
	other := openTestMetaStore(t)
	defer other.close(t)
	s, w := prepareMQTTLifecycle(t, other.db)
	corrupt := other.db.NewBatch()
	defer corrupt.Close()
	corrupt.addOp(7, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		return deleteUpdateRow(mqttWillTable, state, batch, 7, mqttWillPrimaryKey(w.Key))
	})
	require.NoError(t, corrupt.Commit(context.Background()))
	end := mqttLifecycleMutation(s, MQTTLifecycleEnd, 2000)
	end.Session.State = MQTTSessionEnded
	end.Session.LeaseUntilMS = 0
	end.Session.TerminationReason = MQTTSessionExplicit
	fail := other.db.NewBatch()
	defer fail.Close()
	_, err = fail.ApplyMQTTLifecycle(7, end)
	require.NoError(t, err)
	require.ErrorIs(t, fail.Commit(context.Background()), dberrors.ErrCorruptValue)
	require.Equal(t, s, readMQTTLifecycleSession(t, other.db))
}

func TestMQTTLifecycleZeroExpiryAndCounterGuards(t *testing.T) {
	db := openTestMetaStore(t)
	defer db.close(t)
	s := mqttSessionFixture()
	s.WillGeneration = 0
	s.SessionExpirySec = 0
	writeMQTTSession(t, db.db, s, 0)
	install := mqttLifecycleMutation(s, MQTTLifecycleInstallWill, 1100)
	install.Will = mqttLifecycleWill(install)
	writeMQTTLifecycle(t, db.db, install)
	s = readMQTTLifecycleSession(t, db.db)
	invalid := mqttLifecycleMutation(s, MQTTLifecycleDisconnectWithWill, 2000)
	mqttLifecycleOffline(&invalid, 20)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, db.db, invalid).Status, "zero CONNECT expiry cannot increase in DISCONNECT")
	reconnect := mqttLifecycleReconnect(s, 2000, false)
	reconnect.Session.Generation = s.Generation
	require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, db.db, reconnect).Status, "takeover ends a zero-expiry Session")
	reconnect = mqttLifecycleReconnect(s, 2000, false)
	reconnect.Session.PendingMessages = 1
	require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, db.db, reconnect).Status)
	reconnect = mqttLifecycleReconnect(s, 2000, false)
	require.Equal(t, MQTTSessionCASApplied, writeMQTTLifecycle(t, db.db, reconnect).Status)
	w, _, err := db.db.HashSlot(7).GetMQTTWill(context.Background(), install.Will.Key)
	require.NoError(t, err)
	require.Equal(t, MQTTWillReady, w.Stage)
	bad := readMQTTLifecycleSession(t, db.db)
	bad.LastLifecycleDigest = "invalid"
	require.ErrorIs(t, ValidateMQTTSession(bad), dberrors.ErrInvalidArgument)
}

func TestMQTTLifecycleExpiredOwnerLeaseRequiresCloseDecision(t *testing.T) {
	db := openTestMetaStore(t)
	defer db.close(t)
	s, _ := prepareMQTTLifecycle(t, db.db)
	m := mqttLifecycleReconnect(s, 6000, false)
	require.Equal(t, MQTTSessionCASConflict, writeMQTTLifecycle(t, db.db, m).Status, "resolve the expired owner before interpreting reconnect as timely resume")
	require.Equal(t, s, readMQTTLifecycleSession(t, db.db))
}
