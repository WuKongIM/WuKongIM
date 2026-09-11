package migrationv2_test

import (
	"context"
	"encoding/binary"
	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestExpireArchiveRebuildPreservesOriginalLifetimeAndVerifiesNativeTargets(t *testing.T) {
	ctx, reader := context.Background(), migrationv2.Reader{}
	source := compatibleExpiryMessageFixture(t, ^uint32(0))
	plan := diagnosticPlan(t, source)
	open := func() *transfer.Spool {
		w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, w.Close()) })
		return w
	}
	w := open()
	prepared, err := migration.Prepare(ctx, plan, w, reader, reader, nil)
	require.NoError(t, err)
	archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
	require.NoError(t, err)
	_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
	require.NoError(t, err)
	require.NoError(t, os.Rename(source, source+"-unmounted"))
	rebuiltSpool := open()
	rebuilt, err := migration.PrepareArchive(ctx, plan, rebuiltSpool, reader, archive)
	require.NoError(t, err)
	require.Equal(t, prepared.Selection, rebuilt.Selection)
	count := 0
	require.NoError(t, migration.WalkSelectedSources(ctx, rebuiltSpool, func(rec migration.SelectedRecord) error {
		if rec.Row.Table == "Message" && rec.Row.Kind == migration.Primary {
			require.Equal(t, ^uint32(0), binary.BigEndian.Uint32(rec.Row.Fields["Expire"]))
			count++
		}
		return nil
	}))
	require.Equal(t, 4, count)
	require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, rebuiltSpool))
	verified, err := migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, rebuiltSpool, reader, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, "offline_verified", verified.Status)
}
