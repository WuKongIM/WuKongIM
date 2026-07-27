package backup_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestAppStatusUsesOnlyContinuousCoordinationState(t *testing.T) {
	t.Parallel()

	store := &memoryStateStore{state: backupusecase.State{
		SlotFrontiers: []backupusecase.SlotFrontier{{
			Revision:     2,
			HashSlot:     7,
			Generation:   "generation-7",
			SourceSlotID: 9,
			Lease: backupusecase.SlotCaptureLease{
				SlotID: 9, HolderNodeID: 3, LeaderTerm: 4,
				ConfigEpoch: 5, Sequence: 6,
				AcquiredAtUnixMillis: 1_753_056_300_000,
			},
			Metadata: backupusecase.StreamFrontier{SourceHighWatermark: 11},
			Messages: backupusecase.StreamFrontier{SourceHighWatermark: 12},
			LastPromotion: &backupcontract.SlotGenerationPromotion{
				PreviousGeneration:   "generation-6",
				Reason:               backupcontract.RebaseReasonAuditCorruption,
				PromotedAtUnixMillis: 1_753_056_299_000,
			},
		}},
		IntegrityAudit: backupcontract.IntegrityAuditState{
			Revision: 8, DebtObjects: 3,
			Cursor: &backupcontract.IntegrityAuditCursor{
				CycleID: "catalog-segments-8", ScrubEpoch: 2,
				CatalogSequence: 8, HashSlot: 7,
				Generation:          "generation-7",
				Phase:               backupcontract.IntegrityAuditPhaseRebase,
				Category:            backupcontract.IntegrityCorruptionCiphertext,
				UpdatedAtUnixMillis: 1_753_056_350_000,
			},
			Slots: []backupcontract.SlotIntegrityAuditState{{
				HashSlot: 7, Generation: "generation-7",
				Health:              backupcontract.SlotAuditRebaseRequired,
				Category:            backupcontract.IntegrityCorruptionCiphertext,
				UpdatedAtUnixMillis: 1_753_056_350_000,
			}},
			UpdatedAtUnixMillis: 1_753_056_350_000,
		},
	}}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 256, Store: store,
		Now:              func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
		MaxCheckpointAge: 10 * time.Minute,
	})
	require.NoError(t, err)

	status, err := app.Status(context.Background())
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.Equal(t, backupusecase.HealthUnknown, status.Health)
	require.Nil(t, status.CheckpointAgeSeconds)
	require.Equal(t, int64(600), status.MaxCheckpointAgeSeconds)
	require.Len(t, status.CaptureLeases, 1)
	require.Equal(t, uint16(7), status.CaptureLeases[0].HashSlot)
	require.Equal(t, uint64(12), status.CaptureLeases[0].MessageSourceWatermark)
	require.Equal(
		t, "generation-6",
		status.CaptureLeases[0].LastPromotionPreviousGeneration,
	)
	require.Equal(
		t, backupcontract.RebaseReasonAuditCorruption,
		status.CaptureLeases[0].LastPromotionReason,
	)
	require.Equal(
		t, int64(1_753_056_299_000),
		status.CaptureLeases[0].LastPromotionAtUnixMillis,
	)
	require.Equal(t, uint64(8), status.IntegrityAudit.Revision)
	require.Equal(t, uint64(3), status.IntegrityAudit.DebtObjects)
	require.NotNil(t, status.IntegrityAudit.Cursor)
	require.Equal(
		t, backupcontract.IntegrityAuditPhaseRebase,
		status.IntegrityAudit.Cursor.Phase,
	)
	require.Equal(
		t, "generation-7", status.IntegrityAudit.Cursor.Generation,
	)
	require.Len(t, status.IntegrityAudit.Slots, 1)
	require.Equal(
		t, backupcontract.SlotAuditRebaseRequired,
		status.IntegrityAudit.Slots[0].Health,
	)
}

func TestAppDisabledStatusDoesNotReadState(t *testing.T) {
	t.Parallel()

	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: false, HashSlotCount: 256,
		Store: &memoryStateStore{loadErr: errors.New("must not load")},
		Now:   time.Now,
	})
	require.NoError(t, err)

	status, err := app.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupusecase.HealthDisabled, status.Health)
	require.False(t, status.Enabled)
}

func TestErasureLedgerReservationAndCommitRemainBounded(t *testing.T) {
	t.Parallel()

	store := &memoryStateStore{}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 256, Store: store, Now: time.Now,
	})
	require.NoError(t, err)
	reference := backupusecase.ErasureLedgerRecordReference{
		HashSlot:     3,
		EventID:      strings.Repeat("a", 64),
		RecordKey:    "erasure-ledger/events/0003/" + strings.Repeat("a", 64) + ".json",
		RecordSHA256: strings.Repeat("b", 64),
	}

	reserved, err := app.ReserveErasureLedgerCommit(context.Background(), reference)
	require.NoError(t, err)
	require.Equal(t, uint64(1), reserved.Sequence)
	retry, err := app.ReserveErasureLedgerCommit(context.Background(), reference)
	require.NoError(t, err)
	require.Equal(t, reserved, retry)

	head := backupartifact.ErasureStreamHead{
		HashSlot: 3, Sequence: 1,
		CommitKey:    backupartifact.ErasureLedgerCommitKey(reference.EventID, 3, 1),
		CommitSHA256: strings.Repeat("c", 64),
	}
	require.NoError(t, app.CommitErasureLedgerCommit(
		context.Background(), head, reference.EventID,
	))
	state, err := app.CoordinationState(context.Background())
	require.NoError(t, err)
	require.Len(t, state.ErasureStreams, 1)
	require.Nil(t, state.ErasureStreams[0].Pending)
	require.Equal(t, uint64(1), state.ErasureStreams[0].Head.Sequence)
}

func TestCatalogHeadTokenRoundTripsAndRejectsTrailingPayload(t *testing.T) {
	t.Parallel()

	head := backupartifact.CatalogPageReference{
		Sequence: 7,
		Key: backupartifact.CatalogPageObjectKey(
			7, "checkpoint-7",
		),
		SHA256: strings.Repeat("a", 64), Bytes: 512,
		LatestCheckpointID: "checkpoint-7",
	}
	token, err := backupusecase.EncodeCatalogHeadToken(head)
	require.NoError(t, err)
	decoded, err := backupusecase.DecodeCatalogHeadToken(token)
	require.NoError(t, err)
	require.Equal(t, head, decoded)

	body, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)
	trailing := base64.RawURLEncoding.EncodeToString(
		append(body, []byte(`{}`)...),
	)
	_, err = backupusecase.DecodeCatalogHeadToken(trailing)
	require.ErrorIs(t, err, backupusecase.ErrInvalidRequest)

	_, err = backupusecase.DecodeCatalogHeadToken(
		base64.RawURLEncoding.EncodeToString([]byte(`{"version":1}`)),
	)
	require.ErrorIs(t, err, backupusecase.ErrInvalidRequest)
}

func TestCheckpointHoldAdvancesRetentionFenceAndBlocksActiveGC(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_800_000_000_000).UTC()
	head := &backupartifact.CatalogPageReference{
		Sequence: 1,
		Key: backupartifact.CatalogPageObjectKey(
			1, "checkpoint-1",
		),
		SHA256: strings.Repeat("a", 64), Bytes: 512,
		LatestCheckpointID: "checkpoint-1",
	}
	store := &memoryStateStore{state: backupusecase.State{
		Revision: 7, CatalogHead: head,
		CatalogRetentionRevision: 1,
	}}
	retention := &recordingCheckpointRetention{}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 256, Store: store,
		CatalogRetention: retention,
		Now:              func() time.Time { return now },
	})
	require.NoError(t, err)

	checkpoint, err := app.SetCheckpointHold(
		context.Background(), "checkpoint-1", true,
	)
	require.NoError(t, err)
	require.True(t, checkpoint.Held)
	require.Equal(t, uint64(2), store.state.CatalogHead.Sequence)
	require.Equal(t, uint64(2), store.state.CatalogRetentionRevision)
	require.Equal(t, uint64(8), store.state.Revision)

	_, err = app.SetCheckpointHold(
		context.Background(), "checkpoint-1", true,
	)
	require.NoError(t, err)
	require.Equal(t, uint64(8), store.state.Revision)
	require.Equal(t, 2, retention.calls)

	store.state.IntegrityAudit.GCGuards =
		[]backupcontract.IntegrityAuditGCGuard{{
			HashSlot: 7, Token: "active-gc",
			AcquiredAtUnixMillis: now.UnixMilli(),
			ExpiresAtUnixMillis:  now.Add(time.Minute).UnixMilli(),
		}}
	_, err = app.SetCheckpointHold(
		context.Background(), "checkpoint-1", false,
	)
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
	require.Equal(t, 2, retention.calls)

	store.state.IntegrityAudit.GCGuards = nil
	checkpoint, err = app.SetCheckpointHold(
		context.Background(), "checkpoint-1", false,
	)
	require.NoError(t, err)
	require.False(t, checkpoint.Held)
	require.Equal(t, uint64(3), store.state.CatalogHead.Sequence)
	require.Equal(t, uint64(3), store.state.CatalogRetentionRevision)
}

type memoryStateStore struct {
	mu      sync.Mutex
	state   backupusecase.State
	loadErr error
}

type recordingCheckpointRetention struct {
	calls int
	set   bool
	held  bool
}

func (r *recordingCheckpointRetention) SetCheckpointHold(
	_ context.Context,
	head backupartifact.CatalogPageReference,
	checkpointID string,
	held bool,
	_ int64,
) (backupusecase.CheckpointHoldCommit, error) {
	r.calls++
	summary := backupusecase.CheckpointSummary{
		ID: checkpointID, CreatedAtUnixMillis: 100,
		EffectiveAtUnixMillis: 90, Held: held,
	}
	if r.set && r.held == held {
		return backupusecase.CheckpointHoldCommit{
			Checkpoint: summary, Head: head,
		}, nil
	}
	r.set = true
	r.held = held
	next := backupartifact.CatalogPageReference{
		Sequence: head.Sequence + 1,
		Key: backupartifact.CatalogPageObjectKey(
			head.Sequence+1, head.LatestCheckpointID,
		),
		SHA256: strings.Repeat("b", 64), Bytes: 512,
		LatestCheckpointID: head.LatestCheckpointID,
	}
	return backupusecase.CheckpointHoldCommit{
		Checkpoint: summary, Head: next, Changed: true,
	}, nil
}

func (s *memoryStateStore) Load(context.Context) (backupusecase.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone(), s.loadErr
}

func (s *memoryStateStore) CompareAndSwap(
	_ context.Context,
	revision uint64,
	next backupusecase.State,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != revision {
		return backupusecase.ErrStateConflict
	}
	next.Revision = revision + 1
	s.state = next.Clone()
	return nil
}
