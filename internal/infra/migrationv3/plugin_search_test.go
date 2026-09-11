package migrationv3

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func searchCheckpointFixture(t *testing.T) (context.Context, migration.TargetNode, *transfer.Spool) {
	t.Helper()
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "search-checkpoint-fixture", 16<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	for _, c := range []migration.TargetChannel{{Channel: migration.ChannelIdentity{ID: "group:search", Type: 2}, Count: 2, LastSeq: 2}, {Channel: migration.ChannelIdentity{ID: "a@b", Type: 1}, Count: 1, LastSeq: 1}, {Channel: migration.ChannelIdentity{ID: "empty", Type: 2}}} {
		data, err := migration.MarshalState(c)
		require.NoError(t, err)
		require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: []byte("target/channels/" + migration.IdentityKey(c.Channel.ID, c.Channel.Type)), Value: data}}))
	}
	return ctx, migration.TargetNode{NodeID: 101, DataDir: t.TempDir()}, w
}
func TestSearchCheckpointInstallResumeAndReadOnlyVerification(t *testing.T) {
	ctx, node, w := searchCheckpointFixture(t)
	require.NoError(t, installSearchCheckpoints(ctx, node, w))
	before, err := generationDigest(ctx, node.DataDir)
	require.NoError(t, err)
	require.NoError(t, migration.VerifySearchCheckpoints(ctx, w, searchCheckpointReader{node}))
	require.NoError(t, installSearchCheckpoints(ctx, node, w))
	after, err := generationDigest(ctx, node.DataDir)
	require.NoError(t, err)
	require.Equal(t, before, after)
	var count int
	require.NoError(t, (searchCheckpointReader{node}).WalkSearchCheckpoints(ctx, func(_ migration.ChannelIdentity, seq uint64) error { count++; require.Zero(t, seq); return nil }))
	require.Equal(t, 2, count)
}
func TestSearchCheckpointRejectsMissingAdvancedExtraAndStartedState(t *testing.T) {
	for _, fault := range []string{"missing", "advanced", "extra", "started", "symlink"} {
		t.Run(fault, func(t *testing.T) {
			ctx, node, w := searchCheckpointFixture(t)
			require.NoError(t, installSearchCheckpoints(ctx, node, w))
			sandbox := searchSandbox(node)
			switch fault {
			case "started":
				require.NoError(t, os.Mkdir(filepath.Join(sandbox, "message-v2.bleve"), 0700))
			case "symlink":
				require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(sandbox, "alternate")))
			default:
				db, err := pebble.Open(filepath.Join(sandbox, "db"), &pebble.Options{})
				require.NoError(t, err)
				key := searchCheckpointKey(migration.ChannelIdentity{ID: "group:search", Type: 2})
				if fault == "missing" {
					require.NoError(t, db.Delete(key, pebble.Sync))
				} else {
					var value [8]byte
					if fault == "advanced" {
						binary.BigEndian.PutUint64(value[:], 2)
					} else {
						key = searchCheckpointKey(migration.ChannelIdentity{ID: "unplanned", Type: 2})
					}
					require.NoError(t, db.Set(key, value[:], pebble.Sync))
				}
				require.NoError(t, db.Close())
			}
			require.Error(t, migration.VerifySearchCheckpoints(ctx, w, searchCheckpointReader{node}))
			require.Error(t, installSearchCheckpoints(ctx, node, w))
		})
	}
}
