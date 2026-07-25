package backup_test

import (
	"context"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/stretchr/testify/require"
)

func TestCheckpointIndexIntegrityAuditRetentionSourceSelectsSparseFixedSet(
	t *testing.T,
) {
	ctx := context.Background()
	catalog, indexPath := newCheckpointIndexFixture(t)
	now := time.Date(2025, 7, 25, 12, 0, 0, 0, time.UTC)
	created := now.Add(-40 * 24 * time.Hour)
	first, err := catalog.Publish(
		ctx,
		catalogTestCheckpoint(
			"checkpoint-1", created.Add(5*time.Minute).UnixMilli(),
		),
		nil,
	)
	require.NoError(t, err)
	second, err := catalog.Publish(
		ctx,
		catalogTestCheckpoint(
			"checkpoint-2", created.Add(10*time.Minute).UnixMilli(),
		),
		&first.Head,
	)
	require.NoError(t, err)
	third, err := catalog.Publish(
		ctx,
		catalogTestCheckpoint(
			"checkpoint-3", created.Add(15*time.Minute).UnixMilli(),
		),
		&second.Head,
	)
	require.NoError(t, err)
	index, err := backupinfra.NewCheckpointCatalogIndex(catalog, indexPath)
	require.NoError(t, err)
	active := &mutableIntegrityAuditActiveRestoreSource{
		checkpointID: "checkpoint-1",
	}
	source, err :=
		backupinfra.NewCheckpointIndexIntegrityAuditRetentionSource(
			backupinfra.CheckpointIndexIntegrityAuditRetentionSourceOptions{
				Index: index, Policy: backupusecase.RetentionPolicy{},
				ActiveRestore: active,
			},
		)
	require.NoError(t, err)

	selection, err := source.LoadIntegrityAuditRetentionSelection(
		ctx,
		backupinfra.IntegrityAuditRetentionSelectionRequest{
			Head: third.Head, At: now,
		},
	)
	require.NoError(t, err)
	require.Equal(
		t, []string{"checkpoint-3", "checkpoint-1"},
		integrityAuditSelectionIDs(selection),
	)
	require.Len(t, selection.ID, 64)

	active.checkpointID = "checkpoint-2"
	fixedActive := selection.ActiveRestoreCheckpointID
	reloaded, err := source.LoadIntegrityAuditRetentionSelection(
		ctx,
		backupinfra.IntegrityAuditRetentionSelectionRequest{
			Head: third.Head, At: now,
			ActiveRestoreCheckpointID: &fixedActive,
		},
	)
	require.NoError(t, err)
	require.Equal(t, selection, reloaded)
}

type mutableIntegrityAuditActiveRestoreSource struct {
	checkpointID string
}

func (s *mutableIntegrityAuditActiveRestoreSource) ActiveRestoreCheckpointID(
	context.Context,
) (string, error) {
	return s.checkpointID, nil
}

func integrityAuditSelectionIDs(
	selection backupinfra.IntegrityAuditRetentionSelection,
) []string {
	ids := make([]string, len(selection.Checkpoints))
	for index, checkpoint := range selection.Checkpoints {
		ids[index] = checkpoint.ID
	}
	return ids
}
