package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestMigrationCLIProcessesSyntheticCompatibleSource(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		name := "fresh_workspace"
		if reuse {
			name = "same_workspace"
		}
		t.Run(name, func(t *testing.T) { testMigrationCLIProcessesSyntheticCompatibleSource(t, reuse) })
	}
}

func testMigrationCLIProcessesSyntheticCompatibleSource(t *testing.T, reuse bool) {
	dir := t.TempDir()
	source := compatibleMessageFixture(t)
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "cli-fixture", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57881", DataDir: filepath.Join(dir, "target")}}}}
	data, err := json.Marshal(plan)
	require.NoError(t, err)
	// Binary-only deployments need no source revision. Exercise a genuinely
	// absent JSON key, then retry with the legacy explicit plan after preparation.
	var input map[string]any
	require.NoError(t, json.Unmarshal(data, &input))
	delete(input, "source_commit")
	data, err = json.Marshal(input)
	require.NoError(t, err)
	planPath := filepath.Join(dir, "plan.json")
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	var output, diagnostics bytes.Buffer
	run := func(args ...string) int {
		output.Reset()
		diagnostics.Reset()
		return migrationapp.RunWithBuild(context.Background(), args, &output, &diagnostics, dataformat.Build{Program: "wkcli", Version: "test-v3", Commit: "test-commit", BuildSource: "source"})
	}
	base := []string{"--plan", planPath, "--workspace", filepath.Join(dir, "workspace")}
	require.Equal(t, 0, run(append([]string{"prepare"}, base...)...), diagnostics.String())
	var prepared migration.Preflight
	require.NoError(t, json.Unmarshal(output.Bytes(), &prepared))
	require.Equal(t, "prepared", prepared.Status)
	require.False(t, prepared.CutoverReady)
	require.Equal(t, uint64(4), prepared.Conversion.Messages)
	require.Equal(t, migrationv2.SourceCommit, prepared.SourceCommit)
	// The normalized digest must let an existing explicit plan resume/export
	// the same generation, including archive-only import and verification.
	data, err = json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	require.Equal(t, 0, run(append(append([]string{"export"}, base...), "--archive", filepath.Join(dir, "archive"))...), diagnostics.String())
	_, err = os.Stat(filepath.Join(dir, "archive", "COMPLETE"))
	require.NoError(t, err)
	require.Equal(t, 0, run(append([]string{"prepare"}, base...)...), diagnostics.String())
	var resumed migration.Preflight
	require.NoError(t, json.Unmarshal(output.Bytes(), &resumed))
	require.Equal(t, prepared, resumed)
	// Portable imports support both a fresh target-host workspace and reuse of
	// the preparation workspace. Neither may require the source directories.
	require.NoError(t, os.Rename(source, source+"-unmounted"))
	portable := []string{"--plan", planPath, "--workspace", filepath.Join(dir, "import-workspace"), "--archive", filepath.Join(dir, "archive")}
	if reuse {
		portable[3] = filepath.Join(dir, "workspace")
	}
	require.Equal(t, 0, run(append([]string{"import"}, portable...)...), diagnostics.String())
	identity, err := dataformat.Inspect(plan.Target.Nodes[0].DataDir)
	require.NoError(t, err)
	require.Equal(t, "registered", identity.Status)
	require.Equal(t, "test-commit", identity.Metadata.CreatedBy.Commit)
	before, err := os.ReadFile(filepath.Join(plan.Target.Nodes[0].DataDir, dataformat.FileName))
	require.NoError(t, err)
	require.Equal(t, 0, run(append([]string{"import"}, portable...)...), diagnostics.String())
	after, err := os.ReadFile(filepath.Join(plan.Target.Nodes[0].DataDir, dataformat.FileName))
	require.NoError(t, err)
	require.Equal(t, before, after)
	if reuse {
		spool, err := transfer.OpenSpool(portable[3], plan.Digest(), 128<<20)
		require.NoError(t, err)
		defer spool.Close()
		for key, want := range map[string]migration.Preflight{
			"workflow/PREPARED": prepared,
			"workflow/ARCHIVE_PREPARED": func() migration.Preflight {
				copy := prepared
				copy.ArchiveSeal = nil
				return copy
			}(),
		} {
			raw, found, err := spool.Get(context.Background(), []byte(key))
			require.NoError(t, err)
			require.True(t, found)
			var got migration.Preflight
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, want, got, "independent preparation checkpoint %s", key)
		}
		require.NoError(t, spool.Close())
	}
	portable[3] = filepath.Join(dir, "verify-workspace")
	require.Equal(t, 0, run(append([]string{"verify"}, portable...)...), diagnostics.String())
	var verified migration.VerificationReport
	require.NoError(t, json.Unmarshal(output.Bytes(), &verified))
	require.Equal(t, "offline_verified", verified.Status)
	require.Equal(t, uint64(4), verified.Messages)
	// Creation identity belongs to the immutable migration checkpoint; removing
	// it cannot turn a new generation into an accepted legacy unregistered one.
	require.NoError(t, os.Remove(filepath.Join(plan.Target.Nodes[0].DataDir, dataformat.FileName)))
	require.NotZero(t, run(append([]string{"verify"}, portable...)...))
	require.Contains(t, diagnostics.String(), "changed")
	require.NoError(t, os.WriteFile(filepath.Join(plan.Target.Nodes[0].DataDir, dataformat.FileName), before, 0600))

	require.False(t, verified.CutoverReady)
	plan.SourceCommit = "0000000000000000000000000000000000000000"
	data, err = json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	require.NotZero(t, run(append([]string{"prepare"}, base...)...))
	require.Contains(t, diagnostics.String(), "source commit")
}
