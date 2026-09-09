package migrationv2_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/cockroachdb/pebble"
	pebble2 "github.com/cockroachdb/pebble/v2"
	"github.com/stretchr/testify/require"
)

// rewriteFixtureWithPebble2 changes only disposable fixture engine files. It
// copies every raw key/value, so business and Raft comparisons remain exact.
func rewriteFixtureWithPebble2(t *testing.T, root string) {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Name() == "LOCK" {
			paths = append(paths, filepath.Dir(path))
		}
		return err
	}))
	for _, path := range paths {
		old, err := pebble.Open(path, &pebble.Options{ReadOnly: true, ErrorIfNotExists: true})
		require.NoError(t, err)
		fresh := path + "-format19"
		db, err := pebble2.Open(fresh, &pebble2.Options{FormatMajorVersion: 19})
		require.NoError(t, err)
		iter := old.NewIter(nil)
		for ok := iter.First(); ok; ok = iter.Next() {
			require.NoError(t, db.Set(iter.Key(), iter.Value(), pebble2.NoSync))
		}
		require.NoError(t, iter.Error())
		require.NoError(t, iter.Close())
		require.NoError(t, old.Close())
		require.NoError(t, db.Close())
		require.NoError(t, os.RemoveAll(path))
		require.NoError(t, os.Rename(fresh, path))
	}
}

func TestScanPebble2PreservesRowsAndSourceFiles(t *testing.T) {
	dir := unpackFixture(t)
	scan := func() []migrationv2.Row {
		var rows []migrationv2.Row
		require.NoError(t, migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: dir, ShardCount: 2}, func(row migrationv2.Row) error { rows = append(rows, row); return nil }))
		return rows
	}
	want := scan()
	rewriteFixtureWithPebble2(t, dir)
	before := fileDigests(t, dir)
	require.Equal(t, want, scan())
	require.Equal(t, before, fileDigests(t, dir))
}

func TestStoppedPebble2PreservesBusinessAndRaftEvidence(t *testing.T) {
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	scan := func() (migrationv2.NodeSnapshot, []migrationv2.Row) {
		var rows []migrationv2.Row
		snapshot, err := migrationv2.ReadStoppedNode(context.Background(), migrationv2.NodeOptions{NodeID: 1, Options: migrationv2.Options{DataDir: dir, ShardCount: 2}}, func(row migrationv2.Row) error { rows = append(rows, row); return nil }, nil)
		require.NoError(t, err)
		return snapshot, rows
	}
	want, rows := scan()
	rewriteFixtureWithPebble2(t, dir)
	before := fileDigests(t, dir)
	got, actual := scan()
	require.Equal(t, rows, actual)
	require.Equal(t, want.Config, got.Config)
	require.Equal(t, want.ConfigProgress, got.ConfigProgress)
	require.Equal(t, want.SlotProgress, got.SlotProgress)
	require.Equal(t, want.NotificationDepth, got.NotificationDepth)
	require.Equal(t, before, fileDigests(t, dir))
}

func TestScanRejectsUnauditedPebbleFormatWithoutChangingSource(t *testing.T) {
	dir := unpackFixture(t)
	rewriteFixtureWithPebble2(t, dir)
	db, err := pebble2.Open(filepath.Join(dir, "db", "wukongimdb", "shard000"), &pebble2.Options{FormatMajorVersion: 20})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	before := fileDigests(t, dir)
	visited := false
	err = migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: dir, ShardCount: 2}, func(migrationv2.Row) error { visited = true; return nil })
	require.ErrorContains(t, err, "format major version 20")
	require.False(t, visited)
	require.Equal(t, before, fileDigests(t, dir))
}

func TestStoppedNodeCanReadMixedEngineDirectories(t *testing.T) {
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	read := func() migrationv2.NodeSnapshot {
		snapshot, err := migrationv2.ReadStoppedNode(context.Background(), migrationv2.NodeOptions{NodeID: 1, Options: migrationv2.Options{DataDir: dir, ShardCount: 2}}, func(migrationv2.Row) error { return nil }, nil)
		require.NoError(t, err)
		return snapshot
	}
	want := read()
	rewriteFixtureWithPebble2(t, filepath.Join(dir, "db", "wukongimdb", "shard000"))
	rewriteFixtureWithPebble2(t, filepath.Join(dir, "cluster", "config", "cfglogdb"))
	before := fileDigests(t, dir)
	got := read()
	require.Equal(t, want.RowCount, got.RowCount)
	require.Equal(t, want.ConfigProgress, got.ConfigProgress)
	require.Equal(t, want.SlotProgress, got.SlotProgress)
	require.Equal(t, before, fileDigests(t, dir))
}
