package app

import (
	"context"
	"errors"
	"testing"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

func TestRestoreMaintenanceObserverKeepsAdmissionClosedUntilSideEffectsResume(t *testing.T) {
	resumeErr := errors.New("plugin restart failed")
	gateway := &gatewayAdmissionStub{accepting: true}
	plugin := &recordingWorkerRuntime{startErr: resumeErr}
	app := &App{gateway: gateway, pluginHook: plugin}
	observer := appMaintenanceObserver{app: app}

	observer.RestoreMaintenanceChanged(true)
	if gateway.AcceptingNewSessions() || gateway.disconnectCount != 1 || !app.restoreMaintenance.Load() {
		t.Fatalf("maintenance entry accepting=%v disconnects=%d state=%v, want closed admission", gateway.AcceptingNewSessions(), gateway.disconnectCount, app.restoreMaintenance.Load())
	}
	if plugin.stopCount != 1 {
		t.Fatalf("plugin stop count = %d, want 1", plugin.stopCount)
	}

	observer.RestoreMaintenanceChanged(false)
	if gateway.AcceptingNewSessions() || !app.restoreMaintenance.Load() {
		t.Fatalf("failed resume accepting=%v state=%v, want maintenance fence retained", gateway.AcceptingNewSessions(), app.restoreMaintenance.Load())
	}
	if plugin.startCount != 1 || !app.restoreSideEffectsSuspended {
		t.Fatalf("failed resume starts=%d suspended=%v, want retryable suspended state", plugin.startCount, app.restoreSideEffectsSuspended)
	}

	plugin.startErr = nil
	observer.RestoreMaintenanceChanged(false)
	if !gateway.AcceptingNewSessions() || app.restoreMaintenance.Load() {
		t.Fatalf("successful retry accepting=%v state=%v, want admission reopened", gateway.AcceptingNewSessions(), app.restoreMaintenance.Load())
	}
	if plugin.startCount != 2 || app.restoreSideEffectsSuspended {
		t.Fatalf("successful retry starts=%d suspended=%v, want resumed state", plugin.startCount, app.restoreSideEffectsSuspended)
	}
}

func TestRestoreMaintenanceSuspensionReturnsEveryShutdownFailure(t *testing.T) {
	deliveryErr := errors.New("delivery stop failed")
	webhookErr := errors.New("webhook stop failed")
	pluginErr := errors.New("plugin stop failed")
	app := &App{
		deliveryWorker: &recordingWorkerRuntime{stopErr: deliveryErr},
		webhook:        &recordingWorkerRuntime{stopErr: webhookErr},
		pluginHook:     &recordingWorkerRuntime{stopErr: pluginErr},
	}

	err := app.suspendRestoreSideEffects(context.Background())
	for _, want := range []error{deliveryErr, webhookErr, pluginErr} {
		if !errors.Is(err, want) {
			t.Fatalf("suspendRestoreSideEffects() error = %v, want errors.Is(%v)", err, want)
		}
	}
	if !app.restoreSideEffectsSuspended {
		t.Fatal("failed suspension did not retain the maintenance fence")
	}
}

func TestRestoreManagerFacadeFailsExplicitlyWhenBackupIsDisabled(t *testing.T) {
	facade := restoreManagerFacade{}
	if _, err := facade.StartRestore(context.Background(), "archive-1", "operator"); !errors.Is(err, backupusecase.ErrDisabled) {
		t.Fatalf("StartRestore() error = %v, want ErrDisabled", err)
	}
	if err := facade.CancelRestore(context.Background(), "restore-1"); !errors.Is(err, backupusecase.ErrDisabled) {
		t.Fatalf("CancelRestore() error = %v, want ErrDisabled", err)
	}

	var nilApp *App
	if got := nilApp.newRestoreManagement(); got != nil {
		t.Fatalf("nil App newRestoreManagement() = %#v, want nil", got)
	}
	if got := (&App{}).newRestoreManagement(); got != nil {
		t.Fatalf("disabled App newRestoreManagement() = %#v, want nil", got)
	}
}
