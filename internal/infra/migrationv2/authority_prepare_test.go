package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
	"hash/crc32"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// appendFixtureTransition writes a private synthetic derivative: a complete
// original-format config command plus its applied frontier and materialized row.
// It never modifies a committed fixture or any production source directory.
func appendFixtureTransition(t *testing.T, source string, node uint64, historical bool) {
	t.Helper()
	r := migrationv2.Reader{}
	var cfg migration.Row
	snap, err := r.ReadStoppedNode(context.Background(), migration.NodeOptions{NodeID: node, Options: migration.Options{DataDir: source, ShardCount: 2}}, func(row migration.Row) error {
		if row.Table == "ChannelClusterConfig" && row.Kind == migration.Primary && string(row.Fields["ChannelId"]) == "migrationgroup" {
			cfg = row
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, cfg.Fields)
	slot := crc32.ChecksumIEEE(cfg.Fields["ChannelId"]) % snap.Config.SlotCount
	var index uint64
	for _, p := range snap.SlotProgress {
		if p.Group == strconv.Itoa(int(slot)) {
			index = p.LastIndex + 1
		}
	}
	require.Positive(t, index)
	leader := binary.BigEndian.Uint64(cfg.Fields["LeaderId"])
	u64 := func(n uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, n); return b }
	changes := map[uint16][]byte{}
	version := index
	if historical {
		version = 1732364897806218890
		changes[0x0b08], changes[0x0b09] = u64(leader), u64(leader)
	} else {
		learner := uint64(3)
		if learner == leader {
			learner = 1
		}
		var formal []byte
		for n := uint64(1); n <= 3; n++ {
			if n != learner {
				formal = append(formal, u64(n)...)
			}
		}
		changes[0x0b04], changes[0x0b05] = formal, u64(learner)
		changes[0x0b08], changes[0x0b09] = u64(learner), u64(learner)
	}
	changes[0x0b0b] = u64(version)
	names := map[uint16]string{0x0b04: "Replicas", 0x0b05: "Learners", 0x0b08: "MigrateFrom", 0x0b09: "MigrateTo", 0x0b0b: "ConfVersion"}
	for col, v := range changes {
		cfg.Fields[names[col]] = v
	}
	rewriteOriginalIndexFixture(t, source, func(k, v []byte, b *pebble.Batch) bool {
		if len(k) != 14 || !bytes.Equal(k[:12], cfg.Key) {
			return false
		}
		if replacement, ok := changes[binary.BigEndian.Uint16(k[12:])]; ok {
			require.NoError(t, b.Set(k, replacement, nil))
			return true
		}
		return false
	})
	// Encode original CMD v1, save-config type 28, embedded config v1.
	var b bytes.Buffer
	write := func(v any) { require.NoError(t, binary.Write(&b, binary.BigEndian, v)) }
	str := func(v []byte) { write(uint16(len(v))); b.Write(v) }
	write(uint16(1))
	write(uint16(28))
	str(cfg.Fields["ChannelId"])
	b.Write(cfg.Fields["ChannelType"])
	write(uint16(1))
	str(cfg.Fields["ChannelId"])
	b.Write(cfg.Fields["ChannelType"])
	b.Write(cfg.Fields["ReplicaMaxCount"])
	for _, name := range []string{"Replicas", "Learners"} {
		v := cfg.Fields[name]
		write(uint16(len(v) / 8))
		b.Write(v)
	}
	for _, name := range []string{"LeaderId", "Term", "MigrateFrom", "MigrateTo", "Status", "ConfVersion"} {
		b.Write(cfg.Fields[name])
	}
	for _, name := range []string{"CreatedAt", "UpdatedAt"} {
		v := cfg.Fields[name]
		if len(v) == 0 {
			v = u64(0)
		}
		b.Write(v)
	}
	raw := b.Bytes()
	hash := counterHash(strconv.Itoa(int(slot)))
	h := fnv.New32()
	h.Write([]byte(strconv.Itoa(int(slot))))
	shard := h.Sum32() % uint32(snap.SlotShardCount)
	db, err := pebble.Open(filepath.Join(source, "cluster", "logdb", fmt.Sprintf("shard%03d", shard)), &pebble.Options{ErrorIfNotExists: true})
	require.NoError(t, err)
	batch := db.NewBatch()
	it := db.NewIter(nil)
	var prior []byte
	for ok := it.First(); ok; ok = it.Next() {
		k, v := bytes.Clone(it.Key()), bytes.Clone(it.Value())
		if len(k) < 12 || binary.BigEndian.Uint64(k[4:12]) != hash {
			continue
		}
		switch binary.BigEndian.Uint16(k) {
		case 0x0101:
			if binary.BigEndian.Uint64(k[12:]) == index-1 {
				prior = v
			}
		case 0x0202, 0x0303:
			binary.BigEndian.PutUint64(v, index)
			require.NoError(t, batch.Set(k, v, nil))
		}
	}
	require.NoError(t, it.Close())
	require.NotEmpty(t, prior)
	key := make([]byte, 20)
	key[0], key[1] = 1, 1
	binary.BigEndian.PutUint64(key[4:], hash)
	binary.BigEndian.PutUint64(key[12:], index)
	value := append([]byte(nil), prior[:20]...)
	binary.BigEndian.PutUint64(value, index)
	binary.BigEndian.PutUint64(value[8:], index)
	value = append(value, raw...)
	value = append(value, prior[len(prior)-8:]...)
	require.NoError(t, batch.Set(key, value, nil))
	require.NoError(t, batch.Commit(pebble.Sync))
	require.NoError(t, batch.Close())
	require.NoError(t, db.Close())
}

func TestPrepareRebuildsTransitionProofFromRawArchiveCommands(t *testing.T) {
	for _, tc := range []struct {
		name       string
		historical bool
		targets    int
	}{
		{"supplement_to_single_node_cluster", false, 1},
		{"supplement_to_three_node_cluster", false, 3},
		{"historical_to_single_node_cluster", true, 1},
		{"historical_to_three_node_cluster", true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			historical := tc.historical
			ctx := context.Background()
			r := migrationv2.Reader{}
			var sources []migration.NodeOptions
			for n := 1; n <= 3; n++ {
				dir := unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", n))
				clearFixtureMessageExtensions(t, dir)
				appendFixtureTransition(t, dir, uint64(n), historical)
				sources = append(sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
			}
			plan := diagnosticPlan(t, sources[0].DataDir)
			plan.Sources = sources
			plan.Target.Replicas, plan.Target.ChannelReplicas = uint16(tc.targets), uint16(tc.targets)
			for n := 2; n <= tc.targets; n++ {
				plan.Target.Nodes = append(plan.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + n), Addr: fmt.Sprintf("127.0.0.1:%d", 57880+n), DataDir: filepath.Join(t.TempDir(), "target")})
			}
			before := map[string]map[string][32]byte{}
			for _, n := range sources {
				before[n.DataDir] = fileDigests(t, n.DataDir)
			}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "workspace"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			p, err := migration.Prepare(ctx, plan, w, r, r, nil)
			require.NoError(t, err)
			for _, mode := range []string{"missing", "changed", "misplaced", "no_evidence"} {
				t.Run(mode, func(t *testing.T) {
					broken, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "broken"), plan.Digest(), 128<<20)
					require.NoError(t, err)
					defer broken.Close()
					for _, prefix := range []string{"source/", "catalog/"} {
						copyCapturedFixtureRows(t, ctx, w, broken, []byte(prefix))
					}
					capture := p.Capture
					if mode == "no_evidence" {
						capture.Authority = nil
					}
					selected, err := migration.SelectSources(ctx, capture, p.Catalog, disruptedCommandWorkspace{Workspace: broken, mode: mode}, r, nil)
					require.Error(t, err)
					require.Empty(t, selected.Digest)
					rows := 0
					require.NoError(t, broken.Walk(ctx, []byte("selected/"), func(transfer.SpoolRow) error { rows++; return nil }))
					require.Zero(t, rows, "uncertified transitions must fail before selecting business rows")
				})
			}
			require.NotEmpty(t, p.Selection.AuthorityDigest)
			require.EqualValues(t, 3, p.Capture.MarkedConfigurations)
			require.Len(t, p.Capture.Authority, 3)
			marked := 0
			require.NoError(t, migration.WalkSelectedSources(ctx, w, func(row migration.SelectedRecord) error {
				if row.Row.Table == "ChannelClusterConfig" && row.Identity.Channel.ID == "migrationgroup" {
					marked++
					require.NotEqual(t, make([]byte, 8), row.Row.Fields["MigrateFrom"])
					_, err := r.Describe(row.Row, row.Identity)
					require.ErrorContains(t, err, "unfinished migration")
					if historical {
						require.EqualValues(t, 1732364897806218890, binary.BigEndian.Uint64(row.Row.Fields["ConfVersion"]))
					}
				}
				return nil
			}))
			require.Equal(t, 1, marked)
			archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
			require.NoError(t, err)
			_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, p.Capture, p.Catalog, p.Selection, w, archive)
			require.NoError(t, err)
			// Physically unmount the test sources: archive rebuilding must use raw
			// captured commands and rows, never the original paths or an old verdict.
			for _, n := range sources {
				require.Equal(t, before[n.DataDir], fileDigests(t, n.DataDir))
				require.NoError(t, os.Rename(n.DataDir, n.DataDir+"-unmounted"))
			}
			fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "fresh"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer fresh.Close()
			rebuilt, err := migration.PrepareArchive(ctx, plan, fresh, r, archive)
			require.NoError(t, err)
			require.Equal(t, p.Selection, rebuilt.Selection)
			require.Equal(t, p.Conversion, rebuilt.Conversion)
			require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, fresh))
			verified, err := migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
			require.NoError(t, err)
			require.Equal(t, "offline_verified", verified.Status)
		})
	}
}

// Disrupt archive reads without changing its bound inventory, exercising
// original-command completeness, content and placement validation.
type disruptedCommandWorkspace struct {
	migration.Workspace
	mode string
}

func (w disruptedCommandWorkspace) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	return w.Workspace.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		if bytes.Contains(row.Key, []byte("/config-commands/")) {
			if w.mode == "missing" {
				return nil
			}
			var command migration.RawConfigCommand
			if err := json.Unmarshal(row.Value, &command); err != nil {
				return err
			}
			if w.mode == "changed" {
				command.Data = append(command.Data, 0)
			}
			if w.mode == "misplaced" {
				command.Index++
			}
			var err error
			row.Value, err = json.Marshal(command)
			if err != nil {
				return err
			}
		}
		return visit(row)
	})
}

// copyCapturedFixtureRows preserves the complete private fixture with bounded
// synchronous batches. Per-row fsync in this setup dominated the corruption
// matrix, while the assertions exercise selection rather than copy durability.
func copyCapturedFixtureRows(t *testing.T, ctx context.Context, source, target *transfer.Spool, prefix []byte) {
	t.Helper()
	const maxRows = 256
	const maxBytes = 1 << 20
	rows := make([]transfer.SpoolRow, 0, maxRows)
	size := 0
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if err := target.Put(ctx, rows); err != nil {
			return err
		}
		clear(rows)
		rows = rows[:0]
		size = 0
		return nil
	}
	require.NoError(t, source.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		rowBytes := len(row.Key) + len(row.Value)
		if len(rows) == maxRows || size+rowBytes > maxBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		// An individually large record keeps the original Spool's own byte guard
		// without growing the fixture batch or retaining iterator-owned storage.
		if rowBytes > maxBytes {
			return target.Put(ctx, []transfer.SpoolRow{row})
		}
		rows = append(rows, transfer.SpoolRow{Key: bytes.Clone(row.Key), Value: bytes.Clone(row.Value)})
		size += rowBytes
		return nil
	}))
	require.NoError(t, flush())
}
