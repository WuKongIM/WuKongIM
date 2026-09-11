package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"testing"
	"time"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func counterHash(value string) uint64 { h := fnv.New64a(); h.Write([]byte(value)); return h.Sum64() }

// Mirror the original AddDenylist/RemoveAllDenylist writers: independent count
// columns survive without ChannelId/ChannelType. The actual permission uses
// its own UID index, even when GetChannel considers the channel body empty.
func channelCounterFixture(t *testing.T, owner string, withIndex bool) string {
	t.Helper()
	source := compatibleMessageFixture(t)
	for shard := 0; shard < 2; shard++ {
		db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", fmt.Sprintf("shard%03d", shard)), &pebble.Options{ErrorIfNotExists: true})
		require.NoError(t, err)
		for i, uid := range []string{"migrationalice", owner} {
			h := counterHash(uid + "-1")
			if int(h%2) != shard {
				continue
			}
			key := make([]byte, 14)
			copy(key, []byte{6, 1, 1, 0})
			binary.BigEndian.PutUint64(key[4:], h)
			binary.BigEndian.PutUint16(key[12:], 0x0609)
			val := make([]byte, 4)
			binary.BigEndian.PutUint32(val, uint32(i))
			require.NoError(t, db.Set(key, val, pebble.Sync))
			if i == 0 {
				continue
			}
			member := counterHash("migrationalice")
			pk := make([]byte, 22)
			copy(pk, []byte{7, 1, 1, 0})
			binary.BigEndian.PutUint64(pk[4:], h)
			binary.BigEndian.PutUint64(pk[12:], member)
			binary.BigEndian.PutUint16(pk[20:], 0x0701)
			require.NoError(t, db.Set(pk, []byte("migrationalice"), pebble.Sync))
			if withIndex {
				index := make([]byte, 22)
				copy(index, []byte{7, 1, 2, 0, 7, 1})
				binary.BigEndian.PutUint64(index[6:], h)
				binary.BigEndian.PutUint64(index[14:], member)
				value := make([]byte, 8)
				binary.BigEndian.PutUint64(value, member)
				require.NoError(t, db.Set(index, value, pebble.Sync))
			}
		}
		require.NoError(t, db.Close())
	}
	return source
}

func TestOriginalChannelCountersPreservePersonalDenylistWithoutCreatingChannelBody(t *testing.T) {
	source := channelCounterFixture(t, "migrationbob", true)
	// A proven empty administrative pair must not swallow sparse personal
	// counters or the real denylist addressed by an account identity.
	addEmptyChannelPair(t, source, 1, "")
	before := fileDigests(t, source)
	ctx := context.Background()
	root := t.TempDir()
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "counter-only-personal", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57881", DataDir: filepath.Join(root, "target")}}}}
	plan.Metadata = conversationPolicy()
	plan.Metadata.ArchiveEmptyChannels = true
	path := filepath.Join(root, "plan.json")
	data, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	var out, diagnostics bytes.Buffer
	run := func(args ...string) int {
		out.Reset()
		diagnostics.Reset()
		return migrationapp.Run(ctx, args, &out, &diagnostics)
	}
	base := []string{"--plan", path, "--workspace", filepath.Join(root, "workspace")}
	require.Zero(t, run(append([]string{"prepare"}, base...)...), diagnostics.String())
	var prepared migration.Preflight
	require.NoError(t, json.Unmarshal(out.Bytes(), &prepared))
	require.Equal(t, uint64(2), prepared.Selection.Preserved["derived_channel_counters"])
	require.Equal(t, uint64(2), prepared.Selection.Preserved["unreferenced_empty_channel_administration"])
	archive := filepath.Join(root, "archive")
	require.Zero(t, run(append(append([]string{"export"}, base...), "--archive", archive)...), diagnostics.String())
	require.NotContains(t, diagnostics.String(), "source index validation started", "export must reuse a sealed successful preparation instead of repeating semantic joins")
	require.Equal(t, before, fileDigests(t, source))
	require.NoError(t, os.Rename(source, source+"-unmounted"))
	portable := []string{"--plan", path, "--workspace", filepath.Join(root, "portable"), "--archive", archive}
	require.Zero(t, run(append([]string{"import"}, portable...)...), diagnostics.String())
	require.Zero(t, run(append([]string{"verify"}, portable...)...), diagnostics.String())
	// Inspect the portable source and converted permission independently of CLI counters.
	w, err := transfer.OpenSpool(filepath.Join(root, "portable"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	counters := 0
	require.NoError(t, w.Walk(ctx, []byte("source/"), func(r transfer.SpoolRow) error {
		var row migration.Row
		if err := json.Unmarshal(r.Value, &row); err == nil && row.Table == "ChannelInfo" && len(row.Fields) == 1 && len(row.Fields["DenylistCount"]) == 4 {
			counters++
		}
		return nil
	}))
	require.Equal(t, 2, counters)
	found := false
	require.NoError(t, migration.WalkTargetMetadata(ctx, w, func(r migration.TargetRecord) error {
		if r.Table == "channel" {
			var c meta.Channel
			require.NoError(t, migration.UnmarshalState(r.Value, &c))
			require.NotContains(t, []string{"migrationalice", "migrationbob"}, c.ChannelID, "a derived counter cannot create a channel body")
		}
		if r.Table == "subscriber" {
			var m meta.Subscriber
			require.NoError(t, migration.UnmarshalState(r.Value, &m))
			if m.ChannelID == "__wk_internal_memberlist__/deny/1/bWlncmF0aW9uYm9i" {
				require.Equal(t, "migrationalice", m.UID)
				require.Equal(t, int64(1), m.ChannelType)
				found = true
			}
		}
		return nil
	}))
	require.True(t, found, "personal denylist must survive the portable migration")
}

func TestOriginalChannelCountersDoNotHideMissingPermissionIdentityOrIndex(t *testing.T) {
	for _, tc := range []struct {
		name, owner string
		index       bool
		want        string
	}{
		{"unresolved-owner", "unknown-personal-owner", true, "unresolved source hash reference"},
		{"missing-permission-index", "migrationbob", false, "source business index is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := prepareIndexFixture(t, channelCounterFixture(t, tc.owner, tc.index))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestOriginalChannelCounterRecognitionRemainsNarrow(t *testing.T) {
	source := channelCounterFixture(t, "migrationbob", true)
	var original migration.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migration.Options{DataDir: source, ShardCount: 2}, func(r migration.Row) error {
		if r.Table == "ChannelInfo" && len(r.Fields) == 1 {
			original = r
		}
		return nil
	}))
	reader := migrationv2.Reader{}
	id, err := reader.Identify(original)
	require.NoError(t, err)
	require.Empty(t, id.Channel.ID)
	require.Zero(t, id.ChannelHash)
	d, err := reader.Describe(original, id)
	require.NoError(t, err)
	require.True(t, d.DerivedChannelCounters)
	_, err = reader.DecodeBusiness(original, id)
	require.Error(t, err, "a count is not a target channel body")
	for _, tc := range []struct {
		name   string
		change func(*migration.Row)
	}{
		{"policy-even-when-zero", func(r *migration.Row) { r.Fields["Ban"] = []byte{0} }},
		{"empty-channel-id", func(r *migration.Row) { r.Fields["ChannelId"] = []byte{} }},
		{"type-without-id", func(r *migration.Row) { r.Fields["ChannelType"] = []byte{1} }},
		{"timestamp-without-body", func(r *migration.Row) { r.Fields["CreatedAt"] = make([]byte, 8) }},
		{"short-counter", func(r *migration.Row) { r.Fields["DenylistCount"] = []byte{1} }},
		{"wrong-key", func(r *migration.Row) { r.Key[4] ^= 1 }},
		{"extra-value", func(r *migration.Row) { r.Value = []byte{1} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := original
			r.Key = bytes.Clone(r.Key)
			r.Fields = map[string][]byte{}
			for k, v := range original.Fields {
				r.Fields[k] = bytes.Clone(v)
			}
			tc.change(&r)
			_, err := reader.Identify(r)
			require.Error(t, err)
			d, err := reader.Describe(r, migration.RecordIdentity{})
			require.False(t, d.DerivedChannelCounters, "no malformed body may be archived as a derived count")
		})
	}
	wrongShard := original
	wrongShard.Shard = (wrongShard.Shard + 1) % 2
	_, err = reader.DescribeIndexes(wrongShard, id, 2)
	require.ErrorContains(t, err, "wrong shard")
}

type conflictingPersonalHintReader struct {
	migrationv2.Reader
	missingUID bool
}

func (r conflictingPersonalHintReader) Identify(row migration.Row) (migration.RecordIdentity, error) {
	id, err := r.Reader.Identify(row)
	if err == nil && row.Table == "User" && id.UID == "migrationalice" {
		id.UIDPersonalChannelHash = counterHash("migrationgroup-2")
		if r.missingUID {
			id.UID = ""
		}
	}
	return id, err
}
func TestPersonalPermissionHintsCannotOverwriteExplicitChannelIdentities(t *testing.T) {
	for _, missingUID := range []bool{false, true} {
		t.Run(fmt.Sprint(missingUID), func(t *testing.T) {
			ctx := context.Background()
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "personal-hint-collision", 128<<20)
			require.NoError(t, err)
			defer w.Close()
			capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: compatibleMessageFixture(t), ShardCount: 2}}}, migrationv2.Reader{}, w, nil)
			require.NoError(t, err)
			_, err = migration.BuildSourceCatalog(ctx, capture, w, conflictingPersonalHintReader{missingUID: missingUID})
			require.Error(t, err)
		})
	}
}
