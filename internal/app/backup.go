package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultCheckpointRestoreMemoryMaxBytes  = 512 << 20
	checkpointRestoreStagingDirectory       = "checkpoint-segments"
	checkpointRestoreTargetStagingDirectory = "checkpoint-target"
)

type appBackupNode interface {
	backupinfra.CoordinationController
	backupinfra.PartitionPlanNode
	backupinfra.LocalMessageSnapshotNode
	backupinfra.CaptureAuthorityNode
	backupinfra.MetadataLogNode
	backupinfra.MessageLogNode
	backupinfra.SourcePinNode
	runtimebackup.CoordinatorLeadership
	accessnode.PresenceRPCNode
	nodeRPCRegistrar
}

type appRestoreNode interface {
	backupinfra.RestoreCoordinationController
	backupinfra.RestoreTargetClusterNode
	backupinfra.RestoreInstallClusterNode
	backupinfra.CheckpointRestoreReplicaNode
	runtimebackup.CoordinatorLeadership
	accessnode.PresenceRPCNode
	nodeRPCRegistrar
}

type appBackupRepository interface {
	backupartifact.Repository
	backupinfra.RepositoryDoctor
}

type appBackupKeyService interface {
	backupartifact.DataKeyManager
	backupartifact.ManifestSigner
	backupinfra.KMSDoctor
}

var (
	loadAppBackupRepository = func(
		ctx context.Context,
		name, endpoint, region, bucket, prefix string,
		objectLockDays int,
		accessRoleARN string,
	) (appBackupRepository, error) {
		return backupinfra.LoadOSSRepository(
			ctx, name, endpoint, region, bucket, prefix, objectLockDays,
			accessRoleARN,
		)
	}
	loadAppBackupKeyService = func(
		ctx context.Context,
		region, endpoint, roleARN string,
	) (appBackupKeyService, error) {
		return backupinfra.LoadAlibabaKMSAdapter(
			ctx, region, endpoint, roleARN,
		)
	}
	decorateAppBackupSourcePinManager = func(
		manager runtimebackup.SourcePinManager,
	) runtimebackup.SourcePinManager {
		return manager
	}
	loadAppBackupRepairRepository = func(
		ctx context.Context,
		repository appBackupRepository,
		endpoint, region, roleARN string,
	) (backupartifact.RepairRepository, error) {
		ossRepository, ok := repository.(*backupinfra.OSSRepository)
		if !ok {
			return nil, fmt.Errorf(
				"backup app: Alibaba repair requires an OSS repository",
			)
		}
		return backupinfra.LoadOSSRepairRepository(
			ctx, ossRepository, endpoint, region, roleARN,
		)
	}
	loadAppBackupGarbageRepository = func(
		ctx context.Context,
		name, endpoint, region, bucket, prefix string,
		objectLockDays int,
		roleARN string,
		probeSlot uint64,
	) (backupinfra.GenerationGarbageRepository, error) {
		return backupinfra.LoadOSSGarbageRepository(
			ctx, name, endpoint, region, bucket, prefix,
			objectLockDays, roleARN, probeSlot,
		)
	}
	newAppBackupClockProbe = func(endpoint string) (backupinfra.ClockProbe, error) {
		return backupinfra.NewEndpointClockProbe(endpoint, nil)
	}
)

func (a *App) wireBackup(clusterCfg cluster.Config) {
	if a == nil || (!a.cfg.Backup.Enabled && !a.cfg.Backup.RestoreMode) {
		return
	}
	primary, err := loadAppBackupRepository(
		context.Background(), "primary",
		a.cfg.Backup.Primary.Endpoint, a.cfg.Backup.Primary.Region,
		a.cfg.Backup.Primary.Bucket, a.cfg.Backup.Primary.Prefix,
		a.cfg.Backup.ObjectLockDays, a.cfg.Backup.Primary.AccessRoleARN,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	secondary, err := loadAppBackupRepository(
		context.Background(), "secondary",
		a.cfg.Backup.Secondary.Endpoint, a.cfg.Backup.Secondary.Region,
		a.cfg.Backup.Secondary.Bucket, a.cfg.Backup.Secondary.Prefix,
		a.cfg.Backup.ObjectLockDays, a.cfg.Backup.Secondary.AccessRoleARN,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	kms, err := loadAppBackupKeyService(
		context.Background(),
		a.cfg.Backup.KMSRegion, a.cfg.Backup.KMSEndpoint,
		a.cfg.Backup.KMSRoleARN,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	trustedSigningKeyIDs := make([]string, 0, len(a.cfg.Backup.TrustedSigningKeyIDs)+1)
	trustedSigningKeyIDs = append(trustedSigningKeyIDs, a.cfg.Backup.SigningKeyID)
	trustedSigningKeyIDs = append(trustedSigningKeyIDs, a.cfg.Backup.TrustedSigningKeyIDs...)
	manifestSigner, err := backupartifact.NewKeyPinnedManifestSigner(kms, trustedSigningKeyIDs...)
	if err != nil {
		a.backupInitErr = err
		return
	}
	codec := backupartifact.NewObjectCodec(kms, rand.Reader)
	var observer runtimebackup.RuntimeObserver
	var captureObserver runtimebackup.CaptureObserver
	var auditObserver runtimebackup.IntegrityAuditObserver
	if a.metrics != nil {
		observer = a.metrics.Backup
		captureObserver = a.metrics.Backup
		auditObserver = a.metrics.Backup
	}
	if a.cfg.Backup.RestoreMode {
		a.wireRestore(
			clusterCfg, primary, secondary, kms, manifestSigner, codec, observer,
		)
		return
	}
	node, ok := a.cluster.(appBackupNode)
	if !ok {
		a.backupInitErr = fmt.Errorf("backup app: cluster runtime does not expose backup seams")
		return
	}
	stateStore, err := backupinfra.NewControllerStateStore(node)
	if err != nil {
		a.backupInitErr = err
		return
	}
	primaryRepair, err := loadAppBackupRepairRepository(
		context.Background(), primary,
		a.cfg.Backup.Primary.Endpoint,
		a.cfg.Backup.Primary.Region, a.cfg.Backup.Primary.RepairRoleARN,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	secondaryRepair, err := loadAppBackupRepairRepository(
		context.Background(), secondary,
		a.cfg.Backup.Secondary.Endpoint,
		a.cfg.Backup.Secondary.Region,
		a.cfg.Backup.Secondary.RepairRoleARN,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	primaryGarbage, err := loadAppBackupGarbageRepository(
		context.Background(), "primary",
		a.cfg.Backup.Primary.Endpoint,
		a.cfg.Backup.Primary.Region, a.cfg.Backup.Primary.Bucket,
		a.cfg.Backup.Primary.Prefix, a.cfg.Backup.ObjectLockDays,
		a.cfg.Backup.Primary.GarbageRoleARN, clusterCfg.NodeID,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	secondaryGarbage, err := loadAppBackupGarbageRepository(
		context.Background(), "secondary",
		a.cfg.Backup.Secondary.Endpoint, a.cfg.Backup.Secondary.Region,
		a.cfg.Backup.Secondary.Bucket, a.cfg.Backup.Secondary.Prefix,
		a.cfg.Backup.ObjectLockDays,
		a.cfg.Backup.Secondary.GarbageRoleARN, clusterCfg.NodeID,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	segments, err := backupartifact.NewReplicatedSegmentStoreWithRepair(
		primary, secondary, primaryRepair, secondaryRepair,
		backupartifact.NewSegmentCodec(kms, rand.Reader),
		manifestSigner, a.cfg.Backup.SigningKeyID,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	checkpointCatalog, err :=
		backupinfra.NewReplicatedCheckpointCatalogWithRepair(
			primary, secondary, primaryRepair, secondaryRepair,
			manifestSigner, a.cfg.Backup.SigningKeyID,
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	checkpointIndex, err := backupinfra.NewCheckpointCatalogIndex(
		checkpointCatalog, filepath.Join(a.cfg.Backup.StagingDir, "checkpoint-catalog-index.json"),
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	authority, err := backupinfra.NewClusterSlotCaptureAuthority(node)
	if err != nil {
		a.backupInitErr = err
		return
	}
	frontiers, err := backupinfra.NewControllerSlotFrontierStore(
		stateStore, authority,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	memoryBudget, err := runtimebackup.NewCaptureMemoryBudget(
		runtimebackup.DefaultCaptureMemoryBudgetBytes,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	cursorResolver, err := backupinfra.NewMessageCursorResolver(
		segments, memoryBudget,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	messageSource, err := backupinfra.NewMessageLogSource(node, cursorResolver)
	if err != nil {
		a.backupInitErr = err
		return
	}
	metadataSource, err := backupinfra.NewMetadataLogSource(node)
	if err != nil {
		a.backupInitErr = err
		return
	}
	source, err := backupinfra.NewContinuousSource(metadataSource, messageSource)
	if err != nil {
		a.backupInitErr = err
		return
	}
	clusterPins, err := backupinfra.NewClusterSourcePinManager(node, time.Now)
	if err != nil {
		a.backupInitErr = err
		return
	}
	pins := decorateAppBackupSourcePinManager(clusterPins)
	replicator, err := backupinfra.NewChunkReplicator(
		backupinfra.ChunkReplicatorOptions{
			Codec: codec,
			Publisher: backupartifact.NewReplicatedPublisher(
				primary, secondary,
			),
			KMSKeyID:   a.cfg.Backup.KMSKeyID,
			ChunkBytes: int(a.cfg.Backup.BaselineChunkBytes),
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	manifestStore, err := backupinfra.NewReplicatedManifestStore(primary, secondary)
	if err != nil {
		a.backupInitErr = err
		return
	}
	localMessages, err := backupinfra.NewLocalMessageShardCapturer(node, replicator)
	if err != nil {
		a.backupInitErr = err
		return
	}
	client := accessnode.NewClient(node)
	messageRouter, err := backupinfra.NewMessageShardRouter(localMessages, client)
	if err != nil {
		a.backupInitErr = err
		return
	}
	planner, err := backupinfra.NewPartitionPlanner(
		backupinfra.PartitionPlannerOptions{Node: node},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	worker, err := runtimebackup.NewDistributedWorker(
		runtimebackup.DistributedWorkerOptions{
			Planner: planner, Messages: messageRouter,
			Replicator: replicator, Manifests: manifestStore,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	baselines, err := runtimebackup.NewDistributedBaselineCapturer(
		runtimebackup.MaterializedBaselineOptions{
			Worker: worker, Segments: segments,
			RepositoryID:     a.cfg.Backup.RepositoryID,
			SourceClusterID:  clusterCfg.Control.ClusterID,
			SourceGeneration: a.cfg.Backup.SourceGeneration,
			KMSKeyID:         a.cfg.Backup.KMSKeyID,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	generationValidator, err :=
		backupinfra.NewGenerationReplacementValidator(segments)
	if err != nil {
		a.backupInitErr = err
		return
	}
	generationCostPlanner, err :=
		backupinfra.NewConservativeGenerationCostPlanner(
			runtimebackup.DefaultGenerationCompactionIOBytes,
			runtimebackup.DefaultGenerationCompactionNetworkBytes,
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	generationBudget, err := runtimebackup.NewGenerationCompactionBudget(
		runtimebackup.DefaultGenerationCompactionConcurrency,
		runtimebackup.DefaultGenerationCompactionIOBytes,
		runtimebackup.DefaultGenerationCompactionNetworkBytes,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	auditGate, err :=
		backupinfra.NewControllerIntegrityAuditStateStore(stateStore)
	if err != nil {
		a.backupInitErr = err
		return
	}
	rollingPolicy := runtimebackup.DefaultRollingPolicy()
	rollingPolicy.TargetSegmentBytes = int64(a.cfg.Backup.TargetSegmentBytes)
	rollingPolicy.MaxOpenDuration = a.cfg.Backup.MaxSegmentOpenDuration
	capture, err := runtimebackup.NewCaptureEngine(
		runtimebackup.CaptureEngineOptions{
			RepositoryID:      a.cfg.Backup.RepositoryID,
			SourceClusterID:   clusterCfg.Control.ClusterID,
			SourceGeneration:  a.cfg.Backup.SourceGeneration,
			KMSKeyID:          a.cfg.Backup.KMSKeyID,
			InitialGeneration: a.cfg.Backup.SourceGeneration,
			HashSlotCount:     clusterCfg.Slots.HashSlotCount,
			Source:            source, Frontiers: frontiers, Segments: segments,
			CursorLoader:      segments,
			Policy:            rollingPolicy,
			ReconcileInterval: a.cfg.Backup.CaptureReconcileInterval,
			WorkerCount:       a.cfg.Backup.WorkerCount,
			MemoryBudget:      memoryBudget, Observer: captureObserver,
			AuditGate: auditGate,
			Rebase: &runtimebackup.RebaseOptions{
				Policy: runtimebackup.SourcePinPolicy{
					MaxAge:       a.cfg.Backup.SourcePinMaxAge,
					MaxNodeBytes: a.cfg.Backup.MaxSourcePinnedBytes,
				},
				Pins: pins, Baselines: baselines,
				Validator:   generationValidator,
				CostPlanner: generationCostPlanner,
				Budget:      generationBudget,
			},
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	checkpoints, err := backupusecase.NewCheckpointCoordinator(
		backupusecase.CheckpointOptions{
			Enabled: true, HashSlotCount: clusterCfg.Slots.HashSlotCount,
			RepositoryID:     a.cfg.Backup.RepositoryID,
			SourceClusterID:  clusterCfg.Control.ClusterID,
			SourceGeneration: a.cfg.Backup.SourceGeneration,
			Store:            stateStore, Catalog: checkpointCatalog,
			Proofs: segments,
			Now:    time.Now,
			NewCheckpointID: func() string {
				return newBackupID("checkpoint")
			},
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	sourceFenceConvergence, err :=
		backupinfra.NewControllerSourceFenceConvergence(node)
	if err != nil {
		a.backupInitErr = err
		return
	}
	a.backup, err = backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: clusterCfg.Slots.HashSlotCount,
		Store: stateStore, Checkpoints: checkpoints,
		CatalogBrowser:   checkpointIndex,
		CatalogRetention: checkpointIndex,
		SourceClusterID:  clusterCfg.Control.ClusterID, SourceGeneration: a.cfg.Backup.SourceGeneration,
		SourceFenceConvergence: sourceFenceConvergence,
		SourceFenceSigner:      manifestSigner, SigningKeyID: a.cfg.Backup.SigningKeyID,
		NewSourceFenceID: func() string { return newBackupID("source-fence") },
		Now:              time.Now, MaxCheckpointAge: a.cfg.Backup.CheckpointInterval,
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	erasureLedger, err := backupinfra.NewPermanentErasureLedger(backupinfra.PermanentErasureLedgerOptions{
		Primary: primary, Secondary: secondary, Codec: codec, Coordinator: a.backup,
		Signer: manifestSigner, SigningKeyID: a.cfg.Backup.SigningKeyID, KMSKeyID: a.cfg.Backup.KMSKeyID,
		RepositoryID: a.cfg.Backup.RepositoryID, SourceClusterID: clusterCfg.Control.ClusterID, SourceGeneration: a.cfg.Backup.SourceGeneration,
		HashSlotCount: clusterCfg.Slots.HashSlotCount, Now: time.Now, NewAttemptID: func() string { return newBackupID("erasure") },
	})
	if err != nil {
		a.backupInitErr = err
		a.backup = nil
		return
	}
	a.permanentErasureRecorder = erasureLedger
	primaryClock, err := newAppBackupClockProbe(a.cfg.Backup.Primary.Endpoint)
	if err != nil {
		a.backupInitErr = err
		return
	}
	secondaryClock, err := newAppBackupClockProbe(a.cfg.Backup.Secondary.Endpoint)
	if err != nil {
		a.backupInitErr = err
		return
	}
	doctor, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: primary, Secondary: secondary, KMS: kms, EncryptionKey: a.cfg.Backup.KMSKeyID, SigningKey: a.cfg.Backup.SigningKeyID,
		StagingDir: a.cfg.Backup.StagingDir, ApplicationDir: a.cfg.DataDir, StagingMaxBytes: a.cfg.Backup.StagingMaxBytes,
		ClockProbes: []backupinfra.ClockProbe{primaryClock, secondaryClock}, Now: time.Now,
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	catalogWindow, err :=
		backupinfra.NewCoordinationIntegrityAuditCatalogWindowSource(
			stateStore,
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	retentionSource, err :=
		backupinfra.NewCheckpointIndexIntegrityAuditRetentionSource(
			backupinfra.CheckpointIndexIntegrityAuditRetentionSourceOptions{
				Index: checkpointIndex,
				Policy: backupusecase.CheckpointRetentionPolicy{
					MonthlyMonths: a.cfg.Backup.RetentionMonthlyMonths,
				},
				ActiveRestore: backupinfra.NoActiveRestoreSource{},
			},
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	auditPlan, err := backupinfra.NewCatalogSegmentIntegrityAuditPlan(
		backupinfra.CatalogSegmentIntegrityAuditPlanOptions{
			Window: catalogWindow, Selection: retentionSource,
			Catalog:       checkpointCatalog,
			HashSlotCount: clusterCfg.Slots.HashSlotCount,
			ScrubInterval: a.cfg.Backup.AuditScrubInterval,
			Now:           time.Now,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	auditBackend, err := backupinfra.NewSegmentIntegrityAuditBackend(
		auditPlan, segments, segments,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	erasureAuditor, err :=
		backupinfra.NewReplicatedErasureIntegrityAuditor(
			backupinfra.ReplicatedErasureIntegrityAuditorOptions{
				Primary: primary, Secondary: secondary,
				PrimaryRepair:   primaryRepair,
				SecondaryRepair: secondaryRepair,
				Codec:           codec, Signer: manifestSigner,
				RepositoryID:     a.cfg.Backup.RepositoryID,
				SourceClusterID:  clusterCfg.Control.ClusterID,
				SourceGeneration: a.cfg.Backup.SourceGeneration,
				HashSlotCount:    clusterCfg.Slots.HashSlotCount,
			},
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	auditBackend, err = auditBackend.WithErasureAuditor(erasureAuditor)
	if err != nil {
		a.backupInitErr = err
		return
	}
	sourceProbe, err :=
		runtimebackup.NewFrontierIntegrityAuditSourceProbe(
			frontiers, source,
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	auditRecovery, err :=
		runtimebackup.NewCaptureIntegrityAuditRecovery(
			sourceProbe, frontiers,
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	integrityAuditor, err := runtimebackup.NewIntegrityAuditor(
		runtimebackup.IntegrityAuditorOptions{
			Backend: auditBackend, State: auditGate,
			Recovery: auditRecovery, Observer: auditObserver,
			Now: time.Now,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	gcCursors, err :=
		backupinfra.NewControllerGenerationGCCursorStore(stateStore)
	if err != nil {
		a.backupInitErr = err
		return
	}
	vectorCache, err := backupinfra.NewFileGenerationVectorCache(
		filepath.Join(
			a.cfg.Backup.StagingDir, "generation-vector-cache",
		),
		manifestSigner,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	auditRoots, err :=
		backupinfra.NewControllerCatalogAuditRootStore(stateStore)
	if err != nil {
		a.backupInitErr = err
		return
	}
	generationCollector, err :=
		backupinfra.NewGenerationGarbageCollector(
			backupinfra.GenerationGarbageCollectorOptions{
				Primary: primaryGarbage, Secondary: secondaryGarbage,
				Catalog: checkpointCatalog, Signer: manifestSigner,
				Cursors: gcCursors, VectorCache: vectorCache,
				IntegrityGuard:  auditGate,
				AuditProtection: auditPlan, AuditRoots: auditRoots,
				HashSlotCount: clusterCfg.Slots.HashSlotCount,
				SafetyWindow:  a.cfg.Backup.GarbageSafetyWindow,
				MaxRequestsPerRepository: a.cfg.Backup.
					GarbageMaxRequestsPerRepository,
				MaxBytesPerRepository: int64(
					a.cfg.Backup.GarbageMaxBytesPerRepository,
				),
				Now: time.Now,
			},
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	gcMaintenance, err := backupinfra.NewGenerationGCMaintenance(
		backupinfra.GenerationGCMaintenanceOptions{
			State: stateStore, Index: checkpointIndex,
			Collector: generationCollector,
			Policy: backupusecase.CheckpointRetentionPolicy{
				MonthlyMonths: a.cfg.Backup.RetentionMonthlyMonths,
			},
			Now: time.Now,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	checkpointObservation, err :=
		backupinfra.NewCheckpointObservationSource(
			stateStore, checkpointIndex,
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	auditProjection, err :=
		backupinfra.NewIntegrityAuditProjectionRunner(
			auditGate, a.cfg.Backup.CaptureReconcileInterval,
		)
	if err != nil {
		a.backupInitErr = err
		return
	}
	coordinator, err := runtimebackup.NewContinuousCoordinator(
		runtimebackup.ContinuousCoordinatorOptions{
			Capture: capture, Checkpoints: checkpoints,
			LatestCheckpoint: checkpointObservation,
			Doctor:           doctor, Leadership: node,
			CheckpointInterval: a.cfg.Backup.CheckpointInterval,
			TickInterval:       a.cfg.Backup.CaptureReconcileInterval,
			Auditor:            integrityAuditor,
			AuditInterval:      a.cfg.Backup.AuditInterval,
			GarbageCollector:   gcMaintenance,
			GarbageCollectionInterval: a.cfg.Backup.
				GarbageCollectionInterval,
			Projection: auditProjection,
			Now:        time.Now, Observer: observer,
		},
	)
	if err != nil {
		a.backupInitErr = err
		a.backup = nil
		return
	}
	a.backupRuntime = coordinator
	adapter := accessnode.New(accessnode.Options{
		BackupMessages: localMessages, Logger: a.logger.Named("node"),
	})
	node.RegisterRPC(accessnode.BackupMessageShardRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupMessageShardRPC))
	managerBackupAdapter := accessnode.NewManagerBackupAdapter(accessnode.ManagerBackupOptions{
		Local: backupManagerFacade{app: a}, Leadership: node,
	})
	node.RegisterRPC(accessnode.ManagerBackupRPCServiceID, nodeRPCHandlerFunc(managerBackupAdapter.HandleRPC))
}

type unavailablePermanentErasureRecorder struct {
	err error
}

func (r unavailablePermanentErasureRecorder) RecordPermanentMessageErasure(context.Context, backupinfra.PermanentMessageErasure) (backupinfra.ErasureLedgerReceipt, error) {
	if r.err != nil {
		return backupinfra.ErasureLedgerReceipt{}, fmt.Errorf("backup permanent erasure ledger unavailable: %w", r.err)
	}
	return backupinfra.ErasureLedgerReceipt{}, fmt.Errorf("backup permanent erasure ledger unavailable")
}

func (a *App) managerPermanentErasureRecorder() clusterinfra.PermanentMessageErasureRecorder {
	if a == nil || !a.cfg.Backup.Enabled {
		return nil
	}
	if a.permanentErasureRecorder != nil {
		return a.permanentErasureRecorder
	}
	return unavailablePermanentErasureRecorder{err: a.backupInitErr}
}

func (a *App) wireRestore(
	clusterCfg cluster.Config,
	primary, secondary backupartifact.Repository,
	keys backupartifact.DataKeyManager,
	signer backupartifact.ManifestSigner,
	codec *backupartifact.ObjectCodec,
	observer runtimebackup.RuntimeObserver,
) {
	node, ok := a.cluster.(appRestoreNode)
	if !ok {
		a.backupInitErr = fmt.Errorf("backup restore app: cluster runtime does not expose restore seams")
		return
	}
	stagingQuota, err := backupinfra.NewCheckpointRestoreStagingQuota(
		a.cfg.Backup.StagingDir, a.cfg.Backup.StagingMaxBytes,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	client := accessnode.NewClient(node)
	target, err := backupinfra.NewClusterRestoreTargetProbe(backupinfra.ClusterRestoreTargetProbeOptions{
		Node: node, Remote: client, ClusterID: clusterCfg.Control.ClusterID,
		Generation: a.cfg.Backup.TargetGeneration, HashSlotCount: clusterCfg.Slots.HashSlotCount,
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	segments, err := backupartifact.NewReplicatedSegmentStore(
		primary, secondary,
		backupartifact.NewSegmentCodec(keys, rand.Reader),
		signer, a.cfg.Backup.SigningKeyID,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(
		primary, secondary, signer, a.cfg.Backup.SigningKeyID,
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	auditor, err := backupinfra.NewCheckpointRestoreGraphAuditor(segments)
	if err != nil {
		a.backupInitErr = err
		return
	}
	inspector, err := backupinfra.NewCheckpointRestoreInspector(
		backupinfra.CheckpointRestoreInspectorOptions{
			Primary: primary, Secondary: secondary,
			Signer: signer, Codec: codec,
			RepositoryID: a.cfg.Backup.RepositoryID,
			Target:       target, Catalog: catalog, Auditor: auditor,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	store, err := backupinfra.NewControllerRestoreStateStore(node)
	if err != nil {
		a.backupInitErr = err
		return
	}
	targetStagingDir := filepath.Join(
		a.cfg.Backup.StagingDir, checkpointRestoreTargetStagingDirectory,
	)
	replicaReceiver, err := backupinfra.NewCheckpointRestoreReplicaReceiver(
		backupinfra.CheckpointRestoreReplicaReceiverOptions{
			Node: node, StagingDir: targetStagingDir,
			StagingMaxBytes: a.cfg.Backup.StagingMaxBytes,
			StagingQuota:    stagingQuota,
			ActivationState: node,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	distributor, err := backupinfra.NewCheckpointRestoreReplicaDistributor(
		backupinfra.CheckpointRestoreReplicaDistributorOptions{
			Node: node, Local: replicaReceiver, Remote: client,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	restoreTarget, err := backupinfra.NewDurableCheckpointRestoreTarget(
		backupinfra.DurableCheckpointRestoreTargetOptions{
			StagingDir:      targetStagingDir,
			StagingMaxBytes: a.cfg.Backup.StagingMaxBytes,
			StagingQuota:    stagingQuota,
			Distributor:     distributor, Now: time.Now,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	baseline, err := backupinfra.NewMaterializedCheckpointBaselineReplayer(
		backupinfra.MaterializedCheckpointBaselineReplayerOptions{
			Codec: codec, Segments: segments,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	localInstaller, err := backupinfra.NewCheckpointSlotInstaller(
		backupinfra.CheckpointSlotInstallerOptions{
			Primary: primary, Secondary: secondary,
			Catalog: catalog, Segments: segments,
			Signer: signer, Codec: codec,
			RepositoryID: a.cfg.Backup.RepositoryID,
			Baseline:     baseline, Target: restoreTarget,
			StagingDir: filepath.Join(
				a.cfg.Backup.StagingDir,
				checkpointRestoreStagingDirectory,
			),
			StagingMaxBytes: a.cfg.Backup.StagingMaxBytes,
			StagingQuota:    stagingQuota,
			MemoryMaxBytes:  defaultCheckpointRestoreMemoryMaxBytes,
			Progress: func(
				ctx context.Context,
				planID string,
				progress backupusecase.RestorePartition,
			) error {
				// A remote Slot Leader receives the current fenced plan in the
				// install RPC, but its local Controller mirror may not yet have
				// applied the preceding BeginPartitionInstall CAS. Only the
				// Controller Leader persists intermediate byte progress; the
				// coordinator always persists the returned terminal report.
				if node.BackupControllerLeaderID() != node.NodeID() {
					return nil
				}
				if a.restore == nil {
					return fmt.Errorf(
						"backup checkpoint restore app is unavailable",
					)
				}
				_, err := a.restore.ReportPartitionProgress(
					ctx, planID, progress,
				)
				return err
			},
			Now: time.Now,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	partitionInstaller, err := backupinfra.NewClusterRestorePartitionInstaller(backupinfra.ClusterRestorePartitionInstallerOptions{
		Node: node, Local: localInstaller, Remote: client,
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	verifier, err := backupinfra.NewCheckpointRestoreFinalVerifier(
		backupinfra.CheckpointRestoreFinalVerifierOptions{
			Node: node, Local: replicaReceiver, Remote: client,
			MaxParallel: a.cfg.Backup.WorkerCount,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	cleaner, err := backupinfra.NewCheckpointRestoreActivationCleaner(
		backupinfra.CheckpointRestoreActivationCleanerOptions{
			Node: node, Local: replicaReceiver, Remote: client,
			MaxParallel: a.cfg.Backup.WorkerCount,
		},
	)
	if err != nil {
		a.backupInitErr = err
		return
	}
	a.restore, err = backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store, Inspector: inspector,
		Verifier: verifier, Cleaner: cleaner,
		ActivationVerifier: signer,
		Now:                time.Now, NewPlanID: func() string { return newBackupID("restore-plan") },
		NewAuditID: func() string { return newBackupID("break-glass") },
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	restoreRuntime, err := runtimebackup.NewRestoreCoordinator(runtimebackup.RestoreCoordinatorOptions{
		App: a.restore, Leadership: node, Partitions: partitionInstaller,
		MaxParallel: a.cfg.Backup.WorkerCount, Now: time.Now, Observer: observer,
		OnFailure: func(category string, err error) {
			a.logger.Warn(
				"backup restore coordinator retry",
				wklog.Event("internal.app.backup_restore_retry"),
				wklog.String("category", category),
				wklog.Error(err),
			)
		},
	})
	if err != nil {
		a.backupInitErr = err
		a.restore = nil
		return
	}
	a.restoreRuntime = restoreRuntime
	adapter := accessnode.New(accessnode.Options{
		BackupRestoreTarget:     node,
		BackupRestoreInstaller:  localInstaller,
		BackupCheckpointReplica: replicaReceiver,
		Logger:                  a.logger.Named("node"),
	})
	node.RegisterRPC(accessnode.BackupRestoreTargetRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupRestoreTargetRPC))
	node.RegisterRPC(accessnode.BackupRestoreInstallRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupRestoreInstallRPC))
	node.RegisterRPC(accessnode.BackupCheckpointReplicaRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupCheckpointReplicaRPC))
}

func newBackupID(prefix string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())))
		copy(value[:], hash[:len(value)])
	}
	return prefix + "-" + hex.EncodeToString(value[:])
}

type backupManagerFacade struct{ app *App }

type backupManagerRouter struct {
	local      backupManagerFacade
	leadership runtimebackup.CoordinatorLeadership
	client     *accessnode.Client
}

type backupRuntimeStatusProvider interface {
	Status() runtimebackup.CoordinatorStatus
}

func (a *App) newBackupManagement() accessmanager.BackupManagement {
	local := backupManagerFacade{app: a}
	node, ok := a.cluster.(appBackupNode)
	if !ok || a.backup == nil {
		return local
	}
	return backupManagerRouter{local: local, leadership: node, client: accessnode.NewClient(node)}
}

func (a *App) newRestoreManagement() accessmanager.RestoreManagement {
	return restoreManagerFacade{app: a}
}

type restoreManagerFacade struct{ app *App }

func (f restoreManagerFacade) PlanRestore(ctx context.Context, request backupusecase.RestorePlanRequest) (backupusecase.RestorePlan, error) {
	if f.app == nil || f.app.restore == nil {
		return backupusecase.RestorePlan{}, restoreFacadeUnavailable(f.app)
	}
	plan, err := f.app.restore.Plan(ctx, request)
	f.app.logBackupAudit("restore_plan", plan.ID, err,
		wklog.String("checkpointID", plan.CheckpointID), wklog.Bool("invalidateTokens", request.InvalidateTokens))
	return plan, err
}

func (f restoreManagerFacade) StartRestore(ctx context.Context, planID string) (backupusecase.RestorePlan, error) {
	if f.app == nil || f.app.restore == nil {
		return backupusecase.RestorePlan{}, restoreFacadeUnavailable(f.app)
	}
	plan, err := f.app.restore.Start(ctx, planID)
	f.app.logBackupAudit("restore_start", planID, err)
	return plan, err
}

func (f restoreManagerFacade) RestoreStatus(
	ctx context.Context,
) (*backupusecase.RestorePlan, error) {
	if f.app == nil {
		return nil, restoreFacadeUnavailable(f.app)
	}
	if f.app.restore != nil {
		return f.app.restore.Status(ctx)
	}
	controller, ok :=
		f.app.cluster.(backupinfra.RestoreCoordinationController)
	if !ok {
		return nil, restoreFacadeUnavailable(f.app)
	}
	store, err := backupinfra.NewControllerRestoreStateStore(controller)
	if err != nil {
		return nil, err
	}
	state, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	if state.Plan == nil ||
		state.Plan.Status != backupusecase.RestoreStatusActivated {
		return nil, restoreFacadeUnavailable(f.app)
	}
	return state.Plan, nil
}

func (f restoreManagerFacade) RestoreProgress(ctx context.Context) (*backupusecase.RestoreProgress, error) {
	if f.app == nil {
		return nil, restoreFacadeUnavailable(f.app)
	}
	if f.app.restore != nil {
		return f.app.restore.Progress(ctx)
	}
	plan, err := f.RestoreStatus(ctx)
	if err != nil || plan == nil {
		return nil, err
	}
	return nil, nil
}

func (f restoreManagerFacade) VerifyRestore(ctx context.Context, planID string) (backupusecase.RestorePlan, error) {
	if f.app == nil || f.app.restore == nil {
		return backupusecase.RestorePlan{}, restoreFacadeUnavailable(f.app)
	}
	plan, err := f.app.restore.Verify(ctx, planID)
	f.app.logBackupAudit("restore_verify", planID, err)
	return plan, err
}

func (f restoreManagerFacade) ActivateRestore(
	ctx context.Context,
	planID string,
	request backupusecase.RestoreActivationRequest,
) (backupusecase.RestorePlan, error) {
	if f.app == nil || f.app.restore == nil {
		return backupusecase.RestorePlan{}, restoreFacadeUnavailable(f.app)
	}
	plan, err := f.app.restore.Activate(ctx, planID, request)
	f.app.logBackupAudit(
		"restore_activate", planID, err,
		wklog.String("operator", request.Operator),
	)
	return plan, err
}

func restoreFacadeUnavailable(app *App) error {
	if app == nil || !app.cfg.Backup.RestoreMode {
		return backupusecase.ErrRestoreModeRequired
	}
	if app.backupInitErr != nil {
		return app.backupInitErr
	}
	return fmt.Errorf("backup restore runtime is unavailable")
}

func (r backupManagerRouter) leader() (uint64, bool, error) {
	if r.leadership == nil {
		return 0, false, backupusecase.ErrControllerLeaderUnavailable
	}
	leaderID := r.leadership.BackupControllerLeaderID()
	if leaderID == 0 {
		return 0, false, backupusecase.ErrControllerLeaderUnavailable
	}
	return leaderID, leaderID == r.leadership.NodeID(), nil
}

func (r backupManagerRouter) Status(ctx context.Context) (backupusecase.StatusSnapshot, error) {
	leaderID, local, err := r.leader()
	if err != nil {
		return backupusecase.StatusSnapshot{}, err
	}
	if local {
		return r.local.Status(ctx)
	}
	return r.client.ManagerBackupStatus(ctx, leaderID)
}

func (r backupManagerRouter) ListCheckpointsPage(ctx context.Context, request backupusecase.CheckpointListRequest) (backupusecase.CheckpointPage, error) {
	return r.local.ListCheckpointsPage(ctx, request)
}

func (r backupManagerRouter) CheckpointByID(ctx context.Context, checkpointID string) (backupusecase.CheckpointDetail, error) {
	return r.local.CheckpointByID(ctx, checkpointID)
}

func (r backupManagerRouter) PublishCheckpoint(
	ctx context.Context,
) (backupusecase.CheckpointPublication, error) {
	leaderID, local, err := r.leader()
	if err != nil {
		return backupusecase.CheckpointPublication{}, err
	}
	if local {
		return r.local.PublishCheckpoint(ctx)
	}
	return r.client.ManagerBackupPublishCheckpoint(ctx, leaderID)
}

func (r backupManagerRouter) SetCheckpointHold(
	ctx context.Context,
	checkpointID string,
	held bool,
) (backupusecase.CheckpointSummary, error) {
	leaderID, local, err := r.leader()
	if err != nil {
		return backupusecase.CheckpointSummary{}, err
	}
	if local {
		return r.local.SetCheckpointHold(ctx, checkpointID, held)
	}
	return r.client.ManagerBackupSetCheckpointHold(
		ctx, leaderID, checkpointID, held,
	)
}

func (r backupManagerRouter) FenceSource(
	ctx context.Context,
	request backupusecase.SourceFenceRequest,
) (backupusecase.SourceFenceReceipt, error) {
	leaderID, local, err := r.leader()
	if err != nil {
		return backupusecase.SourceFenceReceipt{}, err
	}
	if local {
		return r.local.FenceSource(ctx, request)
	}
	return r.client.ManagerBackupFenceSource(ctx, leaderID, request)
}

func (f backupManagerFacade) Status(ctx context.Context) (backupusecase.StatusSnapshot, error) {
	if f.app == nil || !f.app.cfg.Backup.Enabled {
		return f.observeBackupStatus(backupusecase.StatusSnapshot{Enabled: false, Health: backupusecase.HealthDisabled}), nil
	}
	if f.app.backupInitErr != nil || f.app.backup == nil {
		return f.observeBackupStatus(backupusecase.StatusSnapshot{Enabled: true, Health: backupusecase.HealthFailed}), nil
	}
	status, err := f.app.backup.Status(ctx)
	if err != nil {
		return backupusecase.StatusSnapshot{}, err
	}
	if provider, ok := f.app.backupRuntime.(backupRuntimeStatusProvider); ok {
		operational := provider.Status()
		if operational.DoctorHealth == backupusecase.HealthFailed {
			status.Health = backupusecase.HealthFailed
		} else if operational.LastFailureCategory != "" && status.Health == backupusecase.HealthHealthy {
			status.Health = backupusecase.HealthDegraded
		} else if operational.DoctorHealth != backupusecase.HealthHealthy && status.Health == backupusecase.HealthHealthy {
			status.Health = backupusecase.HealthUnknown
		}
		if operational.LastFailureCategory != "" {
			status.FailureCategory = operational.LastFailureCategory
		}
		status.Running = operational.Running
		if captureProvider, ok := f.app.backupRuntime.(interface {
			CaptureStatus() []backupcontract.SlotCaptureStatus
		}); ok {
			status.LocalCaptureStatuses = captureProvider.CaptureStatus()
		}
	} else if status.Health == backupusecase.HealthHealthy {
		status.Health = backupusecase.HealthUnknown
	}
	return f.observeBackupStatus(status), nil
}

func (f backupManagerFacade) observeBackupStatus(status backupusecase.StatusSnapshot) backupusecase.StatusSnapshot {
	status.ObservedAtUnixMillis = time.Now().UTC().UnixMilli()
	if f.app != nil {
		cfg := f.app.cfg.Backup
		status.Policy = backupusecase.PolicySnapshot{
			CaptureReconcileIntervalSeconds: int64(cfg.CaptureReconcileInterval / time.Second),
			CheckpointIntervalSeconds:       int64(cfg.CheckpointInterval / time.Second),
			CaptureWorkerCount:              cfg.WorkerCount,
			StagingMaxBytes:                 cfg.StagingMaxBytes,
			SourcePinMaxAgeSeconds:          int64(cfg.SourcePinMaxAge / time.Second),
			MaxSourcePinnedBytes:            cfg.MaxSourcePinnedBytes,
		}
		if leadership, ok := f.app.cluster.(runtimebackup.CoordinatorLeadership); ok {
			status.CoordinatorNodeID = leadership.BackupControllerLeaderID()
		}
	}
	return status
}

func (f backupManagerFacade) ListCheckpointsPage(ctx context.Context, request backupusecase.CheckpointListRequest) (backupusecase.CheckpointPage, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.CheckpointPage{}, backupFacadeUnavailable(f.app)
	}
	return f.app.backup.ListCheckpointsPage(ctx, request)
}

func (f backupManagerFacade) CheckpointByID(ctx context.Context, checkpointID string) (backupusecase.CheckpointDetail, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.CheckpointDetail{}, backupFacadeUnavailable(f.app)
	}
	return f.app.backup.CheckpointByID(ctx, checkpointID)
}

func (f backupManagerFacade) PublishCheckpoint(
	ctx context.Context,
) (backupusecase.CheckpointPublication, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.CheckpointPublication{}, backupFacadeUnavailable(f.app)
	}
	if coordinator, ok :=
		f.app.backupRuntime.(interface {
			PublishCheckpoint(context.Context) (
				backupartifact.CheckpointCatalogCommit,
				error,
			)
		}); ok {
		commit, err := coordinator.PublishCheckpoint(ctx)
		if err != nil {
			f.app.logBackupAudit(
				"backup_checkpoint_publish", "", err,
			)
			if errors.Is(err, runtimebackup.ErrContinuousDoctorUnhealthy) {
				return backupusecase.CheckpointPublication{},
					backupusecase.ErrDoctorUnhealthy
			}
			if errors.Is(err, runtimebackup.ErrCaptureNotLeader) {
				return backupusecase.CheckpointPublication{},
					backupusecase.ErrControllerLeaderUnavailable
			}
			return backupusecase.CheckpointPublication{}, err
		}
		publication := backupusecase.CheckpointPublication{
			Checkpoint: backupusecase.CheckpointSummary{
				ID:                    commit.Checkpoint.ID,
				CreatedAtUnixMillis:   commit.Checkpoint.CreatedAtUnixMillis,
				EffectiveAtUnixMillis: commit.Checkpoint.EffectiveAtUnixMillis,
			},
			CheckpointSHA256: commit.Checkpoint.SHA256,
		}
		publication.CatalogHeadToken, err =
			backupusecase.EncodeCatalogHeadToken(commit.Head)
		if err != nil {
			return backupusecase.CheckpointPublication{}, err
		}
		f.app.logBackupAudit(
			"backup_checkpoint_publish", publication.Checkpoint.ID, nil,
		)
		return publication, nil
	}
	publication, err := f.app.backup.PublishCheckpoint(ctx)
	f.app.logBackupAudit(
		"backup_checkpoint_publish", publication.Checkpoint.ID, err,
	)
	return publication, err
}

func (f backupManagerFacade) SetCheckpointHold(
	ctx context.Context,
	checkpointID string,
	held bool,
) (backupusecase.CheckpointSummary, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.CheckpointSummary{},
			backupFacadeUnavailable(f.app)
	}
	checkpoint, err := f.app.backup.SetCheckpointHold(
		ctx, checkpointID, held,
	)
	action := "backup_checkpoint_release"
	if held {
		action = "backup_checkpoint_hold"
	}
	f.app.logBackupAudit(action, checkpointID, err)
	return checkpoint, err
}

func (f backupManagerFacade) FenceSource(
	ctx context.Context,
	request backupusecase.SourceFenceRequest,
) (backupusecase.SourceFenceReceipt, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.SourceFenceReceipt{}, backupFacadeUnavailable(f.app)
	}
	receipt, err := f.app.backup.FenceSource(ctx, request)
	f.app.logBackupAudit(
		"backup_source_fence", request.RestorePlanID, err,
		wklog.String("checkpointID", request.CheckpointID),
		wklog.String("targetClusterID", request.TargetClusterID),
		wklog.String("targetGeneration", request.TargetGeneration),
	)
	return receipt, err
}

func (a *App) logBackupAudit(action, entityID string, err error, fields ...wklog.Field) {
	if a == nil || a.logger == nil {
		return
	}
	result := "succeeded"
	if err != nil {
		result = "failed"
	}
	base := []wklog.Field{
		wklog.Event("internal.app.backup_audit"), wklog.String("action", action), wklog.Result(result),
	}
	if entityID != "" {
		base = append(base, wklog.String("entityID", entityID))
	}
	base = append(base, fields...)
	if err != nil {
		a.logger.Warn("backup audit action failed", append(base, wklog.Error(err))...)
		return
	}
	a.logger.Info("backup audit action completed", base...)
}

func backupFacadeUnavailable(app *App) error {
	if app == nil || !app.cfg.Backup.Enabled {
		return backupusecase.ErrDisabled
	}
	return fmt.Errorf("backup runtime is unavailable")
}

var _ accessmanager.BackupManagement = backupManagerFacade{}
var _ accessmanager.RestoreManagement = restoreManagerFacade{}
var _ appRestoreNode = (*cluster.Node)(nil)
