package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type appScheduledBackupNode interface {
	backupinfra.ScheduledBackupController
	backupinfra.FullExportNode
	backupinfra.RepositoryProbeCluster
	backupinfra.RestoreCluster
	backupinfra.RestorePartitionNode
	runtimebackup.ScheduledLeadership
	accessnode.PresenceRPCNode
	nodeRPCRegistrar
}

// wireBackup builds one clean scheduled-full-backup subsystem. Configuration
// lives exclusively in Controller state and is managed through Manager.
func (a *App) wireBackup(clusterCfg cluster.Config) error {
	if a == nil {
		return nil
	}
	node, ok := a.cluster.(appScheduledBackupNode)
	if !ok {
		// Tests and narrow embeddings may replace the production cluster with a
		// deliberately smaller lifecycle stub.
		return nil
	}
	clusterCfg = clusterCfg.WithDefaults()
	dataDir := strings.TrimSpace(a.cfg.DataDir)
	if dataDir == "" {
		dataDir = strings.TrimSpace(clusterCfg.DataDir)
	}
	if dataDir == "" || strings.TrimSpace(clusterCfg.Control.ClusterID) == "" {
		return fmt.Errorf("backup app: data directory and cluster identity are required")
	}
	var cipher *backupinfra.CredentialCipher
	installationSecret := strings.TrimSpace(a.cfg.Manager.JWTSecret)
	if installationSecret != "" {
		var err error
		cipher, err = backupinfra.NewCredentialCipher(
			installationSecret, clusterCfg.Control.ClusterID,
		)
		if err != nil {
			return err
		}
	}
	repository, err := backupinfra.NewRepositoryProvider(dataDir, cipher)
	if err != nil {
		return err
	}
	stateStore, err := backupinfra.NewScheduledControllerStateStore(node)
	if err != nil {
		return err
	}
	scheduled, err := backupusecase.NewScheduledService(
		backupusecase.ScheduledOptions{
			StateStore: stateStore,
			Now:        time.Now,
			NewID:      func() string { return newBackupIdentity("backup") },
		},
	)
	if err != nil {
		return err
	}
	client := accessnode.NewClient(node)
	exporter, err := backupinfra.NewFullExportService(
		node, repository, client, filepath.Join(dataDir, "backup-staging"),
	)
	if err != nil {
		return err
	}
	probe, err := backupinfra.NewClusterRepositoryProbe(
		node, repository, client,
	)
	if err != nil {
		return err
	}
	management, err := backupusecase.NewManagementService(
		backupusecase.ManagementOptions{
			Scheduled: scheduled, Repository: repository,
			Sealer: repository, Probe: probe,
			ClusterID: clusterCfg.Control.ClusterID, Now: time.Now,
		},
	)
	if err != nil {
		return err
	}
	slots, err := backupinfra.NewDistributedSlotExecutor(
		node, exporter, client,
	)
	if err != nil {
		return err
	}
	finalizer, err := backupinfra.NewArchiveFinalizer(
		backupinfra.ArchiveFinalizerOptions{
			ClusterID:   clusterCfg.Control.ClusterID,
			Application: "wukongim",
			Now:         time.Now,
		},
	)
	if err != nil {
		return err
	}
	jobRunner, err := backupusecase.NewJobRunner(
		backupusecase.JobRunnerOptions{
			Scheduled: scheduled, Repository: repository,
			Slots: slots, Finalizer: finalizer, Now: time.Now,
		},
	)
	if err != nil {
		return err
	}
	localRestore, err := backupinfra.NewStagedRestoreNodeService(
		node, repository, dataDir,
	)
	if err != nil {
		return err
	}
	if a.messageIDs == nil {
		return fmt.Errorf("backup app: message ID allocator is required")
	}
	localRestore.SetMessageIDFloor(a.messageIDs.SetFloor)
	localRestore.SetMaintenanceQuiescer(a.suspendRestoreSideEffects)
	localRestore.SetMaintenanceResumer(a.resumeRestoreSideEffects)
	restoreExecutor, err := backupinfra.NewDistributedRestoreExecutor(
		node, localRestore, client,
	)
	if err != nil {
		return err
	}
	executor := appRestoreExecutor{app: a, delegate: restoreExecutor}
	restoreService, err := backupusecase.NewRestoreService(
		backupusecase.RestoreServiceOptions{
			StateStore: stateStore, Repository: repository,
			Preflight: executor, Now: time.Now,
			NewID: func() string { return newBackupIdentity("restore") },
			NewActivation: func() string {
				return newBackupIdentity("generation")
			},
		},
	)
	if err != nil {
		return err
	}
	restoreRunner, err := backupusecase.NewRestoreRunner(
		scheduled, restoreService, executor, time.Now,
	)
	if err != nil {
		return err
	}
	worker, err := runtimebackup.NewScheduledRuntime(
		runtimebackup.ScheduledRuntimeOptions{
			Scheduled: scheduled, State: scheduled,
			Runner: jobRunner, Restore: restoreRunner,
			Leadership: node, Tick: time.Second,
			OnError: func(err error) {
				if a.logger != nil {
					a.logger.Warn(
						"scheduled backup worker step failed",
						wklog.Event("internal.app.scheduled_backup_step_failed"),
						wklog.Error(err),
					)
				}
			},
		},
	)
	if err != nil {
		return err
	}
	adapter := accessnode.New(accessnode.Options{
		ScheduledBackup: exporter, ScheduledBackupProbe: probe,
		ScheduledRestore: localRestore,
		Logger:           a.logger.Named("access.node.backup"),
	})
	node.RegisterRPC(
		accessnode.ScheduledBackupSlotRPCServiceID,
		nodeRPCHandlerFunc(adapter.HandleScheduledBackupSlotRPC),
	)
	node.RegisterRPC(
		accessnode.ScheduledBackupMessageRPCServiceID,
		nodeRPCHandlerFunc(adapter.HandleScheduledBackupMessageRPC),
	)
	node.RegisterRPC(
		accessnode.ScheduledBackupRepositoryProbeRPCServiceID,
		nodeRPCHandlerFunc(adapter.HandleScheduledBackupRepositoryProbeRPC),
	)
	node.RegisterRPC(
		accessnode.ScheduledBackupRestoreRPCServiceID,
		nodeRPCHandlerFunc(adapter.HandleScheduledBackupRestoreRPC),
	)
	a.backup = management
	a.scheduledBackup = scheduled
	a.restore = restoreService
	a.backupRuntime = worker
	return nil
}

func (a *App) newBackupManagement() accessmanager.BackupManagement {
	if a == nil {
		return nil
	}
	return a.backup
}

type restoreManagerFacade struct {
	service *backupusecase.RestoreService
}

func (f restoreManagerFacade) StartRestore(
	ctx context.Context,
	archiveID string,
	initiator string,
) (job backupcontract.RestoreJob, err error) {
	if f.service == nil {
		return job, backupusecase.ErrDisabled
	}
	return f.service.StartRestore(ctx, archiveID, initiator)
}

func (f restoreManagerFacade) CancelRestore(
	ctx context.Context,
	jobID string,
) error {
	if f.service == nil {
		return backupusecase.ErrDisabled
	}
	return f.service.RequestCancellation(ctx, jobID)
}

func (a *App) newRestoreManagement() accessmanager.RestoreManagement {
	if a == nil || a.restore == nil {
		return nil
	}
	return restoreManagerFacade{service: a.restore}
}

func (a *App) managerSessionEpoch() (uint64, error) {
	if a == nil || a.scheduledBackup == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := a.scheduledBackup.State(ctx)
	if err != nil {
		return 0, err
	}
	return state.ManagerSessionEpoch, nil
}

func newBackupIdentity(prefix string) string {
	var body [16]byte
	if _, err := rand.Read(body[:]); err != nil {
		// The process cannot safely publish an identity without OS entropy.
		panic(fmt.Sprintf("backup app: random identity: %v", err))
	}
	return prefix + "-" + hex.EncodeToString(body[:])
}

var (
	_ accessmanager.BackupManagement  = (*backupusecase.ManagementService)(nil)
	_ accessmanager.RestoreManagement = restoreManagerFacade{}
	_ appScheduledBackupNode          = (*cluster.Node)(nil)
)
