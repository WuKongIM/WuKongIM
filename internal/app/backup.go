package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const defaultMaterializedFullInterval = 30 * 24 * time.Hour

type appBackupNode interface {
	backupinfra.CoordinationController
	backupinfra.PartitionPlanNode
	backupinfra.LocalMessageSnapshotNode
	backupinfra.PartitionRouteNode
	runtimebackup.CoordinatorLeadership
	accessnode.PresenceRPCNode
	nodeRPCRegistrar
}

type appRestoreNode interface {
	backupinfra.RestoreCoordinationController
	backupinfra.RestoreTargetClusterNode
	backupinfra.RestoreInstallNode
	backupinfra.RestoreInstallClusterNode
	backupinfra.RestoreVerificationClusterNode
	runtimebackup.CoordinatorLeadership
	accessnode.PresenceRPCNode
	nodeRPCRegistrar
}

type appBackupRepository interface {
	backupartifact.Repository
	backupinfra.RepositoryDoctor
	backupinfra.RestorePointLister
}

type appBackupKeyService interface {
	backupartifact.DataKeyManager
	backupartifact.ManifestSigner
	backupinfra.KMSDoctor
}

var (
	loadAppBackupRepository = func(ctx context.Context, name, endpoint, region, bucket, prefix string, objectLockDays int) (appBackupRepository, error) {
		return backupinfra.LoadS3Repository(ctx, name, endpoint, region, bucket, prefix, objectLockDays)
	}
	loadAppBackupGarbageRepository = func(ctx context.Context, name, endpoint, region, bucket, prefix string, objectLockDays int, roleARN string) (backupinfra.GarbageRepository, error) {
		return backupinfra.LoadS3GarbageRepository(ctx, name, endpoint, region, bucket, prefix, objectLockDays, roleARN)
	}
	loadAppBackupKeyService = func(ctx context.Context, region, endpoint string) (appBackupKeyService, error) {
		return backupinfra.LoadKMSAdapter(ctx, region, endpoint)
	}
	newAppBackupClockProbe = func(endpoint string) (backupinfra.ClockProbe, error) {
		return backupinfra.NewEndpointClockProbe(endpoint, nil)
	}
)

func (a *App) wireBackup(clusterCfg cluster.Config) {
	if a == nil || (!a.cfg.Backup.Enabled && !a.cfg.Backup.RestoreMode) {
		return
	}
	fingerprint, err := backupConfigFingerprint(a.cfg.Backup, clusterCfg.Control.ClusterID, clusterCfg.Slots.HashSlotCount)
	if err != nil {
		a.backupInitErr = err
		return
	}
	a.backupFingerprint = fingerprint
	primary, err := loadAppBackupRepository(context.Background(), "primary", a.cfg.Backup.Primary.Endpoint, a.cfg.Backup.Primary.Region, a.cfg.Backup.Primary.Bucket, a.cfg.Backup.Primary.Prefix, a.cfg.Backup.ObjectLockDays)
	if err != nil {
		a.backupInitErr = err
		return
	}
	secondary, err := loadAppBackupRepository(context.Background(), "secondary", a.cfg.Backup.Secondary.Endpoint, a.cfg.Backup.Secondary.Region, a.cfg.Backup.Secondary.Bucket, a.cfg.Backup.Secondary.Prefix, a.cfg.Backup.ObjectLockDays)
	if err != nil {
		a.backupInitErr = err
		return
	}
	kms, err := loadAppBackupKeyService(context.Background(), a.cfg.Backup.KMSRegion, a.cfg.Backup.KMSEndpoint)
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
	if a.metrics != nil {
		observer = a.metrics.Backup
	}
	if a.cfg.Backup.RestoreMode {
		a.wireRestore(clusterCfg, primary, secondary, manifestSigner, codec, observer)
		return
	}
	node, ok := a.cluster.(appBackupNode)
	if !ok {
		a.backupInitErr = fmt.Errorf("backup app: cluster runtime does not expose backup seams")
		return
	}
	replicator, err := backupinfra.NewChunkReplicator(backupinfra.ChunkReplicatorOptions{
		Codec: codec, Publisher: backupartifact.NewReplicatedPublisher(primary, secondary), KMSKeyID: a.cfg.Backup.KMSKeyID, ChunkBytes: int(a.cfg.Backup.ChunkSizeBytes),
	})
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
	baseResolver, err := backupinfra.NewBaseResolver(backupinfra.BaseResolverOptions{
		Repository: primary, Signer: manifestSigner, Codec: codec, RepositoryID: a.cfg.Backup.RepositoryID,
		SourceClusterID: clusterCfg.Control.ClusterID, SourceGeneration: a.cfg.Backup.SourceGeneration,
		HashSlotCount: clusterCfg.Slots.HashSlotCount,
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	planner, err := backupinfra.NewPartitionPlanner(backupinfra.PartitionPlannerOptions{Node: node, Base: baseResolver})
	if err != nil {
		a.backupInitErr = err
		return
	}
	worker, err := runtimebackup.NewDistributedWorker(runtimebackup.DistributedWorkerOptions{Planner: planner, Messages: messageRouter, Replicator: replicator, Manifests: manifestStore})
	if err != nil {
		a.backupInitErr = err
		return
	}
	partitionRouter, err := backupinfra.NewPartitionRouter(backupinfra.PartitionRouterOptions{Node: node, Local: worker, Remote: client, ConfigFingerprint: fingerprint})
	if err != nil {
		a.backupInitErr = err
		return
	}
	stateStore, err := backupinfra.NewControllerStateStore(node)
	if err != nil {
		a.backupInitErr = err
		return
	}
	publisher, err := backupinfra.NewRestorePointPublisher(backupinfra.RestorePointPublisherOptions{
		Primary: primary, Secondary: secondary, Signer: manifestSigner, SigningKeyID: a.cfg.Backup.SigningKeyID,
		ApplicationVersion: backupApplicationVersion(), RepositoryID: a.cfg.Backup.RepositoryID,
		SourceClusterID: clusterCfg.Control.ClusterID, SourceGeneration: a.cfg.Backup.SourceGeneration,
		Now: time.Now, NewRestorePointID: func() string { return newBackupID("restore") },
		ErasureLedgerBoundary: func(ctx context.Context) (uint64, error) {
			state, err := stateStore.Load(ctx)
			return state.ErasureLedgerBoundary, err
		},
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	verifier, err := backupinfra.NewRestorePointVerifier(primary, secondary, manifestSigner, time.Now)
	if err != nil {
		a.backupInitErr = err
		return
	}
	primaryGarbage, err := loadAppBackupGarbageRepository(context.Background(), "primary", a.cfg.Backup.Primary.Endpoint, a.cfg.Backup.Primary.Region, a.cfg.Backup.Primary.Bucket, a.cfg.Backup.Primary.Prefix, a.cfg.Backup.ObjectLockDays, a.cfg.Backup.GarbageCollectorRoleARN)
	if err != nil {
		a.backupInitErr = err
		return
	}
	secondaryGarbage, err := loadAppBackupGarbageRepository(context.Background(), "secondary", a.cfg.Backup.Secondary.Endpoint, a.cfg.Backup.Secondary.Region, a.cfg.Backup.Secondary.Bucket, a.cfg.Backup.Secondary.Prefix, a.cfg.Backup.ObjectLockDays, a.cfg.Backup.GarbageCollectorRoleARN)
	if err != nil {
		a.backupInitErr = err
		return
	}
	garbageCollector, err := backupinfra.NewRestorePointGarbageCollector(backupinfra.RestorePointGarbageCollectorOptions{
		Primary: primaryGarbage, Secondary: secondaryGarbage, Signer: manifestSigner, Codec: codec,
		PrimaryRepository: primary.Name(), SecondaryRepository: secondary.Name(),
		RepositoryID: a.cfg.Backup.RepositoryID, SourceClusterID: clusterCfg.Control.ClusterID, SourceGeneration: a.cfg.Backup.SourceGeneration, HashSlotCount: clusterCfg.Slots.HashSlotCount,
		MinimumAge: time.Duration(a.cfg.Backup.ObjectLockDays) * 24 * time.Hour,
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	a.backup, err = backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: clusterCfg.Slots.HashSlotCount, Store: stateStore, Publisher: publisher, Verifier: verifier,
		Now: time.Now, NewJobID: func() string { return newBackupID("job") }, MaxRecoveryPointAge: a.cfg.Backup.RestorePointInterval,
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
	coordinator, err := runtimebackup.NewCoordinator(runtimebackup.CoordinatorOptions{
		App: a.backup, Doctor: doctor, Leadership: node, Partitions: partitionRouter, ConfigFingerprint: fingerprint,
		Policy:         backupusecase.SchedulePolicy{RestorePointInterval: a.cfg.Backup.RestorePointInterval, SyntheticFullInterval: a.cfg.Backup.SyntheticFullInterval, MaterializedFullInterval: defaultMaterializedFullInterval},
		DecideSchedule: backupusecase.DecideSchedule,
		MaxParallel:    a.cfg.Backup.MaxParallelPartitions, TickInterval: a.cfg.Backup.IncrementalInterval, Now: time.Now,
		Observer: observer, RetentionPolicy: backupusecase.RetentionPolicy{MonthlyMonths: a.cfg.Backup.MonthlyRetentionMonths},
		GarbageCollector: garbageCollector,
	})
	if err != nil {
		a.backupInitErr = err
		a.backup = nil
		return
	}
	a.backupRuntime = coordinator
	adapter := accessnode.New(accessnode.Options{BackupMessages: localMessages, BackupPartitions: partitionRouter, Logger: a.logger.Named("node")})
	node.RegisterRPC(accessnode.BackupMessageShardRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupMessageShardRPC))
	node.RegisterRPC(accessnode.BackupPartitionRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupPartitionRPC))
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

func (a *App) wireRestore(clusterCfg cluster.Config, primary, secondary backupartifact.Repository, signer backupartifact.ManifestSigner, codec *backupartifact.ObjectCodec, observer runtimebackup.RuntimeObserver) {
	node, ok := a.cluster.(appRestoreNode)
	if !ok {
		a.backupInitErr = fmt.Errorf("backup restore app: cluster runtime does not expose restore seams")
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
	inspector, err := backupinfra.NewRestoreInspector(backupinfra.RestoreInspectorOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: codec,
		RepositoryID: a.cfg.Backup.RepositoryID, Target: target,
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	store, err := backupinfra.NewControllerRestoreStateStore(node)
	if err != nil {
		a.backupInitErr = err
		return
	}
	localInstaller, err := backupinfra.NewLocalRestoreInstaller(backupinfra.LocalRestoreInstallerOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: codec, Node: node,
		StagingDir: a.cfg.Backup.StagingDir, StagingMaxBytes: a.cfg.Backup.StagingMaxBytes,
	})
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
	verifier, err := backupinfra.NewClusterRestoreVerifier(backupinfra.ClusterRestoreVerifierOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: codec,
		Node: node, Remote: client, MaxParallel: a.cfg.Backup.MaxParallelPartitions,
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	a.restore, err = backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store, Inspector: inspector, Verifier: verifier,
		Now: time.Now, NewPlanID: func() string { return newBackupID("restore-plan") },
	})
	if err != nil {
		a.backupInitErr = err
		return
	}
	restoreRuntime, err := runtimebackup.NewRestoreCoordinator(runtimebackup.RestoreCoordinatorOptions{
		App: a.restore, Leadership: node, Partitions: partitionInstaller,
		MaxParallel: a.cfg.Backup.MaxParallelPartitions, Now: time.Now, Observer: observer,
	})
	if err != nil {
		a.backupInitErr = err
		a.restore = nil
		return
	}
	a.restoreRuntime = restoreRuntime
	adapter := accessnode.New(accessnode.Options{
		BackupRestoreTarget: node, BackupRestoreInstaller: localInstaller, BackupRestoreVerifier: node,
		Logger: a.logger.Named("node"),
	})
	node.RegisterRPC(accessnode.BackupRestoreTargetRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupRestoreTargetRPC))
	node.RegisterRPC(accessnode.BackupRestoreInstallRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupRestoreInstallRPC))
	node.RegisterRPC(accessnode.BackupRestoreVerifyRPCServiceID, nodeRPCHandlerFunc(adapter.HandleBackupRestoreVerifyRPC))
}

func backupConfigFingerprint(cfg BackupConfig, clusterID string, hashSlotCount uint16) (string, error) {
	// StagingDir is node-local scratch space, so it cannot participate in the
	// cluster-wide agreement fence carried by backup jobs and partition RPCs.
	cfg.StagingDir = ""
	value := struct {
		Backup        BackupConfig `json:"backup"`
		ClusterID     string       `json:"cluster_id"`
		HashSlotCount uint16       `json:"hash_slot_count"`
	}{Backup: cfg, ClusterID: clusterID, HashSlotCount: hashSlotCount}
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}

func backupApplicationVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		version := strings.TrimSpace(info.Main.Version)
		if version != "" && version != "(devel)" {
			return version
		}
	}
	return "development"
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

type backupRuntimeStatusProvider interface {
	Status() runtimebackup.CoordinatorStatus
}

func (a *App) newBackupManagement() accessmanager.BackupManagement {
	return backupManagerFacade{app: a}
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
		wklog.String("restorePointID", plan.RestorePointID), wklog.String("repository", request.Repository), wklog.Bool("invalidateTokens", request.InvalidateTokens))
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

func (f restoreManagerFacade) RestoreStatus(ctx context.Context) (*backupusecase.RestorePlan, error) {
	if f.app == nil || f.app.restore == nil {
		return nil, restoreFacadeUnavailable(f.app)
	}
	return f.app.restore.Status(ctx)
}

func (f restoreManagerFacade) VerifyRestore(ctx context.Context, planID string) (backupusecase.RestorePlan, error) {
	if f.app == nil || f.app.restore == nil {
		return backupusecase.RestorePlan{}, restoreFacadeUnavailable(f.app)
	}
	plan, err := f.app.restore.Verify(ctx, planID)
	f.app.logBackupAudit("restore_verify", planID, err)
	return plan, err
}

func (f restoreManagerFacade) ActivateRestore(ctx context.Context, planID, digest string) (backupusecase.RestorePlan, error) {
	if f.app == nil || f.app.restore == nil {
		return backupusecase.RestorePlan{}, restoreFacadeUnavailable(f.app)
	}
	plan, err := f.app.restore.Activate(ctx, planID, digest)
	f.app.logBackupAudit("restore_activate", planID, err)
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

func (f backupManagerFacade) Status(ctx context.Context) (backupusecase.StatusSnapshot, error) {
	if f.app == nil || !f.app.cfg.Backup.Enabled {
		return backupusecase.StatusSnapshot{Enabled: false, Health: backupusecase.HealthDisabled}, nil
	}
	if f.app.backupInitErr != nil || f.app.backup == nil {
		return backupusecase.StatusSnapshot{Enabled: true, Health: backupusecase.HealthFailed}, nil
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
		status.FailureCategory = operational.LastFailureCategory
		if operational.LastAuditSuccessUnixMillis > 0 {
			age := time.Now().UTC().Unix() - time.UnixMilli(operational.LastAuditSuccessUnixMillis).UTC().Unix()
			if age < 0 {
				age = 0
			}
			status.VerificationAgeSeconds = &age
		} else if status.Health == backupusecase.HealthHealthy {
			status.Health = backupusecase.HealthUnknown
		}
	} else if status.Health == backupusecase.HealthHealthy {
		status.Health = backupusecase.HealthUnknown
	}
	return status, nil
}

func (f backupManagerFacade) ListRestorePoints(ctx context.Context) ([]backupusecase.RestorePoint, error) {
	if f.app == nil || f.app.backup == nil {
		return nil, backupFacadeUnavailable(f.app)
	}
	return f.app.backup.ListRestorePoints(ctx)
}

func (f backupManagerFacade) Trigger(ctx context.Context, kind backupartifact.RestorePointKind) (backupusecase.Job, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.Job{}, backupFacadeUnavailable(f.app)
	}
	if coordinator, ok := f.app.backupRuntime.(backupRuntimeStatusProvider); ok && coordinator.Status().DoctorHealth != backupusecase.HealthHealthy {
		return backupusecase.Job{}, fmt.Errorf("backup doctor is not healthy")
	}
	job, err := f.app.backup.Trigger(ctx, backupusecase.TriggerRequest{Kind: kind, ConfigFingerprint: f.app.backupFingerprint})
	f.app.logBackupAudit("backup_trigger", job.ID, err, wklog.String("kind", string(kind)), wklog.Uint64("backupEpoch", job.Epoch))
	return job, err
}

func (f backupManagerFacade) Cancel(ctx context.Context, id string, epoch uint64) (backupusecase.Job, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.Job{}, backupFacadeUnavailable(f.app)
	}
	job, err := f.app.backup.Cancel(ctx, id, epoch)
	f.app.logBackupAudit("backup_cancel", id, err, wklog.Uint64("backupEpoch", epoch))
	return job, err
}

func (f backupManagerFacade) Hold(ctx context.Context, id string) (backupusecase.RestorePoint, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.RestorePoint{}, backupFacadeUnavailable(f.app)
	}
	point, err := f.app.backup.Hold(ctx, id)
	f.app.logBackupAudit("backup_hold", id, err)
	return point, err
}

func (f backupManagerFacade) Release(ctx context.Context, id string) (backupusecase.RestorePoint, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.RestorePoint{}, backupFacadeUnavailable(f.app)
	}
	point, err := f.app.backup.Release(ctx, id)
	f.app.logBackupAudit("backup_release", id, err)
	return point, err
}

func (f backupManagerFacade) Verify(ctx context.Context, id string) (backupusecase.Verification, error) {
	if f.app == nil || f.app.backup == nil {
		return backupusecase.Verification{}, backupFacadeUnavailable(f.app)
	}
	verification, err := f.app.backup.Verify(ctx, id)
	f.app.logBackupAudit("backup_verify", id, err)
	return verification, err
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
