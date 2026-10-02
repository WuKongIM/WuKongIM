package meta

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/stretchr/testify/require"
)

// Tests seed valid historical rows directly so cleanup can cover many lifetimes
// without depending on the orchestration that originally created them.
func seedMQTTReclamation(t *testing.T, db *MetaDB, session MQTTSession, generations []uint64, count int) {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	require.NoError(t, mqttSessionTable.StageUpsert(b, 7, session))
	for _, g := range generations {
		for i := 0; i < count; i++ {
			sub := mqttSubscriptionFixture()
			sub.SessionGeneration = g
			sub.Topic = fmt.Sprintf("topic-%03d", i)
			require.NoError(t, mqttSubscriptionTable.StageUpsert(b, 7, sub))
		}
		key := mqttCursorFixture()
		key.SessionGeneration = g
		c := MQTTDeliveryCursor{LastMutationDigest: strings.Repeat("a", 64), Key: key, Topic: "topic-000", AuthorizationVersion: 9, StartAfter: 100, AccountedThrough: 104, WindowThrough: 101, CompletedThrough: 100, PendingMessages: 2, PendingBytes: 80, Revision: 10, UpdatedAtMS: 1000, InflightCount: 1, InflightBytes: 30, HeadPacketID: 1, TailPacketID: 1, LastWindowPacketID: 1, LastWindowDeliveryOrder: 1, AccountingVersion: 1, AccountingHead: 101, AccountingTail: 101}
		require.NoError(t, mqttDeliveryCursorTable.StageUpsert(b, 7, c))
		p := mqttPublication(101)
		p.Bytes = 30
		in := MQTTInflight{Key: key, Direction: MQTTOutbound, PacketID: 1, DeliveryOrder: 1, Publication: p, QoS: 1, Stage: MQTTInflightAwaitPUBACK, Topic: c.Topic, UpdatedAtMS: 1000}
		require.NoError(t, mqttInflightTable.StageUpsert(b, 7, in))
		r := MQTTAccountingRange{Key: key, From: 101, Through: 104, SubscriptionRevision: 3, EvaluatedAtMS: 1000, Items: []MQTTAccountingItem{{Position: 101, Bytes: 30}, {Position: 103, Bytes: 50}}}
		b.addOp(7, func(_ context.Context, state *batchCommitState, eb *engine.Batch) error {
			return stageMQTTAccounting(state, eb, 7, r, false)
		})
	}
	require.NoError(t, b.Commit(context.Background()))
}
func reclamationRequest(s MQTTSession, through uint64) MQTTSessionReclamation {
	return MQTTSessionReclamation{Namespace: s.Namespace, ClientID: s.ClientID, ExpectedRevision: s.Revision, ThroughGeneration: through, UpdatedAtMS: s.UpdatedAtMS + 1}
}
func reclaimMQTT(t *testing.T, db *MetaDB, m MQTTSessionReclamation) MQTTSessionReclamationResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	r, e := b.ReclaimMQTTSession(7, m)
	require.NoError(t, e)
	require.Equal(t, MQTTSessionReclamationResult{}, *r)
	require.NoError(t, b.Commit(context.Background()))
	return *r
}
func readReclamationSession(t *testing.T, db *MetaDB) MQTTSession {
	t.Helper()
	s, found, e := db.HashSlot(7).GetMQTTSession(context.Background(), "main", "client")
	require.NoError(t, e)
	require.True(t, found)
	return s
}
func assertMQTTLifetimePresent(t *testing.T, db *MetaDB, g uint64, want bool) {
	t.Helper()
	ctx := context.Background()
	key := mqttCursorFixture()
	key.SessionGeneration = g
	_, found, e := db.HashSlot(7).GetMQTTDeliveryCursor(ctx, key)
	require.NoError(t, e)
	require.Equal(t, want, found)
	_, found, e = db.HashSlot(7).GetMQTTInflight(ctx, key.Namespace, key.ClientID, g, MQTTOutbound, 1)
	require.NoError(t, e)
	require.Equal(t, want, found)
	rows, _, _, e := db.HashSlot(7).ListMQTTInflight(ctx, key.Namespace, key.ClientID, g, MQTTOutbound, MQTTInflightCursor{}, 10)
	require.NoError(t, e)
	require.Equal(t, want, len(rows) > 0, "send-order index")
	ak, e := mqttAccountingKey(7, key, 101)
	require.NoError(t, e)
	_, found, e = db.get(ak)
	require.NoError(t, e)
	require.Equal(t, want, found, "qualified charge receipt")
}
func TestMQTTReclamationBoundedPagesPreserveLiveLifetimeAndDetachedDuties(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	s := mqttSessionFixture()
	s.Generation = 2
	s.Revision = 1000
	s.PendingMessages = 2
	s.PendingBytes = 80
	s.OutboundInflight = 1
	seedMQTTReclamation(t, st.db, s, []uint64{1, 2}, 70)
	b := st.db.NewBatch()
	defer b.Close()
	will := mqttWillFixture()
	binding := mqttSourceBindingFixture()
	require.NoError(t, mqttWillTable.StageUpsert(b, 7, will))
	require.NoError(t, mqttSourceBindingTable.StageUpsert(b, 7, binding))
	require.NoError(t, b.Commit(ctx))
	m := reclamationRequest(s, 1)
	first := reclaimMQTT(t, st.db, m)
	require.Equal(t, MQTTSessionCASApplied, first.Status)
	require.False(t, first.Done)
	require.Equal(t, 64, first.RemovedSubscriptions)
	now := readReclamationSession(t, st.db)
	require.Zero(t, now.ReclaimedThroughGeneration)
	rows, _, done, e := st.db.HashSlot(7).ListMQTTSubscriptions(ctx, s.Namespace, s.ClientID, 1, "", 256)
	require.NoError(t, e)
	require.True(t, done)
	require.Len(t, rows, 6)
	assertMQTTLifetimePresent(t, st.db, 1, true)
	assertMQTTLifetimePresent(t, st.db, 2, true)
	require.Equal(t, MQTTSessionCASConflict, reclaimMQTT(t, st.db, m).Status, "partial retry requires fresh revision")
	m = reclamationRequest(now, 1)
	last := reclaimMQTT(t, st.db, m)
	require.Equal(t, MQTTSessionCASApplied, last.Status)
	require.True(t, last.Done)
	require.Equal(t, 6, last.RemovedSubscriptions)
	after := readReclamationSession(t, st.db)
	require.EqualValues(t, 1, after.ReclaimedThroughGeneration)
	require.Equal(t, s.PendingMessages, after.PendingMessages)
	require.Equal(t, s.PendingBytes, after.PendingBytes)
	require.Equal(t, s.OutboundInflight, after.OutboundInflight)
	require.Equal(t, s.UID, after.UID)
	require.Equal(t, s.OwnerGeneration, after.OwnerGeneration)
	assertMQTTLifetimePresent(t, st.db, 1, false)
	assertMQTTLifetimePresent(t, st.db, 2, true)
	pending, _, _, e := st.db.HashSlot(7).ListMQTTSubscriptionRecovery(ctx, MQTTSubscriptionRecoveryCursor{}, 256)
	require.NoError(t, e)
	require.Len(t, pending, 70)
	for _, sub := range pending {
		require.EqualValues(t, 2, sub.SessionGeneration)
	}
	gotWill, found, e := mqttWillTable.Get(ctx, st.db.HashSlot(7), mqttWillPrimaryKey(will.Key))
	require.NoError(t, e)
	require.True(t, found)
	require.Equal(t, will, gotWill)
	gotBinding, found, e := mqttSourceBindingTable.Get(ctx, st.db.HashSlot(7), mqttSourceBindingPrimaryKey(binding.Key))
	require.NoError(t, e)
	require.True(t, found)
	require.Equal(t, binding, gotBinding)
	retry := reclaimMQTT(t, st.db, m)
	require.Equal(t, MQTTSessionCASUnchanged, retry.Status)
	require.True(t, retry.Done)
	require.Equal(t, after, readReclamationSession(t, st.db))
}
func TestMQTTReclamationRejectsUnfinishedOrStaleAuthority(t *testing.T) {
	for _, name := range []string{"active", "offline", "future", "stale", "missing", "old-time"} {
		t.Run(name, func(t *testing.T) {
			st := openTestMetaStore(t)
			defer st.close(t)
			s := mqttSessionFixture()
			s.State = MQTTSessionEnded
			s.LeaseUntilMS = 0
			s.TerminationReason = MQTTSessionExpired
			if name == "active" {
				s = mqttSessionFixture()
			}
			if name == "offline" {
				s.State = MQTTSessionOffline
				s.TerminationReason = 0
				s.OfflineExpiresAtMS = 9000
			}
			seedMQTTReclamation(t, st.db, s, []uint64{1}, 1)
			m := reclamationRequest(s, 1)
			switch name {
			case "future":
				m.ThroughGeneration = 2
			case "stale":
				m.ExpectedRevision++
			case "missing":
				m.ClientID = "absent"
			case "old-time":
				m.UpdatedAtMS = s.UpdatedAtMS - 1
			}
			r := reclaimMQTT(t, st.db, m)
			require.Equal(t, MQTTSessionCASConflict, r.Status)
			require.False(t, r.Done)
			require.Equal(t, s, readReclamationSession(t, st.db))
			assertMQTTLifetimePresent(t, st.db, 1, true)
		})
	}
}
func TestMQTTReclamationEndedClearsCountersAndRetainsFenceAcrossRestore(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	s := mqttSessionFixture()
	s.State = MQTTSessionEnded
	s.LeaseUntilMS = 0
	s.TerminationReason = MQTTSessionQuota
	s.PendingMessages = 2
	s.PendingBytes = 80
	s.OutboundInflight = 1
	seedMQTTReclamation(t, st.db, s, []uint64{1}, 1)
	r := reclaimMQTT(t, st.db, reclamationRequest(s, 1))
	require.True(t, r.Done)
	now := readReclamationSession(t, st.db)
	require.Zero(t, now.PendingMessages)
	require.Zero(t, now.PendingBytes)
	require.Zero(t, now.OutboundInflight)
	require.Equal(t, s.NextPacketID, now.NextPacketID)
	assertMQTTLifetimePresent(t, st.db, 1, false)
	snap, e := st.db.OpenBackupHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, e)
	payload, e := io.ReadAll(snap)
	require.NoError(t, e)
	require.NoError(t, snap.Close())
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)), false))
	require.Equal(t, now, readReclamationSession(t, target.db))
	assertMQTTLifetimePresent(t, target.db, 1, false)
	// Generic writes cannot advance or regress the cleanup fence, even at Clean Start.
	for _, g := range []uint64{0, 2} {
		bad := now
		bad.Revision++
		bad.Generation = 2
		bad.OwnerGeneration++
		bad.State = MQTTSessionActive
		bad.LeaseUntilMS = 9000
		bad.TerminationReason = 0
		bad.ReclaimedThroughGeneration = g
		if ValidateMQTTSession(bad) == nil {
			require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, target.db, bad, now.Revision).Status)
		}
	}
	next := now
	next.Revision++
	next.Generation++
	next.OwnerGeneration++
	next.State = MQTTSessionActive
	next.LeaseUntilMS = 9000
	next.TerminationReason = 0
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, target.db, next, now.Revision).Status)
	sub := mqttSubscriptionFixture()
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSubscription(t, target.db, mqttSubscriptionMutation(next, sub)).Status)
	require.EqualValues(t, 1, readReclamationSession(t, target.db).ReclaimedThroughGeneration)
}
func TestMQTTReclamationSameBatchRowsAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			st := openTestMetaStore(t)
			defer st.close(t)
			ctx := context.Background()
			s := mqttSessionFixture()
			seedMQTTReclamation(t, st.db, s, []uint64{1}, 1)
			b := st.db.NewBatch()
			defer b.Close()
			sub := mqttSubscriptionFixture()
			sub.Topic = "only-in-overlay"
			require.NoError(t, mqttSubscriptionTable.StageUpsert(b, 7, sub))
			s.Revision++
			s.State = MQTTSessionEnded
			s.LeaseUntilMS = 0
			s.TerminationReason = MQTTSessionExpired
			_, e := b.CompareAndSwapMQTTSession(7, 1, s)
			require.NoError(t, e)
			r, e := b.ReclaimMQTTSession(7, reclamationRequest(s, 1))
			require.NoError(t, e)
			b.addOp(7, func(_ context.Context, state *batchCommitState, eb *engine.Batch) error {
				_, found, e := loadUpdateRow(mqttDeliveryCursorTable, state, 7, mqttDeliveryCursorPrimaryKey(mqttCursorFixture()))
				require.NoError(t, e)
				require.False(t, found, "range deletion must mask disk rows")
				_, found, e = loadUpdateRow(mqttSubscriptionTable, state, 7, mqttSubscriptionPrimaryKey(sub.Namespace, sub.ClientID, 1, sub.Topic))
				require.NoError(t, e)
				require.False(t, found, "cleanup must include newly staged intent")
				key, e := mqttAccountingKey(7, mqttCursorFixture(), 101)
				require.NoError(t, e)
				_, found, e = mqttDeliveryCursorTable.loadBatchValue(state, key)
				require.NoError(t, e)
				require.False(t, found)
				if fail {
					return dberrors.ErrConflict
				}
				return nil
			})
			e = b.Commit(ctx)
			if fail {
				require.ErrorIs(t, e, dberrors.ErrConflict)
				require.EqualValues(t, 1, readReclamationSession(t, st.db).Revision)
				assertMQTTLifetimePresent(t, st.db, 1, true)
			} else {
				require.NoError(t, e)
				require.True(t, r.Done)
				require.Equal(t, 2, r.RemovedSubscriptions)
				assertMQTTLifetimePresent(t, st.db, 1, false)
			}
		})
	}
}
func TestMQTTReclamationGenerationRangeIsolationAndOverflow(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	s := mqttSessionFixture()
	s.Generation = math.MaxUint64
	s.State = MQTTSessionEnded
	s.LeaseUntilMS = 0
	s.TerminationReason = MQTTSessionExpired
	seedMQTTReclamation(t, st.db, s, []uint64{1, 2, math.MaxUint64}, 1)
	b := st.db.NewBatch()
	defer b.Close()
	other := mqttSubscriptionFixture()
	other.ClientID = "client-other"
	require.NoError(t, mqttSubscriptionTable.StageUpsert(b, 7, other))
	require.NoError(t, mqttSubscriptionTable.StageUpsert(b, 8, mqttSubscriptionFixture()))
	require.NoError(t, b.Commit(context.Background()))
	r := reclaimMQTT(t, st.db, reclamationRequest(s, math.MaxUint64))
	require.True(t, r.Done)
	require.Equal(t, 3, r.RemovedSubscriptions)
	for _, g := range []uint64{1, 2, math.MaxUint64} {
		assertMQTTLifetimePresent(t, st.db, g, false)
	}
	_, found, e := st.db.HashSlot(7).GetMQTTSubscription(context.Background(), other.Namespace, other.ClientID, 1, other.Topic)
	require.NoError(t, e)
	require.True(t, found)
	other = mqttSubscriptionFixture()
	_, found, e = st.db.HashSlot(8).GetMQTTSubscription(context.Background(), other.Namespace, other.ClientID, 1, other.Topic)
	require.NoError(t, e)
	require.True(t, found)
}
func TestMQTTReclamationFieldAndRequestValidation(t *testing.T) {
	s := mqttSessionFixture()
	payload, e := json.Marshal(s)
	require.NoError(t, e)
	require.NotContains(t, string(payload), "reclaimed_through_generation")
	s.ReclaimedThroughGeneration = 1
	require.ErrorIs(t, ValidateMQTTSession(s), dberrors.ErrInvalidArgument)
	m := reclamationRequest(mqttSessionFixture(), 1)
	for _, change := range []func(*MQTTSessionReclamation){func(m *MQTTSessionReclamation) { m.Namespace = "" }, func(m *MQTTSessionReclamation) { m.ClientID = "" }, func(m *MQTTSessionReclamation) { m.ExpectedRevision = 0 }, func(m *MQTTSessionReclamation) { m.ExpectedRevision = math.MaxUint64 }, func(m *MQTTSessionReclamation) { m.ThroughGeneration = 0 }, func(m *MQTTSessionReclamation) { m.UpdatedAtMS = 0 }} {
		bad := m
		change(&bad)
		require.ErrorIs(t, ValidateMQTTSessionReclamation(bad), dberrors.ErrInvalidArgument)
	}
}

func TestMQTTReclamationCorruptSubscriptionCannotCertifyCompletion(t *testing.T) {
	for _, brokenKey := range []bool{false, true} {
		t.Run(fmt.Sprint(brokenKey), func(t *testing.T) {
			st := openTestMetaStore(t)
			defer st.close(t)
			s := mqttSessionFixture()
			s.State = MQTTSessionEnded
			s.LeaseUntilMS = 0
			s.TerminationReason = MQTTSessionExpired
			seedMQTTReclamation(t, st.db, s, []uint64{1}, 1)
			key, e := mqttSubscriptionTable.primaryRowKey(7, mqttSubscriptionPrimaryKey(s.Namespace, s.ClientID, 1, "topic-000"))
			require.NoError(t, e)
			if brokenKey {
				key = append(key, 0xff)
			}
			eb := st.engine.NewBatch()
			defer eb.Close()
			require.NoError(t, eb.Set(key, []byte("corrupt")))
			require.NoError(t, eb.Commit(true))
			b := st.db.NewBatch()
			defer b.Close()
			_, e = b.ReclaimMQTTSession(7, reclamationRequest(s, 1))
			require.NoError(t, e)
			require.Error(t, b.Commit(context.Background()))
			require.Equal(t, s, readReclamationSession(t, st.db))
			assertMQTTLifetimePresent(t, st.db, 1, true)
		})
	}
}
func TestMQTTReclamationOptionalColumnAndLifecycleFence(t *testing.T) {
	s := mqttSessionFixture()
	s.WillGeneration = 3
	pk := mqttSessionPrimaryKey(s.Namespace, s.ClientID)
	key, e := mqttSessionTable.primaryRowKey(7, pk)
	require.NoError(t, e)
	old, e := hex.DecodeString("3105616c69636514011401140114021106626f6f742d61141113904e16011480a30513001601144014808040140114011400140014904e14808080201403160013d00f")
	require.NoError(t, e)
	got, e := decodeMQTTSessionRow(key, pk, rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, old))
	require.NoError(t, e)
	require.Equal(t, s, got)
	require.Zero(t, got.ReclaimedThroughGeneration)
	st := openTestMetaStore(t)
	defer st.close(t)
	s = mqttSessionFixture()
	s.Generation = 3
	seedMQTTReclamation(t, st.db, s, nil, 0)
	forged := s
	forged.Revision++
	forged.ReclaimedThroughGeneration = 1
	require.Equal(t, MQTTSessionCASConflict, writeMQTTSession(t, st.db, forged, s.Revision).Status)
	forged.Generation++
	forged.OwnerGeneration++
	m := MQTTLifecycleMutation{ExpectedRevision: s.Revision, ExpectedGeneration: s.Generation, OwnerGeneration: s.OwnerGeneration, OwnerNodeID: s.OwnerNodeID, OwnerBootID: s.OwnerBootID, ConnectionID: s.ConnectionID, Event: MQTTLifecycleConnect, CleanStart: true, Session: forged}
	b := st.db.NewBatch()
	defer b.Close()
	r, e := b.ApplyMQTTLifecycle(7, m)
	require.NoError(t, e)
	require.NoError(t, b.Commit(context.Background()))
	require.Equal(t, MQTTSessionCASConflict, r.Status)
}

func TestMQTTReclamationResultsIndependentOfApplyBatchGrouping(t *testing.T) {
	var outcomes [2][]MQTTSessionReclamationResult
	for mode := range 2 {
		st := openTestMetaStore(t)
		defer st.close(t)
		s := mqttSessionFixture()
		s.State = MQTTSessionEnded
		s.LeaseUntilMS = 0
		s.TerminationReason = MQTTSessionExpired
		seedMQTTReclamation(t, st.db, s, []uint64{1}, 130)
		b := st.db.NewBatch()
		defer b.Close()
		var results []*MQTTSessionReclamationResult
		for i := range 3 {
			m := reclamationRequest(s, 1)
			m.ExpectedRevision += uint64(i)
			m.UpdatedAtMS += int64(i)
			r, e := b.ReclaimMQTTSession(7, m)
			require.NoError(t, e)
			results = append(results, r)
			if mode == 0 {
				require.NoError(t, b.Commit(context.Background()))
				b = st.db.NewBatch()
				defer b.Close()
			}
		}
		if mode == 1 {
			require.NoError(t, b.Commit(context.Background()))
		}
		for _, r := range results {
			outcomes[mode] = append(outcomes[mode], *r)
		}
	}
	require.Equal(t, outcomes[0], outcomes[1], "Raft replay batching must not alter results or durable completion")
	require.True(t, outcomes[1][2].Done)
}
