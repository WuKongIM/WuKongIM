package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestSegmentIntegrityAuditBackendPerformsFullPortableArtifactValidation(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	seed := sha256.Sum256([]byte("segment-integrity-backend-key"))
	signer := testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	store, err := backupartifact.NewReplicatedSegmentStoreWithRepair(
		primary, secondary, primary, secondary,
		backupartifact.NewSegmentCodec(
			testWrappingKeyManager{mask: 0x5a},
			bytes.NewReader(bytes.Repeat([]byte{0x71}, 128)),
		),
		signer)
	require.NoError(t, err)
	reference, err := store.Commit(context.Background(), backupartifact.SegmentDescriptor{
		Logical: backupartifact.SegmentLogicalDescriptor{
			RepositoryID: "repository-prod", SourceClusterID: "cluster-source",
			SourceGeneration: "source-generation-1", Generation: "slot-generation-7",
			HashSlot: 7, Stream: backupartifact.SegmentStreamMetadata,
			Sequence: 1, RecordCount: 1,
		},
	}, []byte("authenticated metadata segment"))
	require.NoError(t, err)
	plan := staticSegmentIntegrityPlan{
		cursor: backupcontract.IntegrityAuditCursor{
			CycleID: "audit-cycle-7", CatalogSequence: 9,
			HashSlot: 7, Generation: "slot-generation-7",
			Position: "segment-7-1", Phase: backupcontract.IntegrityAuditPhaseInspect,
		},
		reference: reference,
	}
	backend, err := backupinfra.NewSegmentIntegrityAuditBackend(
		plan, store, store,
	)
	require.NoError(t, err)

	cursor, debt, err := backend.Start(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), debt)
	inspection, err := backend.Inspect(context.Background(), cursor)
	require.NoError(t, err)
	require.True(t, inspection.Copies[0].Healthy)
	require.True(t, inspection.Copies[1].Healthy)
	require.Equal(t, backupcontract.IntegrityAuditPhaseComplete, inspection.Next.Phase)
	require.Positive(t, inspection.ArtifactBytes)
}

func TestSegmentIntegrityAuditBackendRestoresPartitionCycleOnTakeover(
	t *testing.T,
) {
	cursor := backupcontract.IntegrityAuditCursor{
		CycleID: "audit-cycle-takeover", CatalogSequence: 7,
		HashSlot: 1, Generation: "slot-generation-1",
		Position: "partition-1", Phase: backupcontract.IntegrityAuditPhaseInspect,
	}
	next := cursor
	next.Position = "complete"
	next.Phase = backupcontract.IntegrityAuditPhaseComplete
	partitions := &recordingPartitionCopyAuditor{}
	backend, err := backupinfra.NewSegmentIntegrityAuditBackend(
		staticTargetIntegrityPlan{
			target: backupinfra.SegmentIntegrityAuditTarget{
				Kind: backupinfra.IntegrityAuditArtifactPartition,
				Partition: backupartifact.PartitionReference{
					HashSlot: 1, Key: "partition-manifests/test.json",
					ObjectCount: 1, Bytes: 1, CiphertextBytes: 1,
				},
				PartitionObjectIndex: -1,
				Next:                 next,
			},
		},
		noopSegmentCopyAuditor{}, partitions,
	)
	require.NoError(t, err)

	_, err = backend.Inspect(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, []string{cursor.CycleID}, partitions.cycles)
	_, err = backend.Repair(
		context.Background(), cursor, "secondary",
	)
	require.NoError(t, err)
	require.Equal(
		t, []string{cursor.CycleID, cursor.CycleID},
		partitions.cycles,
	)
}

type staticSegmentIntegrityPlan struct {
	cursor    backupcontract.IntegrityAuditCursor
	reference backupartifact.SegmentReference
}

type staticTargetIntegrityPlan struct {
	target backupinfra.SegmentIntegrityAuditTarget
}

func (p staticTargetIntegrityPlan) Start(
	context.Context,
	*backupcontract.IntegrityAuditCursor,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	return backupcontract.IntegrityAuditCursor{}, 0, nil
}

func (p staticTargetIntegrityPlan) Resolve(
	context.Context,
	backupcontract.IntegrityAuditCursor,
) (backupinfra.SegmentIntegrityAuditTarget, error) {
	return p.target, nil
}

type noopSegmentCopyAuditor struct{}

func (noopSegmentCopyAuditor) InspectSegmentCopies(
	context.Context,
	backupartifact.SegmentReference,
) (backupartifact.SegmentAuditReport, error) {
	return backupartifact.SegmentAuditReport{}, nil
}

func (noopSegmentCopyAuditor) RepairSegmentCopy(
	context.Context,
	backupartifact.SegmentReference,
	string,
) (int64, error) {
	return 0, nil
}

type recordingPartitionCopyAuditor struct {
	cycles []string
}

func (a *recordingPartitionCopyAuditor) BeginPartitionAuditCycle(
	cycleID string,
) {
	a.cycles = append(a.cycles, cycleID)
}

func (*recordingPartitionCopyAuditor) InspectPartitionArtifactCopies(
	context.Context,
	backupartifact.PartitionReference,
	int,
) (backupartifact.PartitionArtifactAuditReport, error) {
	return backupartifact.PartitionArtifactAuditReport{
		Copies: []backupartifact.SegmentAuditCopy{
			{Repository: "primary", Healthy: true},
			{Repository: "secondary", Healthy: true},
		},
	}, nil
}

func (*recordingPartitionCopyAuditor) RepairPartitionArtifactCopy(
	context.Context,
	backupartifact.PartitionReference,
	int,
	string,
) (int64, error) {
	return 0, nil
}

func (p staticSegmentIntegrityPlan) Start(
	context.Context,
	*backupcontract.IntegrityAuditCursor,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	return p.cursor, 1, nil
}

func (p staticSegmentIntegrityPlan) Resolve(
	_ context.Context,
	cursor backupcontract.IntegrityAuditCursor,
) (backupinfra.SegmentIntegrityAuditTarget, error) {
	next := cursor
	next.Position = "complete"
	next.Phase = backupcontract.IntegrityAuditPhaseComplete
	return backupinfra.SegmentIntegrityAuditTarget{
		Kind:      backupinfra.IntegrityAuditArtifactSegment,
		Reference: p.reference, Next: next,
	}, nil
}
