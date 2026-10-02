//go:build integration

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func deferredAppConfig() Config {
	return Config{Gateway: GatewayConfig{Listeners: []coregateway.ListenerOptions{{Name: "tcp", Network: "tcp", Address: "127.0.0.1:0", Transport: "gnet", Protocol: "wkproto"}}, Runtime: coregateway.RuntimeOptions{AsyncSendWorkers: 2, AsyncSendQueueCapacity: 8}, Session: coregateway.SessionOptions{MaxInboundBytes: 1024, AsyncSendBatchMaxBytes: 2048}}}
}
func TestProductDeferredGatewayCompositionAndStopBeforeStart(t *testing.T) {
	a, err := newTestApp(t, deferredAppConfig(), WithCluster(newFakePresenceCluster(1, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if a.channelSubmissions == nil {
		t.Fatal("default gateway has no ordered submission owner")
	}
	if _, ok := a.gatewayHandler().(coregateway.DeferredSendBatchHandler); !ok {
		t.Fatal("product gateway not using deferred entry")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.channelSubmissions.Submit([]channelappend.SendBatchItem{{}}, func([]channelappend.SendBatchItemResult) {}); !errors.Is(err, channelappend.ErrRouteNotReady) {
		t.Fatal("stop-before-start leaked owner", err)
	}
}
func TestProductDeferredGatewayPreservesInjectedMessageUsecase(t *testing.T) {
	a, err := newTestApp(t, deferredAppConfig(), WithCluster(newFakePresenceCluster(1, nil)), WithMessages(message.New(message.Options{})))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer a.Stop(ctx)
	if a.channelSubmissions != nil {
		t.Fatal("unneeded owner for injected message usecase")
	}
	if _, ok := a.gatewayHandler().(coregateway.DeferredSendBatchHandler); ok {
		t.Fatal("injected usecase silently replaced")
	}
}
func TestProductDeferredGatewayConstructorFailureClosesOwner(t *testing.T) {
	registry := goruntimeregistry.New()
	baseline := registry.Baseline()
	cfg := deferredAppConfig()
	cfg.Gateway.Listeners[0].Name = ""
	a, err := newTestApp(t, cfg, WithCluster(newFakePresenceCluster(1, nil)), WithGoroutineRegistry(registry))
	if err == nil || a != nil {
		t.Fatal("expected gateway constructor rejection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := registry.Group(goruntimeregistry.ModuleChannelAppend).WaitFrom(ctx, baseline); err != nil {
		t.Fatal("constructor leaked submission workers", err)
	}
}

type appOrderedSender func([]channelappend.SendBatchItem) []channelappend.SendBatchItemResult

func (f appOrderedSender) SendBatch(items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	return f(items)
}
func blockedAppSubmission(t *testing.T) (*channelappend.OrderedSubmitter, chan struct{}) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	owner, err := channelappend.NewOrderedSubmitter(channelappend.OrderedSubmitterOptions{Workers: 1, Capacity: 2, PayloadCapacity: 1024}, appOrderedSender(func(items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
		return make([]channelappend.SendBatchItemResult, len(items))
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Submit([]channelappend.SendBatchItem{{Command: channelappend.SendCommand{FromUID: "u", ChannelID: "g", ChannelType: 2}}}, func([]channelappend.SendBatchItemResult) { close(entered); <-release }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("submission did not start")
	}
	return owner, release
}
func TestProductDeferredStopKeepsDependenciesUntilOwnerDrains(t *testing.T) {
	owner, release := blockedAppSubmission(t)
	var calls []string
	a := &App{started: true, clusterStarted: true, cluster: &fakeCluster{calls: &calls}, channelSubmissions: owner, logger: wklog.NewNop()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.Stop(ctx)
	if err == nil || len(calls) != 0 {
		close(release)
		t.Fatalf("dependencies stopped before accepted callback: err=%v calls=%v", err, calls)
	}
	close(release)
	ctx2, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := a.Stop(ctx2); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "cluster.stop" {
		t.Fatal(calls)
	}
}
func TestProductDeferredRestoreWaitsBeforeReopening(t *testing.T) {
	owner, release := blockedAppSubmission(t)
	worker := &deferredLifecycleWorker{}
	a := &App{channelSubmissions: owner, deliveryWorker: worker, logger: wklog.NewNop()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.suspendRestoreSideEffects(ctx); err == nil {
		close(release)
		t.Fatal("restore skipped accepted callback")
	}
	if err := a.resumeRestoreSideEffects(ctx); err == nil {
		close(release)
		t.Fatal("restore reopened incomplete drain")
	}
	if worker.starts != 0 {
		close(release)
		t.Fatal("dependencies resumed before submission drain")
	}
	close(release)
	ctx2, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	defer owner.Close(ctx2)
	if err := a.suspendRestoreSideEffects(ctx2); err != nil {
		t.Fatal(err)
	}
	if err := a.resumeRestoreSideEffects(ctx2); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	if err := owner.Submit([]channelappend.SendBatchItem{{}}, func([]channelappend.SendBatchItemResult) { close(done) }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx2.Done():
		t.Fatal(ctx2.Err())
	}
}

type deferredLifecycleWorker struct{ starts, stops int }

func (w *deferredLifecycleWorker) Start(context.Context) error { w.starts++; return nil }
func (w *deferredLifecycleWorker) Stop(context.Context) error  { w.stops++; return nil }
