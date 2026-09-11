//go:build integration

package migrationv3

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestSearchCheckpointVerificationExcludesPluginWriter(t *testing.T) {
	ctx, node, w := searchCheckpointFixture(t)
	require.NoError(t, installSearchCheckpoints(ctx, node, w))
	attempt := func() error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSearchCheckpointWriterProcess$")
		cmd.Env = append(os.Environ(), "WK_SEARCH_CHECKPOINT_WRITER_DIR="+filepath.Join(searchSandbox(node), "db"))
		return cmd.Run()
	}
	before, err := generationDigest(ctx, node.DataDir)
	require.NoError(t, err)
	checked := false
	require.NoError(t, (searchCheckpointReader{node}).WalkSearchCheckpoints(ctx, func(_ migration.ChannelIdentity, _ uint64) error {
		if !checked {
			checked = true
			var exit *exec.ExitError
			require.ErrorAs(t, attempt(), &exit)
			require.Equal(t, 23, exit.ExitCode())
		}
		return nil
	}))
	require.True(t, checked)
	after, err := generationDigest(ctx, node.DataDir)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, attempt())
}
func TestSearchCheckpointWriterProcess(t *testing.T) {
	dir := os.Getenv("WK_SEARCH_CHECKPOINT_WRITER_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	db, err := pebble.Open(dir, &pebble.Options{ErrorIfNotExists: true})
	if err != nil {
		os.Exit(23)
	}
	if db.Close() != nil {
		os.Exit(24)
	}
	os.Exit(0)
}
