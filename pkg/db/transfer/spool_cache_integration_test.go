//go:build integration

package transfer

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the migration's real read-while-writing pattern after memtables
// reach their configured size. A read-only reopen would release the cache
// reservations and hide the starvation this regression guards against.
func TestMigrationSpoolCachesReadsWhileWritingIndexes(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"), "cache-regression", 8<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	value := bytes.Repeat([]byte("source-message"), 320)
	for batch := 0; batch < 16; batch++ {
		rows := make([]SpoolRow, 1024)
		for i := range rows {
			rows[i] = SpoolRow{Key: []byte(fmt.Sprintf("source/%08d", batch*1024+i)), Value: value}
		}
		require.NoError(t, s.Put(ctx, rows))
	}
	require.GreaterOrEqual(t, s.db.MetricsSnapshot().MemTableSizeBytes, uint64(16<<20), "fixture must reserve a full-sized memtable")
	visited := 0
	require.NoError(t, s.Walk(ctx, []byte("source/"), func(row SpoolRow) error {
		if visited >= 256 {
			return nil
		}
		visited++
		index := SpoolRow{Key: []byte(fmt.Sprintf("index/%08d", visited)), Value: row.Key}
		if err := s.Put(ctx, []SpoolRow{index}); err != nil {
			return err
		}
		// Warm the exact persisted source block before counting repeated reads.
		_, found, err := s.Get(ctx, row.Key)
		require.NoError(t, err)
		require.True(t, found)
		before := s.db.MetricsSnapshot()
		for repeat := 0; repeat < 8; repeat++ {
			got, found, err := s.Get(ctx, row.Key)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, value, got)
		}
		after := s.db.MetricsSnapshot()
		hits, misses := after.BlockCacheHits-before.BlockCacheHits, after.BlockCacheMisses-before.BlockCacheMisses
		if visited == 1 {
			t.Logf("repeated source reads during index writes: hits=%d misses=%d", hits, misses)
		}
		require.Greater(t, hits, misses, "hot source blocks must remain cached during index writes; hits=%d misses=%d", hits, misses)
		return nil
	}))
	require.Equal(t, 256, visited)
}
