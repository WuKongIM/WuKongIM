package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/stretchr/testify/require"
)

func seedLegacyMQTTReclamationSessions(t *testing.T, db *MetaDB, count int) []MQTTSession {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	rows := make([]MQTTSession, count)
	for i := range count {
		s := mqttSessionFixture()
		s.ClientID = fmt.Sprintf("client-%03d", i)
		if i%2 == 0 {
			s.State = MQTTSessionEnded
			s.LeaseUntilMS = 0
			s.TerminationReason = MQTTSessionExpired
		}
		rows[i] = s
		require.NoError(t, mqttSessionTable.StageUpsert(b, 7, s))
	}
	require.NoError(t, b.Commit(context.Background()))
	// Erase only new derived entries, emulating pre-index durable rows.
	eb := db.engine.NewBatch()
	defer eb.Close()
	p := encodeIndexPrefix(7, TableIDMQTTSession, 3)
	require.NoError(t, eb.DeleteRange(engine.Span{Start: p, End: keycodec.PrefixEnd(p)}))
	require.NoError(t, eb.Commit(true))
	return rows
}
func buildMQTTReclamationIndex(t *testing.T, db *MetaDB) MQTTReclamationIndexResult {
	t.Helper()
	b := db.NewBatch()
	defer b.Close()
	r, e := b.BuildMQTTReclamationIndex(7)
	require.NoError(t, e)
	require.Equal(t, MQTTReclamationIndexResult{}, *r)
	require.NoError(t, b.Commit(context.Background()))
	return *r
}
func TestMQTTReclamationIndexHistoricalCoverageAndResume(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	before := seedLegacyMQTTReclamationSessions(t, st.db, 70)
	q := MQTTRead{Kind: MQTTReadSessionReclamation, Limit: 16}
	_, e := st.db.ReadMQTTState(ctx, 7, q)
	require.ErrorIs(t, e, dberrors.ErrConflict, "empty index without coverage is not empty work")
	first := buildMQTTReclamationIndex(t, st.db)
	require.Equal(t, 64, first.Scanned)
	require.False(t, first.Done)
	_, e = st.db.ReadMQTTState(ctx, 7, q)
	require.ErrorIs(t, e, dberrors.ErrConflict)
	snap, e := st.db.OpenBackupHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, e)
	payload, e := io.ReadAll(snap)
	require.NoError(t, e)
	require.NoError(t, snap.Close())
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshotReaderForRestore(ctx, []uint16{7}, bytes.NewReader(payload), int64(len(payload)), false))
	last := buildMQTTReclamationIndex(t, target.db)
	require.True(t, last.Done)
	require.Equal(t, 6, last.Scanned)
	require.Equal(t, MQTTReclamationIndexResult{Done: true}, buildMQTTReclamationIndex(t, target.db))
	var actual []MQTTSession
	for range 4 {
		page, e := target.db.ReadMQTTState(ctx, 7, q)
		require.NoError(t, e)
		actual = append(actual, page.Sessions...)
		if page.Done {
			break
		}
		require.NotEqual(t, q.After, page.After)
		q.After = page.After
	}
	var want []MQTTSession
	for _, s := range before {
		if s.State == MQTTSessionEnded {
			want = append(want, s)
		}
		got, found, e := target.db.HashSlot(7).GetMQTTSession(ctx, s.Namespace, s.ClientID)
		require.NoError(t, e)
		require.True(t, found)
		require.Equal(t, s, got, "index building cannot change lifecycle")
	}
	require.Equal(t, want, actual)
}
func TestMQTTReclamationIndexTracksLifecycleAndPrimaryOrder(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	require.True(t, buildMQTTReclamationIndex(t, st.db).Done)
	for _, id := range []string{"zz", "a", "ab"} {
		s := mqttSessionFixture()
		s.ClientID = id
		require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, st.db, s, 0).Status)
		s.Revision++
		s.Generation++
		s.OwnerGeneration++
		require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, st.db, s, 1).Status)
	}
	q := MQTTRead{Kind: MQTTReadSessionReclamation, Limit: 1}
	var ids []string
	for range 4 {
		r, e := st.db.ReadMQTTState(ctx, 7, q)
		require.NoError(t, e)
		for _, s := range r.Sessions {
			ids = append(ids, s.ClientID)
		}
		if r.Done {
			break
		}
		q.After = r.After
	}
	require.Equal(t, []string{"a", "ab", "zz"}, ids)
	s, _, e := st.db.HashSlot(7).GetMQTTSession(ctx, "main", "a")
	require.NoError(t, e)
	require.True(t, reclaimMQTT(t, st.db, reclamationRequest(s, 1)).Done)
	q = MQTTRead{Kind: MQTTReadSessionReclamation, Limit: 64}
	r, e := st.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, e)
	require.Len(t, r.Sessions, 2)
	// A later ended current lifetime re-enters even after old generations were reclaimed.
	s, _, e = st.db.HashSlot(7).GetMQTTSession(ctx, "main", "a")
	require.NoError(t, e)
	old := s.Revision
	s.Revision++
	s.State = MQTTSessionEnded
	s.LeaseUntilMS = 0
	s.TerminationReason = MQTTSessionExpired
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, st.db, s, old).Status)
	r, e = st.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, e)
	require.Len(t, r.Sessions, 3)
}
func TestMQTTReclamationIndexApplyGroupingAndRollback(t *testing.T) {
	var outcomes [2][]MQTTReclamationIndexResult
	for mode := range 2 {
		st := openTestMetaStore(t)
		defer st.close(t)
		seedLegacyMQTTReclamationSessions(t, st.db, 130)
		b := st.db.NewBatch()
		defer b.Close()
		var results []*MQTTReclamationIndexResult
		for range 3 {
			r, e := b.BuildMQTTReclamationIndex(7)
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
	require.Equal(t, outcomes[0], outcomes[1])
	require.Equal(t, []MQTTReclamationIndexResult{{Scanned: 64}, {Scanned: 64}, {Scanned: 2, Done: true}}, outcomes[1])
	st := openTestMetaStore(t)
	defer st.close(t)
	seedLegacyMQTTReclamationSessions(t, st.db, 1)
	b := st.db.NewBatch()
	defer b.Close()
	_, e := b.BuildMQTTReclamationIndex(7)
	require.NoError(t, e)
	b.addOp(7, func(context.Context, *batchCommitState, *engine.Batch) error { return dberrors.ErrConflict })
	require.ErrorIs(t, b.Commit(context.Background()), dberrors.ErrConflict)
	_, e = st.db.ReadMQTTState(context.Background(), 7, MQTTRead{Kind: MQTTReadSessionReclamation, Limit: 1})
	require.ErrorIs(t, e, dberrors.ErrConflict)
	require.Equal(t, 1, buildMQTTReclamationIndex(t, st.db).Scanned)
}
func TestMQTTReclamationIndexIncludesSameBatchAndLatePrimaryKeys(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	seedLegacyMQTTReclamationSessions(t, st.db, 65)
	b := st.db.NewBatch()
	defer b.Close()
	added := mqttSessionFixture()
	added.ClientID = "first"
	added.Generation = 2
	require.NoError(t, mqttSessionTable.StageUpsert(b, 7, added))
	first, e := b.BuildMQTTReclamationIndex(7)
	require.NoError(t, e)
	last, e := b.BuildMQTTReclamationIndex(7)
	require.NoError(t, e)
	require.NoError(t, b.Commit(context.Background()))
	require.Equal(t, 64, first.Scanned)
	require.Equal(t, 2, last.Scanned)
	require.True(t, last.Done)
	added.ClientID = "a"
	added.Revision = 1
	added.Generation = 1
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, st.db, added, 0).Status)
	added.Revision++
	added.Generation++
	added.OwnerGeneration++
	require.Equal(t, MQTTSessionCASApplied, writeMQTTSession(t, st.db, added, 1).Status)
	r, e := st.db.ReadMQTTState(context.Background(), 7, MQTTRead{Kind: MQTTReadSessionReclamation, Limit: 64})
	require.NoError(t, e)
	require.Len(t, r.Sessions, 35)
	require.Equal(t, "a", r.Sessions[0].ClientID)
}
func TestMQTTReclamationIndexPinnedCoverageAndStrictWitnesses(t *testing.T) {
	for _, damage := range []string{"missing", "stale", "malformed", "coverage"} {
		t.Run(damage, func(t *testing.T) {
			st := openTestMetaStore(t)
			defer st.close(t)
			rows := seedLegacyMQTTReclamationSessions(t, st.db, 1)
			snap, e := st.engine.NewSnapshot()
			require.NoError(t, e)
			defer snap.Close()
			view := &Shard{db: st.db, hashSlot: 7, readSnapshot: snap}
			buildMQTTReclamationIndex(t, st.db)
			_, _, _, e = view.ListMQTTSessionReclamation(context.Background(), MQTTSessionCursor{}, 1)
			require.ErrorIs(t, e, dberrors.ErrConflict, "new coverage must not validate old snapshot")
			eb := st.engine.NewBatch()
			defer eb.Close()
			row := rows[0]
			pk := mqttSessionPrimaryKey(row.Namespace, row.ClientID)
			key, e := mqttSessionTable.primaryRowKey(7, pk)
			require.NoError(t, e)
			switch damage {
			case "missing":
				require.NoError(t, eb.Delete(key))
			case "stale":
				row = mqttSessionFixture()
				row.ClientID = rows[0].ClientID
				v, e := encodeMQTTSessionRow(key, row)
				require.NoError(t, e)
				require.NoError(t, eb.Set(key, v))
			case "malformed":
				p := encodeIndexPrefix(7, TableIDMQTTSession, 3)
				require.NoError(t, eb.Set(append(p, 0xff), nil))
			case "coverage":
				require.NoError(t, eb.Set(mqttReclamationIndexKey(7), []byte("broken")))
			}
			require.NoError(t, eb.Commit(true))
			_, e = st.db.ReadMQTTState(context.Background(), 7, MQTTRead{Kind: MQTTReadSessionReclamation, Limit: 64})
			require.Error(t, e)
		})
	}
}
func TestMQTTReclamationIndexClosedReadShape(t *testing.T) {
	require.True(t, (MQTTRead{Kind: MQTTReadSessionReclamation}).Recovery())
	for _, q := range []MQTTRead{{Kind: MQTTReadSessionReclamation}, {Kind: MQTTReadSessionReclamation, Limit: 65}, {Kind: MQTTReadSessionReclamation, Limit: 1, Namespace: "main"}, {Kind: MQTTReadSessionReclamation, Limit: 1, After: MQTTReadCursor{Session: MQTTSessionCursor{Namespace: "main"}}}, {Kind: MQTTReadSession, Namespace: "main", ClientID: "c", After: MQTTReadCursor{Session: MQTTSessionCursor{Namespace: "main", ClientID: "c"}}}} {
		require.ErrorIs(t, ValidateMQTTRead(q), dberrors.ErrInvalidArgument)
	}
}

func TestMQTTReclamationIndexProgressCodecAndOldJSON(t *testing.T) {
	key := mqttReclamationIndexKey(7)
	for _, state := range []mqttReclamationIndexProgress{{Done: true}, {After: MQTTSessionCursor{Namespace: "main", ClientID: "a"}}, {After: MQTTSessionCursor{Namespace: "main", ClientID: "a"}, Done: true}} {
		value, e := encodeMQTTReclamationIndexProgress(key, state)
		require.NoError(t, e)
		got, e := decodeMQTTReclamationIndexProgress(key, value)
		require.NoError(t, e)
		require.Equal(t, state, got)
		for n := 0; n < len(value); n++ {
			_, e := decodeMQTTReclamationIndexProgress(key, value[:n])
			require.Error(t, e)
		}
		_, e = decodeMQTTReclamationIndexProgress(mqttReclamationIndexKey(8), value)
		require.Error(t, e)
		env, e := rowcodec.Unwrap(key, value)
		require.NoError(t, e)
		for _, bad := range [][]byte{rowcodec.Wrap(key, 2, env.Codec, env.Flags, env.Payload), rowcodec.Wrap(key, 1, rowcodec.CodecRaw, env.Flags, env.Payload), rowcodec.Wrap(key, 1, env.Codec, 0, env.Payload), rowcodec.Wrap(key, 1, env.Codec, env.Flags, append(bytes.Clone(env.Payload), 0))} {
			_, e := decodeMQTTReclamationIndexProgress(key, bad)
			require.Error(t, e)
		}
	}
	for _, state := range []mqttReclamationIndexProgress{{}, {After: MQTTSessionCursor{Namespace: "main"}}} {
		_, e := encodeMQTTReclamationIndexProgress(key, state)
		require.Error(t, e)
	}
	body, e := json.Marshal(MQTTReadCursor{})
	require.NoError(t, e)
	require.NotContains(t, string(body), `"session":`)
}
