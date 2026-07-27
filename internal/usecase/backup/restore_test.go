package backup_test

import (
	"context"
	"crypto/ed25519"
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
	_, signingKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := restoreTestSigner{privateKey: signingKey}
	cleaner := &fakeRestoreCleaner{}
	app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store,
		Inspector: fakeRestoreInspector{inspection: backupusecase.RestoreInspection{
			CheckpointID: proof.Checkpoint.ID, CheckpointSHA256: proof.Checkpoint.SHA256,
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
		Verifier: fakeRestoreVerifier{}, Cleaner: cleaner,
		ActivationVerifier: signer,
		Now:                func() time.Time { return now }, NewPlanID: func() string { return "plan-7" },
	})
	if err != nil {
		t.Fatalf("NewRestoreApp(): %v", err)
	}
	plan, err := app.Plan(context.Background(), backupusecase.RestorePlanRequest{
		CheckpointID:     proof.Checkpoint.ID,
		CatalogHeadToken: restoreCatalogHeadToken(t, proof.Head),
	})
	if err != nil || plan.Status != backupusecase.RestoreStatusPlanned {
		t.Fatalf("Plan() plan=%+v err=%v", plan, err)
	}
	if plan.ErasureLedgerVersion != backupartifact.ErasureLedgerSnapshotVersion || plan.ErasureEventCount != 7 || plan.ErasureLedgerSHA256 != strings.Repeat("e", 64) {
		t.Fatalf("Plan() erasure ledger fence = version:%d count:%d sha:%q", plan.ErasureLedgerVersion, plan.ErasureEventCount, plan.ErasureLedgerSHA256)
	}
	if _, err := app.Plan(context.Background(), backupusecase.RestorePlanRequest{
		CheckpointID:     proof.Checkpoint.ID,
		CatalogHeadToken: restoreCatalogHeadToken(t, proof.Head),
	}); err == nil {
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
	if _, err := app.Activate(
		context.Background(), plan.ID,
		backupusecase.RestoreActivationRequest{Operator: "recovery-admin"},
	); err == nil {
		t.Fatal("Activate() without reviewed evidence error = nil")
	}
	fenceRecord := backupartifact.SourceFenceRecord{
		Format:  backupartifact.SourceFenceReceiptFormat,
		Version: backupartifact.SourceFenceReceiptVersion,
		ID:      "source-fence-7", SourceClusterID: plan.SourceClusterID,
		SourceGeneration: plan.SourceGeneration, RestorePlanID: plan.ID,
		CheckpointID:            plan.CheckpointID,
		CheckpointSHA256:        plan.CheckpointSHA256,
		TargetClusterID:         plan.TargetClusterID,
		TargetGeneration:        plan.TargetGeneration,
		FenceControllerRevision: 9,
		RequestedAtUnixMillis:   now.Add(-time.Minute).UnixMilli(),
		ConvergedAtUnixMillis:   now.Add(-time.Second).UnixMilli(),
	}
	receipt, err := backupartifact.SignSourceFenceReceipt(
		context.Background(), fenceRecord,
		signer,
	)
	if err != nil {
		t.Fatal(err)
	}
	tampered := receipt
	tampered.RestorePlanID = "other-plan"
	if _, err := app.Activate(
		context.Background(), plan.ID,
		backupusecase.RestoreActivationRequest{
			SourceFenceReceipt: &tampered, Operator: "recovery-admin",
		},
	); err == nil {
		t.Fatal("Activate() accepted a tampered receipt")
	}
	otherPlanRecord := fenceRecord
	otherPlanRecord.ID = "source-fence-other-plan"
	otherPlanRecord.RestorePlanID = "other-plan"
	otherPlanReceipt, err := backupartifact.SignSourceFenceReceipt(
		context.Background(), otherPlanRecord,
		signer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Activate(
		context.Background(), plan.ID,
		backupusecase.RestoreActivationRequest{
			SourceFenceReceipt: &otherPlanReceipt,
			Operator:           "recovery-admin",
		},
	); !errors.Is(err, backupusecase.ErrActivationEvidenceRequired) {
		t.Fatalf(
			"Activate(valid receipt for another plan) error = %v",
			err,
		)
	}
	staleCheckpointRecord := fenceRecord
	staleCheckpointRecord.ID = "source-fence-stale-checkpoint"
	staleCheckpointRecord.CheckpointID = "checkpoint-obsolete"
	staleCheckpointRecord.CheckpointSHA256 = strings.Repeat("b", 64)
	staleCheckpointReceipt, err := backupartifact.SignSourceFenceReceipt(
		context.Background(), staleCheckpointRecord,
		signer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Activate(
		context.Background(), plan.ID,
		backupusecase.RestoreActivationRequest{
			SourceFenceReceipt: &staleCheckpointReceipt,
			Operator:           "recovery-admin",
		},
	); !errors.Is(err, backupusecase.ErrActivationEvidenceRequired) {
		t.Fatalf(
			"Activate(valid stale-checkpoint receipt) error = %v",
			err,
		)
	}
	request := backupusecase.RestoreActivationRequest{
		SourceFenceReceipt: &receipt, Operator: "recovery-admin",
	}
	plan, err = app.Activate(context.Background(), plan.ID, request)
	if err != nil || plan.Status != backupusecase.RestoreStatusActivated {
		t.Fatalf("Activate() plan=%+v err=%v", plan, err)
	}
	if plan.Activation == nil ||
		plan.Activation.Kind != backupartifact.RestoreActivationSourceFence {
		t.Fatalf("Activate() evidence = %#v", plan.Activation)
	}
	if cleaner.calls != 1 ||
		plan.StagingCleanupCompletedAtUnixMillis <= 0 {
		t.Fatalf(
			"activation cleanup calls=%d completed_at=%d",
			cleaner.calls, plan.StagingCleanupCompletedAtUnixMillis,
		)
	}
	plan, err = app.Activate(context.Background(), plan.ID, request)
	if err != nil || plan.Status != backupusecase.RestoreStatusActivated {
		t.Fatalf("idempotent Activate() plan=%+v err=%v", plan, err)
	}
}

func TestRestoreActivationBreakGlassPersistsImmutableAudit(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	part := completeRestorePartition(
		backupusecase.RestorePartition{
			HashSlot: 0, Status: backupusecase.RestorePartitionConverged,
			ReplicaCount: 1,
		},
		now.UnixMilli(),
	)
	part.Verified = true
	store := &memoryRestoreStore{state: backupusecase.RestoreState{
		Plan: &backupusecase.RestorePlan{
			ID: "plan-break-glass", CheckpointID: "checkpoint-1",
			CheckpointSHA256: strings.Repeat("a", 64),
			SourceClusterID:  "source", SourceGeneration: "source-gen",
			TargetClusterID: "target", TargetGeneration: "target-gen",
			HashSlotCount: 1, Status: backupusecase.RestoreStatusVerified,
			CreatedAtUnixMillis:  now.Add(-time.Hour).UnixMilli(),
			UpdatedAtUnixMillis:  now.Add(-time.Minute).UnixMilli(),
			VerifiedAtUnixMillis: now.Add(-time.Minute).UnixMilli(),
			Partitions:           []backupusecase.RestorePartition{part},
		},
	}}
	app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store, Inspector: fakeRestoreInspector{},
		Verifier: fakeRestoreVerifier{}, Cleaner: &fakeRestoreCleaner{},
		Now:        func() time.Time { return now },
		NewPlanID:  func() string { return "unused" },
		NewAuditID: func() string { return "break-glass-audit-1" },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := backupusecase.RestoreActivationRequest{
		BreakGlassReason: "The source cluster and every Controller disk were physically destroyed.",
		Operator:         "incident-commander",
	}
	plan, err := app.Activate(context.Background(), store.state.Plan.ID, request)
	if err != nil {
		t.Fatalf("Activate(break-glass): %v", err)
	}
	if plan.Activation == nil ||
		plan.Activation.Kind != backupartifact.RestoreActivationBreakGlass ||
		plan.Activation.BreakGlass == nil ||
		plan.Activation.BreakGlass.Operator != request.Operator ||
		plan.Activation.BreakGlass.Reason != request.BreakGlassReason {
		t.Fatalf("break-glass audit = %#v", plan.Activation)
	}
	if _, err := app.Activate(
		context.Background(), plan.ID,
		backupusecase.RestoreActivationRequest{
			BreakGlassReason: "A different unreviewed reason that must not replace the audit.",
			Operator:         request.Operator,
		},
	); !errors.Is(err, backupusecase.ErrRestoreTransition) {
		t.Fatalf("Activate(replace audit) = %v", err)
	}
	retry, err := app.Activate(context.Background(), plan.ID, request)
	if err != nil || retry.Activation.EvidenceSHA256 != plan.Activation.EvidenceSHA256 {
		t.Fatalf("Activate(idempotent break-glass) = %#v, %v", retry.Activation, err)
	}
}

func TestRestoreActivationResumesCleanupWithoutReplacingAudit(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	part := completeRestorePartition(
		backupusecase.RestorePartition{
			HashSlot: 0, Status: backupusecase.RestorePartitionConverged,
			ReplicaCount: 1,
		},
		now.UnixMilli(),
	)
	part.Verified = true
	store := &memoryRestoreStore{state: backupusecase.RestoreState{
		Plan: &backupusecase.RestorePlan{
			ID: "plan-cleanup-resume", CheckpointID: "checkpoint-1",
			CheckpointSHA256: strings.Repeat("a", 64),
			SourceClusterID:  "source", SourceGeneration: "source-gen",
			TargetClusterID: "target", TargetGeneration: "target-gen",
			HashSlotCount: 1, Status: backupusecase.RestoreStatusVerified,
			CreatedAtUnixMillis:  now.Add(-time.Hour).UnixMilli(),
			UpdatedAtUnixMillis:  now.Add(-time.Minute).UnixMilli(),
			VerifiedAtUnixMillis: now.Add(-time.Minute).UnixMilli(),
			Partitions:           []backupusecase.RestorePartition{part},
		},
	}}
	cleanupErr := errors.New("node 3 is temporarily unavailable")
	cleaner := &fakeRestoreCleaner{err: cleanupErr}
	auditIDs := 0
	app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store, Inspector: fakeRestoreInspector{},
		Verifier: fakeRestoreVerifier{}, Cleaner: cleaner,
		Now:       func() time.Time { return now },
		NewPlanID: func() string { return "unused" },
		NewAuditID: func() string {
			auditIDs++
			return "break-glass-audit-resume"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := backupusecase.RestoreActivationRequest{
		BreakGlassReason: "All source Controller disks are unrecoverable.",
		Operator:         "incident-commander",
	}
	intermediate, err := app.Activate(
		context.Background(), store.state.Plan.ID, request,
	)
	if !errors.Is(err, cleanupErr) ||
		intermediate.Status != backupusecase.RestoreStatusActivating ||
		intermediate.Activation == nil ||
		intermediate.Activation.BreakGlass == nil {
		t.Fatalf("Activate(cleanup failure) plan=%#v err=%v", intermediate, err)
	}
	auditID := intermediate.Activation.BreakGlass.ID
	cleaner.err = nil
	finished, err := app.Activate(
		context.Background(), store.state.Plan.ID, request,
	)
	if err != nil || finished.Status != backupusecase.RestoreStatusActivated ||
		finished.Activation == nil || finished.Activation.BreakGlass == nil ||
		finished.Activation.BreakGlass.ID != auditID ||
		finished.StagingCleanupCompletedAtUnixMillis <= 0 ||
		auditIDs != 1 || cleaner.calls != 2 {
		t.Fatalf(
			"Activate(resume) plan=%#v err=%v auditIDs=%d cleanup=%d",
			finished, err, auditIDs, cleaner.calls,
		)
	}
}

func TestRestoreActivationRejectsIncompleteFinalEvidence(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	partitions := make([]backupusecase.RestorePartition, 2)
	for index := range partitions {
		partitions[index] = completeRestorePartition(
			backupusecase.RestorePartition{
				HashSlot: uint16(index), Status: backupusecase.RestorePartitionConverged,
				ReplicaCount: 2,
			},
			now.Add(-time.Minute).UnixMilli(),
		)
		partitions[index].Verified = true
	}
	erasureErr := errors.New("erasure ledger was not applied through the selected checkpoint")
	tests := []struct {
		name   string
		verify func(backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error)
	}{
		{
			name: "missing hash slot",
			verify: func(plan backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error) {
				return append([]backupusecase.RestorePartition(nil), plan.Partitions[:1]...), nil
			},
		},
		{
			name: "digest mismatch",
			verify: func(plan backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error) {
				result := append([]backupusecase.RestorePartition(nil), plan.Partitions...)
				result[0].ContentSHA256 = strings.Repeat("d", 64)
				return result, nil
			},
		},
		{
			name: "erasure ledger not applied",
			verify: func(backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error) {
				return nil, erasureErr
			},
		},
		{
			name: "replica not converged",
			verify: func(plan backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error) {
				result := append([]backupusecase.RestorePartition(nil), plan.Partitions...)
				result[1].ConvergedReplicas--
				return result, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &memoryRestoreStore{state: backupusecase.RestoreState{
				Plan: &backupusecase.RestorePlan{
					ID: "plan-final-evidence", CheckpointID: "checkpoint-final-evidence",
					CheckpointSHA256: strings.Repeat("a", 64),
					SourceClusterID:  "source", SourceGeneration: "source-gen",
					TargetClusterID: "target", TargetGeneration: "target-gen",
					HashSlotCount: 2, Status: backupusecase.RestoreStatusVerified,
					CreatedAtUnixMillis:  now.Add(-time.Hour).UnixMilli(),
					UpdatedAtUnixMillis:  now.Add(-time.Minute).UnixMilli(),
					VerifiedAtUnixMillis: now.Add(-time.Minute).UnixMilli(),
					Partitions:           append([]backupusecase.RestorePartition(nil), partitions...),
				},
			}}
			cleaner := &fakeRestoreCleaner{}
			app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
				Enabled: true, Store: store, Inspector: fakeRestoreInspector{},
				Verifier: restoreVerifierFunc(test.verify), Cleaner: cleaner,
				Now:       func() time.Time { return now },
				NewPlanID: func() string { return "unused" },
				NewAuditID: func() string {
					return "break-glass-final-evidence"
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			_, err = app.Activate(
				context.Background(), store.state.Plan.ID,
				backupusecase.RestoreActivationRequest{
					BreakGlassReason: "The source Controller quorum is permanently unrecoverable.",
					Operator:         "incident-commander",
				},
			)

			if err == nil {
				t.Fatal("Activate() error = nil")
			}
			if store.state.Plan.Status != backupusecase.RestoreStatusVerified {
				t.Fatalf("plan status = %q, want verified", store.state.Plan.Status)
			}
			if cleaner.calls != 0 {
				t.Fatalf("cleanup calls = %d, want 0", cleaner.calls)
			}
		})
	}
}

func TestCheckpointRestorePersistsLeaderAttemptAndConvergenceEvidence(t *testing.T) {
	now := time.UnixMilli(1_753_400_210_000).UTC()
	proof := restoreTestCatalogProof()
	store := &memoryRestoreStore{}
	app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store,
		Inspector: fakeRestoreInspector{inspection: backupusecase.RestoreInspection{
			CheckpointID:                    proof.Checkpoint.ID,
			CheckpointSHA256:                proof.Checkpoint.SHA256,
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
		Verifier: fakeRestoreVerifier{}, Cleaner: &fakeRestoreCleaner{},
		Now:       func() time.Time { return now },
		NewPlanID: func() string { return "checkpoint-plan" },
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := app.Plan(context.Background(), backupusecase.RestorePlanRequest{
		CheckpointID:     proof.Checkpoint.ID,
		CatalogHeadToken: restoreCatalogHeadToken(t, proof.Head),
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
		Cleaner: &fakeRestoreCleaner{},
		Now:     func() time.Time { return now }, NewPlanID: func() string { return "unused" },
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

func (f fakeRestoreInspector) Inspect(context.Context, backupusecase.RestoreInspectRequest) (backupusecase.RestoreInspection, error) {
	return f.inspection, nil
}

func restoreCatalogHeadToken(
	t *testing.T,
	head backupartifact.CatalogPageReference,
) string {
	t.Helper()
	token, err := backupusecase.EncodeCatalogHeadToken(head)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type fakeRestoreVerifier struct{}

func (fakeRestoreVerifier) VerifyRestore(_ context.Context, plan backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error) {
	result := append([]backupusecase.RestorePartition(nil), plan.Partitions...)
	for index := range result {
		result[index].Verified = true
	}
	return result, nil
}

type restoreVerifierFunc func(
	backupusecase.RestorePlan,
) ([]backupusecase.RestorePartition, error)

func (f restoreVerifierFunc) VerifyRestore(
	_ context.Context,
	plan backupusecase.RestorePlan,
) ([]backupusecase.RestorePartition, error) {
	return f(plan)
}

type fakeRestoreCleaner struct {
	calls int
	err   error
}

func (f *fakeRestoreCleaner) CleanupRestoreStaging(
	_ context.Context,
	_ backupusecase.RestorePlan,
) error {
	f.calls++
	return f.err
}

type restoreTestSigner struct {
	privateKey ed25519.PrivateKey
}

func (s restoreTestSigner) Sign(
	_ context.Context,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	return backupartifact.ManifestSignature{
		Algorithm: "ed25519", KeyID: "ed25519:test",
		Value: ed25519.Sign(s.privateKey, message),
	}, nil
}

func (s restoreTestSigner) Verify(
	_ context.Context,
	signature backupartifact.ManifestSignature,
	message []byte,
) error {
	publicKey := s.privateKey.Public().(ed25519.PublicKey)
	if signature.Algorithm != "ed25519" ||
		!ed25519.Verify(publicKey, message, signature.Value) {
		return backupartifact.ErrInvalidSignature
	}
	return nil
}
