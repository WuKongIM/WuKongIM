//go:build integration

package migrationv2_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/cockroachdb/pebble"
	pebble2 "github.com/cockroachdb/pebble/v2"
	"github.com/stretchr/testify/require"
)

func TestOriginalV2WriterCannotOpenSourceDuringScan(t *testing.T) {
	dir := unpackFixture(t)
	attempt := func() error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOriginalV2WriterProcess$")
		cmd.Env = append(os.Environ(), "WK_MIGRATION_TEST_WRITER_DIR="+filepath.Join(dir, "db", "wukongimdb", "shard000"))
		return cmd.Run()
	}
	checked := false
	require.NoError(t, migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: dir, ShardCount: 2}, func(migrationv2.Row) error {
		if !checked {
			checked = true
			var exit *exec.ExitError
			require.ErrorAs(t, attempt(), &exit)
			require.Equal(t, 23, exit.ExitCode())
		}
		return nil
	}))
	require.True(t, checked)
	require.NoError(t, attempt(), "the original writer can open again only after the scan releases all locks")
}

func TestPebble2WriterCannotOpenSourceDuringScan(t *testing.T) {
	dir := unpackFixture(t)
	rewriteFixtureWithPebble2(t, dir)
	attempt := func() error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOriginalV2WriterProcess$")
		cmd.Env = append(os.Environ(), "WK_MIGRATION_TEST_WRITER_DIR="+filepath.Join(dir, "db", "wukongimdb", "shard000"), "WK_MIGRATION_TEST_PEBBLE2=1")
		return cmd.Run()
	}
	checked := false
	before := fileDigests(t, dir)
	require.NoError(t, migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: dir, ShardCount: 2}, func(migrationv2.Row) error {
		if !checked {
			checked = true
			var exit *exec.ExitError
			require.ErrorAs(t, attempt(), &exit)
			require.Equal(t, 23, exit.ExitCode())
		}
		return nil
	}))
	require.True(t, checked)
	require.Equal(t, before, fileDigests(t, dir))
	require.NoError(t, attempt(), "the Pebble v2 writer can open only after source locks are released")
}

// A separate process is essential: original Pebble uses process-owned fcntl
// locks, so a goroutine pretending to be the source writer would be misleading.
func TestOriginalV2WriterProcess(t *testing.T) {
	dir := os.Getenv("WK_MIGRATION_TEST_WRITER_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	if os.Getenv("WK_MIGRATION_TEST_PEBBLE2") == "1" {
		db, err := pebble2.Open(dir, &pebble2.Options{ErrorIfNotExists: true})
		if err != nil {
			os.Exit(23)
		}
		if db.Close() != nil {
			os.Exit(24)
		}
		os.Exit(0)
	}
	db, err := pebble.Open(dir, &pebble.Options{ErrorIfNotExists: true})
	if err != nil {
		os.Exit(23)
	}
	if err := db.Close(); err != nil {
		os.Exit(24)
	}
	os.Exit(0)
}
