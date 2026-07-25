package backup_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestCheckpointCatalogIndexProvidesStablePagesAcrossNewPublication(t *testing.T) {
	catalog, indexPath := newCheckpointIndexFixture(t)
	var head *backupartifact.CatalogPageReference
	for sequence := 1; sequence <= 3; sequence++ {
		id := "checkpoint-" + string(rune('0'+sequence))
		commit, err := catalog.Publish(context.Background(), catalogTestCheckpoint(id, 1_753_400_200_000+int64(sequence)*1_000), head)
		require.NoError(t, err)
		head = &commit.Head
	}
	index, err := backupinfra.NewCheckpointCatalogIndex(catalog, indexPath)
	require.NoError(t, err)

	first, err := index.List(context.Background(), *head, backupusecase.CheckpointListRequest{Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"checkpoint-3", "checkpoint-2"}, checkpointPageIDs(first))
	require.NotEmpty(t, first.NextCursor)
	require.Equal(t, 3, first.Total)

	commit, err := catalog.Publish(context.Background(), catalogTestCheckpoint("checkpoint-4", 1_753_400_204_000), head)
	require.NoError(t, err)
	head = &commit.Head
	second, err := index.List(context.Background(), *head, backupusecase.CheckpointListRequest{
		Limit: 2, Cursor: first.NextCursor,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"checkpoint-1"}, checkpointPageIDs(second))
	require.Empty(t, second.NextCursor)
}

func TestCheckpointCatalogIndexRebuildsDamagedDerivedIndex(t *testing.T) {
	catalog, indexPath := newCheckpointIndexFixture(t)
	commit, err := catalog.Publish(context.Background(), catalogTestCheckpoint("checkpoint-rebuild", 1_753_400_201_000), nil)
	require.NoError(t, err)
	index, err := backupinfra.NewCheckpointCatalogIndex(catalog, indexPath)
	require.NoError(t, err)
	_, err = index.List(context.Background(), commit.Head, backupusecase.CheckpointListRequest{Limit: 10})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(indexPath, []byte(`{"damaged":true}`), 0o600))

	restarted, err := backupinfra.NewCheckpointCatalogIndex(catalog, indexPath)
	require.NoError(t, err)
	page, err := restarted.List(context.Background(), commit.Head, backupusecase.CheckpointListRequest{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"checkpoint-rebuild"}, checkpointPageIDs(page))
	body, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	require.NotContains(t, string(body), "damaged")
}

func TestCheckpointCatalogIndexDoesNotTrustChecksummedInjectedRowsOnColdStart(t *testing.T) {
	catalog, indexPath := newCheckpointIndexFixture(t)
	commit, err := catalog.Publish(context.Background(), catalogTestCheckpoint("checkpoint-authentic", 1_753_400_201_000), nil)
	require.NoError(t, err)
	index, err := backupinfra.NewCheckpointCatalogIndex(catalog, indexPath)
	require.NoError(t, err)
	_, err = index.List(context.Background(), commit.Head, backupusecase.CheckpointListRequest{Limit: 10})
	require.NoError(t, err)

	body, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	var envelope struct {
		Payload json.RawMessage `json:"payload"`
		SHA256  string          `json:"sha256"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(envelope.Payload, &snapshot))
	entries := snapshot["entries"].([]any)
	forged := map[string]any{
		"id": "checkpoint-injected", "key": backupartifact.CheckpointObjectKey("checkpoint-injected"),
		"sha256": entries[0].(map[string]any)["sha256"], "bytes": float64(1024),
		"created_at_unix_millis":   float64(1_753_400_200_000),
		"effective_at_unix_millis": float64(1_753_400_199_000),
	}
	snapshot["entries"] = append(entries, forged)
	payload, err := json.Marshal(snapshot)
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	envelope.Payload = payload
	envelope.SHA256 = hex.EncodeToString(sum[:])
	body, err = json.Marshal(envelope)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(indexPath, body, 0o600))

	restarted, err := backupinfra.NewCheckpointCatalogIndex(catalog, indexPath)
	require.NoError(t, err)
	page, err := restarted.List(context.Background(), commit.Head, backupusecase.CheckpointListRequest{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"checkpoint-authentic"}, checkpointPageIDs(page))
}

func TestCheckpointCatalogIndexQueriesExactCheckpointID(t *testing.T) {
	catalog, indexPath := newCheckpointIndexFixture(t)
	commit, err := catalog.Publish(context.Background(), catalogTestCheckpoint("checkpoint-detail", 1_753_400_201_000), nil)
	require.NoError(t, err)
	index, err := backupinfra.NewCheckpointCatalogIndex(catalog, indexPath)
	require.NoError(t, err)

	detail, err := index.Get(context.Background(), commit.Head, "checkpoint-detail")
	require.NoError(t, err)
	require.Equal(t, "cluster-source", detail.SourceClusterID)
	require.Equal(t, uint16(2), detail.HashSlotCount)
	_, err = index.Get(context.Background(), commit.Head, "checkpoint-missing")
	require.ErrorIs(t, err, backupusecase.ErrCheckpointNotFound)
}

func TestCheckpointCatalogIndexReplaysSignedHoldAndReleaseState(t *testing.T) {
	catalog, indexPath := newCheckpointIndexFixture(t)
	first, err := catalog.Publish(
		context.Background(), catalogTestCheckpoint("checkpoint-old", 1_753_400_201_000), nil,
	)
	require.NoError(t, err)
	second, err := catalog.Publish(
		context.Background(), catalogTestCheckpoint("checkpoint-new", 1_753_400_202_000), &first.Head,
	)
	require.NoError(t, err)
	index, err := backupinfra.NewCheckpointCatalogIndex(catalog, indexPath)
	require.NoError(t, err)
	page, err := index.List(context.Background(), second.Head, backupusecase.CheckpointListRequest{Limit: 10})
	require.NoError(t, err)
	require.False(t, page.Items[1].Held)

	held, err := catalog.SetCheckpointHold(
		context.Background(), first.Checkpoint, true, 1_753_400_203_000, &second.Head,
	)
	require.NoError(t, err)
	page, err = index.List(context.Background(), held.Head, backupusecase.CheckpointListRequest{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"checkpoint-new", "checkpoint-old"}, checkpointPageIDs(page))
	require.False(t, page.Items[0].Held)
	require.True(t, page.Items[1].Held)
	detail, err := index.Get(context.Background(), held.Head, "checkpoint-old")
	require.NoError(t, err)
	require.True(t, detail.Held)

	released, err := catalog.SetCheckpointHold(
		context.Background(), held.Checkpoint, false, 1_753_400_204_000, &held.Head,
	)
	require.NoError(t, err)
	page, err = index.List(context.Background(), released.Head, backupusecase.CheckpointListRequest{Limit: 10})
	require.NoError(t, err)
	require.False(t, page.Items[1].Held)
}

func newCheckpointIndexFixture(t *testing.T) (*backupinfra.ReplicatedCheckpointCatalog, string) {
	t.Helper()
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, newCatalogTestSigner(), "signing-key")
	require.NoError(t, err)
	return catalog, filepath.Join(t.TempDir(), "checkpoint-index.json")
}

func checkpointPageIDs(page backupusecase.CheckpointPage) []string {
	ids := make([]string, len(page.Items))
	for index := range page.Items {
		ids[index] = page.Items[index].ID
	}
	return ids
}
