package app

import (
	"context"
	"errors"
	"testing"
)

func TestProductLifecycleOrdersEveryGenericSideEffectOwner(t *testing.T) {
	t.Parallel()

	t.Run("successful generation", func(t *testing.T) {
		calls := make([]string, 0, 32)
		app := newGenericLifecycleContractApp(&calls, nil)

		if err := app.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if err := app.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}

		want := "cluster.start,seed.start,seed.wait," +
			"backup.start,presence.start,plugin-runtime.start,plugin-hook.start," +
			"webhook.start,delivery.start,top.start,api.start,manager.start," +
			"prometheus.start,gateway.start," +
			"gateway.stop,prometheus.stop,manager.stop,api.stop,top.stop," +
			"backup.stop,delivery.stop,webhook.stop,plugin-hook.stop," +
			"plugin-runtime.stop,presence.stop,seed.stop,cluster.stop"
		if got := joinCalls(calls); got != want {
			t.Fatalf("lifecycle calls = %s, want %s", got, want)
		}
		if app.started {
			t.Fatal("successful Stop retained generation ownership")
		}
	})

	t.Run("gateway startup rollback", func(t *testing.T) {
		gatewayErr := errors.New("gateway admission failed")
		calls := make([]string, 0, 32)
		app := newGenericLifecycleContractApp(&calls, gatewayErr)

		err := app.Start(context.Background())
		if !errors.Is(err, gatewayErr) {
			t.Fatalf("Start() error = %v, want gateway failure", err)
		}

		want := "cluster.start,seed.start,seed.wait," +
			"backup.start,presence.start,plugin-runtime.start,plugin-hook.start," +
			"webhook.start,delivery.start,top.start,api.start,manager.start," +
			"prometheus.start,gateway.start," +
			"prometheus.stop,manager.stop,api.stop,top.stop,backup.stop," +
			"delivery.stop,webhook.stop,plugin-hook.stop,plugin-runtime.stop," +
			"presence.stop,seed.stop,cluster.stop"
		if got := joinCalls(calls); got != want {
			t.Fatalf("rollback calls = %s, want %s", got, want)
		}
		if app.started || app.clusterStarted || app.seedJoinStarted ||
			app.backupRuntimeStarted || app.presenceStarted ||
			app.pluginRuntimeStarted || app.pluginHookStarted ||
			app.webhookStarted || app.deliveryStarted || app.topStarted ||
			app.apiStarted || app.managerStarted || app.prometheusStarted ||
			app.gatewayStarted {
			t.Fatalf("rollback retained lifecycle ownership: %#v", app)
		}
	})
}

func newGenericLifecycleContractApp(
	calls *[]string,
	gatewayStartErr error,
) *App {
	return &App{
		cluster:      &fakeCluster{calls: calls},
		seedJoinLoop: &lifecycleSeedJoinRuntime{calls: calls},
		backupRuntime: &recordingWorkerRuntime{
			calls: calls, name: "backup",
		},
		presenceWorker: &recordingWorkerRuntime{
			calls: calls, name: "presence",
		},
		pluginRuntime: &recordingWorkerRuntime{
			calls: calls, name: "plugin-runtime",
		},
		pluginHook: &recordingWorkerRuntime{
			calls: calls, name: "plugin-hook",
		},
		webhook: &recordingWorkerRuntime{
			calls: calls, name: "webhook",
		},
		deliveryWorker: &recordingWorkerRuntime{
			calls: calls, name: "delivery",
		},
		top:        &recordingWorkerRuntime{calls: calls, name: "top"},
		api:        &fakeAPI{calls: calls},
		manager:    &fakeManager{calls: calls},
		prometheus: &recordingWorkerRuntime{calls: calls, name: "prometheus"},
		gateway:    &fakeGateway{calls: calls, startErr: gatewayStartErr},
	}
}
