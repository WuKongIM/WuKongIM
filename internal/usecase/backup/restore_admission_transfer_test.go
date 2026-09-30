package backup_test

import (
	"context"
	"errors"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/stretchr/testify/require"
)

// A returned admission receipt must reflect one atomic job/lease transfer.
// Cleanup cannot downgrade known admission or reclassify an unknown commit.
func TestRestoreAdmissionTransfersExactArchiveLease(t *testing.T) {
	for _, mode := range []string{"complete", "cleanup-unavailable", "newer-history", "lease-missing", "lease-token", "lease-kind", "lease-archive", "lease-node", "lease-term", "lease-time", "expired", "preflight-failed", "canceled", "rejected-admit", "unknown-admit-unapplied", "unknown-admit-applied", "unknown-admit-applied-foreign", "unknown-acquire-applied"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
			archive, err := backupinfra.NewFileArchiveStore(t.TempDir())
			require.NoError(t, err)
			writeCatalogArchive(t, archive, "transfer-archive", true, now.UnixMilli())
			memory := &memoryScheduledStateStore{state: backupcontract.SystemState{Revision: 7, Plan: &backupcontract.Plan{Revision: 3, Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile}}}}
			unknown := errors.New("restore admission result unavailable")
			store := &restoreTransferStore{memoryScheduledStateStore: memory, mode: mode, unknown: unknown}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			preflight := &recordingRestorePreflight{beforeReturn: func() {
				if mode == "expired" {
					now = now.Add(48*time.Hour + time.Millisecond)
				}
				if mode == "canceled" {
					cancel()
				}
				memory.mu.Lock()
				defer memory.mu.Unlock()
				op := memory.state.ActiveArchiveOperation
				switch mode {
				case "newer-history":
					memory.state.History = []backupcontract.TaskRecord{{ID: "newer", Kind: "retention", Status: "succeeded"}}
				case "lease-missing":
					memory.state.ActiveArchiveOperation = nil
				case "lease-token":
					op.Token = "foreign-token"
				case "lease-kind":
					op.Kind = "delete"
				case "lease-archive":
					op.ArchiveID = "other-archive"
				case "lease-node":
					op.CoordinatorNodeID = 2
				case "lease-term":
					op.CoordinatorTerm = 2
				case "lease-time":
					op.ExpiresUnixMillis++
				}
				if mode == "newer-history" || len(mode) >= 6 && mode[:6] == "lease-" {
					memory.state.Revision++
				}
			}}
			if mode == "preflight-failed" {
				preflight.err = unknown
			}
			ids := []string{"transfer-operation", "transfer-job"}
			service, err := backupusecase.NewRestoreService(backupusecase.RestoreServiceOptions{StateStore: store, Repository: fixedRepositoryProvider{store: archive}, Preflight: preflight, Now: func() time.Time { return now }, NewID: func() string { id := ids[0]; ids = ids[1:]; return id }, NewActivation: func() string { return "transfer-activation" }})
			require.NoError(t, err)
			job, err := service.StartRestore(ctx, "transfer-archive", "operator")
			state, readErr := memory.Load(context.Background())
			require.NoError(t, readErr)
			switch mode {
			case "complete", "cleanup-unavailable", "newer-history":
				require.NoError(t, err)
				require.Equal(t, "transfer-job", job.ID)
				require.NotNil(t, state.ActiveRestore)
				require.True(t, store.transferred, "the admission proposal must consume its lease")
				require.Nil(t, state.ActiveArchiveOperation)
				if mode == "newer-history" {
					require.Len(t, state.History, 1)
					require.Equal(t, "newer", state.History[0].ID)
				}
			case "unknown-admit-applied", "unknown-admit-applied-foreign":
				require.ErrorIs(t, err, unknown)
				require.NotErrorIs(t, err, backupusecase.ErrStateConflict, "cleanup cannot grant a definite retry classification")
				require.Empty(t, job.ID)
				require.NotNil(t, state.ActiveRestore, "unknown response does not prove non-admission")
				if mode == "unknown-admit-applied-foreign" {
					require.NotNil(t, state.ActiveArchiveOperation)
					require.Equal(t, "foreign-token", state.ActiveArchiveOperation.Token)
				} else {
					require.Nil(t, state.ActiveArchiveOperation)
				}
			case "unknown-acquire-applied":
				require.ErrorIs(t, err, unknown)
				require.Empty(t, job.ID)
				require.Nil(t, state.ActiveRestore)
				require.NotNil(t, state.ActiveArchiveOperation, "unknown acquisition cannot authorize cleanup or restore")
			case "unknown-admit-unapplied", "preflight-failed":
				require.ErrorIs(t, err, unknown)
				require.Empty(t, job.ID)
				require.Nil(t, state.ActiveRestore)
				require.Nil(t, state.ActiveArchiveOperation)
			case "canceled":
				require.ErrorIs(t, err, context.Canceled)
				require.Empty(t, job.ID)
				require.Nil(t, state.ActiveRestore)
				require.Nil(t, state.ActiveArchiveOperation)
			default:
				require.ErrorIs(t, err, backupusecase.ErrStateConflict)
				require.Empty(t, job.ID)
				require.Nil(t, state.ActiveRestore)
				if mode == "lease-missing" || mode == "expired" || mode == "rejected-admit" {
					require.Nil(t, state.ActiveArchiveOperation)
				} else {
					require.NotNil(t, state.ActiveArchiveOperation, "changed operation belongs to different authority")
				}
			}
			if mode != "unknown-acquire-applied" {
				require.LessOrEqual(t, store.admissions, 1, "admission is never retried after a possible write")
			}
		})
	}
}

type restoreTransferStore struct {
	*memoryScheduledStateStore
	mode        string
	unknown     error
	admissions  int
	transferred bool
}

func (s *restoreTransferStore) Load(ctx context.Context) (backupcontract.SystemState, error) {
	if err := ctx.Err(); err != nil {
		return backupcontract.SystemState{}, err
	}
	state, err := s.memoryScheduledStateStore.Load(ctx)
	if s.mode == "cleanup-unavailable" && state.ActiveRestore != nil {
		return backupcontract.SystemState{}, s.unknown
	}
	return state, err
}

func (s *restoreTransferStore) CompareAndSwap(ctx context.Context, revision uint64, next backupcontract.SystemState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := s.memoryScheduledStateStore.Load(ctx)
	if err != nil {
		return err
	}
	admission := before.ActiveRestore == nil && next.ActiveRestore != nil
	if admission {
		s.admissions++
		s.transferred = next.ActiveArchiveOperation == nil
		switch s.mode {
		case "rejected-admit":
			return backupusecase.ErrStateConflict
		case "unknown-admit-unapplied":
			return s.unknown
		}
	}
	if err := s.memoryScheduledStateStore.CompareAndSwap(ctx, revision, next); err != nil {
		return err
	}
	if s.mode == "unknown-acquire-applied" && before.ActiveArchiveOperation == nil && next.ActiveArchiveOperation != nil {
		return s.unknown
	}
	if admission && (s.mode == "unknown-admit-applied" || s.mode == "unknown-admit-applied-foreign") {
		if s.mode == "unknown-admit-applied-foreign" {
			s.mu.Lock()
			s.state.Revision++
			s.state.ActiveArchiveOperation = &backupcontract.ArchiveOperation{Token: "foreign-token", Kind: "delete", ArchiveID: "another-archive"}
			s.mu.Unlock()
		}
		return s.unknown
	}
	return nil
}
