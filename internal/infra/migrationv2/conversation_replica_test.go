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

func TestOriginalConversationReplicaChoiceRebuildsAndVerifies(t *testing.T) {
	for _, archiveOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("archive_only_%t", archiveOnly), func(t *testing.T) { testOriginalConversationReplicaChoice(t, archiveOnly) })
	}
}

func testOriginalConversationReplicaChoice(t *testing.T, archiveOnly bool) {
	ctx := context.Background()
	r := migrationv2.Reader{}
	var sources []migration.NodeOptions
	var original migration.Row
	var leader uint64
	changed := false
	for node := uint64(1); node <= 3; node++ {
		dir := unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", node))
		clearFixtureMessageExtensions(t, dir)
		row, isLeader, _ := addConversationCopy(t, dir, node, "")
		original = row
		if isLeader {
			leader = node
		}
		if !isLeader && !changed {
			n := rewriteOriginalIndexFixture(t, dir, func(k, v []byte, b *pebble.Batch) bool {
				if len(k) != 22 || !bytes.Equal(k[:20], row.Key) || binary.BigEndian.Uint16(k[20:]) != 0x0906 {
					return false
				}
				value := make([]byte, 8)
				binary.BigEndian.PutUint64(value, binary.BigEndian.Uint64(v)+1)
				require.NoError(t, b.Set(k, value, nil))
				return true
			})
			require.Equal(t, 1, n)
			changed = true
		}
		sources = append(sources, migration.NodeOptions{NodeID: node, Options: migration.Options{DataDir: dir, ShardCount: 2}})
	}
	id, err := r.Identify(original)
	require.NoError(t, err)
	description, err := r.Describe(original, id)
	require.NoError(t, err)
	plan := diagnosticPlan(t, sources[0].DataDir)
	plan.Sources = sources
	plan.Metadata = conversationPolicy()
	open := func() *transfer.Spool {
		w, e := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
		require.NoError(t, e)
		t.Cleanup(func() { require.NoError(t, w.Close()) })
		return w
	}
	failed := open()
	failedReport, err := migration.Prepare(ctx, plan, failed, r, r, nil)
	require.ErrorContains(t, err, "source Conversation record conflicts")
	type candidate struct {
		SourceKey []byte `json:"source_key"`
		Digest    string `json:"digest"`
	}
	candidates := map[uint64]candidate{}
	originals := map[uint64]migration.Row{}
	hash := sha256.New()
	for node := uint64(1); node <= 3; node++ {
		key := []byte(fmt.Sprintf("candidate/metadata/%020d/Conversation/%s", node, description.Key))
		data, found, e := failed.Get(ctx, key)
		require.NoError(t, e)
		require.True(t, found)
		var c candidate
		require.NoError(t, migration.UnmarshalState(data, &c))
		candidates[node] = c
		raw, found, e := failed.Get(ctx, c.SourceKey)
		require.NoError(t, e)
		require.True(t, found)
		var row migration.Row
		require.NoError(t, json.Unmarshal(raw, &row))
		originals[node] = row
		fmt.Fprintf(hash, "%d %x %x\n", node, sha256.Sum256(data), sha256.Sum256(raw))
	}
	var chosen uint64
	for node, c := range candidates {
		if node != leader && c.Digest == candidates[leader].Digest {
			chosen = node
		}
	}
	require.NotZero(t, chosen)
	plan.Metadata.ConversationReplicas = []migration.ConversationReplicaRecovery{{LogicalKey: description.Key, SourceNodeID: chosen, CopiesSHA256: fmt.Sprintf("%x", hash.Sum(nil))}}
	if archiveOnly {
		plan.Metadata.ConversationReplicas[0].SourceNodeID = 0
		plan.Metadata.ConversationReplicas[0].ArchiveOnly = true
		hashText := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
		plan.Metadata.MissingConversations = []migration.MissingConversationRecovery{{CaptureDigest: failedReport.Capture.Digest, UIDSHA256: hashText(id.UID), ChannelSHA256: hashText(migration.IdentityKey(id.Channel.ID, id.Channel.Type)), RetainedTail: 3, Visibility: "hidden_until_new_message"}}
	}
	w := open()
	prepared, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	if archiveOnly {
		require.EqualValues(t, 1, prepared.Selection.Metadata.ReplicaRecovery.Archived)
		require.EqualValues(t, 1, prepared.Conversion.HiddenMemberships)
	} else {
		require.EqualValues(t, 1, prepared.Selection.Metadata.ReplicaRecovery.Retained)
	}
	selected := 0
	require.NoError(t, migration.WalkSelectedSources(ctx, w, func(rec migration.SelectedRecord) error {
		if rec.Row.Table == "Conversation" && rec.LogicalKey == description.Key {
			selected++
			require.Equal(t, chosen, rec.NodeID)
			require.Equal(t, originals[chosen], rec.Row)
		}
		return nil
	}))
	if archiveOnly {
		require.Zero(t, selected)
	} else {
		require.Equal(t, 1, selected)
	}
	archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
	require.NoError(t, err)
	_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
	require.NoError(t, err)
	fresh := open()
	rebuilt, err := migration.PrepareArchive(ctx, plan, fresh, r, archive)
	require.NoError(t, err)
	require.Equal(t, prepared.Selection, rebuilt.Selection)
	require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, fresh))
	verified, err := migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, "offline_verified", verified.Status)
}
