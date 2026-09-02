package app

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStartRejectsInvalidOrTerminalLifecycleState(t *testing.T) {
	var nilApp *App
	if err := nilApp.Start(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil App Start() error = %v, want ErrInvalidConfig", err)
	}
	if err := (&App{}).Start(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing cluster Start() error = %v, want ErrInvalidConfig", err)
	}
	if err := (&App{cluster: &fakeCluster{}, stopped: true}).Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("stopped App Start() error = %v, want ErrStopped", err)
	}
	if err := (&App{cluster: &fakeCluster{}, started: true}).Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("started App Start() error = %v, want ErrAlreadyStarted", err)
	}
}

func TestStartFailureAtEachOptionalRuntimeReleasesClusterOwnership(t *testing.T) {
	stageErr := errors.New("stage start failed")
	tests := []struct {
		name    string
		install func(*App, *[]string)
	}{
		{
			name: "backup runtime",
			install: func(app *App, calls *[]string) {
				app.backupRuntime = &recordingWorkerRuntime{calls: calls, name: "backup", startErr: stageErr}
			},
		},
		{
			name: "presence worker",
			install: func(app *App, calls *[]string) {
				app.presenceWorker = &recordingWorkerRuntime{calls: calls, name: "presence", startErr: stageErr}
			},
		},
		{
			name: "plugin runtime",
			install: func(app *App, calls *[]string) {
				app.pluginRuntime = &recordingWorkerRuntime{calls: calls, name: "plugin-runtime", startErr: stageErr}
			},
		},
		{
			name: "plugin hook",
			install: func(app *App, calls *[]string) {
				app.pluginHook = &recordingWorkerRuntime{calls: calls, name: "plugin-hook", startErr: stageErr}
			},
		},
		{
			name: "webhook",
			install: func(app *App, calls *[]string) {
				app.webhook = &recordingWorkerRuntime{calls: calls, name: "webhook", startErr: stageErr}
			},
		},
		{
			name: "delivery worker",
			install: func(app *App, calls *[]string) {
				app.deliveryWorker = &recordingWorkerRuntime{calls: calls, name: "delivery", startErr: stageErr}
			},
		},
		{
			name: "top collector",
			install: func(app *App, calls *[]string) {
				app.top = &recordingWorkerRuntime{calls: calls, name: "top", startErr: stageErr}
			},
		},
		{
			name: "api",
			install: func(app *App, calls *[]string) {
				app.api = &fakeAPI{calls: calls, startErr: stageErr}
			},
		},
		{
			name: "manager",
			install: func(app *App, calls *[]string) {
				app.manager = &fakeManager{calls: calls, startErr: stageErr}
			},
		},
		{
			name: "prometheus",
			install: func(app *App, calls *[]string) {
				app.prometheus = &recordingWorkerRuntime{calls: calls, name: "prometheus", startErr: stageErr}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := make([]string, 0, 4)
			app := &App{cluster: &fakeCluster{calls: &calls}}
			test.install(app, &calls)

			err := app.Start(context.Background())
			if !errors.Is(err, stageErr) {
				t.Fatalf("Start() error = %v, want stage failure", err)
			}
			if len(calls) != 3 || calls[0] != "cluster.start" || !strings.HasSuffix(calls[1], ".start") || calls[2] != "cluster.stop" {
				t.Fatalf("calls = %v, want cluster start, failing stage, cluster rollback", calls)
			}
			if app.started || app.clusterStarted {
				t.Fatalf("failed Start retained app=%v cluster=%v ownership", app.started, app.clusterStarted)
			}
		})
	}
}

func TestStartSeedJoinFailuresReleaseEveryAcquiredOwner(t *testing.T) {
	startErr := errors.New("seed start failed")
	waitErr := errors.New("seed admission failed")
	t.Run("start", func(t *testing.T) {
		calls := make([]string, 0, 4)
		app := &App{
			cluster:      &fakeCluster{calls: &calls},
			seedJoinLoop: &lifecycleSeedJoinRuntime{calls: &calls, startErr: startErr},
		}
		err := app.Start(context.Background())
		if !errors.Is(err, startErr) {
			t.Fatalf("Start() error = %v, want seed start failure", err)
		}
		if got := joinCalls(calls); got != "cluster.start,seed.start,cluster.stop" {
			t.Fatalf("calls = %s", got)
		}
		if app.started || app.clusterStarted || app.seedJoinStarted {
			t.Fatalf("ownership remained: started=%v cluster=%v seed=%v", app.started, app.clusterStarted, app.seedJoinStarted)
		}
	})
	t.Run("admission", func(t *testing.T) {
		calls := make([]string, 0, 5)
		app := &App{
			cluster:      &fakeCluster{calls: &calls},
			seedJoinLoop: &lifecycleSeedJoinRuntime{calls: &calls, waitErr: waitErr},
		}
		err := app.Start(context.Background())
		if !errors.Is(err, waitErr) {
			t.Fatalf("Start() error = %v, want seed admission failure", err)
		}
		if got := joinCalls(calls); got != "cluster.start,seed.start,seed.wait,seed.stop,cluster.stop" {
			t.Fatalf("calls = %s", got)
		}
		if app.started || app.clusterStarted || app.seedJoinStarted {
			t.Fatalf("ownership remained: started=%v cluster=%v seed=%v", app.started, app.clusterStarted, app.seedJoinStarted)
		}
	})
}

func TestStopFailureAtEachLifecycleOwnerRemainsRetryable(t *testing.T) {
	stopErr := errors.New("stage stop failed")
	tests := []struct {
		name    string
		install func(*App, *[]string) func()
	}{
		{
			name: "gateway",
			install: func(app *App, calls *[]string) func() {
				runtime := &fakeGateway{calls: calls, stopErr: stopErr}
				app.gateway, app.gatewayStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "prometheus",
			install: func(app *App, calls *[]string) func() {
				runtime := &recordingWorkerRuntime{calls: calls, name: "prometheus", stopErr: stopErr}
				app.prometheus, app.prometheusStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "manager",
			install: func(app *App, calls *[]string) func() {
				runtime := &fakeManager{calls: calls, stopErr: stopErr}
				app.manager, app.managerStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "api",
			install: func(app *App, calls *[]string) func() {
				runtime := &fakeAPI{calls: calls, stopErr: stopErr}
				app.api, app.apiStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "top",
			install: func(app *App, calls *[]string) func() {
				runtime := &recordingWorkerRuntime{calls: calls, name: "top", stopErr: stopErr}
				app.top, app.topStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "backup runtime",
			install: func(app *App, calls *[]string) func() {
				runtime := &recordingWorkerRuntime{calls: calls, name: "backup", stopErr: stopErr}
				app.backupRuntime, app.backupRuntimeStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "delivery worker",
			install: func(app *App, calls *[]string) func() {
				runtime := &recordingWorkerRuntime{calls: calls, name: "delivery", stopErr: stopErr}
				app.deliveryWorker, app.deliveryStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "webhook",
			install: func(app *App, calls *[]string) func() {
				runtime := &recordingWorkerRuntime{calls: calls, name: "webhook", stopErr: stopErr}
				app.webhook, app.webhookStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "plugin hook",
			install: func(app *App, calls *[]string) func() {
				runtime := &recordingWorkerRuntime{calls: calls, name: "plugin-hook", stopErr: stopErr}
				app.pluginHook, app.pluginHookStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "plugin runtime",
			install: func(app *App, calls *[]string) func() {
				runtime := &recordingWorkerRuntime{calls: calls, name: "plugin-runtime", stopErr: stopErr}
				app.pluginRuntime, app.pluginRuntimeStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "presence worker",
			install: func(app *App, calls *[]string) func() {
				runtime := &recordingWorkerRuntime{calls: calls, name: "presence", stopErr: stopErr}
				app.presenceWorker, app.presenceStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "seed join",
			install: func(app *App, calls *[]string) func() {
				runtime := &lifecycleSeedJoinRuntime{calls: calls, stopErr: stopErr}
				app.seedJoinLoop, app.seedJoinStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
		{
			name: "cluster",
			install: func(app *App, calls *[]string) func() {
				runtime := &fakeCluster{calls: calls, stopErr: stopErr}
				app.cluster, app.clusterStarted = runtime, true
				return func() { runtime.stopErr = nil }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := make([]string, 0, 2)
			app := &App{started: true}
			clearFailure := test.install(app, &calls)

			if err := app.Stop(context.Background()); !errors.Is(err, stopErr) {
				t.Fatalf("first Stop() error = %v, want stage stop failure", err)
			}
			if !app.started {
				t.Fatal("failed Stop discarded retry ownership")
			}
			clearFailure()
			if err := app.Stop(context.Background()); err != nil {
				t.Fatalf("retry Stop() error = %v", err)
			}
			if app.started {
				t.Fatal("successful retry retained app ownership")
			}
			if len(calls) != 2 {
				t.Fatalf("stop calls = %v, want one failed attempt and one retry", calls)
			}
		})
	}
}

func TestGatewayStartRollbackClosesStartedEntriesBeforeBackupRuntime(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 12)
	app := &App{
		cluster:       &fakeCluster{calls: &calls},
		backupRuntime: &recordingWorkerRuntime{calls: &calls, name: "backup"},
		api:           &fakeAPI{calls: &calls},
		manager:       &fakeManager{calls: &calls},
		prometheus:    &recordingWorkerRuntime{calls: &calls, name: "prometheus"},
		gateway:       &fakeGateway{calls: &calls, startErr: gatewayErr},
	}

	err := app.Start(context.Background())
	if !errors.Is(err, gatewayErr) {
		t.Fatalf("Start() error = %v, want gateway failure", err)
	}
	want := "cluster.start,backup.start,api.start,manager.start,prometheus.start,gateway.start," +
		"prometheus.stop,manager.stop,api.stop,backup.stop,cluster.stop"
	if got := joinCalls(calls); got != want {
		t.Fatalf("rollback calls = %s, want %s", got, want)
	}
	if app.started || app.clusterStarted || app.backupRuntimeStarted || app.apiStarted || app.managerStarted || app.prometheusStarted {
		t.Fatalf("rollback ownership remained: started=%v cluster=%v backup=%v api=%v manager=%v prometheus=%v",
			app.started, app.clusterStarted, app.backupRuntimeStarted, app.apiStarted, app.managerStarted, app.prometheusStarted)
	}
}

func TestGatewayStartRollbackAggregatesEveryStopErrorAndRetainsRetryOwnership(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	prometheusStopErr := errors.New("prometheus stop failed")
	managerStopErr := errors.New("manager stop failed")
	apiStopErr := errors.New("api stop failed")
	backupStopErr := errors.New("backup stop failed")
	clusterStopErr := errors.New("cluster stop failed")
	calls := make([]string, 0, 24)
	cluster := &fakeCluster{calls: &calls, stopErr: clusterStopErr}
	backup := &recordingWorkerRuntime{calls: &calls, name: "backup", stopErr: backupStopErr}
	api := &fakeAPI{calls: &calls, stopErr: apiStopErr}
	manager := &fakeManager{calls: &calls, stopErr: managerStopErr}
	prometheus := &recordingWorkerRuntime{calls: &calls, name: "prometheus", stopErr: prometheusStopErr}
	app := &App{
		cluster:       cluster,
		backupRuntime: backup,
		api:           api,
		manager:       manager,
		prometheus:    prometheus,
		gateway:       &fakeGateway{calls: &calls, startErr: gatewayErr},
	}

	err := app.Start(context.Background())
	for _, wantErr := range []error{gatewayErr, prometheusStopErr, managerStopErr, apiStopErr, backupStopErr, clusterStopErr} {
		if !errors.Is(err, wantErr) {
			t.Fatalf("Start() error = %v, want joined %v", err, wantErr)
		}
	}
	wantFirst := "cluster.start,backup.start,api.start,manager.start,prometheus.start,gateway.start," +
		"prometheus.stop,manager.stop,api.stop,backup.stop,cluster.stop"
	if got := joinCalls(calls); got != wantFirst {
		t.Fatalf("failed rollback calls = %s, want %s", got, wantFirst)
	}
	if !app.started || !app.clusterStarted || !app.backupRuntimeStarted || !app.apiStarted || !app.managerStarted || !app.prometheusStarted {
		t.Fatalf("failed rollback discarded retry ownership: started=%v cluster=%v backup=%v api=%v manager=%v prometheus=%v",
			app.started, app.clusterStarted, app.backupRuntimeStarted, app.apiStarted, app.managerStarted, app.prometheusStarted)
	}

	cluster.stopErr = nil
	backup.stopErr = nil
	api.stopErr = nil
	manager.stopErr = nil
	prometheus.stopErr = nil
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() retry error = %v", err)
	}
	wantRetry := wantFirst + ",prometheus.stop,manager.stop,api.stop,backup.stop,cluster.stop"
	if got := joinCalls(calls); got != wantRetry {
		t.Fatalf("retry calls = %s, want %s", got, wantRetry)
	}
	if app.started || app.clusterStarted || app.backupRuntimeStarted || app.apiStarted || app.managerStarted || app.prometheusStarted {
		t.Fatalf("retry ownership remained: started=%v cluster=%v backup=%v api=%v manager=%v prometheus=%v",
			app.started, app.clusterStarted, app.backupRuntimeStarted, app.apiStarted, app.managerStarted, app.prometheusStarted)
	}
}

type lifecycleSeedJoinRuntime struct {
	calls    *[]string
	startErr error
	waitErr  error
	stopErr  error
}

func (r *lifecycleSeedJoinRuntime) Start(context.Context) error {
	*r.calls = append(*r.calls, "seed.start")
	return r.startErr
}

func (r *lifecycleSeedJoinRuntime) WaitForAdmission(context.Context) error {
	*r.calls = append(*r.calls, "seed.wait")
	return r.waitErr
}

func (r *lifecycleSeedJoinRuntime) Stop(context.Context) error {
	*r.calls = append(*r.calls, "seed.stop")
	return r.stopErr
}
