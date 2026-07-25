package backup_test

import (
	"context"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestControllerCatalogAuditRootStoreWaitsForOlderAuditCycle(t *testing.T) {
	state := &erasureLedgerStateStore{state: backupcontract.State{
		Revision: 1,
		CatalogHead: &backupartifact.CatalogPageReference{
			Sequence: 5,
			Key: backupartifact.CatalogPageObjectKey(
				5, "checkpoint-5",
			),
			SHA256: strings.Repeat("a", 64), Bytes: 512,
			LatestCheckpointID: "checkpoint-5",
		},
		CatalogAuditRootSequence: 1,
		IntegrityAudit: backupcontract.IntegrityAuditState{
			Revision: 1,
			Cursor: &backupcontract.IntegrityAuditCursor{
				CycleID:         "catalog-segments-0001",
				CatalogSequence: 5, CatalogRootSequence: 1,
				Phase: backupcontract.IntegrityAuditPhaseInspect,
			},
		},
	}}
	store, err := backupinfra.NewControllerCatalogAuditRootStore(state)
	require.NoError(t, err)

	err = store.AdvanceCatalogAuditRoot(context.Background(), 3)
	require.ErrorIs(t, err, backupinfra.ErrCatalogAuditRootBusy)
	loaded, err := state.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), loaded.CatalogAuditRootSequence)

	state.mu.Lock()
	state.state.IntegrityAudit.Cursor.Phase =
		backupcontract.IntegrityAuditPhaseComplete
	state.mu.Unlock()
	require.NoError(t, store.AdvanceCatalogAuditRoot(
		context.Background(), 3,
	))
	loaded, err = state.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(3), loaded.CatalogAuditRootSequence)
	require.Equal(t, uint64(2), loaded.Revision)
	require.Error(t, store.AdvanceCatalogAuditRoot(
		context.Background(), 2,
	))
}
