package migrationv2_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func quarantinedMessageFixture(t *testing.T) (string, migration.Row) {
	t.Helper()
	source := compatibleMessageFixture(t)
	var original migration.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migration.Options{DataDir: source, ShardCount: 2}, func(r migration.Row) error {
		if r.Table == "Message" && r.Kind == migration.Primary && r.ID == 2 && string(r.Fields["ChannelId"]) == "migrationgroup" {
			original = r
		}
		return nil
	}))
	require.NotEmpty(t, original.Key)
	rewriteOriginalIndexFixture(t, source, func(k, v []byte, b *pebble.Batch) bool {
		if len(k) == 22 && bytes.Equal(k[:20], original.Key) && binary.BigEndian.Uint16(k[20:]) == 0x0109 {
			require.NoError(t, b.Set(k, []byte{0}, nil))
			return true
		}
		return false
	})
	original.Fields["ChannelType"] = []byte{0}
	return source, original
}

func quarantineBinding(t *testing.T, r migration.Row) migration.QuarantineRow {
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	return migration.QuarantineRow{NodeID: 1, Shard: r.Shard, Key: r.Key, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Reason: "invalid_message_channel"}
}

func TestQuarantineKeepsRawArchiveAndMapsMissingMiddlePosition(t *testing.T) {
	ctx := context.Background()
	source, row := quarantinedMessageFixture(t)
	before := fileDigests(t, source)
	plan := diagnosticPlan(t, source)
	plan.Metadata = conversationPolicy()
	plan.Messages = &migration.MessagePolicy{ExcludeCMD: true, CompactSequences: true}
	plan.Quarantine = []migration.QuarantineRow{quarantineBinding(t, row)}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "prepare"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	prepared, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, prepared.Conversion.Transformation.QuarantineDrops)
	require.EqualValues(t, 2, prepared.Conversion.Messages)
	require.EqualValues(t, 1, prepared.Selection.Quarantine.PhysicalRows["invalid_message_channel"])
	require.GreaterOrEqual(t, prepared.Selection.Quarantine.PhysicalRows["quarantined_primary_index"], uint64(3))
	var middle migration.MessageSequenceMapping
	require.NoError(t, migration.WalkMessageSequenceMappings(ctx, w, func(m migration.MessageSequenceMapping) error {
		if m.Channel.ID == "migrationgroup" && m.OriginalSeq == 2 {
			middle = m
		}
		return nil
	}))
	require.Equal(t, "quarantined_invalid_message_channel", middle.Omitted)
	require.Zero(t, middle.TargetSeq)
	require.EqualValues(t, 1, middle.BoundarySeq)
	state, err := migration.MarshalState(row)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(state)), middle.SourceSHA256)
	require.NoError(t, migrationv3.Install(ctx, plan.Target, prepared.Conversion, w))
	verified, err := migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, prepared.Conversion.Transformation, verified.Transformation)
	archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
	require.NoError(t, err)
	_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
	require.NoError(t, err)
	rawRowCount := 0
	_, err = migration.ReadSourceArchive(ctx, archive, func(s transfer.SpoolRow) error {
		if bytes.HasPrefix(s.Key, []byte("source/")) && bytes.Contains(s.Key, []byte("/rows/")) {
			var archived migration.Row
			require.NoError(t, json.Unmarshal(s.Value, &archived))
			if archived.Shard == row.Shard && bytes.Equal(archived.Key, row.Key) {
				require.Equal(t, row, archived)
				rawRowCount++
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, rawRowCount)
	fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "rebuild"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer fresh.Close()
	rebuilt, err := migration.PrepareArchive(ctx, plan, fresh, r, archive)
	require.NoError(t, err)
	require.Equal(t, prepared.Selection, rebuilt.Selection)
	require.Equal(t, prepared.Conversion, rebuilt.Conversion)
	require.Equal(t, before, fileDigests(t, source))
	// A different archived/captured row cannot use the earlier approval, even if
	// the selected target records are still byte-for-byte unchanged.
	row.Fields["Payload"] = []byte("altered private source")
	raw, err := json.Marshal(row)
	require.NoError(t, err)
	key := []byte(fmt.Sprintf("source/%020d/rows/%04d/%x", 1, row.Shard, row.Key))
	// A fresh transform over a source view with substituted bytes must reject it.
	_, err = migration.VerifyTargets(ctx, plan.Target, prepared.Selection, quarantineDriftWorkspace{Workspace: w, key: key, raw: raw}, r, migrationv3.Inspector{})
	require.ErrorContains(t, err, "quarantine original changed")
}

type quarantineDriftWorkspace struct {
	migration.Workspace
	key, raw []byte
}

func (w quarantineDriftWorkspace) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if bytes.Equal(key, w.key) {
		return w.raw, true, nil
	}
	return w.Workspace.Get(ctx, key)
}

func TestQuarantineRequiresExactAuthorizedMalformedRow(t *testing.T) {
	for _, mode := range []string{"missing-policy", "wrong-sha", "wrong-reason", "wrong-node", "duplicate", "healthy"} {
		t.Run(mode, func(t *testing.T) {
			source, row := quarantinedMessageFixture(t)
			plan := diagnosticPlan(t, source)
			plan.Messages = &migration.MessagePolicy{ExcludeCMD: true, CompactSequences: true}
			plan.Quarantine = []migration.QuarantineRow{quarantineBinding(t, row)}
			switch mode {
			case "missing-policy":
				plan.Quarantine = nil
			case "wrong-sha":
				plan.Quarantine[0].SHA256 = fmt.Sprintf("%064x", 0)
			case "wrong-reason":
				plan.Quarantine[0].Reason = "unresolved_allowlist_channel"
			case "wrong-node":
				plan.Quarantine[0].NodeID = 999
			case "duplicate":
				plan.Quarantine = append(plan.Quarantine, plan.Quarantine[0])
			case "healthy":
				require.NoError(t, migrationv2.Scan(context.Background(), plan.Sources[0].Options, func(r migration.Row) error {
					if r.Table == "Message" && r.Kind == migration.Primary && r.ID == 1 && r.Owner == row.Owner {
						plan.Quarantine[0] = quarantineBinding(t, r)
					}
					return nil
				}))
			}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			_, err = migration.Prepare(context.Background(), plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
			require.Error(t, err)
		})
	}
}

func TestQuarantineAllMessagesOfOwnerKeepsOriginalTailArchived(t *testing.T) {
	ctx := context.Background()
	source := compatibleMessageFixture(t)
	var row migration.Row
	require.NoError(t, migrationv2.Scan(ctx, migration.Options{DataDir: source, ShardCount: 2}, func(r migration.Row) error {
		if r.Table == "Message" && r.Kind == migration.Primary && bytes.HasSuffix(r.Fields["ChannelId"], []byte("____cmd")) {
			row = r
		}
		return nil
	}))
	require.NotEmpty(t, row.Key)
	rewriteOriginalIndexFixture(t, source, func(k, v []byte, b *pebble.Batch) bool {
		if len(k) == 22 && bytes.Equal(k[:20], row.Key) && binary.BigEndian.Uint16(k[20:]) == 0x0109 {
			require.NoError(t, b.Set(k, []byte{0}, nil))
			return true
		}
		return false
	})
	row.Fields["ChannelType"] = []byte{0}
	plan := diagnosticPlan(t, source)
	plan.Metadata = conversationPolicy()
	plan.Messages = &migration.MessagePolicy{ExcludeCMD: true, CompactSequences: true}
	plan.Quarantine = []migration.QuarantineRow{quarantineBinding(t, row)}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	prepared, err := migration.Prepare(ctx, plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, prepared.Selection.Quarantine.PhysicalRows["quarantined_owner_tail"])
	require.Empty(t, prepared.Selection.Quarantine.Positions)
	require.EqualValues(t, 3, prepared.Conversion.Messages)
	require.Zero(t, prepared.Conversion.Transformation.QuarantineDrops, "no fabricated channel or message mapping for an entirely quarantined owner")
	require.GreaterOrEqual(t, prepared.Conversion.Transformation.MaxSourceMessageID, binary.BigEndian.Uint64(row.Fields["MessageId"]))
}

func TestQuarantineReaderRejectsHealthyCMDAndResolvesExactIndexPointers(t *testing.T) {
	reader := migrationv2.Reader{}
	source := compatibleMessageFixture(t)
	var cmd migration.Row
	var allow migration.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migration.Options{DataDir: source, ShardCount: 2}, func(r migration.Row) error {
		if r.Kind == migration.Primary && r.Table == "Conversation" {
			cmd = r
		}
		if r.Kind == migration.Primary && r.Table == "Allowlist" {
			allow = r
		}
		return nil
	}))
	require.NotEmpty(t, cmd.Key)
	require.NotEmpty(t, allow.Key)
	// Derive a valid CMD conversation from the original column shape.
	cmd.Fields["ChannelId"] = append(bytes.Clone(cmd.Fields["ChannelId"]), []byte("____cmd")...)
	cmd.Fields["Type"] = []byte{1}
	_, err := reader.InspectQuarantine(cmd, "inconsistent_cmd_conversation")
	require.Error(t, err)
	cmd.Fields["Type"] = []byte{0}
	_, err = reader.InspectQuarantine(cmd, "inconsistent_cmd_conversation")
	require.NoError(t, err)
	f, err := reader.InspectQuarantine(allow, "unresolved_allowlist_channel")
	require.NoError(t, err)
	require.Equal(t, allow.Owner, f.UnresolvedChannel)
	// Original v2 timestamp and conversation secondary indexes have different
	// owner offsets. Both must address the exact captured aggregated primary.
	for _, tc := range []struct {
		table     string
		prefix    []byte
		ownerAt   int
		kind      migration.Kind
		owner, id uint64
	}{
		{"Message", []byte{1, 1, 2, 0, 1, 3}, 14, migration.Index, 11, 12},
		{"Allowlist", []byte{8, 1, 3, 0, 8, 1}, 6, migration.SecondaryIndex, 21, 22},
		{"Conversation", []byte{9, 1, 3, 0}, 4, migration.SecondaryIndex, 31, 32},
	} {
		key := make([]byte, 30)
		copy(key, tc.prefix)
		binary.BigEndian.PutUint64(key[tc.ownerAt:], tc.owner)
		binary.BigEndian.PutUint64(key[22:], tc.id)
		if tc.table == "Conversation" {
			key[12] = 9
			key[13] = 1
		}
		got, err := reader.QuarantineIndexPrimary(migration.Row{Table: tc.table, Kind: tc.kind, Key: key})
		require.NoError(t, err)
		require.Len(t, got, 20)
		require.EqualValues(t, migration.Primary, got[2])
		require.Equal(t, tc.owner, binary.BigEndian.Uint64(got[4:]))
		require.Equal(t, tc.id, binary.BigEndian.Uint64(got[12:]))
	}
}
