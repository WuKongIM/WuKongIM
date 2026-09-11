package migrationv2_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixtureConversationDecision binds all original duplicates, including fields
// omitted from list-state comparison, to the exact indexed primary.
func fixtureConversationDecision(t *testing.T, dir string, original migration.Row) (migration.ConversationStateRecovery, migration.Row) {
	t.Helper()
	var rows []migration.Row
	r := migrationv2.Reader{}
	id, err := r.Identify(original)
	require.NoError(t, err)
	description, err := r.Describe(original, id)
	require.NoError(t, err)
	_, err = r.ReadStoppedNode(context.Background(), migration.NodeOptions{NodeID: 1, Options: migration.Options{DataDir: dir, ShardCount: 2}}, func(row migration.Row) error {
		if row.Table == "Conversation" && row.Kind == migration.Primary && bytes.Equal(row.Fields["Uid"], original.Fields["Uid"]) && bytes.Equal(row.Fields["ChannelId"], original.Fields["ChannelId"]) {
			rows = append(rows, row)
		}
		return nil
	}, nil)
	require.NoError(t, err)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	decision := migration.ConversationStateRecovery{NodeID: 1, LogicalKey: description.Key}
	h := sha256.New()
	var indexed migration.Row
	for _, row := range rows {
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		fmt.Fprintln(h, digest)
		if row.ID == original.ID {
			decision.IndexedSHA256 = digest
			indexed = row
		}
	}
	decision.RowsSHA256 = hex.EncodeToString(h.Sum(nil))
	require.NotEmpty(t, decision.IndexedSHA256)
	return decision, indexed
}

func TestConversationApprovedRecoveryArchiveAndNativeVerification(t *testing.T) {
	for _, mode := range []string{"list_state_conflict", "leader_tie"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			r := migrationv2.Reader{}
			dir := compatibleMessageFixture(t)
			original, _, _ := addConversationCopy(t, dir, 1, mode)
			decision, indexed := fixtureConversationDecision(t, dir, original)
			plan := diagnosticPlan(t, dir)
			plan.Metadata = conversationPolicy()
			plan.Metadata.ConversationListLimit = 1
			plan.Metadata.PreserveAllConversations = true
			plan.Metadata.ConversationRecoveries = []migration.ConversationStateRecovery{decision}
			open := func() *transfer.Spool {
				w, e := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
				require.NoError(t, e)
				t.Cleanup(func() { require.NoError(t, w.Close()) })
				return w
			}
			w := open()
			prepared, err := migration.Prepare(ctx, plan, w, r, r, nil)
			require.NoError(t, err)
			require.EqualValues(t, 1, prepared.Selection.Metadata.Policy.ConversationListLimit)
			report := prepared.Selection.Metadata.Conversations
			require.EqualValues(t, 1, report.RecoveredConflicts)
			require.Greater(t, report.UsersOverOriginalLimit, uint64(0))
			found := 0
			require.NoError(t, migration.WalkSelectedSources(ctx, w, func(rec migration.SelectedRecord) error {
				if rec.Row.Table == "Conversation" && bytes.Equal(rec.Row.Key, indexed.Key) {
					found++
					require.Equal(t, indexed.Fields, rec.Row.Fields)
				}
				return nil
			}))
			require.Equal(t, 1, found)
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
		})
	}
}

func TestConversationRecoveryRejectsChangedOrUnusedEvidence(t *testing.T) {
	for _, mode := range []string{"indexed_digest", "group_digest", "wrong_group", "unused_healthy_group"} {
		t.Run(mode, func(t *testing.T) {
			dir := compatibleMessageFixture(t)
			fixtureMode := "list_state_conflict"
			if mode == "unused_healthy_group" {
				fixtureMode = ""
			}
			original, _, _ := addConversationCopy(t, dir, 1, fixtureMode)
			decision, _ := fixtureConversationDecision(t, dir, original)
			if mode == "indexed_digest" {
				decision.IndexedSHA256 = strings.Repeat("0", 64)
			}
			if mode == "group_digest" {
				decision.RowsSHA256 = strings.Repeat("0", 64)
			}
			if mode == "wrong_group" {
				decision.LogicalKey += "changed"
			}
			plan := diagnosticPlan(t, dir)
			plan.Metadata = conversationPolicy()
			plan.Metadata.ConversationRecoveries = []migration.ConversationStateRecovery{decision}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			prepared, err := migration.Prepare(context.Background(), plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
			require.Error(t, err)
			require.Empty(t, prepared.Selection.Digest)
			if mode == "unused_healthy_group" {
				require.ErrorContains(t, err, "was not applied")
			} else if mode != "wrong_group" {
				require.ErrorContains(t, err, "differs from original row evidence")
			}
		})
	}
}
