//go:build integration

package engine

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/cockroachdb/pebble/v2"
	"github.com/stretchr/testify/require"
)

// readBatchCommitStats uses the public method shape so the first regression run
// can fail on missing behavior before the engine-neutral DTO is implemented.
func readBatchCommitStats(t *testing.T, batch *Batch) (bool, map[string]time.Duration) {
	t.Helper()
	method := reflect.ValueOf(batch).MethodByName("CommitStats")
	require.True(t, method.IsValid(), "Batch.CommitStats is missing: native observations must be explicit")
	results := method.Call(nil)
	require.Len(t, results, 1)
	value := results[0]
	require.Equal(t, reflect.Struct, value.Kind())
	require.Equal(t, 8, value.NumField(), "only availability and the seven native fields belong in the DTO")
	available := value.FieldByName("Available")
	require.True(t, available.IsValid())
	require.Equal(t, reflect.Bool, available.Kind())
	out := make(map[string]time.Duration, 7)
	for _, name := range []string{"TotalDuration", "SemaphoreWaitDuration", "WALQueueWaitDuration", "MemTableWriteStallDuration", "L0ReadAmpWriteStallDuration", "WALRotationDuration", "CommitWaitDuration"} {
		field := value.FieldByName(name)
		require.True(t, field.IsValid(), "missing native field %s", name)
		require.Equal(t, reflect.TypeOf(time.Duration(0)), field.Type())
		out[name] = time.Duration(field.Int())
	}
	return available.Bool(), out
}

func requireNoBatchCommitStats(t *testing.T, batch *Batch) {
	t.Helper()
	available, values := readBatchCommitStats(t, batch)
	require.False(t, available)
	for name, value := range values {
		require.Zero(t, value, "%s retained a value without an available native commit", name)
	}
}

func requireExactBatchCommitStats(t *testing.T, batch *Batch) {
	t.Helper()
	available, values := readBatchCommitStats(t, batch)
	require.True(t, available)
	native := batch.batch.CommitStats()
	require.Equal(t, map[string]time.Duration{
		"TotalDuration":               native.TotalDuration,
		"SemaphoreWaitDuration":       native.SemaphoreWaitDuration,
		"WALQueueWaitDuration":        native.WALQueueWaitDuration,
		"MemTableWriteStallDuration":  native.MemTableWriteStallDuration,
		"L0ReadAmpWriteStallDuration": native.L0ReadAmpWriteStallDuration,
		"WALRotationDuration":         native.WALRotationDuration,
		"CommitWaitDuration":          native.CommitWaitDuration,
	}, values)
}

func TestBatchCommitStatsNativeRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name         string
		sync, sealed bool
	}{
		{name: "plain_sync", sync: true}, {name: "plain_nosync"}, {name: "sealed_sync", sync: true, sealed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			db, err := Open(dir, Options{})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			if test.sealed {
				_, err = db.ConfigureRecoverySeal([]byte("seal"), Span{Start: []byte("cert/"), End: []byte("cert0")})
				require.NoError(t, err)
			}
			batch := db.NewBatch()
			requireNoBatchCommitStats(t, batch)
			require.NoError(t, batch.Set([]byte("key"), []byte("value")))
			require.NoError(t, batch.Commit(test.sync))
			requireExactBatchCommitStats(t, batch)
			require.NoError(t, batch.Close())
			requireNoBatchCommitStats(t, batch)
			require.NoError(t, db.Close())
			db, err = Open(dir, Options{})
			require.NoError(t, err)
			value, found, err := db.Get([]byte("key"))
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, []byte("value"), value)
			if test.sealed {
				valid, err := db.ConfigureRecoverySeal([]byte("seal"), Span{Start: []byte("cert/"), End: []byte("cert0")})
				require.NoError(t, err)
				require.True(t, valid)
			}
		})
	}
}

func TestBatchCommitStatsUnavailableWithoutNativeWork(t *testing.T) {
	var nilBatch *Batch
	requireNoBatchCommitStats(t, nilBatch)
	require.ErrorIs(t, nilBatch.Commit(true), dberrors.ErrClosed)
	requireNoBatchCommitStats(t, &Batch{})
	db, err := Open(t.TempDir(), Options{})
	require.NoError(t, err)
	defer db.Close()
	batch := db.NewBatch()
	require.NoError(t, batch.Commit(true))
	requireNoBatchCommitStats(t, batch)
	require.NoError(t, batch.Close())
	require.ErrorIs(t, batch.Commit(true), dberrors.ErrClosed)
	requireNoBatchCommitStats(t, batch)
}

func TestBatchCommitStatsReadOnlyNativeRejection(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir, Options{})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = Open(dir, Options{ReadOnly: true})
	require.NoError(t, err)
	defer db.Close()
	batch := db.NewBatch()
	defer batch.Close()
	require.NoError(t, batch.Set([]byte("key"), []byte("value")))
	require.ErrorIs(t, batch.Commit(true), pebble.ErrReadOnly)
	requireNoBatchCommitStats(t, batch)
	_, found, err := db.Get([]byte("key"))
	require.NoError(t, err)
	require.False(t, found)
}

func TestBatchCommitStatsRecoverySealBoundaries(t *testing.T) {
	for _, mode := range []string{"invalid_certificate", "broken_seal", "post_native_mismatch"} {
		t.Run(mode, func(t *testing.T) {
			db, err := Open(t.TempDir(), Options{})
			require.NoError(t, err)
			defer db.Close()
			_, err = db.ConfigureRecoverySeal([]byte("seal"), Span{Start: []byte("cert/"), End: []byte("cert0")})
			require.NoError(t, err)
			batch := db.NewBatch()
			defer batch.Close()
			require.NoError(t, batch.Set([]byte("key"), []byte("value")))
			switch mode {
			case "invalid_certificate":
				batch.PreserveRecoveryCertificateAt([]byte("outside"), db.RecoveryEpoch())
			case "broken_seal":
				db.seal.broken = true
			case "post_native_mismatch":
				db.seal.next++
			}
			err = batch.Commit(true)
			require.Error(t, err)
			if mode == "post_native_mismatch" {
				require.True(t, errors.Is(err, errRecoverySealBroken))
				requireExactBatchCommitStats(t, batch)
				// The next attempt rejects before native work and must not reuse
				// the previous attempt's successful physical statistics.
				require.ErrorIs(t, batch.Commit(true), errRecoverySealBroken)
				requireNoBatchCommitStats(t, batch)
			} else {
				requireNoBatchCommitStats(t, batch)
			}
		})
	}
}

func TestBatchCommitStatsLargeFlushableKeepsNativeObservation(t *testing.T) {
	db, err := Open(t.TempDir(), Options{MemTableSize: 1 << 20})
	require.NoError(t, err)
	defer db.Close()
	batch := db.NewBatch()
	defer batch.Close()
	require.NoError(t, batch.Set([]byte("large"), bytes.Repeat([]byte("x"), 2<<20)))
	require.NoError(t, batch.Commit(true))
	require.True(t, batch.batch.Empty(), "fixture must exercise Pebble's detached flushable backing")
	requireExactBatchCommitStats(t, batch)
	value, found, err := db.Get([]byte("large"))
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, value, 2<<20)
}
