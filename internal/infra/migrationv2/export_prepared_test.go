package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

type exportDriftWorkspace struct {
	migration.Workspace
	report  []byte
	prefix  string
	changed bool
}

func (w *exportDriftWorkspace) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if w.report != nil && bytes.Equal(key, []byte("workflow/PREPARED")) {
		return w.report, true, nil
	}
	return w.Workspace.Get(ctx, key)
}

func (w *exportDriftWorkspace) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	err := w.Workspace.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		if !w.changed && w.prefix != "" && string(prefix) == w.prefix {
			row.Value = append(bytes.Clone(row.Value), ' ')
			w.changed = true
		}
		return visit(row)
	})
	if err == nil && !w.changed && w.prefix != "" && string(prefix) == w.prefix {
		w.changed = true
		return visit(transfer.SpoolRow{Key: append(bytes.Clone(prefix), []byte("unexpected")...), Value: []byte("injected")})
	}
	return err
}

func TestPreparedExportRejectsChangedSourceReportAndPublishedRows(t *testing.T) {
	for _, mode := range []string{"source_file", "report", "missing_seal", "source/", "catalog/", "selected/", "plugin-artifacts/"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			source := compatibleMessageFixture(t)
			plan := diagnosticPlan(t, source)
			plan.Metadata = conversationPolicy()
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "work"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			r := migrationv2.Reader{}
			prepared, err := migration.Prepare(ctx, plan, w, r, r, nil)
			require.NoError(t, err)
			require.NotNil(t, prepared.ArchiveSeal)
			drift := &exportDriftWorkspace{Workspace: w}
			switch mode {
			case "source_file":
				require.NoError(t, os.WriteFile(filepath.Join(source, "new-unbound-file"), []byte("new source"), 0600))
			case "report":
				prepared.Conversion.Metadata["forged"] = 1
				drift.report, err = json.Marshal(prepared)
				require.NoError(t, err)
			case "missing_seal":
				prepared.ArchiveSeal = nil
				drift.report, err = json.Marshal(prepared)
				require.NoError(t, err)
			default:
				drift.prefix = mode
			}
			root := filepath.Join(t.TempDir(), "archive")
			archive, err := archivefs.NewFileArchiveStore(root)
			require.NoError(t, err)
			_, err = migration.ExportPreparedArchive(ctx, plan, drift, r, archive, nil)
			require.Error(t, err)
			if drift.prefix != "" {
				require.ErrorContains(t, err, "workspace checksum mismatch")
			}
			_, err = os.Stat(filepath.Join(root, "COMPLETE"))
			require.True(t, os.IsNotExist(err), "failed export must never publish completion")
		})
	}
}
