//go:build integration

package migrationv2_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"testing"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// TestOriginalSearchArchiveWorkflow requires the audited original executable.
// Its optional output directory retains native synthetic targets for Linux
// runtime acceptance; ordinary integration runs never download an artifact.
func TestOriginalSearchArchiveWorkflow(t *testing.T) {
	programPath := os.Getenv("WK_MIGRATION_SEARCH_PROGRAM")
	if programPath == "" {
		t.Skip("set WK_MIGRATION_SEARCH_PROGRAM to the audited Linux/amd64 executable")
	}
	program, err := os.ReadFile(programPath)
	require.NoError(t, err)
	require.Len(t, program, 63324496)
	require.Equal(t, "68079973350ec42480d84ee83770e5f7910fc06daeba41b746362e28492d939f", fmt.Sprintf("%x", sha256.Sum256(program)))
	out := os.Getenv("WK_MIGRATION_SEARCH_REHEARSAL_DIR")
	if out == "" {
		out = filepath.Join(t.TempDir(), "rehearsal")
	}
	require.NoError(t, os.Mkdir(out, 0700), "rehearsal output must be fresh")
	ctx, r := context.Background(), migrationv2.Reader{}
	p := diagnosticPlan(t, "")
	p.Sources, p.Target.Nodes = nil, nil
	p.Target.ClusterID = "search-archive-rehearsal-20260910"
	p.Target.SlotCount, p.Target.HashSlotCount = 12, 256
	p.Target.Replicas, p.Target.ChannelReplicas = 3, 3
	for n := 1; n <= 3; n++ {
		dir := unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", n))
		clearFixtureMessageExtensions(t, dir)
		// Only this synthetic derivative uses a JSON text payload suitable for
		// the business search plugin. Message IDs and sequences remain fixed.
		rewriteOriginalIndexFixture(t, dir, func(key, value []byte, b *pebble.Batch) bool {
			if len(key) == 22 && binary.BigEndian.Uint16(key) == 0x0101 && key[2] == 1 && binary.BigEndian.Uint16(key[20:]) == 0x010c {
				require.NoError(t, b.Set(key, []byte(`{"type":1,"content":"migration archive search needle20260910"}`), nil))
				return true
			}
			return false
		})
		db, err := pebble.Open(filepath.Join(dir, "db", "wukongimdb", "shard000"), &pebble.Options{ErrorIfNotExists: true})
		require.NoError(t, err)
		h := fnv.New64a()
		_, err = h.Write([]byte(migration.SearchPluginNo))
		require.NoError(t, err)
		for col, value := range map[uint16][]byte{0x1501: []byte(migration.SearchPluginNo), 0x1502: []byte("search"), 0x1506: binary.BigEndian.AppendUint32(nil, 0), 0x1507: []byte("0.0.1"), 0x1508: []byte(`["PersistAfter","Route"]`), 0x1509: binary.BigEndian.AppendUint32(nil, 1), 0x150a: []byte(`{}`)} {
			key := make([]byte, 14)
			binary.BigEndian.PutUint16(key, 0x1501)
			key[2] = byte(migration.Primary)
			binary.BigEndian.PutUint64(key[4:12], h.Sum64())
			binary.BigEndian.PutUint16(key[12:], col)
			require.NoError(t, db.Set(key, value, pebble.Sync))
		}
		require.NoError(t, db.Close())
		path := filepath.Join(t.TempDir(), "source-plugin")
		require.NoError(t, os.WriteFile(path, program, 0700))
		p.Sources = append(p.Sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
		p.Target.Nodes = append(p.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + n), Addr: fmt.Sprintf("127.0.0.1:%d", 19410+n), DataDir: filepath.Join(out, fmt.Sprintf("node%d", 100+n))})
		p.PluginNodes = append(p.PluginNodes, migration.PluginNodeMapping{SourceNode: uint64(n), TargetNode: uint64(100 + n)})
		p.PluginArtifacts = append(p.PluginArtifacts, migration.PluginArtifactSpec{SourceNode: uint64(n), PluginNo: migration.SearchPluginNo, Path: path, Bytes: int64(len(program)), SHA256: fmt.Sprintf("%x", sha256.Sum256(program)), Profile: migration.SearchPersistRouteProfile})
	}
	p.PluginConfigs = []migration.PluginConfigMapping{{PluginNo: migration.SearchPluginNo, SourceNode: 1}}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "prepare"), p.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	prepared, err := migration.Prepare(ctx, p, w, r, r, nil)
	require.NoError(t, err)
	require.Equal(t, "prepared", prepared.Status)
	archivePath := filepath.Join(out, "archive")
	archive, err := archivefs.NewFileArchiveStore(archivePath)
	require.NoError(t, err)
	_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: p.Digest(), SourceCommit: p.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
	require.NoError(t, err)
	for _, n := range p.Sources {
		require.NoError(t, os.Rename(n.DataDir, n.DataDir+"-unmounted"))
	}
	for _, a := range p.PluginArtifacts {
		require.NoError(t, os.Rename(a.Path, a.Path+"-unmounted"))
	}
	fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "import"), p.Digest(), 128<<20)
	require.NoError(t, err)
	defer fresh.Close()
	rebuilt, err := migration.PrepareArchive(ctx, p, fresh, r, archive)
	require.NoError(t, err)
	require.Equal(t, prepared.Selection, rebuilt.Selection)
	require.Equal(t, prepared.Conversion, rebuilt.Conversion)
	options := migrationv3.InstallOptions{PluginSettings: rebuilt.PluginSettings, PluginArtifacts: rebuilt.PluginArtifacts}
	require.NoError(t, migrationv3.Install(ctx, p.Target, rebuilt.Conversion, fresh, options))
	require.NoError(t, migrationv3.Install(ctx, p.Target, rebuilt.Conversion, fresh, options), "exact resume")
	planPath := filepath.Join(out, "plan.json")
	data, err := json.MarshalIndent(p, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	var output, diagnostics bytes.Buffer
	require.Equal(t, 0, migrationapp.Run(ctx, []string{"verify", "--plan", planPath, "--workspace", filepath.Join(t.TempDir(), "verify"), "--archive", archivePath}, &output, &diagnostics), diagnostics.String())
	var verified migration.VerificationReport
	require.NoError(t, json.Unmarshal(output.Bytes(), &verified))
	require.Equal(t, "offline_verified", verified.Status)
	require.False(t, verified.CutoverReady)
	require.NotNil(t, verified.PluginArtifacts)
	for _, n := range p.Target.Nodes {
		require.Equal(t, uint64(1), verified.PluginArtifacts.ByTarget[n.NodeID])
	}
	require.NoError(t, os.WriteFile(filepath.Join(out, "offline-verification.json"), output.Bytes(), 0600))
}
