package backup_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestCheckpointCoordinatorPublishesCompleteHealthyVectorBeforeAdvancingHead(t *testing.T) {
	store := &memoryStateStore{state: backupusecase.State{
		Revision: 5, SlotFrontiers: checkpointTestFrontiers(256),
	}}
	statuses := checkpointTestStatuses(256)
	catalog := &recordingCheckpointCatalog{store: store}
	proofs := &recordingCheckpointProofs{}
	coordinator, err := backupusecase.NewCheckpointCoordinator(backupusecase.CheckpointOptions{
		Enabled: true, HashSlotCount: 256, RepositoryID: "repository-prod", SourceClusterID: "cluster-a",
		SourceGeneration: "source-generation-1", Store: store, Catalog: catalog,
		Proofs:          proofs,
		CaptureStatus:   checkpointStatusSource{statuses: statuses},
		Now:             func() time.Time { return time.UnixMilli(1_753_400_300_000).UTC() },
		NewCheckpointID: func() string { return "checkpoint-256" },
	})
	require.NoError(t, err)

	commit, err := coordinator.Publish(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, catalog.calls)
	require.Len(t, catalog.checkpoint.Slots, 256)
	require.Equal(t, int64(1_753_400_100_000), catalog.checkpoint.EffectiveAtUnixMillis)
	require.Equal(t, uint16(255), catalog.checkpoint.Slots[255].HashSlot)
	require.Equal(t, commit.Head, *store.state.CatalogHead)
	require.True(t, catalog.controllerHeadWasNil, "catalog must commit before Controller visibility")
	require.Equal(t, int64(256*3), proofs.calls.Load())
}

func TestCheckpointCoordinatorRejectsMissingDuplicateAndUnhealthySlots(t *testing.T) {
	tests := []struct {
		name     string
		frontier []backupcontract.SlotFrontier
		statuses []backupcontract.SlotCaptureStatus
		wantErr  error
	}{
		{
			name: "missing", frontier: checkpointTestFrontiers(1),
			statuses: checkpointTestStatuses(1), wantErr: backupusecase.ErrPartitionsIncomplete,
		},
		{
			name: "duplicate", frontier: checkpointTestFrontiers(2),
			statuses: func() []backupcontract.SlotCaptureStatus {
				statuses := checkpointTestStatuses(2)
				statuses[1].HashSlot = 0
				statuses[1].Frontier.HashSlot = 0
				return statuses
			}(),
			wantErr: backupusecase.ErrPartitionsIncomplete,
		},
		{
			name: "unhealthy", frontier: checkpointTestFrontiers(2),
			statuses: func() []backupcontract.SlotCaptureStatus {
				statuses := checkpointTestStatuses(2)
				statuses[1].State = backupcontract.CaptureStateDegraded
				return statuses
			}(),
			wantErr: backupusecase.ErrCheckpointUnhealthy,
		},
		{
			name: "durably rebasing", frontier: checkpointTestFrontiers(2),
			statuses: func() []backupcontract.SlotCaptureStatus {
				statuses := checkpointTestStatuses(2)
				statuses[1].Frontier.Rebase = &backupcontract.SlotRebase{
					TargetGeneration: "rebase-00001-00000000000000000002",
					Epoch:            2,
					Reason:           backupcontract.RebaseReasonPinAge, StartedAtUnixMillis: 1_753_400_200_000,
				}
				return statuses
			}(),
			wantErr: backupusecase.ErrCheckpointUnhealthy,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &memoryStateStore{state: backupusecase.State{SlotFrontiers: test.frontier}}
			catalog := &recordingCheckpointCatalog{}
			coordinator, err := backupusecase.NewCheckpointCoordinator(backupusecase.CheckpointOptions{
				Enabled: true, HashSlotCount: 2, RepositoryID: "repository-prod", SourceClusterID: "cluster-a",
				SourceGeneration: "source-generation-1", Store: store, Catalog: catalog,
				Proofs:          &recordingCheckpointProofs{},
				CaptureStatus:   checkpointStatusSource{statuses: test.statuses},
				Now:             func() time.Time { return time.UnixMilli(1_753_400_300_000).UTC() },
				NewCheckpointID: func() string { return "checkpoint-invalid" },
			})
			require.NoError(t, err)

			_, err = coordinator.Publish(context.Background())
			require.ErrorIs(t, err, test.wantErr)
			require.Zero(t, catalog.calls)
			require.Nil(t, store.state.CatalogHead)
		})
	}
}

func TestCheckpointCoordinatorKeepsHeadInvisibleWhenCatalogPublishFails(t *testing.T) {
	store := &memoryStateStore{state: backupusecase.State{SlotFrontiers: checkpointTestFrontiers(2)}}
	catalog := &recordingCheckpointCatalog{err: errors.New("secondary unavailable")}
	coordinator, err := backupusecase.NewCheckpointCoordinator(backupusecase.CheckpointOptions{
		Enabled: true, HashSlotCount: 2, RepositoryID: "repository-prod", SourceClusterID: "cluster-a",
		SourceGeneration: "source-generation-1", Store: store, Catalog: catalog,
		Proofs:          &recordingCheckpointProofs{},
		CaptureStatus:   checkpointStatusSource{statuses: checkpointTestStatuses(2)},
		Now:             func() time.Time { return time.UnixMilli(1_753_400_300_000).UTC() },
		NewCheckpointID: func() string { return "checkpoint-failed" },
	})
	require.NoError(t, err)

	_, err = coordinator.Publish(context.Background())
	require.EqualError(t, err, "secondary unavailable")
	require.Nil(t, store.state.CatalogHead)
}

func TestCheckpointCoordinatorRejectsMissingCurrentCommitProof(t *testing.T) {
	store := &memoryStateStore{state: backupusecase.State{SlotFrontiers: checkpointTestFrontiers(2)}}
	catalog := &recordingCheckpointCatalog{}
	proofs := &recordingCheckpointProofs{err: backupartifact.ErrRepositoryIncomplete}
	coordinator, err := backupusecase.NewCheckpointCoordinator(backupusecase.CheckpointOptions{
		Enabled: true, HashSlotCount: 2, RepositoryID: "repository-prod", SourceClusterID: "cluster-a",
		SourceGeneration: "source-generation-1", Store: store, Catalog: catalog,
		Proofs: proofs, CaptureStatus: checkpointStatusSource{statuses: checkpointTestStatuses(2)},
		Now:             func() time.Time { return time.UnixMilli(1_753_400_300_000).UTC() },
		NewCheckpointID: func() string { return "checkpoint-missing-proof" },
	})
	require.NoError(t, err)

	_, err = coordinator.Publish(context.Background())
	require.ErrorIs(t, err, backupartifact.ErrRepositoryIncomplete)
	require.Zero(t, catalog.calls)
	require.Nil(t, store.state.CatalogHead)
}

func TestCheckpointCoordinatorRetriesUnrelatedControllerConflictWithoutLosingState(t *testing.T) {
	store := &checkpointConflictStateStore{
		state:        backupusecase.State{Revision: 3, LastEpoch: 7, SlotFrontiers: checkpointTestFrontiers(2)},
		conflictOnce: true,
	}
	coordinator, err := backupusecase.NewCheckpointCoordinator(backupusecase.CheckpointOptions{
		Enabled: true, HashSlotCount: 2, RepositoryID: "repository-prod", SourceClusterID: "cluster-a",
		SourceGeneration: "source-generation-1", Store: store,
		Catalog: &recordingCheckpointCatalog{}, Proofs: &recordingCheckpointProofs{},
		CaptureStatus:   checkpointStatusSource{statuses: checkpointTestStatuses(2)},
		Now:             func() time.Time { return time.UnixMilli(1_753_400_300_000).UTC() },
		NewCheckpointID: func() string { return "checkpoint-conflict" },
	})
	require.NoError(t, err)

	commit, err := coordinator.Publish(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, store.compareCalls)
	require.Equal(t, uint64(8), store.state.LastEpoch)
	require.Equal(t, commit.Head, *store.state.CatalogHead)
}

func TestCheckpointCoordinatorRejectsProofFromDifferentSlot(t *testing.T) {
	frontiers := checkpointTestFrontiers(2)
	store := &memoryStateStore{state: backupusecase.State{SlotFrontiers: frontiers}}
	proofs := &recordingCheckpointProofs{
		mutate: func(logical *backupartifact.SegmentLogicalDescriptor) {
			logical.HashSlot = 99
		},
	}
	coordinator, err := backupusecase.NewCheckpointCoordinator(backupusecase.CheckpointOptions{
		Enabled: true, HashSlotCount: 2, RepositoryID: "repository-prod", SourceClusterID: "cluster-a",
		SourceGeneration: "source-generation-1", Store: store,
		Catalog: &recordingCheckpointCatalog{}, Proofs: proofs,
		CaptureStatus:   checkpointStatusSource{statuses: checkpointTestStatuses(2)},
		Now:             func() time.Time { return time.UnixMilli(1_753_400_300_000).UTC() },
		NewCheckpointID: func() string { return "checkpoint-wrong-proof" },
	})
	require.NoError(t, err)

	_, err = coordinator.Publish(context.Background())
	require.ErrorIs(t, err, backupusecase.ErrInvalidRequest)
	require.Nil(t, store.state.CatalogHead)
}

func TestCheckpointCoordinatorRejectsDuplicateCommitProofReference(t *testing.T) {
	statuses := checkpointTestStatuses(2)
	statuses[1].Frontier.Metadata.Head = cloneTestSegment(statuses[0].Frontier.Metadata.Head)
	store := &memoryStateStore{}
	catalog := &recordingCheckpointCatalog{}
	coordinator, err := backupusecase.NewCheckpointCoordinator(backupusecase.CheckpointOptions{
		Enabled: true, HashSlotCount: 2, RepositoryID: "repository-prod", SourceClusterID: "cluster-a",
		SourceGeneration: "source-generation-1", Store: store,
		Catalog: catalog, Proofs: &recordingCheckpointProofs{},
		CaptureStatus:   checkpointStatusSource{statuses: statuses},
		Now:             func() time.Time { return time.UnixMilli(1_753_400_300_000).UTC() },
		NewCheckpointID: func() string { return "checkpoint-duplicate-proof" },
	})
	require.NoError(t, err)

	_, err = coordinator.Publish(context.Background())
	require.ErrorIs(t, err, backupusecase.ErrInvalidRequest)
	require.Zero(t, catalog.calls)
}

type checkpointStatusSource struct {
	statuses []backupcontract.SlotCaptureStatus
}

func (s checkpointStatusSource) Status() []backupcontract.SlotCaptureStatus {
	out := make([]backupcontract.SlotCaptureStatus, len(s.statuses))
	copy(out, s.statuses)
	return out
}

type recordingCheckpointProofs struct {
	calls  atomic.Int64
	err    error
	mutate func(*backupartifact.SegmentLogicalDescriptor)
}

func (v *recordingCheckpointProofs) VerifyCommit(_ context.Context, reference backupartifact.SegmentReference) (backupartifact.SegmentHeader, error) {
	v.calls.Add(1)
	if v.err != nil {
		return backupartifact.SegmentHeader{}, v.err
	}
	numericID, err := strconv.ParseUint(reference.SegmentID, 16, 64)
	if err != nil || numericID == 0 {
		return backupartifact.SegmentHeader{}, backupartifact.ErrInvalidObject
	}
	stream := backupartifact.SegmentStreamMessageCursor
	switch numericID % 3 {
	case 1:
		stream = backupartifact.SegmentStreamMetadata
	case 2:
		stream = backupartifact.SegmentStreamMessages
	}
	logical := backupartifact.SegmentLogicalDescriptor{
		RepositoryID: "repository-prod", SourceClusterID: "cluster-a",
		SourceGeneration: "source-generation-1", Generation: "slot-generation-1",
		HashSlot: uint16((numericID - 1) / 3), Stream: stream, Sequence: 1,
	}
	if v.mutate != nil {
		v.mutate(&logical)
	}
	return backupartifact.SegmentHeader{Logical: logical, PlaintextBytes: reference.PlaintextBytes}, nil
}

type recordingCheckpointCatalog struct {
	store                *memoryStateStore
	checkpoint           backupartifact.Checkpoint
	calls                int
	err                  error
	controllerHeadWasNil bool
}

type checkpointConflictStateStore struct {
	state        backupusecase.State
	conflictOnce bool
	compareCalls int
}

func (s *checkpointConflictStateStore) Load(context.Context) (backupusecase.State, error) {
	return s.state.Clone(), nil
}

func (s *checkpointConflictStateStore) CompareAndSwap(_ context.Context, revision uint64, next backupusecase.State) error {
	s.compareCalls++
	if s.conflictOnce {
		s.conflictOnce = false
		s.state.Revision++
		s.state.LastEpoch++
		return backupusecase.ErrStateConflict
	}
	if s.state.Revision != revision {
		return backupusecase.ErrStateConflict
	}
	next.Revision = revision + 1
	s.state = next.Clone()
	return nil
}

func (c *recordingCheckpointCatalog) Publish(
	_ context.Context,
	checkpoint backupartifact.Checkpoint,
	previous *backupartifact.CatalogPageReference,
) (backupartifact.CheckpointCatalogCommit, error) {
	c.calls++
	c.checkpoint = checkpoint
	if c.store != nil {
		c.controllerHeadWasNil = c.store.state.CatalogHead == nil
	}
	if c.err != nil {
		return backupartifact.CheckpointCatalogCommit{}, c.err
	}
	sequence := uint64(1)
	if previous != nil {
		sequence = previous.Sequence + 1
	}
	return backupartifact.CheckpointCatalogCommit{
		Checkpoint: backupartifact.CatalogCheckpointReference{
			ID: checkpoint.ID, Key: backupartifact.CheckpointObjectKey(checkpoint.ID),
			SHA256: strings.Repeat("a", 64), Bytes: 1024,
			CreatedAtUnixMillis:   checkpoint.CreatedAtUnixMillis,
			EffectiveAtUnixMillis: checkpoint.EffectiveAtUnixMillis,
		},
		Head: backupartifact.CatalogPageReference{
			Sequence: sequence, Key: backupartifact.CatalogPageObjectKey(sequence, checkpoint.ID),
			SHA256: strings.Repeat("b", 64), Bytes: 512, LatestCheckpointID: checkpoint.ID,
		},
	}, nil
}

func checkpointTestFrontiers(hashSlotCount uint16) []backupcontract.SlotFrontier {
	frontiers := make([]backupcontract.SlotFrontier, hashSlotCount)
	for hashSlot := uint16(0); hashSlot < hashSlotCount; hashSlot++ {
		watermark := int64(1_753_400_100_000) + int64(hashSlot)
		frontiers[hashSlot] = backupcontract.SlotFrontier{
			Revision: 1, HashSlot: hashSlot, Generation: "slot-generation-1",
			Metadata: backupcontract.StreamFrontier{
				Sequence: 1, Head: checkpointTestSegment(uint64(hashSlot)*3 + 1),
				SourceHighWatermark: 10, WatermarkAtUnixMillis: watermark,
			},
			Messages: backupcontract.StreamFrontier{
				Sequence: 1, Head: checkpointTestSegment(uint64(hashSlot)*3 + 2),
				CursorHead:          checkpointTestSegment(uint64(hashSlot)*3 + 3),
				SourceHighWatermark: 20, WatermarkAtUnixMillis: watermark,
			},
			WatermarkAtUnixMillis: watermark, UpdatedAtUnixMillis: watermark + 1_000,
		}
	}
	return frontiers
}

func checkpointTestStatuses(hashSlotCount uint16) []backupcontract.SlotCaptureStatus {
	statuses := make([]backupcontract.SlotCaptureStatus, hashSlotCount)
	frontiers := checkpointTestFrontiers(hashSlotCount)
	for hashSlot := uint16(0); hashSlot < hashSlotCount; hashSlot++ {
		statuses[hashSlot] = backupcontract.SlotCaptureStatus{
			HashSlot: hashSlot, State: backupcontract.CaptureStateIdle,
			Frontier:             backupcontract.CloneSlotFrontier(frontiers[hashSlot]),
			ObservedAtUnixMillis: 1_753_400_300_000,
		}
	}
	return statuses
}

func checkpointTestSegment(id uint64) *backupartifact.SegmentReference {
	segmentID := fmt.Sprintf("%064x", id)
	return &backupartifact.SegmentReference{
		SegmentID:    segmentID,
		CommitKey:    "segments/" + segmentID + "/commit.json",
		CommitSHA256: strings.Repeat("d", 64), PlaintextBytes: 1,
	}
}

func cloneTestSegment(reference *backupartifact.SegmentReference) *backupartifact.SegmentReference {
	if reference == nil {
		return nil
	}
	out := *reference
	return &out
}
