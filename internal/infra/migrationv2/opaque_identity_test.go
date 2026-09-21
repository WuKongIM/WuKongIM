package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// Original v2 column writers and FNV indexes accept string bytes verbatim.
// These two channel IDs would collapse to the same replacement character if
// any intermediate identity, comparison key or native row used ordinary JSON.
func opaqueConversationFixture(t *testing.T) (string, []string, []string) {
	t.Helper()
	source := compatibleMessageFixture(t)
	channels := []string{"opaque-\xff", "opaque-\xfe"}
	uids := []string{"migrationalice", "opaque-user-\xff"}
	var config migration.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migration.Options{DataDir: source, ShardCount: 2}, func(row migration.Row) error {
		if row.Table == "ChannelClusterConfig" && row.Kind == migration.Primary && config.Key == nil {
			config = row
		}
		return nil
	}))
	require.NotEmpty(t, config.Key)
	configColumns := []string{"ChannelId", "ChannelType", "ReplicaMaxCount", "Replicas", "Learners", "LeaderId", "Term", "MigrateFrom", "MigrateTo", "Status", "ConfVersion", "Version", "CreatedAt", "UpdatedAt"}
	for shard := 0; shard < 2; shard++ {
		db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", fmt.Sprintf("shard%03d", shard)), &pebble.Options{ErrorIfNotExists: true})
		require.NoError(t, err)
		put := func(prefix []byte, column uint16, value []byte) {
			key := make([]byte, len(prefix)+2)
			copy(key, prefix)
			binary.BigEndian.PutUint16(key[len(prefix):], column)
			require.NoError(t, db.Set(key, value, pebble.Sync))
		}
		for ci, channel := range channels {
			channelHash := counterHash(channel + "-1")
			if shard == 0 {
				prefix := make([]byte, 12)
				copy(prefix, []byte{11, 1, 1, 0})
				binary.BigEndian.PutUint64(prefix[4:], channelHash)
				for col, name := range configColumns {
					value, exists := config.Fields[name]
					if name == "ChannelId" {
						value, exists = []byte(channel), true
					}
					if name == "ChannelType" {
						value, exists = []byte{1}, true
					}
					if exists {
						put(prefix, uint16(0x0b01+col), value)
					}
				}
			}
			for ui, uid := range uids {
				h := fnv.New32()
				_, _ = h.Write([]byte(uid))
				if int(h.Sum32()%2) != shard {
					continue
				}
				owner, id := counterHash(uid), uint64(900000+ci*10+ui)
				prefix := make([]byte, 20)
				copy(prefix, []byte{9, 1, 1, 0})
				binary.BigEndian.PutUint64(prefix[4:], owner)
				binary.BigEndian.PutUint64(prefix[12:], id)
				fields := [][]byte{[]byte(uid), []byte(channel), {1}, {0}, make([]byte, 4), make([]byte, 8), make([]byte, 8), make([]byte, 8), make([]byte, 8)}
				for col, value := range fields {
					put(prefix, uint16(0x0901+col), value)
				}
				index := make([]byte, 22)
				copy(index, []byte{9, 1, 2, 0, 9, 1})
				binary.BigEndian.PutUint64(index[6:], owner)
				binary.BigEndian.PutUint64(index[14:], channelHash)
				value := make([]byte, 8)
				binary.BigEndian.PutUint64(value, id)
				require.NoError(t, db.Set(index, value, pebble.Sync))
			}
		}
		require.NoError(t, db.Close())
	}
	return source, channels, uids
}

func TestOpaqueConversationIdentitiesSurvivePortableMigrationAndNativeRecovery(t *testing.T) {
	source, channels, uids := opaqueConversationFixture(t)
	before := fileDigests(t, source)
	root, ctx := t.TempDir(), context.Background()
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, Exclusions: &migration.Exclusions{LegacyStreamStorage: true}, Target: migration.TargetPlan{ClusterID: "opaque-identities", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57881", DataDir: filepath.Join(root, "target")}}}}
	data, err := json.Marshal(plan)
	require.NoError(t, err)
	planPath := filepath.Join(root, "plan.json")
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	var output, diagnostics bytes.Buffer
	run := func(args ...string) {
		t.Helper()
		output.Reset()
		diagnostics.Reset()
		require.Zero(t, migrationapp.Run(ctx, args, &output, &diagnostics), diagnostics.String())
	}
	base := []string{"--plan", planPath, "--workspace", filepath.Join(root, "workspace")}
	run(append([]string{"prepare"}, base...)...)
	archive := filepath.Join(root, "archive")
	run(append(append([]string{"export"}, base...), "--archive", archive)...)
	require.Equal(t, before, fileDigests(t, source))
	require.NoError(t, os.Rename(source, source+"-unmounted"))
	portable := []string{"--plan", planPath, "--workspace", filepath.Join(root, "portable"), "--archive", archive}
	run(append([]string{"import"}, portable...)...)
	run(append([]string{"verify"}, portable...)...)
	var verified migration.VerificationReport
	require.NoError(t, json.Unmarshal(output.Bytes(), &verified))
	require.Equal(t, "offline_verified", verified.Status)
	require.False(t, verified.CutoverReady)
	w, err := transfer.OpenSpool(filepath.Join(root, "portable"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	// This workspace was reconstructed from the archive, without source capture.
	preparedBytes, found, err := w.Get(ctx, []byte("workflow/ARCHIVE_PREPARED"))
	require.NoError(t, err)
	require.True(t, found)
	var prepared migration.Preflight
	require.NoError(t, json.Unmarshal(preparedBytes, &prepared))
	_, err = migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, migrationv2.Reader{}, alteredOpaqueInspector{})
	require.ErrorContains(t, err, "business fields differ", "different invalid bytes must never compare equal")
	require.NoError(t, w.Close())
	readMemberships := func(db *meta.MetaDB) {
		t.Helper()
		for _, uid := range uids {
			s := db.HashSlot(uint16(crc32.ChecksumIEEE([]byte(uid)) % 256))
			for _, channel := range channels {
				row, found, err := s.GetUserChannelMembership(ctx, uid, channel, 1)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, uid, row.UID)
				require.Equal(t, channel, row.ChannelID)
				require.Equal(t, uint64(1), row.JoinSeq)
				require.Zero(t, row.ReadSeq)
				require.Zero(t, row.ActivatedAt)
				_, found, err = s.GetUserChannelMembership(ctx, uid, strings.ToValidUTF8(channel, "\ufffd"), 1)
				require.NoError(t, err)
				require.False(t, found, "no replacement-character alias may be created")
			}
		}
	}
	path := filepath.Join(plan.Target.Nodes[0].DataDir, "slotmeta")
	// Reopen the installed native database, then recover its actual snapshot
	// into a separate database using the same portable format as Slot recovery.
	db, err := meta.Open(path)
	require.NoError(t, err)
	readMemberships(db.MetaDB())
	var hashes []uint16
	for slot := uint16(0); slot < 256; slot++ {
		hashes = append(hashes, slot)
	}
	reader, err := db.MetaDB().OpenHashSlotSnapshot(ctx, hashes)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.NoError(t, db.Close())
	recovered, err := meta.Open(filepath.Join(root, "recovered"))
	require.NoError(t, err)
	require.NoError(t, recovered.MetaDB().ImportHashSlotSnapshotReader(ctx, hashes, bytes.NewReader(body), int64(len(body))))
	readMemberships(recovered.MetaDB())
	require.NoError(t, recovered.Close())
	db, err = meta.Open(path)
	require.NoError(t, err)
	readMemberships(db.MetaDB())
	require.NoError(t, db.Close())
}

// Simulate a target decoder returning another invalid byte at the same key.
// Ordinary json.Marshal would turn both values into the same replacement rune.
type alteredOpaqueInspector struct{ migrationv3.Inspector }

func (i alteredOpaqueInspector) Open(ctx context.Context, plan migration.TargetPlan, node migration.TargetNode) (migration.TargetView, error) {
	v, err := i.Inspector.Open(ctx, plan, node)
	if err != nil {
		return nil, err
	}
	return alteredOpaqueView{v}, nil
}

type alteredOpaqueView struct{ migration.TargetView }

func (v alteredOpaqueView) Metadata(ctx context.Context, table, owner string, key map[string]any) (map[string]any, bool, error) {
	row, found, err := v.TargetView.Metadata(ctx, table, owner, key)
	if err == nil && found && row["channel_id"] == "opaque-\xff" {
		row["channel_id"] = "opaque-\xfe"
	}
	return row, found, err
}
