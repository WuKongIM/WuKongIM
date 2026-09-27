//go:build integration

package engine_test

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/cockroachdb/pebble/v2"
	"github.com/stretchr/testify/require"
)

// A recovery seal must reject changes made by an older binary that knows
// nothing about checkpoint invalidation, even if it leaves applied indexes intact.
func TestRecoverySealDetectsUnawareWriter(t *testing.T) {
	path := t.TempDir()
	sealKey := []byte("seal")
	certificates := engine.Span{Start: []byte("cert/"), End: []byte("cert0")}
	db, err := engine.Open(path, engine.Options{})
	require.NoError(t, err)
	valid, err := db.ConfigureRecoverySeal(sealKey, certificates)
	require.NoError(t, err)
	require.False(t, valid)
	batch := db.NewBatch()
	require.NoError(t, batch.Set([]byte("user"), []byte("first")))
	require.NoError(t, batch.Set([]byte("cert/1"), []byte("proof")))
	batch.PreserveRecoveryCertificateAt([]byte("cert/1"), db.RecoveryEpoch())
	require.NoError(t, batch.Commit(true))
	require.NoError(t, batch.Close())
	require.NoError(t, db.Close())

	db, err = engine.Open(path, engine.Options{})
	require.NoError(t, err)
	valid, err = db.ConfigureRecoverySeal(sealKey, certificates)
	require.NoError(t, err)
	require.True(t, valid)
	require.NoError(t, db.Close())

	legacy, err := pebble.Open(path, &pebble.Options{})
	require.NoError(t, err)
	require.NoError(t, legacy.Set([]byte("user"), []byte("foreign-write"), pebble.Sync))
	require.NoError(t, legacy.Close())
	db, err = engine.Open(path, engine.Options{})
	require.NoError(t, err)
	defer db.Close()
	valid, err = db.ConfigureRecoverySeal(sealKey, certificates)
	require.NoError(t, err)
	require.False(t, valid, "a stale certificate survived an unaware writer")
}

func TestRecoverySealInvalidatesUncertifiedAndMixedBatches(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "mixed"}[mixed], func(t *testing.T) {
			path := t.TempDir()
			db, err := engine.Open(path, engine.Options{})
			require.NoError(t, err)
			_, err = db.ConfigureRecoverySeal([]byte("seal"), engine.Span{Start: []byte("cert/"), End: []byte("cert0")})
			require.NoError(t, err)
			b := db.NewBatch()
			require.NoError(t, b.Set([]byte("cert/1"), []byte("proof")))
			b.PreserveRecoveryCertificateAt([]byte("cert/1"), db.RecoveryEpoch())
			require.NoError(t, b.Commit(true))
			require.NoError(t, b.Close())
			b = db.NewBatch()
			if mixed {
				b.PreserveRecoveryCertificateAt([]byte("cert/1"), db.RecoveryEpoch())
				b.InvalidateRecoveryCertificates()
				require.NoError(t, b.Set([]byte("cert/2"), []byte("other")))
			}
			require.NoError(t, b.Set([]byte("user"), []byte("replacement")))
			require.NoError(t, b.Commit(true))
			require.NoError(t, b.Close())
			for _, key := range []string{"cert/1", "cert/2"} {
				_, found, err := db.Get([]byte(key))
				require.NoError(t, err)
				require.False(t, found)
			}
			require.NoError(t, db.Close())
			db, err = engine.Open(path, engine.Options{})
			require.NoError(t, err)
			defer db.Close()
			valid, err := db.ConfigureRecoverySeal([]byte("seal"), engine.Span{Start: []byte("cert/"), End: []byte("cert0")})
			require.NoError(t, err)
			require.True(t, valid, "invalidation is itself a sealed commit")
		})
	}
}

// An uncertified commit must also fence already-built proofs in running FSMs.
// Otherwise a subsequent business write could certify the untracked mutation.
func TestRecoverySealRejectsStaleLiveEpoch(t *testing.T) {
	db, err := engine.Open(t.TempDir(), engine.Options{})
	require.NoError(t, err)
	defer db.Close()
	_, err = db.ConfigureRecoverySeal([]byte("seal"), engine.Span{Start: []byte("cert/"), End: []byte("cert0")})
	require.NoError(t, err)
	epoch := db.RecoveryEpoch()
	stale := db.NewBatch()
	defer stale.Close()
	require.NoError(t, stale.Set([]byte("cert/1"), []byte("proof")))
	stale.PreserveRecoveryCertificateAt([]byte("cert/1"), epoch)
	ordinary := db.NewBatch()
	require.NoError(t, ordinary.Set([]byte("untracked"), []byte("mutation")))
	require.NoError(t, ordinary.Commit(true))
	require.NoError(t, ordinary.Close())
	require.Greater(t, db.RecoveryEpoch(), epoch)
	require.NoError(t, stale.Commit(true))
	_, found, err := db.Get([]byte("cert/1"))
	require.NoError(t, err)
	require.False(t, found, "stale in-memory proof certified an intervening mutation")
	fresh := db.NewBatch()
	defer fresh.Close()
	require.NoError(t, fresh.Set([]byte("cert/1"), []byte("new verified snapshot anchor")))
	fresh.PreserveRecoveryCertificateAt([]byte("cert/1"), db.RecoveryEpoch())
	require.NoError(t, fresh.Commit(true))
	_, found, err = db.Get([]byte("cert/1"))
	require.NoError(t, err)
	require.True(t, found)
}

// Pebble can retain a large batch as a flushable and clear the caller's batch
// representation after Commit. Verification must keep its assigned header.
func TestRecoverySealSurvivesFlushableBatch(t *testing.T) {
	path := t.TempDir()
	opts := engine.Options{MemTableSize: 1 << 20}
	db, err := engine.Open(path, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ConfigureRecoverySeal([]byte("seal"), engine.Span{Start: []byte("cert/"), End: []byte("cert0")})
	require.NoError(t, err)
	large := db.NewBatchWithSize(3 << 20)
	require.NoError(t, large.Set([]byte("large"), bytes.Repeat([]byte("x"), 2<<20)))
	require.NoError(t, large.Set([]byte("cert/1"), []byte("large proof")))
	large.PreserveRecoveryCertificateAt([]byte("cert/1"), db.RecoveryEpoch())
	require.NoError(t, large.Commit(true))
	require.NoError(t, large.Close())
	small := db.NewBatch()
	require.NoError(t, small.Set([]byte("cert/1"), []byte("next proof")))
	small.PreserveRecoveryCertificateAt([]byte("cert/1"), db.RecoveryEpoch())
	require.NoError(t, small.Commit(true))
	require.NoError(t, small.Close())
	require.NoError(t, db.Close())
	db, err = engine.Open(path, opts)
	require.NoError(t, err)
	defer db.Close()
	valid, err := db.ConfigureRecoverySeal([]byte("seal"), engine.Span{Start: []byte("cert/"), End: []byte("cert0")})
	require.NoError(t, err)
	require.True(t, valid)
	value, found, err := db.Get([]byte("large"))
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, value, 2<<20)
}
