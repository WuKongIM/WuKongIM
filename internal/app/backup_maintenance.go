package app

import (
	"context"
	"errors"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type appMaintenanceObserver struct {
	app  *App
	next cluster.RestoreMaintenanceObserver
}

func (o appMaintenanceObserver) RestoreMaintenanceChanged(enabled bool) {
	if o.next != nil {
		o.next.RestoreMaintenanceChanged(enabled)
	}
	if o.app == nil {
		return
	}
	if enabled {
		// Close every foreground entry before draining node-local side effects.
		o.app.applyRestoreGatewayMaintenance(true)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		o.app.observeRestoreSideEffectError(
			"suspend", o.app.suspendRestoreSideEffects(ctx),
		)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := o.app.resumeRestoreSideEffects(ctx); err != nil {
		o.app.observeRestoreSideEffectError("resume", err)
		// Keep entry admission closed if a local runtime could not restart.
		return
	}
	o.app.applyRestoreGatewayMaintenance(false)
}

type restoreGatewayRuntime interface {
	SetAcceptingNewSessions(bool)
	DisconnectAll()
}

func (a *App) applyRestoreGatewayMaintenance(enabled bool) {
	if a == nil {
		return
	}
	a.restoreMaintenance.Store(enabled)
	gateway, ok := a.gateway.(restoreGatewayRuntime)
	if !ok {
		return
	}
	gateway.SetAcceptingNewSessions(!enabled)
	if enabled {
		gateway.DisconnectAll()
	}
}

func (a *App) observeRestoreSideEffectError(phase string, err error) {
	if a == nil || err == nil || a.logger == nil {
		return
	}
	a.logger.Error(
		"restore node side-effect transition failed",
		wklog.Event("internal.app.restore_side_effect_transition_failed"),
		wklog.String("phase", phase),
		wklog.Error(err),
	)
}

func (a *App) suspendRestoreSideEffects(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.restoreSideEffectsMu.Lock()
	defer a.restoreSideEffectsMu.Unlock()
	// The observer can run while App.Start is still composing runtimes. Repeat
	// idempotent stops on every call so a later startup pass cannot escape a
	// maintenance fence merely because an earlier pass saw closed workers.
	a.restoreSideEffectsSuspended = true
	var resultErr error
	conversationQuiesced := true
	if err := a.pauseRestoreAdmissions(ctx); err != nil {
		conversationQuiesced = false
		resultErr = errors.Join(resultErr, err)
	}
	if a.channelAppends != nil {
		a.channelAppends.PauseForRestore()
		if err := a.channelAppends.WaitIdle(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		} else if err := a.channelAppends.ResetAfterRestore(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.deliveryWorker != nil {
		if err := a.deliveryWorker.Stop(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.webhook != nil {
		if err := a.webhook.Stop(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.pluginHook != nil {
		if err := a.pluginHook.Stop(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.conversationActiveWorker != nil {
		if err := a.conversationActiveWorker.Stop(ctx); err != nil {
			conversationQuiesced = false
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.conversationAuthority != nil && conversationQuiesced {
		a.conversationAuthority.resetAfterRestore()
	}
	a.resetRestoreSensitiveCaches()
	if runtime, ok := a.cluster.(interface{ PauseLocalRestoreRuntime() }); ok {
		runtime.PauseLocalRestoreRuntime()
	}
	return resultErr
}

func (a *App) resumeRestoreSideEffects(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.restoreSideEffectsMu.Lock()
	defer a.restoreSideEffectsMu.Unlock()
	if !a.restoreSideEffectsSuspended {
		return nil
	}
	var resultErr error
	// Clear again after the durable logical activation. The first reset at
	// maintenance entry drains pre-restore state; this second reset prevents
	// maintenance-local or late pre-restore reads from surviving resume.
	a.resetRestoreSensitiveCaches()
	if a.users != nil {
		if err := a.users.ReloadSystemUIDCache(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.conversationActiveWorker != nil {
		if err := a.conversationActiveWorker.Start(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.pluginHook != nil {
		if err := a.pluginHook.Start(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.webhook != nil {
		if err := a.webhook.Start(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if a.deliveryWorker != nil {
		if err := a.deliveryWorker.Start(ctx); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	if resultErr != nil {
		return resultErr
	}
	if a.channelAppends != nil {
		a.channelAppends.ResumeAfterRestore()
	}
	if a.conversationAuthority != nil {
		a.conversationAuthority.resumeAfterRestore()
	}
	if runtime, ok := a.cluster.(interface{ ResumeLocalRestoreRuntime() }); ok {
		runtime.ResumeLocalRestoreRuntime()
	}
	a.restoreSideEffectsSuspended = false
	return nil
}

func (a *App) resetRestoreSensitiveCaches() {
	if a == nil {
		return
	}
	if a.deliveryMeta != nil {
		a.deliveryMeta.resetAfterRestore()
	}
	if a.channelAppendMetadata != nil {
		a.channelAppendMetadata.ResetAfterRestore()
	}
	if a.messages != nil {
		a.messages.ResetAfterRestore()
	}
	if resetter, ok := a.cluster.(interface{ ResetLocalRestoreCaches() }); ok {
		resetter.ResetLocalRestoreCaches()
	}
}

func (a *App) pauseRestoreAdmissions(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if a.channelAppends != nil {
		a.channelAppends.PauseForRestore()
	}
	if a.conversationAuthority != nil {
		return a.conversationAuthority.pauseForRestore(ctx)
	}
	return nil
}

type appRestoreExecutor struct {
	app      *App
	delegate *backupinfra.DistributedRestoreExecutor
}

func (e appRestoreExecutor) Check(
	ctx context.Context,
	job backupcontract.RestoreJob,
	plan backupcontract.Plan,
	manifest backupartifact.ArchiveManifest,
) error {
	return e.delegate.Check(ctx, job, plan, manifest)
}

func (e appRestoreExecutor) EnterMaintenance(
	ctx context.Context,
	job backupcontract.RestoreJob,
) (string, error) {
	return e.delegate.EnterMaintenance(ctx, job)
}

func (e appRestoreExecutor) VerifyArchive(
	ctx context.Context,
	job backupcontract.RestoreJob,
) error {
	return e.delegate.VerifyArchive(ctx, job)
}

func (e appRestoreExecutor) StageSlot(
	ctx context.Context,
	job backupcontract.RestoreJob,
	hashSlot uint16,
	attempt uint32,
) (backupusecase.RestoreStageResult, error) {
	return e.delegate.StageSlot(ctx, job, hashSlot, attempt)
}

func (e appRestoreExecutor) VerifySlot(
	ctx context.Context,
	job backupcontract.RestoreJob,
	hashSlot uint16,
	attempt uint32,
) error {
	return e.delegate.VerifySlot(ctx, job, hashSlot, attempt)
}

func (e appRestoreExecutor) ActivateRestore(
	ctx context.Context,
	job backupcontract.RestoreJob,
) error {
	return e.delegate.ActivateRestore(ctx, job)
}

func (e appRestoreExecutor) Rollback(
	ctx context.Context,
	job backupcontract.RestoreJob,
) error {
	return e.delegate.Rollback(ctx, job)
}

func (e appRestoreExecutor) ExitMaintenance(
	ctx context.Context,
	job backupcontract.RestoreJob,
	succeeded bool,
) error {
	if err := e.delegate.ExitMaintenance(ctx, job, succeeded); err != nil {
		return err
	}
	return nil
}

var (
	_ backupusecase.RestorePreflight = appRestoreExecutor{}
	_ backupusecase.RestoreExecutor  = appRestoreExecutor{}
)
