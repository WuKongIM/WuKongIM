package backup_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestCatalogSegmentIntegrityAuditPlanWalksOnlyNewCatalogDelta(t *testing.T) {
	now := time.Unix(24*60*60, 0)
	firstReference := catalogAuditTestSegment(1)
	secondReference := catalogAuditTestSegment(2)
	firstPageReference := catalogAuditTestPageReference(1, "checkpoint-1")
	secondPageReference := catalogAuditTestPageReference(2, "checkpoint-2")
	catalog := &recordingIntegrityAuditCatalog{
		pages: map[uint64]backupartifact.CatalogPage{
			1: {
				Sequence: 1,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-1",
				}},
			},
			2: {
				Sequence: 2, Previous: &firstPageReference,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-2",
				}},
			},
		},
		checkpoints: map[string]backupartifact.Checkpoint{
			"checkpoint-1": catalogAuditTestCheckpoint(firstReference),
			"checkpoint-2": catalogAuditTestCheckpoint(secondReference),
		},
	}
	plan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: staticIntegrityAuditWindowSource{head: &secondPageReference, retainedRoot: 1},
			Selection: staticIntegrityAuditRetentionSelectionSource{
				retained: []string{"checkpoint-1", "checkpoint-2"},
			},
			Catalog: catalog, HashSlotCount: 2,
			Now: func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	previous := backupcontract.IntegrityAuditCursor{
		CycleID: "complete-1", ScrubEpoch: 2, CatalogSequence: 1,
		CatalogRootSequence: 1,
		Generation:          "complete", Position: "complete",
		Phase: backupcontract.IntegrityAuditPhaseComplete,
	}

	cursor, debt, err := plan.Start(context.Background(), &previous)
	require.NoError(t, err)
	require.Positive(t, debt)
	require.Equal(t, uint64(2), cursor.CatalogSequence)
	for {
		target, resolveErr := plan.Resolve(context.Background(), cursor)
		require.NoError(t, resolveErr)
		if !target.Administrative {
			break
		}
		cursor, debt, err = plan.Advance(
			context.Background(), cursor, backupinfra.IntegrityAuditArtifactReport{},
		)
		require.NoError(t, err)
	}
	require.Equal(t, uint16(0), cursor.HashSlot)
	require.Equal(t, "slot-generation-1", cursor.Generation)
	target, err := plan.Resolve(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, secondReference, target.Reference)

	next, debt, err := plan.Advance(
		context.Background(),
		cursor,
		backupinfra.IntegrityAuditArtifactReport{
			Segment: backupartifact.SegmentAuditReport{
				Header: backupartifact.SegmentHeader{
					PlaintextBytes: secondReference.PlaintextBytes,
					Logical: backupartifact.SegmentLogicalDescriptor{
						HashSlot: 0, Generation: "slot-generation-1",
					},
				},
				Previous: &firstReference,
			},
		},
	)
	require.NoError(t, err)
	require.Zero(t, debt)
	require.Equal(t, backupcontract.IntegrityAuditPhaseComplete, next.Phase)
	require.Equal(t, uint64(2), next.CatalogSequence)
	require.Equal(t, cursor.CycleID, next.CycleID)

	pageLoads := catalog.pageLoads
	checkpointLoads := catalog.checkpointLoads
	resumed, resumedDebt, err := plan.Start(context.Background(), &next)
	require.NoError(t, err)
	require.Equal(t, next, resumed)
	require.Zero(t, resumedDebt)
	require.Equal(t, pageLoads, catalog.pageLoads)
	require.Equal(t, checkpointLoads, catalog.checkpointLoads)
}

func TestCatalogSegmentIntegrityAuditPlanStartsPeriodicLatentDamageScrub(t *testing.T) {
	now := time.Unix(24*60*60, 0)
	reference := catalogAuditTestSegment(1)
	pageReference := catalogAuditTestPageReference(1, "checkpoint-1")
	catalog := &recordingIntegrityAuditCatalog{
		pages: map[uint64]backupartifact.CatalogPage{
			1: {
				Sequence: 1,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-1",
				}},
			},
		},
		checkpoints: map[string]backupartifact.Checkpoint{
			"checkpoint-1": catalogAuditTestCheckpoint(reference),
		},
	}
	plan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: staticIntegrityAuditWindowSource{head: &pageReference, retainedRoot: 1},
			Selection: staticIntegrityAuditRetentionSelectionSource{
				retained: []string{"checkpoint-1"},
			},
			Catalog: catalog, HashSlotCount: 2,
			Now: func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	previous := backupcontract.IntegrityAuditCursor{
		CycleID: "complete-epoch-2", ScrubEpoch: 2, CatalogSequence: 1,
		CatalogRootSequence: 1,
		Generation:          "complete", Position: "complete",
		Phase: backupcontract.IntegrityAuditPhaseComplete,
	}

	same, debt, err := plan.Start(context.Background(), &previous)
	require.NoError(t, err)
	require.Equal(t, previous, same)
	require.Zero(t, debt)
	require.Zero(t, catalog.pageLoads)

	now = time.Unix(2*24*60*60, 0)
	scrub, debt, err := plan.Start(context.Background(), &previous)
	require.NoError(t, err)
	require.Equal(t, uint64(3), scrub.ScrubEpoch)
	require.Equal(t, uint64(1), scrub.CatalogSequence)
	require.NotEqual(t, previous.CycleID, scrub.CycleID)
	require.Positive(t, debt)
	require.Equal(t, 1, catalog.pageLoads)
}

func TestCatalogSegmentIntegrityAuditPlanScrubsOnlyRetainedCatalogRoot(t *testing.T) {
	now := time.Unix(3*24*60*60, 0)
	pageTwoReference := catalogAuditTestPageReference(2, "checkpoint-2")
	pageThreeReference := catalogAuditTestPageReference(3, "checkpoint-3")
	expiredPageReference := catalogAuditTestPageReference(1, "checkpoint-expired")
	catalog := &recordingIntegrityAuditCatalog{
		pages: map[uint64]backupartifact.CatalogPage{
			2: {
				Sequence: 2, Previous: &expiredPageReference,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-2",
				}},
			},
			3: {
				Sequence: 3, Previous: &pageTwoReference,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-3",
				}},
			},
		},
		checkpoints: map[string]backupartifact.Checkpoint{
			"checkpoint-2": catalogAuditTestCheckpoint(
				catalogAuditTestSegment(2),
			),
			"checkpoint-3": catalogAuditTestCheckpoint(
				catalogAuditTestSegment(3),
			),
		},
	}
	plan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: staticIntegrityAuditWindowSource{
				head: &pageThreeReference, retainedRoot: 2,
			},
			Selection: staticIntegrityAuditRetentionSelectionSource{
				retained: []string{"checkpoint-2", "checkpoint-3"},
			},
			Catalog: catalog, HashSlotCount: 2,
			Now: func() time.Time { return now },
		},
	)
	require.NoError(t, err)

	cursor, _, err := plan.Start(context.Background(), nil)
	require.NoError(t, err)
	for cursor.Phase != backupcontract.IntegrityAuditPhaseComplete {
		target, resolveErr := plan.Resolve(context.Background(), cursor)
		require.NoError(t, resolveErr)
		report := backupinfra.IntegrityAuditArtifactReport{}
		if !target.Administrative {
			report.Segment = backupartifact.SegmentAuditReport{
				Header: backupartifact.SegmentHeader{
					PlaintextBytes: target.Reference.PlaintextBytes,
					Logical: backupartifact.SegmentLogicalDescriptor{
						HashSlot:   cursor.HashSlot,
						Generation: cursor.Generation,
					},
				},
			}
			if target.Reference == catalogAuditTestSegment(3) {
				previous := catalogAuditTestSegment(2)
				report.Segment.Previous = &previous
			}
		}
		cursor, _, err = plan.Advance(
			context.Background(), cursor, report,
		)
		require.NoError(t, err)
	}
	require.Equal(t, 2, catalog.pageLoads)
	require.Equal(t, 2, catalog.checkpointLoads)
}

func TestCatalogSegmentIntegrityAuditPlanSkipsCollectedSparseRetentionHole(
	t *testing.T,
) {
	now := time.Unix(3*24*60*60, 0)
	firstReference := catalogAuditTestSegment(1)
	thirdReference := catalogAuditTestSegment(3)
	firstPageReference := catalogAuditTestPageReference(1, "checkpoint-1")
	secondPageReference := catalogAuditTestPageReference(2, "checkpoint-2")
	thirdPageReference := catalogAuditTestPageReference(3, "checkpoint-3")
	catalog := &recordingIntegrityAuditCatalog{
		pages: map[uint64]backupartifact.CatalogPage{
			1: {
				Sequence: 1,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-1",
				}},
			},
			2: {
				Sequence: 2, Previous: &firstPageReference,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-2",
				}},
			},
			3: {
				Sequence: 3, Previous: &secondPageReference,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-3",
				}},
			},
		},
		// checkpoint-2 deliberately models a Generation already collected by
		// sparse retention and must never be loaded by the scrub.
		checkpoints: map[string]backupartifact.Checkpoint{
			"checkpoint-1": catalogAuditTestCheckpoint(firstReference),
			"checkpoint-3": catalogAuditTestCheckpoint(thirdReference),
		},
	}
	plan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: staticIntegrityAuditWindowSource{
				head: &thirdPageReference, retainedRoot: 1,
			},
			Selection: staticIntegrityAuditRetentionSelectionSource{
				retained: []string{"checkpoint-1", "checkpoint-3"},
			},
			Catalog: catalog, HashSlotCount: 2,
			Now: func() time.Time { return now },
		},
	)
	require.NoError(t, err)

	cursor, _, err := plan.Start(context.Background(), nil)
	require.NoError(t, err)
	protected, err := plan.LoadIntegrityAuditRetainedCheckpoints(
		context.Background(), cursor,
	)
	require.NoError(t, err)
	require.Equal(
		t, []string{"checkpoint-1", "checkpoint-3"},
		catalogAuditCheckpointReferenceIDs(protected),
	)
	for cursor.Phase != backupcontract.IntegrityAuditPhaseComplete {
		target, resolveErr := plan.Resolve(context.Background(), cursor)
		require.NoError(t, resolveErr)
		report := backupinfra.IntegrityAuditArtifactReport{}
		if !target.Administrative {
			report.Segment = backupartifact.SegmentAuditReport{
				Header: backupartifact.SegmentHeader{
					PlaintextBytes: target.Reference.PlaintextBytes,
					Logical: backupartifact.SegmentLogicalDescriptor{
						HashSlot: cursor.HashSlot, Generation: cursor.Generation,
					},
				},
			}
			if target.Reference == thirdReference {
				report.Segment.Previous = &firstReference
			}
		}
		cursor, _, err = plan.Advance(
			context.Background(), cursor, report,
		)
		require.NoError(t, err)
	}
	require.Equal(t, 3, catalog.pageLoads)
	require.Equal(t, 2, catalog.checkpointLoads)
}

func catalogAuditCheckpointReferenceIDs(
	references []backupartifact.CatalogCheckpointReference,
) []string {
	ids := make([]string, len(references))
	for index, reference := range references {
		ids[index] = reference.ID
	}
	return ids
}

func TestCatalogSegmentIntegrityAuditPlanWalksBaselinePartitionGraph(t *testing.T) {
	now := time.Unix(24*60*60, 0)
	pageReference := catalogAuditTestPageReference(1, "checkpoint-1")
	partition := backupartifact.PartitionReference{
		HashSlot: 0, Key: "partition-manifests/slot-generation-1/00000.json",
		SHA256: strings.Repeat("b", 64), Bytes: 128,
		ObjectCount: 1, CiphertextBytes: 64,
		Evidence: backupartifact.PartitionEvidence{
			Version: backupartifact.PartitionEvidenceVersion,
		},
	}
	checkpoint := backupartifact.Checkpoint{
		HashSlotCount: 2,
		Slots: []backupartifact.CheckpointSlot{
			{
				HashSlot: 0, Generation: "slot-generation-1",
				Baseline: &backupartifact.CheckpointBaseline{
					Partition:     partition,
					MessageCursor: catalogAuditTestSegment(4),
				},
			},
			{HashSlot: 1, Generation: "slot-generation-1"},
		},
	}
	catalog := &recordingIntegrityAuditCatalog{
		pages: map[uint64]backupartifact.CatalogPage{
			1: {
				Sequence: 1,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-1",
				}},
			},
		},
		checkpoints: map[string]backupartifact.Checkpoint{
			"checkpoint-1": checkpoint,
		},
	}
	plan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: staticIntegrityAuditWindowSource{head: &pageReference, retainedRoot: 1},
			Selection: staticIntegrityAuditRetentionSelectionSource{
				retained: []string{"checkpoint-1"},
			},
			Catalog: catalog, HashSlotCount: 2,
			Now: func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	cursor, _, err := plan.Start(context.Background(), nil)
	require.NoError(t, err)
	for {
		target, resolveErr := plan.Resolve(context.Background(), cursor)
		require.NoError(t, resolveErr)
		if !target.Administrative &&
			target.Kind == backupinfra.IntegrityAuditArtifactPartition {
			require.Equal(t, partition, target.Partition)
			require.Equal(t, -1, target.PartitionObjectIndex)
			break
		}
		var report backupinfra.IntegrityAuditArtifactReport
		if !target.Administrative {
			report.Segment = backupartifact.SegmentAuditReport{
				Header: backupartifact.SegmentHeader{
					PlaintextBytes: target.Reference.PlaintextBytes,
					Logical: backupartifact.SegmentLogicalDescriptor{
						HashSlot: 0, Generation: "slot-generation-1",
					},
				},
			}
		}
		cursor, _, err = plan.Advance(context.Background(), cursor, report)
		require.NoError(t, err)
	}
	navigation := backupartifact.PartitionArtifactAuditNavigation{
		Format:      backupartifact.PartitionManifestFormat,
		HashSlot:    0,
		ObjectCount: 1,
	}
	cursor, _, err = plan.Advance(
		context.Background(), cursor,
		backupinfra.IntegrityAuditArtifactReport{
			Partition: backupartifact.PartitionArtifactAuditReport{
				Navigation: navigation,
			},
		},
	)
	require.NoError(t, err)
	target, err := plan.Resolve(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, 0, target.PartitionObjectIndex)
	cursor, _, err = plan.Advance(
		context.Background(), cursor,
		backupinfra.IntegrityAuditArtifactReport{
			Partition: backupartifact.PartitionArtifactAuditReport{
				Navigation: navigation,
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseComplete, cursor.Phase)
}

func TestCatalogSegmentIntegrityAuditPlanWalksPermanentErasureGraph(t *testing.T) {
	now := time.Unix(24*60*60, 0)
	pageReference := catalogAuditTestPageReference(1, "checkpoint-1")
	namespace := strings.Repeat("d", 64)
	commitOneSHA := strings.Repeat("1", 64)
	commitTwoSHA := strings.Repeat("2", 64)
	eventOneID := strings.Repeat("3", 64)
	eventTwoID := strings.Repeat("4", 64)
	checkpoint := catalogAuditTestCheckpoint(catalogAuditTestSegment(1))
	checkpoint.ErasureHeads = []backupartifact.ErasureStreamHead{{
		HashSlot: 0, Sequence: 2,
		CommitKey:    backupartifact.ErasureLedgerCommitKey(namespace, 0, 2),
		CommitSHA256: commitTwoSHA,
	}}
	catalog := &recordingIntegrityAuditCatalog{
		pages: map[uint64]backupartifact.CatalogPage{
			1: {
				Sequence: 1,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-1",
				}},
			},
		},
		checkpoints: map[string]backupartifact.Checkpoint{
			"checkpoint-1": checkpoint,
		},
	}
	plan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: staticIntegrityAuditWindowSource{
				head: &pageReference, retainedRoot: 1,
			},
			Selection: staticIntegrityAuditRetentionSelectionSource{
				retained: []string{"checkpoint-1"},
			},
			Catalog: catalog, HashSlotCount: 2,
			Now: func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	cursor, _, err := plan.Start(context.Background(), nil)
	require.NoError(t, err)
	for {
		target, resolveErr := plan.Resolve(context.Background(), cursor)
		require.NoError(t, resolveErr)
		if !target.Administrative &&
			target.Kind == backupinfra.IntegrityAuditArtifactErasure {
			break
		}
		report := backupinfra.IntegrityAuditArtifactReport{}
		if !target.Administrative {
			report.Segment = backupartifact.SegmentAuditReport{
				Header: backupartifact.SegmentHeader{
					PlaintextBytes: target.Reference.PlaintextBytes,
					Logical: backupartifact.SegmentLogicalDescriptor{
						HashSlot:   cursor.HashSlot,
						Generation: cursor.Generation,
					},
				},
			}
		}
		cursor, _, err = plan.Advance(
			context.Background(), cursor, report,
		)
		require.NoError(t, err)
	}
	require.Equal(t, "erasure-ledger", cursor.Generation)
	target, err := plan.Resolve(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, backupinfra.ErasureIntegrityArtifactCommit, target.Erasure.Kind)
	require.Equal(t, uint64(2), target.Erasure.Sequence)

	recordTwo := backupartifact.ErasureLedgerRecord{
		EventID: eventTwoID, HashSlot: 0,
		Object: backupartifact.ObjectEntry{
			Key:      "objects/erasure-ledger/" + eventTwoID + "/attempt.wkb",
			HashSlot: 0,
			DataKey: backupartifact.DataKeyEnvelope{
				Version: 1, Algorithm: "TEST_XOR",
				KeyID: strings.Repeat("key-sensitive-", 1024),
				Nonce: []byte{1},
				Value: []byte(strings.Repeat("wrapped-sensitive-", 1024)),
			},
		},
	}
	commitTwo := backupartifact.ErasureLedgerCommit{
		HashSlot: 0, Sequence: 2, EventID: eventTwoID,
		RecordKey:            backupartifact.ErasureLedgerRecordKey(0, eventTwoID),
		RecordSHA256:         strings.Repeat("5", 64),
		PreviousCommitSHA256: commitOneSHA,
	}
	cursor, _, err = plan.Advance(
		context.Background(), cursor,
		backupinfra.IntegrityAuditArtifactReport{
			Erasure: backupinfra.ErasureIntegrityAuditReport{
				Commit: commitTwo,
			},
		},
	)
	require.NoError(t, err)
	target, err = plan.Resolve(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, backupinfra.ErasureIntegrityArtifactReceipt, target.Erasure.Kind)
	cursor, _, err = plan.Advance(
		context.Background(), cursor,
		backupinfra.IntegrityAuditArtifactReport{
			Erasure: backupinfra.ErasureIntegrityAuditReport{
				Commit: commitTwo,
			},
		},
	)
	require.NoError(t, err)
	cursor, _, err = plan.Advance(
		context.Background(), cursor,
		backupinfra.IntegrityAuditArtifactReport{
			Erasure: backupinfra.ErasureIntegrityAuditReport{
				Record: recordTwo,
			},
		},
	)
	require.NoError(t, err)
	require.Less(t, len(cursor.Position), 8<<10)
	rawPosition, err := base64.RawURLEncoding.DecodeString(cursor.Position)
	require.NoError(t, err)
	require.NotContains(t, string(rawPosition), "kms-sensitive")
	require.NotContains(t, string(rawPosition), "wrapped-sensitive")
	cursor, _, err = plan.Advance(
		context.Background(), cursor,
		backupinfra.IntegrityAuditArtifactReport{
			Erasure: backupinfra.ErasureIntegrityAuditReport{
				Event: backupartifact.ErasureLedgerEvent{
					EventID: eventTwoID, HashSlot: 0,
				},
			},
		},
	)
	require.NoError(t, err)
	target, err = plan.Resolve(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, backupinfra.ErasureIntegrityArtifactCommit, target.Erasure.Kind)
	require.Equal(t, uint64(1), target.Erasure.Sequence)
	require.Equal(t, commitOneSHA, target.Erasure.ExpectedCommitSHA256)
	require.Equal(
		t, backupartifact.ErasureLedgerCommitKey(namespace, 0, 1),
		target.Erasure.CommitKey,
	)

	commitOne := backupartifact.ErasureLedgerCommit{
		HashSlot: 0, Sequence: 1, EventID: eventOneID,
		RecordKey:    backupartifact.ErasureLedgerRecordKey(0, eventOneID),
		RecordSHA256: strings.Repeat("6", 64),
	}
	recordOne := backupartifact.ErasureLedgerRecord{
		EventID: eventOneID, HashSlot: 0,
		Object: backupartifact.ObjectEntry{
			Key:      "objects/erasure-ledger/" + eventOneID + "/attempt.wkb",
			HashSlot: 0,
		},
	}
	for _, report := range []backupinfra.IntegrityAuditArtifactReport{
		{Erasure: backupinfra.ErasureIntegrityAuditReport{Commit: commitOne}},
		{Erasure: backupinfra.ErasureIntegrityAuditReport{Commit: commitOne}},
		{Erasure: backupinfra.ErasureIntegrityAuditReport{Record: recordOne}},
		{Erasure: backupinfra.ErasureIntegrityAuditReport{
			Event: backupartifact.ErasureLedgerEvent{
				EventID: eventOneID, HashSlot: 0,
			},
		}},
	} {
		cursor, _, err = plan.Advance(
			context.Background(), cursor, report,
		)
		require.NoError(t, err)
	}
	require.Equal(t, backupcontract.IntegrityAuditPhaseComplete, cursor.Phase)
}

func TestCatalogSegmentIntegrityAuditPlanContinuesAfterDualBadErasureCommit(t *testing.T) {
	now := time.Unix(24*60*60, 0)
	pageReference := catalogAuditTestPageReference(1, "checkpoint-1")
	namespace := strings.Repeat("d", 64)
	checkpoint := backupartifact.Checkpoint{
		HashSlotCount: 2,
		Slots: []backupartifact.CheckpointSlot{
			{HashSlot: 0, Generation: "slot-generation-1"},
			{HashSlot: 1, Generation: "slot-generation-1"},
		},
		ErasureHeads: []backupartifact.ErasureStreamHead{
			{
				HashSlot: 0, Sequence: 1,
				CommitKey: backupartifact.ErasureLedgerCommitKey(
					namespace, 0, 1,
				),
				CommitSHA256: strings.Repeat("1", 64),
			},
			{
				HashSlot: 1, Sequence: 1,
				CommitKey: backupartifact.ErasureLedgerCommitKey(
					namespace, 1, 1,
				),
				CommitSHA256: strings.Repeat("2", 64),
			},
		},
	}
	catalog := &recordingIntegrityAuditCatalog{
		pages: map[uint64]backupartifact.CatalogPage{
			1: {
				Sequence: 1,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-1",
				}},
			},
		},
		checkpoints: map[string]backupartifact.Checkpoint{
			"checkpoint-1": checkpoint,
		},
	}
	plan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: staticIntegrityAuditWindowSource{
				head: &pageReference, retainedRoot: 1,
			},
			Selection: staticIntegrityAuditRetentionSelectionSource{
				retained: []string{"checkpoint-1"},
			},
			Catalog: catalog, HashSlotCount: 2,
			Now: func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	cursor, _, err := plan.Start(context.Background(), nil)
	require.NoError(t, err)
	for {
		target, resolveErr := plan.Resolve(context.Background(), cursor)
		require.NoError(t, resolveErr)
		if !target.Administrative {
			break
		}
		cursor, _, err = plan.Advance(
			context.Background(), cursor,
			backupinfra.IntegrityAuditArtifactReport{},
		)
		require.NoError(t, err)
	}
	require.Equal(t, uint16(0), cursor.HashSlot)
	cursor, _, err = plan.Advance(
		context.Background(), cursor,
		backupinfra.IntegrityAuditArtifactReport{},
	)
	require.NoError(t, err)
	target, err := plan.Resolve(context.Background(), cursor)
	require.NoError(t, err)
	require.Equal(t, backupinfra.IntegrityAuditArtifactErasure, target.Kind)
	require.Equal(t, uint16(1), cursor.HashSlot)
	require.Equal(t, uint64(1), target.Erasure.Sequence)
}

func TestCatalogSegmentIntegrityAuditPlanSkipsHoldPagesWithBoundedSteps(t *testing.T) {
	now := time.Unix(24*60*60, 0)
	firstReference := catalogAuditTestSegment(1)
	secondReference := catalogAuditTestSegment(2)
	firstPageReference := catalogAuditTestPageReference(1, "checkpoint-1")
	secondPageReference := catalogAuditTestPageReference(2, "checkpoint-2")
	holdPageReference := catalogAuditTestPageReference(3, "checkpoint-1")
	catalog := &recordingIntegrityAuditCatalog{
		pages: map[uint64]backupartifact.CatalogPage{
			1: {
				Sequence: 1,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-1",
				}},
			},
			2: {
				Sequence: 2, Previous: &firstPageReference,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-2",
				}},
			},
			3: {
				Sequence: 3, Previous: &secondPageReference,
				Entries: []backupartifact.CatalogCheckpointReference{{
					ID: "checkpoint-1", Held: true, StateOnly: true,
				}},
			},
		},
		checkpoints: map[string]backupartifact.Checkpoint{
			"checkpoint-1": catalogAuditTestCheckpoint(firstReference),
			"checkpoint-2": catalogAuditTestCheckpoint(secondReference),
		},
	}
	plan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: staticIntegrityAuditWindowSource{head: &holdPageReference, retainedRoot: 1},
			Selection: staticIntegrityAuditRetentionSelectionSource{
				retained: []string{"checkpoint-1", "checkpoint-2"},
			},
			Catalog: catalog, HashSlotCount: 2,
			Now: func() time.Time { return now },
		},
	)
	require.NoError(t, err)
	previous := backupcontract.IntegrityAuditCursor{
		CycleID: "complete-1", ScrubEpoch: 2, CatalogSequence: 1,
		CatalogRootSequence: 1,
		Generation:          "complete", Position: "complete",
		Phase: backupcontract.IntegrityAuditPhaseComplete,
	}

	cursor, _, err := plan.Start(context.Background(), &previous)
	require.NoError(t, err)
	navigationSteps := 0
	for {
		target, resolveErr := plan.Resolve(context.Background(), cursor)
		require.NoError(t, resolveErr)
		if !target.Administrative {
			require.Equal(t, secondReference, target.Reference)
			break
		}
		navigationSteps++
		cursor, _, err = plan.Advance(
			context.Background(), cursor, backupinfra.IntegrityAuditArtifactReport{},
		)
		require.NoError(t, err)
	}
	require.Equal(t, 3, navigationSteps)
	require.Equal(t, 3, catalog.pageLoads)
	require.Equal(t, 2, catalog.checkpointLoads)

	next, _, err := plan.Advance(
		context.Background(),
		cursor,
		backupinfra.IntegrityAuditArtifactReport{
			Segment: backupartifact.SegmentAuditReport{
				Header: backupartifact.SegmentHeader{
					PlaintextBytes: secondReference.PlaintextBytes,
					Logical: backupartifact.SegmentLogicalDescriptor{
						HashSlot: 0, Generation: "slot-generation-1",
					},
				},
				Previous: &firstReference,
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseComplete, next.Phase)
	require.Equal(t, 2, catalog.checkpointLoads)
}

type staticIntegrityAuditWindowSource struct {
	head         *backupartifact.CatalogPageReference
	retainedRoot uint64
}

type staticIntegrityAuditRetentionSelectionSource struct {
	retained []string
}

func (s staticIntegrityAuditRetentionSelectionSource) LoadIntegrityAuditRetentionSelection(
	_ context.Context,
	request backupinfra.IntegrityAuditRetentionSelectionRequest,
) (backupinfra.IntegrityAuditRetentionSelection, error) {
	activeRestoreCheckpointID := ""
	if request.ActiveRestoreCheckpointID != nil {
		activeRestoreCheckpointID = *request.ActiveRestoreCheckpointID
	}
	references := make(
		[]backupartifact.CatalogCheckpointReference,
		0, len(s.retained),
	)
	for _, checkpointID := range s.retained {
		references = append(
			references,
			backupartifact.CatalogCheckpointReference{ID: checkpointID},
		)
	}
	return backupinfra.NewIntegrityAuditRetentionSelection(
		request.Head, request.At, activeRestoreCheckpointID, references,
	)
}

func (s staticIntegrityAuditWindowSource) LoadIntegrityAuditCatalogWindow(
	context.Context,
) (backupinfra.IntegrityAuditCatalogWindow, error) {
	if s.head == nil {
		return backupinfra.IntegrityAuditCatalogWindow{}, nil
	}
	head := *s.head
	return backupinfra.IntegrityAuditCatalogWindow{
		Head: &head, RetainedRootSequence: s.retainedRoot,
	}, nil
}

type recordingIntegrityAuditCatalog struct {
	pages           map[uint64]backupartifact.CatalogPage
	checkpoints     map[string]backupartifact.Checkpoint
	pageLoads       int
	checkpointLoads int
}

func (c *recordingIntegrityAuditCatalog) LoadPageForIntegrityAudit(
	_ context.Context,
	reference backupartifact.CatalogPageReference,
) (backupartifact.CatalogPage, error) {
	c.pageLoads++
	page, found := c.pages[reference.Sequence]
	if !found {
		return backupartifact.CatalogPage{}, backupartifact.ErrObjectNotFound
	}
	return page, nil
}

func (c *recordingIntegrityAuditCatalog) LoadCheckpointForIntegrityAudit(
	_ context.Context,
	reference backupartifact.CatalogCheckpointReference,
) (backupartifact.Checkpoint, error) {
	c.checkpointLoads++
	checkpoint, found := c.checkpoints[reference.ID]
	if !found {
		return backupartifact.Checkpoint{}, backupartifact.ErrObjectNotFound
	}
	return checkpoint, nil
}

func catalogAuditTestCheckpoint(
	metadata backupartifact.SegmentReference,
) backupartifact.Checkpoint {
	return backupartifact.Checkpoint{
		HashSlotCount: 2,
		Slots: []backupartifact.CheckpointSlot{
			{
				HashSlot: 0, Generation: "slot-generation-1",
				Metadata: backupartifact.CheckpointStream{
					Sequence: 1, Head: &metadata,
				},
			},
			{HashSlot: 1, Generation: "slot-generation-1"},
		},
	}
}

func catalogAuditTestSegment(id int) backupartifact.SegmentReference {
	segmentID := fmt.Sprintf("%064x", id)
	return backupartifact.SegmentReference{
		SegmentID:      segmentID,
		CommitKey:      "segments/" + segmentID + "/commit.json",
		CommitSHA256:   strings.Repeat("a", 64),
		PlaintextBytes: 64,
	}
}

func catalogAuditTestPageReference(
	sequence uint64,
	checkpointID string,
) backupartifact.CatalogPageReference {
	return backupartifact.CatalogPageReference{
		Sequence: sequence,
		Key:      fmt.Sprintf("catalog/%020d-%s.json", sequence, checkpointID),
		SHA256:   strings.Repeat("b", 64), Bytes: 512,
		LatestCheckpointID: checkpointID,
	}
}
