package backup_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestRestoreLifecycleRequiresEmptyFreshGenerationAndFence(t *testing.T) {
	store := &memoryRestoreStore{}
	now := time.Unix(1_800_000_000, 0)
	proof := restoreTestCatalogProofForSlots(2)
	app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store,
		Inspector: fakeRestoreInspector{inspection: backupusecase.RestoreInspection{
			RestorePointID: proof.Checkpoint.ID, ManifestSHA256: proof.Checkpoint.SHA256,
			CatalogProof: &proof, CheckpointVersion: backupartifact.CheckpointVersion,
			CheckpointCreatedAtUnixMillis:   proof.Checkpoint.CreatedAtUnixMillis,
			CheckpointEffectiveAtUnixMillis: proof.Checkpoint.EffectiveAtUnixMillis,
			SourceClusterID:                 "old", SourceGeneration: "old-gen", TargetClusterID: "new", TargetGeneration: "new-gen",
			HashSlotCount: 2, TargetEmpty: true,
			ErasureLedgerVersion: backupartifact.ErasureLedgerSnapshotVersion, ErasureEventCount: 7,
			ErasureHeads: []backupartifact.ErasureStreamHead{{
				HashSlot: 0, Sequence: 7, CommitKey: backupartifact.ErasureLedgerCommitKey(strings.Repeat("e", 64), 0, 7), CommitSHA256: strings.Repeat("f", 64),
			}},
			ErasureLedgerSHA256: strings.Repeat("e", 64),
		}},
		Verifier: fakeRestoreVerifier{}, Now: func() time.Time { return now }, NewPlanID: func() string { return "plan-7" },
	})
	if err != nil {
		t.Fatalf("NewRestoreApp(): %v", err)
	}
	plan, err := app.Plan(context.Background(), backupusecase.RestorePlanRequest{RestorePointID: proof.Checkpoint.ID, Repository: "primary"})
	if err != nil || plan.Status != backupusecase.RestoreStatusPlanned {
		t.Fatalf("Plan() plan=%+v err=%v", plan, err)
	}
	if plan.ErasureLedgerVersion != backupartifact.ErasureLedgerSnapshotVersion || plan.ErasureEventCount != 7 || plan.ErasureLedgerSHA256 != strings.Repeat("e", 64) {
		t.Fatalf("Plan() erasure ledger fence = version:%d count:%d sha:%q", plan.ErasureLedgerVersion, plan.ErasureEventCount, plan.ErasureLedgerSHA256)
	}
	if _, err := app.Plan(context.Background(), backupusecase.RestorePlanRequest{RestorePointID: proof.Checkpoint.ID, Repository: "primary"}); err == nil {
		t.Fatal("second Plan() error = nil")
	}
	plan, err = app.Start(context.Background(), plan.ID)
	if err != nil || plan.Status != backupusecase.RestoreStatusInstalling {
		t.Fatalf("Start() plan=%+v err=%v", plan, err)
	}
	plan, err = app.Start(context.Background(), plan.ID)
	if err != nil || plan.Status != backupusecase.RestoreStatusInstalling {
		t.Fatalf("idempotent Start() plan=%+v err=%v", plan, err)
	}
	for hashSlot := uint16(0); hashSlot < 2; hashSlot++ {
		plan, err = app.BeginPartitionInstall(
			context.Background(), plan.ID,
			backupusecase.RestorePartitionAssignment{
				HashSlot: hashSlot, TargetSlotID: uint32(hashSlot) + 1,
				LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1,
				ReplicaCount: 1,
			},
		)
		if err != nil {
			t.Fatalf("BeginPartitionInstall(%d): %v", hashSlot, err)
		}
		report := completeRestorePartition(plan.Partitions[hashSlot], now.UnixMilli())
		plan, err = app.ReportPartitionProgress(context.Background(), plan.ID, report)
		if err != nil {
			t.Fatalf("ReportPartitionProgress(%d): %v", hashSlot, err)
		}
		if hashSlot == 0 {
			if _, err := app.ReportPartitionProgress(context.Background(), plan.ID, report); err != nil {
				t.Fatalf("idempotent ReportPartitionProgress(%d): %v", hashSlot, err)
			}
		}
	}
	if plan.Status != backupusecase.RestoreStatusInstalled {
		t.Fatalf("installed status = %q", plan.Status)
	}
	plan, err = app.Verify(context.Background(), plan.ID)
	if err != nil || plan.Status != backupusecase.RestoreStatusVerified {
		t.Fatalf("Verify() plan=%+v err=%v", plan, err)
	}
	if _, err := app.Activate(context.Background(), plan.ID, "dns-changed"); err == nil {
		t.Fatal("Activate() without cryptographic fence digest error = nil")
	}
	plan, err = app.Activate(context.Background(), plan.ID, strings.Repeat("f", 64))
	if err != nil || plan.Status != backupusecase.RestoreStatusActivated {
		t.Fatalf("Activate() plan=%+v err=%v", plan, err)
	}
	if _, err := app.Activate(context.Background(), plan.ID, strings.Repeat("F", 64)); err == nil {
		t.Fatal("Activate() uppercase digest error = nil")
	}
	plan, err = app.Activate(context.Background(), plan.ID, strings.Repeat("f", 64))
	if err != nil || plan.Status != backupusecase.RestoreStatusActivated {
		t.Fatalf("idempotent Activate() plan=%+v err=%v", plan, err)
	}
}

func TestCheckpointRestorePersistsLeaderAttemptAndConvergenceEvidence(t *testing.T) {
	now := time.UnixMilli(1_753_400_210_000).UTC()
	proof := restoreTestCatalogProof()
	store := &memoryRestoreStore{}
	app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store,
		Inspector: fakeRestoreInspector{inspection: backupusecase.RestoreInspection{
			RestorePointID:                  proof.Checkpoint.ID,
			ManifestSHA256:                  proof.Checkpoint.SHA256,
			CatalogProof:                    &proof,
			CheckpointVersion:               backupartifact.CheckpointVersion,
			CheckpointCreatedAtUnixMillis:   proof.Checkpoint.CreatedAtUnixMillis,
			CheckpointEffectiveAtUnixMillis: proof.Checkpoint.EffectiveAtUnixMillis,
			SourceClusterID:                 "cluster-source",
			SourceGeneration:                "source-generation-1",
			TargetClusterID:                 "cluster-target",
			TargetGeneration:                "target-generation-2",
			HashSlotCount:                   1, TargetEmpty: true,
			ErasureLedgerVersion: backupartifact.ErasureLedgerSnapshotVersion,
			ErasureLedgerSHA256:  backupartifact.EmptyErasureLedgerSnapshotSHA256,
		}},
		Verifier: fakeRestoreVerifier{}, Now: func() time.Time { return now },
		NewPlanID: func() string { return "checkpoint-plan" },
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := app.Plan(context.Background(), backupusecase.RestorePlanRequest{
		RestorePointID: proof.Checkpoint.ID, Repository: "primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.CatalogProof == nil ||
		plan.Partitions[0].Status != backupcontract.RestorePartitionPending {
		t.Fatalf("checkpoint plan = %#v", plan)
	}
	plan, err = app.Start(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = app.BeginPartitionInstall(
		context.Background(), plan.ID,
		backupusecase.RestorePartitionAssignment{
			HashSlot: 0, TargetSlotID: 7, LeaderNodeID: 2,
			LeaderTerm: 9, ConfigEpoch: 4, ReplicaCount: 3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	progress := plan.Partitions[0]
	if progress.Status != backupcontract.RestorePartitionInstalling ||
		progress.InstallAttempt != 1 || progress.LeaderNodeID != 2 {
		t.Fatalf("install progress = %#v", progress)
	}
	progress.DownloadedBytes = 64
	plan, err = app.ReportPartitionProgress(context.Background(), plan.ID, progress)
	if err != nil {
		t.Fatal(err)
	}
	regressed := plan.Partitions[0]
	regressed.DownloadedBytes = 63
	if _, err := app.ReportPartitionProgress(
		context.Background(), plan.ID, regressed,
	); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("regressed download progress error = %v", err)
	}
	firstStartedAt := plan.Partitions[0].StartedAtUnixMillis
	now = now.Add(time.Minute)
	plan, err = app.BeginPartitionInstall(
		context.Background(), plan.ID,
		backupusecase.RestorePartitionAssignment{
			HashSlot: 0, TargetSlotID: 7, LeaderNodeID: 3,
			LeaderTerm: 10, ConfigEpoch: 4, ReplicaCount: 3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Partitions[0].InstallAttempt != 2 ||
		plan.Partitions[0].StartedAtUnixMillis != firstStartedAt {
		t.Fatalf("promoted Leader progress = %#v", plan.Partitions[0])
	}

	progress = plan.Partitions[0]
	progress.Status = backupcontract.RestorePartitionConverging
	progress.EvidenceVersion = backupartifact.RestoreEvidenceVersion
	progress.Installed = true
	progress.PlainBytes = 99
	progress.MetadataRecordCount = 3
	progress.MessageCount = 2
	progress.MaxMessageID = 11
	progress.MetadataSHA256 = strings.Repeat("a", 64)
	progress.ContentSHA256 = strings.Repeat("a", 64)
	progress.MessageMerkleSHA256 = strings.Repeat("b", 64)
	progress.ChannelBoundaryCount = 1
	progress.DownloadedBytes = 120
	progress.ReplicatedBytes = 240
	progress.ConvergedReplicas = 2
	progress.InstalledAtUnixMillis = now.UnixMilli()
	plan, err = app.ReportPartitionProgress(context.Background(), plan.ID, progress)
	if err != nil {
		t.Fatal(err)
	}
	regressed = plan.Partitions[0]
	regressed.ConvergedReplicas = 1
	if _, err := app.ReportPartitionProgress(
		context.Background(), plan.ID, regressed,
	); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("regressed convergence progress error = %v", err)
	}
	progress = plan.Partitions[0]
	progress.Status = backupcontract.RestorePartitionConverged
	progress.ConvergedReplicas = 3
	plan, err = app.ReportPartitionProgress(context.Background(), plan.ID, progress)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != backupusecase.RestoreStatusInstalled {
		t.Fatalf("plan status = %q", plan.Status)
	}
}

func TestCheckpointRestoreProgressReportsThroughputAndETA(t *testing.T) {
	now := time.UnixMilli(1_753_400_210_000).UTC()
	store := &memoryRestoreStore{state: backupusecase.RestoreState{
		Plan: &backupusecase.RestorePlan{
			ID: "checkpoint-progress", HashSlotCount: 4,
			Status:              backupusecase.RestoreStatusInstalling,
			CreatedAtUnixMillis: now.Add(-10 * time.Second).UnixMilli(),
			Partitions: []backupusecase.RestorePartition{
				{
					HashSlot: 0, Status: backupcontract.RestorePartitionConverged,
					DownloadedBytes: 1_000, ReplicatedBytes: 2_000,
					StartedAtUnixMillis: now.Add(-10 * time.Second).UnixMilli(),
				},
				{
					HashSlot: 1, Status: backupcontract.RestorePartitionInstalling,
					DownloadedBytes:     500,
					StartedAtUnixMillis: now.Add(-10 * time.Second).UnixMilli(),
				},
				{HashSlot: 2, Status: backupcontract.RestorePartitionPending},
				{HashSlot: 3, Status: backupcontract.RestorePartitionPending},
			},
		},
	}}
	app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store,
		Inspector: fakeRestoreInspector{}, Verifier: fakeRestoreVerifier{},
		Now: func() time.Time { return now }, NewPlanID: func() string { return "unused" },
	})
	if err != nil {
		t.Fatal(err)
	}
	progress, err := app.Progress(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if progress.DownloadedBytes != 1_500 ||
		progress.ReplicatedBytes != 2_000 ||
		progress.ThroughputBytesPerSecond != 150 ||
		progress.ConvergedSlots != 1 ||
		progress.InstallingSlots != 1 ||
		progress.PendingSlots != 2 ||
		progress.ETASeconds == nil || *progress.ETASeconds != 30 {
		t.Fatalf("progress = %#v", progress)
	}
}

func restoreTestCatalogProof() backupartifact.CheckpointCatalogProof {
	return restoreTestCatalogProofForSlots(1)
}

func restoreTestCatalogProofForSlots(
	hashSlotCount uint16,
) backupartifact.CheckpointCatalogProof {
	checkpointID := "checkpoint-1"
	vectorID := strings.Repeat("c", 64)
	entry := backupartifact.CatalogCheckpointReference{
		ID: checkpointID, Key: backupartifact.CheckpointObjectKey(checkpointID),
		SHA256: strings.Repeat("a", 64), Bytes: 100,
		CreatedAtUnixMillis:   1_753_400_201_000,
		EffectiveAtUnixMillis: 1_753_400_200_000,
		GenerationVector: backupartifact.GenerationVectorReference{
			ID: vectorID, Key: backupartifact.GenerationVectorObjectKey(vectorID),
			SHA256: strings.Repeat("d", 64), Bytes: 100,
			HashSlotCount: hashSlotCount,
		},
	}
	page := backupartifact.CatalogPageReference{
		Sequence: 1, Key: backupartifact.CatalogPageObjectKey(1, checkpointID),
		SHA256: strings.Repeat("b", 64), Bytes: 100,
		LatestCheckpointID: checkpointID,
	}
	return backupartifact.CheckpointCatalogProof{
		Head: page, EntryPage: page, Checkpoint: entry,
	}
}

func completeRestorePartition(
	progress backupusecase.RestorePartition,
	installedAt int64,
) backupusecase.RestorePartition {
	progress.Status = backupcontract.RestorePartitionConverged
	progress.EvidenceVersion = backupartifact.RestoreEvidenceVersion
	progress.Installed = true
	progress.MetadataSHA256 = strings.Repeat("a", 64)
	progress.ContentSHA256 = strings.Repeat("b", 64)
	progress.MessageMerkleSHA256 = strings.Repeat("c", 64)
	progress.ConvergedReplicas = progress.ReplicaCount
	progress.InstalledAtUnixMillis = installedAt
	return progress
}

type memoryRestoreStore struct{ state backupusecase.RestoreState }

func (s *memoryRestoreStore) Load(context.Context) (backupusecase.RestoreState, error) {
	return s.state, nil
}
func (s *memoryRestoreStore) CompareAndSwap(_ context.Context, revision uint64, next backupusecase.RestoreState) error {
	if revision != s.state.Revision {
		return backupusecase.ErrStateConflict
	}
	next.Revision = revision + 1
	s.state = next
	return nil
}

type fakeRestoreInspector struct {
	inspection backupusecase.RestoreInspection
}

func (f fakeRestoreInspector) Inspect(context.Context, backupusecase.RestorePlanRequest) (backupusecase.RestoreInspection, error) {
	return f.inspection, nil
}

type fakeRestoreVerifier struct{}

func (fakeRestoreVerifier) VerifyRestore(_ context.Context, plan backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error) {
	result := append([]backupusecase.RestorePartition(nil), plan.Partitions...)
	for index := range result {
		result[index].Verified = true
	}
	return result, nil
}
