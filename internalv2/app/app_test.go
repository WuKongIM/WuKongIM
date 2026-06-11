package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytransport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func newTestApp(t *testing.T, cfg Config, opts ...Option) (*App, error) {
	t.Helper()
	opts = append([]Option{WithLogger(wklog.NewNop())}, opts...)
	app, err := New(cfg, opts...)
	if app != nil {
		t.Cleanup(app.restoreDiagnosticsSink)
	}
	return app, err
}

func startTestApp(t *testing.T, app *App) {
	t.Helper()
	startCtx, startCancel := context.WithTimeout(context.Background(), time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
}

func TestStartOrderIsClusterThenGateway(t *testing.T) {
	calls := make([]string, 0, 2)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start", got)
	}
}

func TestGatewayStartFailureStopsCluster(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 3)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	logger := &recordingAppLogger{}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway), WithLogger(logger))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) {
		t.Fatalf("Start() error = %v, want gateway error", err)
	}
	if got := joinCalls(calls); got != "cluster.start,gateway.start,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,cluster.stop", got)
	}
	requireAppLogEvent(t, logger, "ERROR", "internalv2.app.lifecycle_start_failed")
}

func TestStartOrderIncludesAPIBeforeGatewayWhenConfigured(t *testing.T) {
	calls := make([]string, 0, 3)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,api.start,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,api.start,gateway.start", got)
	}
}

func TestDefaultPresenceConfigUsesTouchDefaults(t *testing.T) {
	cfg := defaultPresenceConfig(PresenceConfig{})

	if cfg.ActivationTimeout != 3*time.Second {
		t.Fatalf("ActivationTimeout = %v, want 3s", cfg.ActivationTimeout)
	}
	if cfg.TouchFlushInterval != time.Second {
		t.Fatalf("TouchFlushInterval = %v, want 1s", cfg.TouchFlushInterval)
	}
	if cfg.TouchBatchSize != 512 {
		t.Fatalf("TouchBatchSize = %d, want 512", cfg.TouchBatchSize)
	}
	if cfg.RouteTTL != 90*time.Second {
		t.Fatalf("RouteTTL = %v, want 90s", cfg.RouteTTL)
	}

	negative := defaultPresenceConfig(PresenceConfig{
		ActivationTimeout:  -time.Second,
		TouchFlushInterval: -time.Second,
		TouchBatchSize:     -1,
		RouteTTL:           -time.Second,
	})
	if negative.ActivationTimeout != -time.Second ||
		negative.TouchFlushInterval != -time.Second ||
		negative.TouchBatchSize != -1 ||
		negative.RouteTTL != -time.Second {
		t.Fatalf("negative presence values were overwritten: %#v", negative)
	}
}

func TestValidatePresenceConfigRejectsInvalidTouchValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  PresenceConfig
	}{
		{name: "activation timeout", cfg: PresenceConfig{ActivationTimeout: -time.Nanosecond}},
		{name: "touch flush interval", cfg: PresenceConfig{TouchFlushInterval: -time.Nanosecond}},
		{name: "touch batch size", cfg: PresenceConfig{TouchBatchSize: -1}},
		{name: "route ttl", cfg: PresenceConfig{RouteTTL: -time.Nanosecond}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validatePresenceConfig(tt.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validatePresenceConfig() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestDefaultDeliveryConfigKeepsDisabledAndUsesRuntimeDefaults(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(6)
	t.Cleanup(func() { runtime.GOMAXPROCS(oldProcs) })

	cfg := defaultDeliveryConfig(DeliveryConfig{})

	if cfg.Enabled {
		t.Fatalf("Enabled = true, want false by default")
	}
	if cfg.ChannelWriteReactorCount != 6 {
		t.Fatalf("ChannelWriteReactorCount = %d, want 6", cfg.ChannelWriteReactorCount)
	}
	if cfg.ChannelWritePrepareWorkers != 8 {
		t.Fatalf("ChannelWritePrepareWorkers = %d, want 8", cfg.ChannelWritePrepareWorkers)
	}
	if cfg.ChannelWriteAppendWorkers != 96 {
		t.Fatalf("ChannelWriteAppendWorkers = %d, want 96", cfg.ChannelWriteAppendWorkers)
	}
	if cfg.ChannelWritePostCommitWorkers != 8 {
		t.Fatalf("ChannelWritePostCommitWorkers = %d, want 8", cfg.ChannelWritePostCommitWorkers)
	}
	if cfg.ChannelWriteRecipientDispatchConcurrency != 4 {
		t.Fatalf("ChannelWriteRecipientDispatchConcurrency = %d, want 4", cfg.ChannelWriteRecipientDispatchConcurrency)
	}
	if cfg.FanoutPageSize != 512 {
		t.Fatalf("FanoutPageSize = %d, want 512", cfg.FanoutPageSize)
	}
	if cfg.PushBatchSize != 512 {
		t.Fatalf("PushBatchSize = %d, want 512", cfg.PushBatchSize)
	}
	if cfg.PendingAckTTL != 30*time.Second {
		t.Fatalf("PendingAckTTL = %v, want 30s", cfg.PendingAckTTL)
	}
	if cfg.PendingAckMaxPerSession != 1024 {
		t.Fatalf("PendingAckMaxPerSession = %d, want 1024", cfg.PendingAckMaxPerSession)
	}
	if cfg.EventQueueSize != 1024 {
		t.Fatalf("EventQueueSize = %d, want 1024", cfg.EventQueueSize)
	}

	negative := defaultDeliveryConfig(DeliveryConfig{
		Enabled:                                  true,
		ChannelWriteReactorCount:                 -5,
		ChannelWritePrepareWorkers:               -6,
		ChannelWriteAppendWorkers:                -7,
		ChannelWritePostCommitWorkers:            -8,
		ChannelWriteRecipientDispatchConcurrency: -7,
		FanoutPageSize:                           -1,
		PushBatchSize:                            -2,
		PendingAckTTL:                            -time.Second,
		PendingAckMaxPerSession:                  -3,
		EventQueueSize:                           -4,
	})
	if !negative.Enabled || negative.ChannelWriteReactorCount != -5 ||
		negative.ChannelWritePrepareWorkers != -6 ||
		negative.ChannelWriteAppendWorkers != -7 ||
		negative.ChannelWritePostCommitWorkers != -8 ||
		negative.ChannelWriteRecipientDispatchConcurrency != -7 ||
		negative.FanoutPageSize != -1 || negative.PushBatchSize != -2 ||
		negative.PendingAckTTL != -time.Second || negative.PendingAckMaxPerSession != -3 ||
		negative.EventQueueSize != -4 {
		t.Fatalf("negative delivery values were overwritten: %#v", negative)
	}
}

func TestValidateDeliveryConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  DeliveryConfig
	}{
		{name: "channelwrite reactor count", cfg: DeliveryConfig{ChannelWriteReactorCount: -1}},
		{name: "channelwrite prepare workers", cfg: DeliveryConfig{ChannelWritePrepareWorkers: -1}},
		{name: "channelwrite append workers", cfg: DeliveryConfig{ChannelWriteAppendWorkers: -1}},
		{name: "channelwrite post-commit workers", cfg: DeliveryConfig{ChannelWritePostCommitWorkers: -1}},
		{name: "channelwrite recipient dispatch concurrency", cfg: DeliveryConfig{ChannelWriteRecipientDispatchConcurrency: -1}},
		{name: "fanout page size", cfg: DeliveryConfig{FanoutPageSize: -1}},
		{name: "push batch size", cfg: DeliveryConfig{PushBatchSize: -1}},
		{name: "pending ack ttl", cfg: DeliveryConfig{PendingAckTTL: -time.Nanosecond}},
		{name: "pending ack max per session", cfg: DeliveryConfig{PendingAckMaxPerSession: -1}},
		{name: "event queue size", cfg: DeliveryConfig{EventQueueSize: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDeliveryConfig(tt.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateDeliveryConfig() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestDefaultConversationConfigUsesRuntimeDefaults(t *testing.T) {
	cfg := defaultConversationConfig(ConversationConfig{})

	if cfg.MaxLastMessageConcurrency != 32 {
		t.Fatalf("MaxLastMessageConcurrency = %d, want 32", cfg.MaxLastMessageConcurrency)
	}

	negative := defaultConversationConfig(ConversationConfig{
		MaxLastMessageConcurrency: -2,
	})
	if negative.MaxLastMessageConcurrency != -2 {
		t.Fatalf("negative conversation values were overwritten: %#v", negative)
	}
}

func TestDefaultConversationAuthorityConfig(t *testing.T) {
	cfg := defaultConversationConfig(ConversationConfig{})
	if cfg.AuthorityCacheMaxRowsPerUID != 4096 ||
		cfg.AuthorityCacheMaxRows != 100000 ||
		cfg.AuthorityListDBWindowMax != 1000 ||
		cfg.AuthorityHandoffTimeout != 3*time.Second ||
		cfg.AuthorityFlushInterval != time.Second ||
		cfg.AuthorityFlushBatchRows != 512 ||
		cfg.AuthorityAdmitBatchRows != 512 ||
		cfg.AuthorityAdmitConcurrency != 16 {
		t.Fatalf("conversation authority defaults = %#v", cfg)
	}
}

func TestValidateConversationConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ConversationConfig)
	}{
		{name: "last message concurrency", mutate: func(cfg *ConversationConfig) { cfg.MaxLastMessageConcurrency = -1 }},
		{name: "authority cache max rows per uid negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityCacheMaxRowsPerUID = -1 }},
		{name: "authority cache max rows per uid zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityCacheMaxRowsPerUID = 0 }},
		{name: "authority cache max rows negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityCacheMaxRows = -1 }},
		{name: "authority cache max rows zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityCacheMaxRows = 0 }},
		{name: "authority list db window max negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityListDBWindowMax = -1 }},
		{name: "authority list db window max zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityListDBWindowMax = 0 }},
		{name: "authority handoff timeout negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityHandoffTimeout = -time.Nanosecond }},
		{name: "authority handoff timeout zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityHandoffTimeout = 0 }},
		{name: "authority flush interval negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushInterval = -time.Nanosecond }},
		{name: "authority flush interval zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushInterval = 0 }},
		{name: "authority flush batch rows negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushBatchRows = -1 }},
		{name: "authority flush batch rows zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityFlushBatchRows = 0 }},
		{name: "authority admit batch rows negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityAdmitBatchRows = -1 }},
		{name: "authority admit batch rows zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityAdmitBatchRows = 0 }},
		{name: "authority admit concurrency negative", mutate: func(cfg *ConversationConfig) { cfg.AuthorityAdmitConcurrency = -1 }},
		{name: "authority admit concurrency zero", mutate: func(cfg *ConversationConfig) { cfg.AuthorityAdmitConcurrency = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConversationConfig(ConversationConfig{})
			tt.mutate(&cfg)
			if err := validateConversationConfig(cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateConversationConfig() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestNewBuildsRootLogger(t *testing.T) {
	calls := make([]string, 0, 2)
	cfg := Config{Log: LogConfig{Dir: t.TempDir(), Console: false, Format: "json"}}
	cfg.Log.SetExplicitFlags(false, true)
	app, err := New(
		cfg,
		WithCluster(&fakeCluster{calls: &calls}),
		WithGateway(&fakeGateway{calls: &calls}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(app.restoreDiagnosticsSink)
	if app.logger == nil {
		t.Fatal("logger was not wired")
	}
	if app.logger.Named("internalv2") == nil {
		t.Fatal("named logger is nil")
	}
}

func TestStopSyncsLogger(t *testing.T) {
	calls := make([]string, 0, 4)
	logger := &recordingAppLogger{}
	app, err := New(
		Config{},
		WithCluster(&fakeCluster{calls: &calls}),
		WithGateway(&fakeGateway{calls: &calls}),
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if logger.syncCalls != 1 {
		t.Fatalf("Sync calls = %d, want 1", logger.syncCalls)
	}
}

func TestNewWiresDeliveryWhenEnabled(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 1},
			Delivery: DeliveryConfig{Enabled: true},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if app.Delivery() == nil {
		t.Fatal("delivery usecase was not wired")
	}
	if app.deliveryManager == nil {
		t.Fatal("delivery manager was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.DeliveryPushRPCServiceID]; !ok {
		t.Fatalf("delivery push rpc service was not registered")
	}
	if app.deliveryWorker == nil {
		t.Fatal("delivery worker was not wired")
	}
	if app.channelWriteDeliveryWorker == nil {
		t.Fatal("channelwrite recipient delivery worker was not wired")
	}
	if app.deliveryManager == nil || app.deliveryManager.PendingAckCount() != 0 {
		t.Fatal("delivery manager was not initialized for async runtime")
	}
	group, ok := app.deliveryWorker.(deliveryWorkerGroup)
	if !ok {
		t.Fatalf("delivery worker = %T, want deliveryWorkerGroup", app.deliveryWorker)
	}
	if len(group) != 3 {
		t.Fatalf("delivery worker count = %d, want recipient worker, retry scheduler, and manager", len(group))
	}
	if group[0] != app.deliveryRetry {
		t.Fatalf("delivery worker[0] = %T, want retry scheduler", group[0])
	}
	if group[1] != app.deliveryManager {
		t.Fatalf("delivery worker[1] = %T, want manager", group[1])
	}
	if _, ok := group[2].(*channelwrite.RecipientDeliveryWorker); !ok {
		t.Fatalf("delivery worker[2] = %T, want recipient delivery worker", group[2])
	}
	if group[2] != app.channelWriteDeliveryWorker {
		t.Fatalf("delivery worker[2] = %T, want app channelwrite recipient delivery worker", group[2])
	}
	if app.deliveryRetry == nil {
		t.Fatal("delivery retry scheduler was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.DeliveryFanoutRPCServiceID]; !ok {
		t.Fatalf("delivery fanout rpc service was not registered")
	}
}

func TestNewWiresChannelMembershipProjection(t *testing.T) {
	cluster := &recordingDeliveryMetaNode{
		snapshot: readyFakeClusterSnapshot(1, 16),
	}
	app, err := newTestApp(t,
		Config{Cluster: clusterv2.Config{NodeID: 1}},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.channels == nil {
		t.Fatal("channel usecase was not wired")
	}

	if err := app.channels.AddSubscribers(context.Background(), channelusecase.SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Subscribers: []string{"u1", "u2"},
	}); err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if len(cluster.membershipUpserts) != 1 {
		t.Fatalf("membership upserts = %#v, want one call", cluster.membershipUpserts)
	}
	got := cluster.membershipUpserts[0]
	if got.channelID != "g1" || got.channelType != int64(frame.ChannelTypeGroup) || !reflect.DeepEqual(got.uids, []string{"u1", "u2"}) {
		t.Fatalf("membership upsert = %#v, want g1 group u1/u2", got)
	}
}

func TestNewWiresMessageAppendMetricsWhenDeliveryDisabled(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster:       clusterv2.Config{NodeID: 3},
			Observability: ObservabilityConfig{MetricsEnabled: true},
			Delivery:      DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.messages == nil {
		t.Fatal("message usecase was not wired")
	}
	if app.channelWrites == nil || app.channelWriteRouter == nil {
		t.Fatalf("channel write runtime = (%T, %T), want group and router", app.channelWrites, app.channelWriteRouter)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   "room-metrics",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}

	families, err := app.metrics.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != "wukongim_message_append_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["path"] == "channelplane" && labels["result"] == "ok" && metric.GetCounter().GetValue() == 1 {
				return
			}
		}
	}
	t.Fatal("message append metric for successful channelplane append was not observed")
}

func TestNewWiresChannelWriteCommitEffectsWhenDeliveryDisabled(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 3},
			Delivery: DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.messages == nil {
		t.Fatal("message usecase was not wired")
	}
	if app.channelWrites == nil || app.channelWriteRouter == nil {
		t.Fatalf("channel write runtime = (%T, %T), want group and router", app.channelWrites, app.channelWriteRouter)
	}
	startTestApp(t, app)

	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-conversation-1",
		Payload:     []byte("conversation payload"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}

	cluster.mu.Lock()
	if len(cluster.conversationStateBatches) != 0 {
		t.Fatalf("conversation state batches = %#v, want no synchronous DB write", cluster.conversationStateBatches)
	}
	cluster.mu.Unlock()
	if app.conversationAuthority == nil {
		t.Fatal("local conversation authority was not wired")
	}
	if app.conversationAuthorityClient == nil {
		t.Fatal("conversation authority client was not reused by app wiring")
	}
	if _, ok := cluster.registeredHandlers[accessnode.ConversationAuthorityRPCServiceID]; !ok {
		t.Fatalf("conversation authority rpc service was not registered")
	}
	if _, ok := cluster.registeredHandlers[accessnode.ChannelWriteRPCServiceID]; !ok {
		t.Fatalf("channel write rpc service was not registered")
	}

	for _, uid := range []string{"u1", "u2"} {
		requireConversationEventually(t, app, uid, channelID, frame.ChannelTypePerson)
	}
}

func TestNewDoesNotWireConversationFallbackWhenAuthorityUnavailable(t *testing.T) {
	cluster := &fakeConversationFallbackCluster{}
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 3},
			Delivery: DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.conversationAuthority != nil || app.conversationAuthorityClient != nil {
		t.Fatalf("authority fields = (%T, %T), want authority unavailable", app.conversationAuthority, app.conversationAuthorityClient)
	}
	if app.channelWrites == nil || app.channelWriteRouter == nil {
		t.Fatalf("channel write runtime = (%T, %T), want append-only group and router", app.channelWrites, app.channelWriteRouter)
	}
	startTestApp(t, app)

	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-fallback-1",
		Payload:     []byte("fallback payload"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}

	cluster.mu.Lock()
	stateBatches := append([][]metadb.UserConversationState(nil), cluster.conversationStateBatches...)
	cluster.mu.Unlock()
	if len(stateBatches) != 0 {
		t.Fatalf("conversation state batches = %#v, want no legacy DB projection", stateBatches)
	}

	list, err := app.Conversations().List(context.Background(), conversationusecase.ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Conversations().List() error = %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("conversation list = %#v, want no legacy DB fallback row", list.Items)
	}
}

func TestChannelWriteUpdatesPersonConversationEventually(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 3},
			Delivery: DeliveryConfig{Enabled: false},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	first, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-coalesce-1",
		Payload:     []byte("old"),
	})
	if err != nil || first.Reason != message.ReasonSuccess {
		t.Fatalf("first Send() = %#v err=%v, want success", first, err)
	}
	second, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "u2",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-coalesce-2",
		Payload:     []byte("new"),
	})
	if err != nil || second.Reason != message.ReasonSuccess {
		t.Fatalf("second Send() = %#v err=%v, want success", second, err)
	}

	for _, uid := range []string{"u1", "u2"} {
		requireConversationEventually(t, app, uid, channelID, frame.ChannelTypePerson)
	}
}

func TestConversationAuthorityFansOutConfiguredSmallGroups(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	cluster.subscribers = map[string][]string{"g-small": []string{"sender", "member"}}
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   "g-small",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "client-small-1",
		Payload:     []byte("small"),
	})
	if err != nil || result.Reason != message.ReasonSuccess {
		t.Fatalf("Send() = %#v err=%v, want success", result, err)
	}

	for _, uid := range []string{"sender", "member"} {
		requireConversationEventually(t, app, uid, "g-small", frame.ChannelTypeGroup)
	}
}

func TestChannelWriteUsesDurableSubscribersAndSenderActiveRow(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	cluster.subscribers = map[string][]string{"g-small-missing-sender": []string{"member"}}
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   "g-small-missing-sender",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "client-small-missing-sender-1",
		Payload:     []byte("small"),
	})
	if err != nil || result.Reason != message.ReasonSuccess {
		t.Fatalf("Send() = %#v err=%v, want success", result, err)
	}

	member := requireConversationEventually(t, app, "member", "g-small-missing-sender", frame.ChannelTypeGroup)
	if member.ReadSeq != 0 {
		t.Fatalf("member ReadSeq = %d, want receiver row without sender read state", member.ReadSeq)
	}
	sender := requireConversationEventually(t, app, "sender", "g-small-missing-sender", frame.ChannelTypeGroup)
	if sender.ReadSeq != result.MessageSeq {
		t.Fatalf("sender ReadSeq = %d, want latest message seq %d", sender.ReadSeq, result.MessageSeq)
	}
}

func TestAppStopFlushesConversationActiveRows(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
			Conversation: ConversationConfig{
				AuthorityFlushInterval:  time.Hour,
				AuthorityFlushBatchRows: 8,
			},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.conversationActiveWorker == nil {
		t.Fatal("conversation active flush worker was not wired")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	channelID := runtimechannelid.EncodePersonChannel("sender", "receiver")
	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "client-stop-flush-1",
		Payload:     []byte("flush"),
	})
	if err != nil || result.Reason != message.ReasonSuccess {
		t.Fatalf("Send() = %#v err=%v, want success", result, err)
	}
	requireConversationEventually(t, app, "sender", channelID, frame.ChannelTypePerson)

	cluster.mu.Lock()
	beforeStop := len(cluster.conversationPatchBatches)
	cluster.mu.Unlock()
	if beforeStop != 0 {
		t.Fatalf("conversation active flush batches before stop = %d, want no periodic flush with hourly interval", beforeStop)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if len(cluster.conversationPatchBatches) == 0 {
		t.Fatal("conversation active flush batches after stop = 0, want final dirty flush")
	}
	patches := conversationPatchesByUID(cluster.conversationPatchBatches[len(cluster.conversationPatchBatches)-1])
	if patches["sender"].ReadSeq != result.MessageSeq {
		t.Fatalf("sender flushed ReadSeq = %d, want %d", patches["sender"].ReadSeq, result.MessageSeq)
	}
	if patches["receiver"].ReadSeq != 0 {
		t.Fatalf("receiver flushed ReadSeq = %d, want receiver row without sender read state", patches["receiver"].ReadSeq)
	}
}

func TestChannelWritePagesAllGroupSubscribers(t *testing.T) {
	cluster := newFakePresenceCluster(3, nil)
	cluster.snapshot = readyFakeClusterSnapshot(3, 16)
	cluster.subscribers = map[string][]string{"g-large": []string{"sender", "member"}}
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 3},
		},
		WithCluster(cluster),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.messages.Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   "g-large",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "client-large-1",
		Payload:     []byte("large"),
	})
	if err != nil || result.Reason != message.ReasonSuccess {
		t.Fatalf("Send() = %#v err=%v, want success", result, err)
	}

	for _, uid := range []string{"sender", "member"} {
		requireConversationEventually(t, app, uid, "g-large", frame.ChannelTypeGroup)
	}
}

func TestConversationAuthorityRouteLifecycleWatchesLocalAuthorityEvents(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 20}
	store := &appRecordingConversationAuthorityStore{}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	watch := make(chan clusterv2.RouteAuthorityEvent, 1)
	node := &recordingConversationAuthorityRouteNode{
		nodeID: 1,
		routes: map[string]clusterv2.Route{
			"u1": routeFromConversationTarget(target),
		},
		watch: watch,
	}
	client := clusterinfra.NewConversationAuthorityClient(node, local)
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Watch:          node.WatchRouteAuthorities,
		HandoffTimeout: 50 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(target)}}
	waitUntil(t, time.Second, func() bool {
		return local.AdmitPatches(context.Background(), target, nil) == nil
	})

	patch := conversationusecase.ActivePatch{
		UID:         "u1",
		ChannelID:   "g-watch",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   101,
		MessageSeq:  7,
	}
	if err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{patch}); err != nil {
		t.Fatalf("client AdmitPatches() error = %v", err)
	}
	page, err := client.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("client ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g-watch" || page.Rows[0].ActiveAt != 100 {
		t.Fatalf("authority page rows = %#v, want watched local cache row", page.Rows)
	}
}

func TestConversationActiveAdmitBatchRPCUpdatesRemoteAndLocalCache(t *testing.T) {
	localTarget := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 20}
	remoteTarget := conversationusecase.RouteTarget{HashSlot: 2, SlotID: 2, LeaderNodeID: 2, RouteRevision: 11, AuthorityEpoch: 21}
	localStore := &appRecordingConversationAuthorityStore{}
	remoteStore := &appRecordingConversationAuthorityStore{}
	localAuthority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: localStore})
	remoteAuthority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 2, Store: remoteStore})
	localAuthority.markActive(localTarget)
	remoteAuthority.markActive(remoteTarget)
	remoteAdapter := accessnode.New(accessnode.Options{ConversationAuthority: remoteAuthority})
	node := &recordingConversationAuthorityRouteNode{
		nodeID: 1,
		routes: map[string]clusterv2.Route{
			"sender":   routeFromConversationTarget(remoteTarget),
			"receiver": routeFromConversationTarget(localTarget),
		},
		handler: appNodeRPCHandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return remoteAdapter.HandleConversationAuthorityRPC(ctx, payload)
		}),
	}
	client := clusterinfra.NewConversationAuthorityClient(node, localAuthority)

	err := client.AdmitActiveBatch(context.Background(), conversationactive.ActiveBatch{
		SenderUID:   "sender",
		ChannelID:   "g-active",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAtMS:  1234,
		Recipients:  []conversationactive.ActiveEntry{{UID: "receiver"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	senderPage, err := client.ListUserConversationActiveView(context.Background(), "sender", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView(sender) error = %v", err)
	}
	if len(senderPage.Rows) != 1 || senderPage.Rows[0].ChannelID != "g-active" || senderPage.Rows[0].ReadSeq != 42 || senderPage.Rows[0].ActiveAt != 1234 {
		t.Fatalf("sender active rows = %#v, want cached sender row with read seq", senderPage.Rows)
	}

	receiverPage, err := client.ListUserConversationActiveView(context.Background(), "receiver", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView(receiver) error = %v", err)
	}
	if len(receiverPage.Rows) != 1 || receiverPage.Rows[0].ChannelID != "g-active" || receiverPage.Rows[0].ReadSeq != 0 || receiverPage.Rows[0].ActiveAt != 1234 {
		t.Fatalf("receiver active rows = %#v, want cached receiver row without sender read seq", receiverPage.Rows)
	}
	if localStore.totalTouchPatches() != 0 || remoteStore.totalTouchPatches() != 0 {
		t.Fatalf("touch patches local/remote = %d/%d, want cache-visible rows before flush", localStore.totalTouchPatches(), remoteStore.totalTouchPatches())
	}
}

func TestConversationAuthorityRouteLifecycleRemoteEventDrainsPreviousLocalTarget(t *testing.T) {
	localTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 20}
	remoteTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, RouteRevision: 11, AuthorityEpoch: 21}
	store := &appRecordingConversationAuthorityStore{}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	watch := make(chan clusterv2.RouteAuthorityEvent)
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Initial: func() []clusterv2.RouteAuthority {
			return []clusterv2.RouteAuthority{authorityFromConversationTarget(localTarget)}
		},
		Watch:          func() <-chan clusterv2.RouteAuthorityEvent { return watch },
		HandoffTimeout: 75 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := local.AdmitPatches(context.Background(), localTarget, []conversationusecase.ActivePatch{{
		UID:         "u1",
		ChannelID:   "g-drain",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   101,
		MessageSeq:  7,
	}}); err != nil {
		t.Fatalf("seed AdmitPatches() error = %v", err)
	}

	sent := make(chan struct{})
	go func() {
		watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(remoteTarget)}}
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("timed out sending remote route-authority event")
	}
	waitUntil(t, time.Second, func() bool {
		return store.totalTouchPatches() == 1
	})
	if !store.lastTouchDeadlineWithin(75*time.Millisecond, 75*time.Millisecond) {
		t.Fatalf("handoff drain deadline = %v, want about 75ms", store.lastTouchDeadline())
	}
	if _, err := local.ListUserConversationActiveViewForTarget(context.Background(), localTarget, "u1", metadb.UserConversationActiveCursor{}, 10); !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("stale local ListUserConversationActiveViewForTarget() error = %v, want %v", err, conversationusecase.ErrStaleRoute)
	}
	if err := local.AdmitPatches(context.Background(), localTarget, []conversationusecase.ActivePatch{{
		UID:         "u1",
		ChannelID:   "g-drain-2",
		ChannelType: 2,
		ActiveAt:    102,
		MessageSeq:  8,
	}}); !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("stale local AdmitPatches() error = %v, want %v", err, conversationusecase.ErrStaleRoute)
	}
	if err := lifecycle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestConversationAuthorityRouteLifecycleIgnoresStaleRouteEvents(t *testing.T) {
	currentTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 1, RouteRevision: 10, AuthorityEpoch: 20}
	staleTarget := conversationusecase.RouteTarget{HashSlot: 7, SlotID: 2, LeaderNodeID: 2, RouteRevision: 9, AuthorityEpoch: 21}
	store := &appRecordingConversationAuthorityStore{}
	local := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store})
	watch := make(chan clusterv2.RouteAuthorityEvent)
	lifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
		LocalAuthority: local,
		LocalNodeID:    1,
		Initial: func() []clusterv2.RouteAuthority {
			return []clusterv2.RouteAuthority{authorityFromConversationTarget(currentTarget)}
		},
		Watch:          func() <-chan clusterv2.RouteAuthorityEvent { return watch },
		HandoffTimeout: 50 * time.Millisecond,
	})
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	sent := make(chan struct{})
	go func() {
		watch <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{authorityFromConversationTarget(staleTarget)}}
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("timed out sending stale route-authority event")
	}
	if err := lifecycle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if store.totalTouchPatches() != 0 {
		t.Fatalf("touch patches = %d, want stale route event ignored without drain", store.totalTouchPatches())
	}
	if err := local.AdmitPatches(context.Background(), currentTarget, nil); err != nil {
		t.Fatalf("current local target was not preserved: %v", err)
	}
}

func TestDeliveryWorkerGroupStopKeepsDependenciesRunningWhenDrainFails(t *testing.T) {
	retry := &recordingWorkerRuntime{}
	manager := &recordingWorkerRuntime{stopErr: context.DeadlineExceeded}
	group := deliveryWorkerGroup{retry, manager}

	err := group.Stop(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}
	if manager.stopCount != 1 {
		t.Fatalf("manager stop count = %d, want 1", manager.stopCount)
	}
	if retry.stopCount != 0 {
		t.Fatalf("retry stop count = %d, want dependency kept running", retry.stopCount)
	}
}

func TestAppSubscriberPlannerReturnsPersonChannelUIDs(t *testing.T) {
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	page, err := appSubscriberPlanner{}.NextPartitionPage(context.Background(), runtimedelivery.FanoutTask{
		Envelope: runtimedelivery.Envelope{ChannelID: channelID, ChannelType: frame.ChannelTypePerson},
	}, "", 512)
	if err != nil {
		t.Fatalf("NextPartitionPage() error = %v", err)
	}
	if !page.Done {
		t.Fatalf("Done = false, want true")
	}
	if len(page.UIDs) != 2 || page.UIDs[0] == page.UIDs[1] {
		t.Fatalf("UIDs = %#v, want two distinct participants", page.UIDs)
	}
	want := map[string]bool{"u1": true, "u2": true}
	for _, uid := range page.UIDs {
		if !want[uid] {
			t.Fatalf("unexpected UID %q in %#v", uid, page.UIDs)
		}
	}
}

func TestDeliveryRuntimeAdapterScopesPersonChannelAcrossPartitions(t *testing.T) {
	runner := &appRecordingFanoutRunner{}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		Planner: runtimedelivery.NewPlanner(runtimedelivery.PlannerOptions{
			Partitioner: appStaticDeliveryPartitioner{
				partitions: []runtimedelivery.Partition{
					{ID: 1, LeaderNodeID: 1},
					{ID: 2, LeaderNodeID: 2},
					{ID: 3, LeaderNodeID: 3},
				},
			},
		}),
		Runner: runner,
	})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")

	err := deliveryRuntimeAdapter{manager: manager}.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		MessageID:   1,
		MessageSeq:  1,
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		FromUID:     "u1",
	})
	if err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	tasks := waitAppFanoutTasks(t, runner, 1, time.Second)
	if len(tasks) != 1 {
		t.Fatalf("fanout tasks = %d, want 1 scoped person task", len(tasks))
	}
	got := tasks[0].Envelope.MessageScopedUIDs
	if len(got) != 2 {
		t.Fatalf("MessageScopedUIDs = %#v, want two participants", got)
	}
	want := map[string]bool{"u1": true, "u2": true}
	for _, uid := range got {
		if !want[uid] {
			t.Fatalf("unexpected scoped UID %q in %#v", uid, got)
		}
	}
}

func waitAppDeliveryPendingAckCount(t *testing.T, app *App, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got int
	for time.Now().Before(deadline) {
		got = app.deliveryManager.PendingAckCount()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending ack count = %d, want %d", got, want)
}

func waitAppFanoutTasks(t *testing.T, runner *appRecordingFanoutRunner, want int, timeout time.Duration) []runtimedelivery.FanoutTask {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var tasks []runtimedelivery.FanoutTask
	for time.Now().Before(deadline) {
		tasks = runner.snapshot()
		if len(tasks) == want {
			return tasks
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fanout tasks = %d, want %d", len(tasks), want)
	return nil
}

func TestDeliveryEnabledPersonSendWritesRecvAndRecvackClearsPending(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 1},
			Delivery: DeliveryConfig{
				Enabled:        true,
				EventQueueSize: 8,
			},
			Presence: PresenceConfig{TouchFlushInterval: time.Hour},
		},
		WithCluster(cluster),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	senderWrites := &sendackSmokeSessionWrites{}
	recipientWrites := &sendackSmokeSessionWrites{}
	sender := newAppDeliveryTestSession(101, senderWrites)
	recipient := newAppDeliveryTestSession(102, recipientWrites)
	activateAppDeliverySession(t, app, sender, "u1")
	activateAppDeliverySession(t, app, recipient, "u2")

	send := &frame.SendPacket{
		ClientSeq:   1,
		ClientMsgNo: "client-person-1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}
	if err := app.Handler().OnFrame(gateway.Context{Session: sender, RequestContext: context.Background()}, send); err != nil {
		t.Fatalf("OnFrame(send) error = %v", err)
	}
	ack := senderWrites.requireOnlySendack(t)
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("sendack reason = %v, want success", ack.ReasonCode)
	}

	recv := recipientWrites.waitForRecvPacket(t, time.Second)
	if recv.MessageID != ack.MessageID || recv.MessageSeq != ack.MessageSeq {
		t.Fatalf("recv id/seq = %d/%d, want %d/%d", recv.MessageID, recv.MessageSeq, ack.MessageID, ack.MessageSeq)
	}
	if recv.ChannelID != "u1" || recv.ChannelType != frame.ChannelTypePerson || recv.FromUID != "u1" || string(recv.Payload) != "hello" {
		t.Fatalf("recv packet = %#v, want person view from u1", recv)
	}
	waitAppDeliveryPendingAckCount(t, app, 1, time.Second)

	if err := app.Handler().OnFrame(gateway.Context{Session: recipient, RequestContext: context.Background()}, &frame.RecvackPacket{
		MessageID:  recv.MessageID,
		MessageSeq: recv.MessageSeq,
	}); err != nil {
		t.Fatalf("OnFrame(recvack) error = %v", err)
	}
	waitAppDeliveryPendingAckCount(t, app, 0, time.Second)
}

func TestDeliveryEnabledGroupSendUsesSubscriberSource(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	subscribers := &fakeDeliverySubscriberSource{
		pages: []runtimedelivery.UIDPage{
			{UIDs: []string{"u2"}, Done: true},
		},
	}
	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 1},
			Delivery: DeliveryConfig{
				Enabled:        true,
				EventQueueSize: 8,
			},
			Presence: PresenceConfig{TouchFlushInterval: time.Hour},
		},
		WithCluster(cluster),
		WithDeliverySubscriberSource(subscribers),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	senderWrites := &sendackSmokeSessionWrites{}
	recipientWrites := &sendackSmokeSessionWrites{}
	sender := newAppDeliveryTestSession(201, senderWrites)
	recipient := newAppDeliveryTestSession(202, recipientWrites)
	activateAppDeliverySession(t, app, sender, "u1")
	activateAppDeliverySession(t, app, recipient, "u2")

	send := &frame.SendPacket{
		ClientSeq:   1,
		ClientMsgNo: "client-group-1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hello group"),
	}
	if err := app.Handler().OnFrame(gateway.Context{Session: sender, RequestContext: context.Background()}, send); err != nil {
		t.Fatalf("OnFrame(send) error = %v", err)
	}
	ack := senderWrites.requireOnlySendack(t)
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("sendack reason = %v, want success", ack.ReasonCode)
	}

	recv := recipientWrites.waitForRecvPacket(t, time.Second)
	if recv.MessageID != ack.MessageID || recv.MessageSeq != ack.MessageSeq {
		t.Fatalf("recv id/seq = %d/%d, want %d/%d", recv.MessageID, recv.MessageSeq, ack.MessageID, ack.MessageSeq)
	}
	if recv.ChannelID != "g1" || recv.ChannelType != frame.ChannelTypeGroup || recv.FromUID != "u1" ||
		string(recv.Payload) != "hello group" {
		t.Fatalf("recv packet = %#v, want group delivery from u1", recv)
	}
	waitAppDeliveryPendingAckCount(t, app, 1, time.Second)
	if len(subscribers.requests) != 1 {
		t.Fatalf("subscriber requests = %d, want 1", len(subscribers.requests))
	}
	req := subscribers.requests[0]
	if req.ChannelID != "g1" || req.ChannelType != frame.ChannelTypeGroup || req.Limit != app.cfg.Delivery.FanoutPageSize {
		t.Fatalf("subscriber request = %#v, want group channel with configured page size", req)
	}
	if req.Partition != (runtimedelivery.Partition{}) {
		t.Fatalf("subscriber partition = %#v, want recipient-authority unpartitioned scan", req.Partition)
	}

	if err := app.Handler().OnFrame(gateway.Context{Session: recipient, RequestContext: context.Background()}, &frame.RecvackPacket{
		MessageID:  recv.MessageID,
		MessageSeq: recv.MessageSeq,
	}); err != nil {
		t.Fatalf("OnFrame(recvack) error = %v", err)
	}
	waitAppDeliveryPendingAckCount(t, app, 0, time.Second)
}

func TestDeliveryMetaStoreWritesBenchDataAndFiltersSubscriberPages(t *testing.T) {
	const hashSlotCount = 16
	slotThreeUID := testUIDForHashSlot(t, 3, hashSlotCount)
	slotNineUID := testUIDForHashSlot(t, 9, hashSlotCount)
	node := &recordingDeliveryMetaNode{
		snapshot:    readyFakeClusterSnapshot(1, hashSlotCount),
		subscribers: map[string][]string{"g1": []string{slotThreeUID, slotNineUID}},
	}
	store := newDeliveryMetaStore(node)

	acceptedChannels, err := store.UpsertChannels(context.Background(), []accessapi.BenchChannelMutation{{
		ChannelID:     "g1",
		ChannelType:   frame.ChannelTypeGroup,
		AllowStranger: true,
	}})
	if err != nil {
		t.Fatalf("UpsertChannels() error = %v", err)
	}
	if acceptedChannels != 1 || len(node.upserted) != 1 || node.upserted[0].ChannelID != "g1" || node.upserted[0].AllowStranger != 1 {
		t.Fatalf("upserted = %#v accepted=%d, want real channel metadata", node.upserted, acceptedChannels)
	}
	acceptedSubscribers, err := store.AddSubscribers(context.Background(), []accessapi.BenchSubscriberMutation{{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Subscribers: []string{slotThreeUID},
	}, {
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Subscribers: []string{slotNineUID},
	}})
	if err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}
	if acceptedSubscribers != 2 || len(node.added) != 2 || node.added[0].version != 1 || node.added[1].version != 2 {
		t.Fatalf("added = %#v accepted=%d, want ordered subscriber mutation versions 1 and 2", node.added, acceptedSubscribers)
	}

	page, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 3, HashSlotEnd: 3},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListSubscribers() error = %v", err)
	}
	if len(page.UIDs) != 1 || page.UIDs[0] != slotThreeUID || !page.Done {
		t.Fatalf("slot-filtered page = %#v, want only slot 3 uid", page)
	}
	first, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 0, HashSlotEnd: hashSlotCount - 1},
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("ListSubscribers(first) error = %v", err)
	}
	if len(first.UIDs) != 1 || first.NextCursor == "" || first.Done {
		t.Fatalf("first page = %#v, want one uid and continuation", first)
	}
	second, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 0, HashSlotEnd: hashSlotCount - 1},
		Cursor:      first.NextCursor,
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("ListSubscribers(second) error = %v", err)
	}
	if len(second.UIDs) != 1 || !second.Done {
		t.Fatalf("second page = %#v, want final uid", second)
	}
}

func TestDeliveryMetaStoreCachesSubscriberSnapshotAcrossPartitions(t *testing.T) {
	const hashSlotCount = 16
	slotThreeUID := testUIDForHashSlot(t, 3, hashSlotCount)
	slotNineUID := testUIDForHashSlot(t, 9, hashSlotCount)
	node := &recordingDeliveryMetaNode{
		snapshot:    readyFakeClusterSnapshot(1, hashSlotCount),
		subscribers: map[string][]string{"g1": []string{slotThreeUID, slotNineUID}},
	}
	store := newDeliveryMetaStore(node)

	first, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 3, HashSlotEnd: 3},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListSubscribers(first) error = %v", err)
	}
	second, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Partition:   runtimedelivery.Partition{HashSlotStart: 9, HashSlotEnd: 9},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListSubscribers(second) error = %v", err)
	}

	if len(first.UIDs) != 1 || first.UIDs[0] != slotThreeUID || len(second.UIDs) != 1 || second.UIDs[0] != slotNineUID {
		t.Fatalf("partition pages first=%#v second=%#v, want cached slot-specific results", first, second)
	}
	if node.listCalls != 1 {
		t.Fatalf("subscriber list calls = %d, want one cached channel snapshot read", node.listCalls)
	}
}

func TestDeliveryMetaStoreInvalidatesSubscriberCacheAfterMutation(t *testing.T) {
	node := &recordingDeliveryMetaNode{
		snapshot:    readyFakeClusterSnapshot(1, 16),
		subscribers: map[string][]string{"g1": []string{"u1"}},
	}
	store := newDeliveryMetaStore(node)
	if _, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Limit: 10}); err != nil {
		t.Fatalf("ListSubscribers(before) error = %v", err)
	}
	if _, err := store.AddSubscribers(context.Background(), []accessapi.BenchSubscriberMutation{{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Subscribers: []string{"u2"},
	}}); err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}
	node.mu.Lock()
	node.subscribers["g1"] = []string{"u1", "u2"}
	node.mu.Unlock()
	after, err := store.ListSubscribers(context.Background(), runtimedelivery.SubscriberPageRequest{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Limit: 10})
	if err != nil {
		t.Fatalf("ListSubscribers(after) error = %v", err)
	}
	if len(after.UIDs) != 2 || after.UIDs[1] != "u2" {
		t.Fatalf("after mutation page = %#v, want refreshed subscribers", after)
	}
	if node.listCalls != 2 {
		t.Fatalf("subscriber list calls = %d, want cache miss after mutation", node.listCalls)
	}
}

func TestNewWiresDeliveryMetaStoreWhenClusterProvidesRealMetadata(t *testing.T) {
	cluster := &recordingDeliveryMetaNode{
		fakeCluster: fakeCluster{calls: &[]string{}},
		snapshot:    readyFakeClusterSnapshot(1, 16),
		subscribers: map[string][]string{},
	}
	app, err := newTestApp(t,
		Config{
			Cluster:  clusterv2.Config{NodeID: 1},
			Delivery: DeliveryConfig{Enabled: true},
		},
		WithCluster(cluster),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.deliverySubscribers == nil {
		t.Fatal("delivery subscriber source was not wired")
	}
	if _, ok := app.deliverySubscribers.(*deliveryMetaStore); !ok {
		t.Fatalf("deliverySubscribers = %T, want *deliveryMetaStore", app.deliverySubscribers)
	}
}

func TestNewWiresConversationUsecaseWhenClusterProvidesConversationReads(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t,
		Config{Cluster: clusterv2.Config{NodeID: 1}},
		WithCluster(cluster),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.Conversations() == nil {
		t.Fatal("conversation usecase was not wired")
	}
	result, err := app.Conversations().List(context.Background(), conversationusecase.ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Conversations().List() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("conversation items = %d, want empty page", len(result.Items))
	}
}

func TestNewDoesNotOverwriteWithMessagesWhenDeliveryEnabled(t *testing.T) {
	override := message.New(message.Options{})
	app, err := newTestApp(t,
		Config{Cluster: clusterv2.Config{NodeID: 1}, Delivery: DeliveryConfig{Enabled: true}},
		WithCluster(newFakePresenceCluster(1, nil)),
		WithMessages(override),
		WithGateway(&fakeGateway{calls: &[]string{}}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.Messages() != override {
		t.Fatal("New() overwrote WithMessages override")
	}
}

func TestNewWiresPresenceWhenGatewayEnabled(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	gatewayRuntime := &fakeGateway{calls: &[]string{}}

	app, err := newTestApp(t,
		Config{
			Cluster: clusterv2.Config{NodeID: 1},
			Gateway: GatewayConfig{Listeners: []gateway.ListenerOptions{{
				Network: "tcp",
				Address: "127.0.0.1:0",
			}}},
		},
		WithCluster(cluster),
		WithGateway(gatewayRuntime),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if app.presence == nil {
		t.Fatal("presence usecase was not wired")
	}
	if app.online == nil {
		t.Fatal("online registry was not wired")
	}
	if _, ok := cluster.registeredHandlers[accessnode.PresenceAuthorityRPCServiceID]; !ok {
		t.Fatalf("presence authority rpc service was not registered")
	}
	if _, ok := cluster.registeredHandlers[accessnode.PresenceOwnerRPCServiceID]; !ok {
		t.Fatalf("presence owner rpc service was not registered")
	}
	if app.Handler() == nil {
		t.Fatal("gateway handler was not wired")
	}
	if _, err := app.Handler().OnSessionActivate(nil); !errors.Is(err, accessgateway.ErrUnauthenticatedSession) {
		t.Fatalf("OnSessionActivate(nil) error = %v, want unauthenticated session instead of missing presence", err)
	}
}

func TestLocalOwnerPusherWritesRecvPacketAndBindsPendingAck(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	pusher := localOwnerPusher{online: reg, delivery: manager}
	env := runtimedelivery.Envelope{
		MessageID:   9001,
		MessageSeq:  42,
		ChannelID:   "ch1",
		ChannelType: 2,
		FromUID:     "sender",
		ClientMsgNo: "client-1",
		RedDot:      true,
		Payload:     []byte("hello"),
	}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    env,
		Routes: []runtimedelivery.Route{{
			UID:         "u1",
			OwnerNodeID: 1,
			OwnerBootID: 7,
			OwnerSeq:    11,
			SessionID:   101,
		}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 || len(result.Retryable) != 0 || len(result.Dropped) != 0 {
		t.Fatalf("push result = %#v, want one accepted", result)
	}
	if len(session.writes) != 1 {
		t.Fatalf("delivery writes = %d, want 1", len(session.writes))
	}
	recv, ok := session.writes[0].(*frame.RecvPacket)
	if !ok {
		t.Fatalf("delivery write = %T, want *frame.RecvPacket", session.writes[0])
	}
	if recv.MessageID != int64(env.MessageID) || recv.MessageSeq != env.MessageSeq || recv.ChannelID != env.ChannelID ||
		recv.ChannelType != env.ChannelType || recv.FromUID != env.FromUID || recv.ClientMsgNo != env.ClientMsgNo ||
		string(recv.Payload) != "hello" || !recv.RedDot {
		t.Fatalf("recv packet = %#v", recv)
	}
	env.Payload[0] = 'H'
	if string(recv.Payload) != "hello" {
		t.Fatalf("recv payload = %q, want cloned hello", string(recv.Payload))
	}
	if manager.PendingAckCount() != 1 {
		t.Fatalf("pending ack count = %d, want 1", manager.PendingAckCount())
	}
}

func TestLocalOwnerPusherDropsMissingOrInactiveSession(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	inactiveRoute := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: inactiveRoute, Session: &recordingSessionHandle{}}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	pusher := localOwnerPusher{online: reg, delivery: runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
		Routes: []runtimedelivery.Route{
			{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101},
			{UID: "missing", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 102},
		},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 2 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("push result = %#v, want two dropped", result)
	}
}

func TestLocalOwnerPusherDropsWhenPendingAckLimitReached(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
		Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 1, MaxPendingPerSession: 1}),
	})
	if !manager.BindPendingAck(runtimedelivery.PendingRecvAck{UID: "u1", SessionID: 101, MessageID: 1}) {
		t.Fatalf("preload pending ack failed")
	}
	pusher := localOwnerPusher{online: reg, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 2, MessageSeq: 2},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("push result = %#v, want one dropped", result)
	}
	if len(session.writes) != 0 {
		t.Fatalf("delivery writes = %d, want 0", len(session.writes))
	}
	if manager.PendingAckCount() != 1 {
		t.Fatalf("pending ack count = %d, want preload only", manager.PendingAckCount())
	}
}

func TestLocalOwnerPusherMarksWriteErrorRetryable(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{writeErr: errors.New("write failed")}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	pusher := localOwnerPusher{online: reg, delivery: runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Retryable) != 1 || len(result.Accepted) != 0 || len(result.Dropped) != 0 {
		t.Fatalf("push result = %#v, want one retryable", result)
	}
	if pusher.delivery.PendingAckCount() != 0 {
		t.Fatalf("pending ack count = %d, want 0 after write error", pusher.delivery.PendingAckCount())
	}
}

func TestLocalOwnerPusherDropsTerminalWriteErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "session closed", err: session.ErrSessionClosed},
		{name: "outbound overflow", err: gatewaytransport.ErrOutboundBytesExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
			sessionHandle := &recordingSessionHandle{writeErr: tt.err}
			route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
			if err := reg.RegisterPending(online.LocalSession{Route: route, Session: sessionHandle}); err != nil {
				t.Fatalf("RegisterPending() error = %v", err)
			}
			if err := reg.MarkActive(route.SessionID); err != nil {
				t.Fatalf("MarkActive() error = %v", err)
			}
			manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
			pusher := localOwnerPusher{online: reg, delivery: manager}

			result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
				OwnerNodeID: 1,
				Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
				Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
			})
			if err != nil {
				t.Fatalf("Push() error = %v", err)
			}
			if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
				t.Fatalf("push result = %#v, want one dropped", result)
			}
			if manager.PendingAckCount() != 0 {
				t.Fatalf("pending ack count = %d, want 0 after terminal write error", manager.PendingAckCount())
			}
		})
	}
}

func TestLocalOwnerPusherExpiresPendingAcksDuringPushActivity(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	now := int64(200)
	tracker := runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
		ShardCount: 1,
		Now: func() int64 {
			return now
		},
	})
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{Acks: tracker})
	manager.BindPendingAck(runtimedelivery.PendingRecvAck{UID: "u1", SessionID: 101, MessageID: 1, MessageSeq: 1, DeliveredAt: 100})
	pusher := localOwnerPusher{online: reg, delivery: manager, pendingAckTTL: 50 * time.Second}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 2, MessageSeq: 2},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("accepted routes = %d, want 1", len(result.Accepted))
	}
	if _, ok := tracker.Ack(runtimedelivery.Recvack{UID: "u1", SessionID: 101, MessageID: 1}); ok {
		t.Fatalf("old pending ack still exists after delivery activity expiration")
	}
	if pending, ok := tracker.Ack(runtimedelivery.Recvack{UID: "u1", SessionID: 101, MessageID: 2}); !ok || pending.MessageID != 2 {
		t.Fatalf("new pending ack = %#v, %v, want message 2 true", pending, ok)
	}
}

func TestLocalOwnerPusherDropsOverflowMessageID(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	pusher := localOwnerPusher{online: reg, delivery: manager}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: uint64(1 << 63), MessageSeq: 1},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("push result = %#v, want one dropped", result)
	}
	if len(session.writes) != 0 {
		t.Fatalf("delivery writes = %d, want 0", len(session.writes))
	}
	if manager.PendingAckCount() != 0 {
		t.Fatalf("pending ack count = %d, want 0", manager.PendingAckCount())
	}
}

func TestLocalOwnerPusherDropsIncompleteRouteIdentity(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(route.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	pusher := localOwnerPusher{online: reg, delivery: runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})}

	result, err := pusher.Push(context.Background(), runtimedelivery.PushCommand{
		OwnerNodeID: 1,
		Envelope:    runtimedelivery.Envelope{MessageID: 1, MessageSeq: 1},
		Routes:      []runtimedelivery.Route{{UID: "u1", OwnerNodeID: 1, OwnerSeq: 11, SessionID: 101}},
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Dropped) != 1 || len(result.Accepted) != 0 || len(result.Retryable) != 0 {
		t.Fatalf("push result = %#v, want incomplete identity dropped", result)
	}
	if len(session.writes) != 0 {
		t.Fatalf("delivery writes = %d, want 0", len(session.writes))
	}
}

func TestPresenceBenchSnapshotAggregatesOwnerAndAuthorityState(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	app, err := newTestApp(t, Config{Cluster: clusterv2.Config{NodeID: 1}}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.presenceDirectory == nil {
		t.Fatal("presence directory was not wired")
	}
	pending := online.OwnerRoute{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 1, SessionID: 101, ConnectedUnix: 100}
	active := online.OwnerRoute{UID: "u2", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 2, SessionID: 102, ConnectedUnix: 100}
	if err := app.online.RegisterPending(online.LocalSession{Route: pending}); err != nil {
		t.Fatalf("RegisterPending(pending) error = %v", err)
	}
	if err := app.online.RegisterPending(online.LocalSession{Route: active}); err != nil {
		t.Fatalf("RegisterPending(active) error = %v", err)
	}
	if err := app.online.MarkActive(active.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	app.online.MarkTouched(active.SessionID, 120)

	target := presence.RouteTarget{HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 2}
	app.presenceDirectory.BecomeAuthority(target)
	if _, err := app.presenceDirectory.RegisterRoute(target, presence.Route{UID: "u2", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 2, SessionID: 102, ConnectedUnix: 100, LastSeenUnix: 120}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := app.presenceDirectory.TouchRoutes(target, []presence.Route{{UID: "u3", OwnerNodeID: 2, OwnerBootID: 8, OwnerSeq: 1, SessionID: 201, ConnectedUnix: 100, LastSeenUnix: 121}}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}

	controller := app.benchPresenceController()
	if controller == nil {
		t.Fatal("bench presence controller is nil")
	}
	snap, err := controller.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snap.NodeID != 1 || snap.OwnerRoutesPending != 1 || snap.OwnerRoutesActive != 1 || snap.OwnerTouchedDirty != 1 {
		t.Fatalf("owner snapshot = %+v, want node 1 pending 1 active 1 dirty 1", snap)
	}
	if snap.AuthorityRoutesActive != 2 || snap.AuthorityRoutesByHashSlot[9] != 2 || snap.TouchRoutesTotal != 1 {
		t.Fatalf("authority snapshot = %+v, want active 2 hashSlot 9 count 2 touch total 1", snap)
	}
}

func TestStartOrderStartsClusterThenPresenceWorkerThenGateway(t *testing.T) {
	calls := make([]string, 0, 3)
	events := make(chan clusterv2.RouteAuthorityEvent)
	cluster := newFakePresenceCluster(1, events)
	cluster.calls = &calls
	cluster.snapshot = clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 1}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,presence.start,presence.start,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,presence.start,presence.start,gateway.start", got)
	}
}

func TestStartSeedsPresenceAuthorityFromCurrentRoutes(t *testing.T) {
	events := make(chan clusterv2.RouteAuthorityEvent)
	cluster := newFakePresenceCluster(1, events)
	cluster.snapshot = clusterv2.Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 10}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(&fakeGateway{calls: &[]string{}}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer app.Stop(context.Background())

	err = app.presence.Activate(context.Background(), presence.ActivateCommand{
		UID:       "u1",
		SessionID: 11,
	})
	if err != nil {
		t.Fatalf("Activate() error = %v, want seeded local authority", err)
	}
}

func TestPresenceTouchWorkerFlushesDirtyRoutesByTarget(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	conns := []online.OwnerRoute{
		{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1001},
		{UID: "u2", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 12, SessionID: 102, DeviceID: "d2", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1002},
		{UID: "u3", HashSlot: 8, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 13, SessionID: 103, DeviceID: "d3", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 1003},
	}
	for _, conn := range conns {
		if err := reg.RegisterPending(online.LocalSession{Route: conn}); err != nil {
			t.Fatalf("RegisterPending(%d) error = %v", conn.SessionID, err)
		}
		if err := reg.MarkActive(conn.SessionID); err != nil {
			t.Fatalf("MarkActive(%d) error = %v", conn.SessionID, err)
		}
		reg.MarkTouched(conn.SessionID, conn.ConnectedUnix+10)
	}
	targetA := presence.RouteTarget{HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 2}
	targetB := presence.RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 2, RouteRevision: 4, AuthorityEpoch: 5}
	authority := &recordingTouchAuthority{targets: map[string]presence.RouteTarget{
		"u1": targetA,
		"u2": targetA,
		"u3": targetB,
	}}
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:     reg,
		Authority: authority,
		Directory: directory,
		BatchSize: 10,
		RouteTTL:  90 * time.Second,
	})

	worker.flushOnce(context.Background(), time.Unix(2000, 0))

	if len(authority.batches) != 2 {
		t.Fatalf("touch batches = %d, want 2", len(authority.batches))
	}
	if got := len(routesForTarget(authority.batches, targetA)); got != 2 {
		t.Fatalf("targetA route count = %d, want 2", got)
	}
	targetBRoutes := routesForTarget(authority.batches, targetB)
	if len(targetBRoutes) != 1 {
		t.Fatalf("targetB route count = %d, want 1", len(targetBRoutes))
	}
	if targetBRoutes[0].LastSeenUnix != conns[2].ConnectedUnix+10 {
		t.Fatalf("LastSeenUnix = %d, want %d", targetBRoutes[0].LastSeenUnix, conns[2].ConnectedUnix+10)
	}
	if len(reg.DrainTouched(10)) != 0 {
		t.Fatalf("dirty routes were not cleared after successful touch")
	}
	if len(directory.expires) != 1 {
		t.Fatalf("ExpireRoutes calls = %d, want 1", len(directory.expires))
	}
}

func TestPresenceTouchWorkerRequeuesFailedFlush(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	conn := online.OwnerRoute{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: conn}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if err := reg.MarkActive(conn.SessionID); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	reg.MarkTouched(conn.SessionID, 1010)
	authority := &recordingTouchAuthority{
		targets: map[string]presence.RouteTarget{"u1": {HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 2}},
		err:     errors.New("touch failed"),
	}
	logger := &recordingAppLogger{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:     reg,
		Authority: authority,
		BatchSize: 10,
		Logger:    logger,
	})

	worker.flushOnce(context.Background(), time.Now())

	requeued := reg.DrainTouched(10)
	if len(requeued) != 1 {
		t.Fatalf("requeued dirty routes = %d, want 1", len(requeued))
	}
	if requeued[0].SessionID != conn.SessionID || requeued[0].UID != conn.UID {
		t.Fatalf("requeued route = %#v, want session %d uid %s", requeued[0], conn.SessionID, conn.UID)
	}
	requireAppLogEvent(t, logger, "WARN", "internalv2.app.presence_touch_failed")
}

func TestPresenceTouchWorkerRequeuesAllGroupsWhenContextCancelsAfterDrain(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	conns := []online.OwnerRoute{
		{UID: "u1", HashSlot: 9, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001},
		{UID: "u2", HashSlot: 8, OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 12, SessionID: 102, ConnectedUnix: 1002},
	}
	for _, conn := range conns {
		if err := reg.RegisterPending(online.LocalSession{Route: conn}); err != nil {
			t.Fatalf("RegisterPending(%d) error = %v", conn.SessionID, err)
		}
		if err := reg.MarkActive(conn.SessionID); err != nil {
			t.Fatalf("MarkActive(%d) error = %v", conn.SessionID, err)
		}
		reg.MarkTouched(conn.SessionID, conn.ConnectedUnix+10)
	}
	ctx, cancel := context.WithCancel(context.Background())
	authority := &recordingTouchAuthority{
		targets: map[string]presence.RouteTarget{
			"u1": {HashSlot: 9, SlotID: 1, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 2},
			"u2": {HashSlot: 8, SlotID: 1, LeaderNodeID: 2, RouteRevision: 4, AuthorityEpoch: 5},
		},
		resolveHook: func(string) { cancel() },
	}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		Local:     reg,
		Authority: authority,
		BatchSize: 10,
	})

	worker.flushOnce(ctx, time.Now())

	if len(authority.batches) != 0 {
		t.Fatalf("touch batches = %d, want 0 after context cancellation", len(authority.batches))
	}
	requeued := reg.DrainTouched(10)
	if len(requeued) != len(conns) {
		t.Fatalf("requeued dirty routes = %d, want %d", len(requeued), len(conns))
	}
}

func TestOwnerRouteFromRouteCarriesRouteMetadata(t *testing.T) {
	route := presence.Route{
		UID:           "u1",
		OwnerNodeID:   1,
		OwnerBootID:   7,
		OwnerSeq:      11,
		SessionID:     101,
		DeviceID:      "d1",
		DeviceFlag:    2,
		DeviceLevel:   3,
		Listener:      "tcp",
		ConnectedUnix: 1001,
		LastSeenUnix:  1010,
	}

	conn := ownerRouteFromRoute(route)

	if conn.DeviceID != route.DeviceID ||
		conn.DeviceFlag != route.DeviceFlag ||
		conn.DeviceLevel != route.DeviceLevel ||
		conn.Listener != route.Listener {
		t.Fatalf("online conn metadata = %#v, want route metadata %#v", conn, route)
	}
	if conn.LastActivityUnix != route.LastSeenUnix {
		t.Fatalf("LastActivityUnix = %d, want %d", conn.LastActivityUnix, route.LastSeenUnix)
	}
}

func TestPresenceOwnerActionsClosesAndUnregistersMatchingLocalSession(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}

	actions := presenceOwnerActions{local: reg}
	err := actions.ApplyRouteAction(context.Background(), presence.RouteAction{
		UID:         route.UID,
		OwnerNodeID: route.OwnerNodeID,
		OwnerBootID: route.OwnerBootID,
		SessionID:   route.SessionID,
		Reason:      "conflict",
	})
	if err != nil {
		t.Fatalf("ApplyRouteAction() error = %v", err)
	}
	if session.reason != "conflict" {
		t.Fatalf("close reason = %q, want conflict", session.reason)
	}
	if _, ok := reg.LocalSession(route.SessionID); ok {
		t.Fatalf("session %d still registered after owner action", route.SessionID)
	}
}

func TestPresenceOwnerActionsIgnoresMismatchedLocalSession(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &recordingSessionHandle{}
	route := online.OwnerRoute{UID: "u1", OwnerNodeID: 1, OwnerBootID: 7, OwnerSeq: 11, SessionID: 101, ConnectedUnix: 1001}
	if err := reg.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}

	actions := presenceOwnerActions{local: reg}
	err := actions.ApplyRouteAction(context.Background(), presence.RouteAction{
		UID:         route.UID,
		OwnerNodeID: route.OwnerNodeID,
		OwnerBootID: route.OwnerBootID + 1,
		SessionID:   route.SessionID,
		Reason:      "stale conflict",
	})
	if err != nil {
		t.Fatalf("ApplyRouteAction() error = %v", err)
	}
	if session.reason != "" {
		t.Fatalf("session was closed with reason %q, want no close", session.reason)
	}
	if _, ok := reg.LocalSession(route.SessionID); !ok {
		t.Fatalf("session %d was unregistered for a mismatched action", route.SessionID)
	}
}

func TestPresenceTouchWorkerIgnoresStaleAuthorityAfterNewerEvent(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Directory: directory,
	})

	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   2,
		RouteRevision:  3,
		AuthorityEpoch: 2,
	})

	if got := directory.becomeSnapshot(); len(got) != 1 || got[0].LeaderNodeID != 1 || got[0].AuthorityEpoch != 3 {
		t.Fatalf("become targets = %#v, want one current local authority", got)
	}
	if got := directory.loseSnapshot(); len(got) != 0 {
		t.Fatalf("lost slots = %v, want stale remote authority ignored", got)
	}
}

func TestPresenceTouchWorkerAcceptsNewerNoLeaderAuthority(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Directory: directory,
	})

	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:      9,
		SlotID:        1,
		LeaderNodeID:  0,
		RouteRevision: 5,
	})

	if got := directory.loseSnapshot(); !reflect.DeepEqual(got, []uint16{9}) {
		t.Fatalf("lost slots = %v, want newer no-leader authority to clear local authority", got)
	}
}

func TestPresenceTouchWorkerKeepsEpochFenceAcrossNoLeaderAuthority(t *testing.T) {
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Directory: directory,
	})

	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   0,
		RouteRevision:  4,
		AuthorityEpoch: 4,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	})
	worker.handleAuthority(clusterv2.RouteAuthority{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  4,
		AuthorityEpoch: 5,
	})

	got := directory.becomeSnapshot()
	if len(got) != 2 || got[0].AuthorityEpoch != 3 || got[1].AuthorityEpoch != 5 {
		t.Fatalf("become targets = %#v, want epochs 3 then 5", got)
	}
	if lost := directory.loseSnapshot(); !reflect.DeepEqual(lost, []uint16{9}) {
		t.Fatalf("lost slots = %v, want one no-leader clear", lost)
	}
}

func TestCurrentPresenceAuthoritiesIncludesNoLeaderRoutes(t *testing.T) {
	cluster := &fakeWriteReadyCluster{
		snapshots: []clusterv2.Snapshot{{HashSlotCount: 1}},
		routes: map[uint16]clusterv2.Route{
			0: {HashSlot: 0, SlotID: 1, Leader: 0, Revision: 4, AuthorityEpoch: 3},
		},
	}
	app := &App{cluster: cluster}

	got := app.currentPresenceAuthorities()

	if len(got) != 1 {
		t.Fatalf("authorities len = %d, want 1", len(got))
	}
	if got[0].LeaderNodeID != 0 || got[0].RouteRevision != 4 || got[0].AuthorityEpoch != 3 {
		t.Fatalf("authority = %#v, want no-leader revision 4 epoch 3", got[0])
	}
}

func TestPresenceTouchWorkerUpdatesAuthorityDirectoryFromEvents(t *testing.T) {
	events := make(chan clusterv2.RouteAuthorityEvent, 3)
	directory := &recordingPresenceDirectory{}
	worker := newPresenceTouchWorker(presenceTouchWorkerOptions{
		NodeID:    1,
		Events:    events,
		Directory: directory,
	})
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer worker.Stop(context.Background())

	events <- clusterv2.RouteAuthorityEvent{Authorities: []clusterv2.RouteAuthority{{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  3,
		AuthorityEpoch: 2,
	}, {
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   2,
		RouteRevision:  4,
		AuthorityEpoch: 3,
	}, {
		HashSlot:       10,
		SlotID:         1,
		LeaderNodeID:   0,
		RouteRevision:  5,
		AuthorityEpoch: 4,
	}}}

	waitUntil(t, time.Second, func() bool {
		return len(directory.becomeSnapshot()) == 1 && len(directory.loseSnapshot()) == 2
	})
	if got := directory.becomeSnapshot()[0]; got.HashSlot != 9 || got.LeaderNodeID != 1 || got.AuthorityEpoch != 2 {
		t.Fatalf("become target = %#v, want hashSlot=9 leader=1 epoch=2", got)
	}
	if got := directory.loseSnapshot(); !reflect.DeepEqual(got, []uint16{9, 10}) {
		t.Fatalf("lost slots = %v, want [9 10]", got)
	}
}

func TestNewWiresBenchRuntimeControllerWhenClusterSupportsIt(t *testing.T) {
	app, err := newTestApp(t,
		Config{
			API:   APIConfig{ListenAddr: "127.0.0.1:0"},
			Bench: BenchConfig{APIEnabled: true},
		},
		WithCluster(&fakeRuntimeBenchCluster{}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	req := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)
	rec := httptest.NewRecorder()
	apiSrv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var caps struct {
		Supports struct {
			ChannelRuntimeSnapshot bool `json:"channel_runtime_snapshot"`
			ChannelRuntimeProbe    bool `json:"channel_runtime_probe"`
			ChannelRuntimeEvict    bool `json:"channel_runtime_evict"`
		} `json:"supports"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if !caps.Supports.ChannelRuntimeSnapshot {
		t.Fatalf("channel_runtime_snapshot = false, want true")
	}
	if !caps.Supports.ChannelRuntimeProbe {
		t.Fatalf("channel_runtime_probe = false, want true")
	}
	if !caps.Supports.ChannelRuntimeEvict {
		t.Fatalf("channel_runtime_evict = false, want true")
	}
}

func TestAppWiresLegacyChannelRoutesToClusterMetadata(t *testing.T) {
	cluster := &recordingDeliveryMetaNode{}
	app, err := newTestApp(t, Config{
		API: APIConfig{ListenAddr: "127.0.0.1:0"},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/info", strings.NewReader(`{"channel_id":"g1","channel_type":2,"ban":1,"allow_stranger":1}`))
	req.Header.Set("Content-Type", "application/json")
	apiSrv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(cluster.upserted) != 1 {
		t.Fatalf("upserted = %#v, want one channel", cluster.upserted)
	}
	got := cluster.upserted[0]
	if got.ChannelID != "g1" || got.ChannelType != int64(frame.ChannelTypeGroup) || got.Ban != 1 || got.AllowStranger != 1 {
		t.Fatalf("upserted channel = %#v, want mapped legacy channel metadata", got)
	}
}

func TestAppWiresConversationListRouteToUsecase(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t, Config{
		API: APIConfig{ListenAddr: "127.0.0.1:0"},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", strings.NewReader(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	apiSrv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"conversations":[]`) {
		t.Fatalf("body = %s, want empty conversation list", rec.Body.String())
	}
}

func TestAppWiresConversationListMetrics(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	app, err := newTestApp(t, Config{
		API:           APIConfig{ListenAddr: "127.0.0.1:0"},
		Observability: ObservabilityConfig{MetricsEnabled: true},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", strings.NewReader(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	apiSrv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	families, err := app.metrics.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != "wukongim_conversation_list_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["result"] == "ok" && labels["more"] == "false" && metric.GetCounter().GetValue() == 1 {
				return
			}
		}
	}
	t.Fatal("conversation list metric for successful request was not observed")
}

func TestGatewayStartFailureStopsAPIThenCluster(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	calls := make([]string, 0, 5)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) {
		t.Fatalf("Start() error = %v, want gateway error", err)
	}
	if got := joinCalls(calls); got != "cluster.start,api.start,gateway.start,api.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,api.start,gateway.start,api.stop,cluster.stop", got)
	}
}

func TestStartWaitsForClusterWriteReadinessBeforeGateway(t *testing.T) {
	calls := make([]string, 0, 3)
	cluster := &fakeWriteReadyCluster{
		fakeCluster: fakeCluster{calls: &calls},
		snapshots: []clusterv2.Snapshot{
			{RoutesReady: true, SlotsReady: true, ChannelsReady: true, HashSlotCount: 1},
		},
		routes: map[uint16]clusterv2.Route{
			0: {Leader: 1, Peers: []uint64{1}},
		},
	}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,cluster.route,gateway.start" {
		t.Fatalf("calls = %s, want cluster.start,cluster.route,gateway.start", got)
	}
}

func TestClusterWriteReadinessFailureStopsClusterBeforeGateway(t *testing.T) {
	calls := make([]string, 0, 2)
	cluster := &fakeWriteReadyCluster{
		fakeCluster: fakeCluster{calls: &calls},
		snapshots: []clusterv2.Snapshot{
			{RoutesReady: false, SlotsReady: true, ChannelsReady: true, HashSlotCount: 1},
		},
	}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{Cluster: clusterv2.Config{Timeouts: clusterv2.TimeoutConfig{Start: time.Millisecond}}}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cluster write readiness") {
		t.Fatalf("Start() error = %v, want cluster write readiness error", err)
	}
	if got := joinCalls(calls); got != "cluster.start,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,cluster.stop", got)
	}
}

func TestStopOrderIsGatewayThenCluster(t *testing.T) {
	calls := make([]string, 0, 4)
	cluster := &fakeCluster{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start,gateway.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,gateway.stop,cluster.stop", got)
	}
}

func TestStopOrderIncludesAPIBeforeCluster(t *testing.T) {
	calls := make([]string, 0, 6)
	cluster := &fakeCluster{calls: &calls}
	api := &fakeAPI{calls: &calls}
	gateway := &fakeGateway{calls: &calls}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithAPI(api), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if got := joinCalls(calls); got != "cluster.start,api.start,gateway.start,gateway.stop,api.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,api.start,gateway.start,gateway.stop,api.stop,cluster.stop", got)
	}
}

func TestConcurrentStartStopCannotLeaveGatewayRunningAfterStopReturns(t *testing.T) {
	cluster := newBlockingCluster()
	gateway := newStateGateway()
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	startDone := make(chan error, 1)
	go func() {
		startDone <- app.Start(context.Background())
	}()
	<-cluster.startEntered

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- app.Stop(context.Background())
	}()
	time.Sleep(10 * time.Millisecond)

	close(cluster.releaseStart)

	if err := <-startDone; err != nil && !errors.Is(err, ErrStopped) {
		t.Fatalf("Start() error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if gateway.runningState() {
		t.Fatalf("gateway is running after Stop returned")
	}
}

func TestRollbackStopFailureLeavesClusterCleanupRetryPossible(t *testing.T) {
	gatewayErr := errors.New("gateway start failed")
	rollbackErr := errors.New("cluster rollback failed")
	calls := make([]string, 0, 4)
	cluster := &fakeCluster{calls: &calls, stopErr: rollbackErr}
	gateway := &fakeGateway{calls: &calls, startErr: gatewayErr}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(gateway))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = app.Start(context.Background())
	if !errors.Is(err, gatewayErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("Start() error = %v, want gateway and rollback errors", err)
	}
	if err := app.Stop(context.Background()); !errors.Is(err, rollbackErr) {
		t.Fatalf("Stop() error = %v, want rollback retry error", err)
	}

	if got := joinCalls(calls); got != "cluster.start,gateway.start,cluster.stop,cluster.stop" {
		t.Fatalf("calls = %s, want cluster.start,gateway.start,cluster.stop,cluster.stop", got)
	}
}

func TestNewSeedsMessageIDsFromEffectiveClusterNodeID(t *testing.T) {
	cluster := newFakePresenceCluster(7, nil)
	cluster.snapshot = readyFakeClusterSnapshot(7, 16)
	app, err := newTestApp(t, Config{Cluster: clusterv2.Config{NodeID: 7}}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startTestApp(t, app)

	result, err := app.Messages().Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   "room-message-id",
		ChannelType: frame.ChannelTypeGroup,
		ClientMsgNo: "client-message-id-1",
		Payload:     []byte("seed"),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Reason != message.ReasonSuccess {
		t.Fatalf("send reason = %v, want success", result.Reason)
	}
	if got, want := result.MessageID, uint64(7<<48)+1; got != want {
		t.Fatalf("first message id = %d, want %d", got, want)
	}
}

func TestStaticMultiNodeClusterStartsControllerVoters(t *testing.T) {
	addrs := []string{freeSendackSmokeTCPAddr(t), freeSendackSmokeTCPAddr(t), freeSendackSmokeTCPAddr(t)}
	voters := []clusterv2.ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	apps := make([]*App, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{
			NodeID:  voter.NodeID,
			DataDir: t.TempDir(),
			Cluster: clusterv2.Config{
				NodeID:     voter.NodeID,
				ListenAddr: voter.Addr,
				DataDir:    t.TempDir(),
				Control: clusterv2.ControlConfig{
					ClusterID:      "internalv2-app-static-three",
					Voters:         voters,
					AllowBootstrap: true,
				},
				Slots: clusterv2.SlotConfig{
					InitialSlotCount: 1,
					HashSlotCount:    4,
					ReplicaCount:     3,
				},
				Channel:  clusterv2.ChannelConfig{TickInterval: time.Millisecond},
				Timeouts: clusterv2.TimeoutConfig{Start: 5 * time.Second},
			},
		}
		app, err := newTestApp(t, cfg)
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", voter.NodeID, err)
		}
		apps = append(apps, app)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	errs := make(chan error, len(apps))
	for _, app := range apps {
		app := app
		go func() { errs <- app.Start(startCtx) }()
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			_ = app.Stop(stopCtx)
		})
	}
	for range apps {
		if err := <-errs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}

	nodes := make([]*clusterv2.Node, 0, len(apps))
	for _, app := range apps {
		node, ok := app.cluster.(*clusterv2.Node)
		if !ok {
			t.Fatalf("cluster runtime = %T, want *clusterv2.Node", app.cluster)
		}
		nodes = append(nodes, node)
	}
	waitAppClusterSnapshotsConverge(t, nodes)

	ack := sendDefaultMetaSmokePacket(t, apps[0], channelv2.ChannelID{ID: "room-static-three", Type: 1}, 1, "client-static-three-1")
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("sendack reason = %v, want %v", ack.ReasonCode, frame.ReasonSuccess)
	}
	if ack.MessageSeq != 1 {
		t.Fatalf("sendack message seq = %d, want 1", ack.MessageSeq)
	}
}

type fakeCluster struct {
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeCluster) Start(context.Context) error {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.start")
	}
	return f.startErr
}

func (f *fakeCluster) Stop(context.Context) error {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.stop")
	}
	return f.stopErr
}

var _ clusterinfra.ChannelRuntimeBenchNode = (*fakeRuntimeBenchCluster)(nil)

type fakeRuntimeBenchCluster struct {
	fakeCluster
}

func (f *fakeRuntimeBenchCluster) NodeID() uint64 {
	return 1
}

func (f *fakeRuntimeBenchCluster) ChannelRuntimeSnapshot(context.Context) (channelv2.RuntimeSnapshot, error) {
	return channelv2.RuntimeSnapshot{NodeID: 1}, nil
}

func (f *fakeRuntimeBenchCluster) ChannelRuntimeProbe(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error) {
	return channelv2.RuntimeProbeResult{}, nil
}

func (f *fakeRuntimeBenchCluster) ChannelRuntimeEvict(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error) {
	return channelv2.RuntimeEvictResult{}, nil
}

type fakeWriteReadyCluster struct {
	fakeCluster
	snapshots []clusterv2.Snapshot
	routes    map[uint16]clusterv2.Route
}

func (f *fakeWriteReadyCluster) Snapshot() clusterv2.Snapshot {
	if len(f.snapshots) == 0 {
		return clusterv2.Snapshot{}
	}
	return f.snapshots[0]
}

func (f *fakeWriteReadyCluster) RouteHashSlot(hashSlot uint16) (clusterv2.Route, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, "cluster.route")
	}
	route, ok := f.routes[hashSlot]
	if !ok {
		return clusterv2.Route{}, clusterv2.ErrRouteNotReady
	}
	return route, nil
}

type fakePresenceCluster struct {
	fakeCluster
	nodeID                   uint64
	events                   <-chan clusterv2.RouteAuthorityEvent
	snapshot                 clusterv2.Snapshot
	registeredService        uint8
	registeredHandler        clusterv2.NodeRPCHandler
	registeredHandlers       map[uint8]clusterv2.NodeRPCHandler
	appendSeq                uint64
	mu                       sync.Mutex
	conversationStateBatches [][]metadb.UserConversationState
	conversationPatchBatches [][]metadb.UserConversationActivePatch
	subscribers              map[string][]string
}

type fakeConversationFallbackCluster struct {
	fakeCluster
	appendSeq                uint64
	mu                       sync.Mutex
	messages                 map[metadb.ConversationKey][]channelv2.Message
	conversationStateBatches [][]metadb.UserConversationState
	subscribers              map[string][]string
}

type recordingDeliveryMetaNode struct {
	fakeCluster
	mu                sync.Mutex
	snapshot          clusterv2.Snapshot
	upserted          []metadb.Channel
	added             []recordedSubscriberMutation
	membershipUpserts []recordedMembershipProjection
	membershipDeletes []recordedMembershipProjection
	subscribers       map[string][]string
	listCalls         int
}

type recordedSubscriberMutation struct {
	channelID   string
	channelType int64
	uids        []string
	version     uint64
}

type recordedMembershipProjection struct {
	channelID   string
	channelType int64
	uids        []string
	joinSeq     uint64
	updatedAt   int64
}

func newFakePresenceCluster(nodeID uint64, events <-chan clusterv2.RouteAuthorityEvent) *fakePresenceCluster {
	return &fakePresenceCluster{nodeID: nodeID, events: events}
}

func conversationStatesByUID(states []metadb.UserConversationState) map[string]metadb.UserConversationState {
	out := make(map[string]metadb.UserConversationState, len(states))
	for _, state := range states {
		out[state.UID] = state
	}
	return out
}

func conversationPatchesByUID(patches []metadb.UserConversationActivePatch) map[string]metadb.UserConversationActivePatch {
	out := make(map[string]metadb.UserConversationActivePatch, len(patches))
	for _, patch := range patches {
		out[patch.UID] = patch
	}
	return out
}

type appRecordingConversationAuthorityStore struct {
	mu                 sync.Mutex
	touchBatches       [][]metadb.UserConversationActivePatch
	rows               map[metadb.ConversationKey]metadb.UserConversationState
	lastTouchDeadlineV time.Time
}

func (s *appRecordingConversationAuthorityStore) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]metadb.UserConversationState, 0, len(s.rows))
	for _, row := range s.rows {
		if row.UID == uid && conversationRowAfter(row, after) {
			rows = append(rows, row)
		}
	}
	sortConversationRows(rows)
	if limit <= 0 || len(rows) <= limit {
		return append([]metadb.UserConversationState(nil), rows...), conversationRowsCursor(rows, after), true, nil
	}
	page := append([]metadb.UserConversationState(nil), rows[:limit]...)
	return page, conversationRowsCursor(page, after), false, nil
}

func (s *appRecordingConversationAuthorityStore) GetUserConversationState(_ context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok || row.UID != uid {
		return metadb.UserConversationState{}, false, nil
	}
	return row, true, nil
}

func (s *appRecordingConversationAuthorityStore) TouchUserConversationActiveAtBatch(ctx context.Context, patches []metadb.UserConversationActivePatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		s.lastTouchDeadlineV = deadline
	}
	s.touchBatches = append(s.touchBatches, append([]metadb.UserConversationActivePatch(nil), patches...))
	if s.rows == nil {
		s.rows = make(map[metadb.ConversationKey]metadb.UserConversationState)
	}
	for _, patch := range patches {
		key := metadb.ConversationKey{ChannelID: patch.ChannelID, ChannelType: patch.ChannelType}
		row := s.rows[key]
		row.UID = patch.UID
		row.ChannelID = patch.ChannelID
		row.ChannelType = patch.ChannelType
		if patch.ReadSeq > row.ReadSeq {
			row.ReadSeq = patch.ReadSeq
		}
		if patch.DeletedToSeq > row.DeletedToSeq {
			row.DeletedToSeq = patch.DeletedToSeq
		}
		if patch.ActiveAt > row.ActiveAt {
			row.ActiveAt = patch.ActiveAt
		}
		if patch.UpdatedAt > row.UpdatedAt {
			row.UpdatedAt = patch.UpdatedAt
		}
		if patch.SparseActiveSet {
			row.SparseActive = patch.SparseActive
		}
		s.rows[key] = row
	}
	return nil
}

func (s *appRecordingConversationAuthorityStore) UpsertUserConversationStatesBatch(ctx context.Context, states []metadb.UserConversationState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.rows == nil {
		s.rows = make(map[metadb.ConversationKey]metadb.UserConversationState)
	}
	for _, state := range states {
		key := metadb.ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}
		if existing, ok := s.rows[key]; ok && existing.UID == state.UID {
			state = mergeConversationState(existing, state)
		}
		s.rows[key] = state
	}
	return nil
}

func (s *appRecordingConversationAuthorityStore) totalTouchPatches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, batch := range s.touchBatches {
		total += len(batch)
	}
	return total
}

func (s *appRecordingConversationAuthorityStore) lastTouchDeadline() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTouchDeadlineV
}

func (s *appRecordingConversationAuthorityStore) lastTouchDeadlineWithin(timeout, slop time.Duration) bool {
	deadline := s.lastTouchDeadline()
	if deadline.IsZero() {
		return false
	}
	remaining := time.Until(deadline)
	return remaining > 0 && remaining <= timeout+slop
}

type recordingConversationAuthorityRouteNode struct {
	nodeID  uint64
	routes  map[string]clusterv2.Route
	watch   <-chan clusterv2.RouteAuthorityEvent
	handler clusterv2.NodeRPCHandler
}

func (n *recordingConversationAuthorityRouteNode) NodeID() uint64 {
	return n.nodeID
}

func (n *recordingConversationAuthorityRouteNode) RouteKey(uid string) (clusterv2.Route, error) {
	route, ok := n.routes[uid]
	if !ok {
		return clusterv2.Route{}, clusterv2.ErrRouteNotReady
	}
	return route, nil
}

func (n *recordingConversationAuthorityRouteNode) CallRPC(ctx context.Context, _ uint64, _ uint8, payload []byte) ([]byte, error) {
	if n.handler != nil {
		return n.handler.HandleRPC(ctx, payload)
	}
	return nil, errors.New("unexpected conversation authority rpc")
}

func (n *recordingConversationAuthorityRouteNode) RegisterRPC(uint8, clusterv2.NodeRPCHandler) {}

func (n *recordingConversationAuthorityRouteNode) WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent {
	return n.watch
}

type appNodeRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f appNodeRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

func routeFromConversationTarget(target conversationusecase.RouteTarget) clusterv2.Route {
	return clusterv2.Route{
		HashSlot:       target.HashSlot,
		SlotID:         target.SlotID,
		Leader:         target.LeaderNodeID,
		Revision:       target.RouteRevision,
		AuthorityEpoch: target.AuthorityEpoch,
	}
}

func authorityFromConversationTarget(target conversationusecase.RouteTarget) clusterv2.RouteAuthority {
	return clusterv2.RouteAuthority{
		HashSlot:       target.HashSlot,
		SlotID:         target.SlotID,
		LeaderNodeID:   target.LeaderNodeID,
		RouteRevision:  target.RouteRevision,
		AuthorityEpoch: target.AuthorityEpoch,
	}
}

func readyFakeClusterSnapshot(nodeID uint64, hashSlotCount uint16) clusterv2.Snapshot {
	return clusterv2.Snapshot{
		NodeID:        nodeID,
		RoutesReady:   true,
		SlotsReady:    true,
		ChannelsReady: true,
		SlotCount:     1,
		HashSlotCount: hashSlotCount,
	}
}

func fakeChannelAuthorityMeta(nodeID uint64, id channelv2.ChannelID) channelv2.Meta {
	leader := channelv2.NodeID(nodeID)
	return channelv2.Meta{
		Key:         channelv2.ChannelKeyForID(id),
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      leader,
		Replicas:    []channelv2.NodeID{leader},
		ISR:         []channelv2.NodeID{leader},
		MinISR:      1,
		Status:      channelv2.StatusActive,
	}
}

func testUIDForHashSlot(t *testing.T, want, count uint16) string {
	t.Helper()
	for i := 0; i < 100000; i++ {
		uid := fmt.Sprintf("bench-u-%d", i)
		if routing.HashSlotForKey(uid, count) == want {
			return uid
		}
	}
	t.Fatalf("no uid found for hash slot %d/%d", want, count)
	return ""
}

func (n *recordingDeliveryMetaNode) Snapshot() clusterv2.Snapshot {
	return n.snapshot
}

func (n *recordingDeliveryMetaNode) RouteHashSlot(hashSlot uint16) (clusterv2.Route, error) {
	return clusterv2.Route{HashSlot: hashSlot, SlotID: 1, Leader: 1, Revision: 1, AuthorityEpoch: 1}, nil
}

func (n *recordingDeliveryMetaNode) UpsertChannelMetadata(_ context.Context, channel metadb.Channel) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.upserted = append(n.upserted, channel)
	return nil
}

func (n *recordingDeliveryMetaNode) GetChannelMetadata(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := len(n.upserted) - 1; i >= 0; i-- {
		ch := n.upserted[i]
		if ch.ChannelID == channelID && ch.ChannelType == channelType {
			return ch, nil
		}
	}
	return metadb.Channel{}, metadb.ErrNotFound
}

func (n *recordingDeliveryMetaNode) DeleteChannelMetadata(_ context.Context, channelID string, channelType int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	filtered := n.upserted[:0]
	for _, ch := range n.upserted {
		if ch.ChannelID != channelID || ch.ChannelType != channelType {
			filtered = append(filtered, ch)
		}
	}
	n.upserted = filtered
	delete(n.subscribers, channelID)
	return nil
}

func (n *recordingDeliveryMetaNode) AddChannelSubscribers(_ context.Context, channelID string, channelType int64, uids []string, version uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.added = append(n.added, recordedSubscriberMutation{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		version:     version,
	})
	return nil
}

func (n *recordingDeliveryMetaNode) RemoveChannelSubscribers(_ context.Context, channelID string, channelType int64, uids []string, version uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.added = append(n.added, recordedSubscriberMutation{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		version:     version,
	})
	return nil
}

func (n *recordingDeliveryMetaNode) ListChannelSubscribersPage(_ context.Context, channelID string, _ int64, afterUID string, limit int) ([]string, string, bool, error) {
	n.mu.Lock()
	n.listCalls++
	uids := append([]string(nil), n.subscribers[channelID]...)
	n.mu.Unlock()
	start := 0
	for start < len(uids) && afterUID != "" {
		if uids[start] == afterUID {
			start++
			break
		}
		start++
	}
	if limit <= 0 || start >= len(uids) {
		return nil, "", true, nil
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := append([]string(nil), uids[start:end]...)
	if end >= len(uids) {
		return page, "", true, nil
	}
	return page, page[len(page)-1], false, nil
}

func (n *recordingDeliveryMetaNode) UpsertUserChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.membershipUpserts = append(n.membershipUpserts, recordedMembershipProjection{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		joinSeq:     joinSeq,
		updatedAt:   updatedAt,
	})
	return nil
}

func (n *recordingDeliveryMetaNode) DeleteUserChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, updatedAt int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.membershipDeletes = append(n.membershipDeletes, recordedMembershipProjection{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		updatedAt:   updatedAt,
	})
	return nil
}

func (f *fakePresenceCluster) NodeID() uint64 {
	return f.nodeID
}

func (f *fakePresenceCluster) ResolveChannelAppendAuthority(_ context.Context, id channelv2.ChannelID) (channelv2.Meta, error) {
	return fakeChannelAuthorityMeta(f.nodeID, id), nil
}

func (f *fakePresenceCluster) RouteKey(uid string) (clusterv2.Route, error) {
	return clusterv2.Route{HashSlot: 9, SlotID: 1, Leader: f.nodeID, Revision: 3, AuthorityEpoch: 2}, nil
}

func (f *fakePresenceCluster) RouteHashSlot(hashSlot uint16) (clusterv2.Route, error) {
	return clusterv2.Route{HashSlot: hashSlot, SlotID: 1, Leader: f.nodeID, Revision: 3, AuthorityEpoch: 2}, nil
}

func (f *fakePresenceCluster) Snapshot() clusterv2.Snapshot {
	return f.snapshot
}

func (f *fakePresenceCluster) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return nil, errors.New("unexpected presence rpc call")
}

func (f *fakePresenceCluster) RegisterRPC(serviceID uint8, handler clusterv2.NodeRPCHandler) {
	f.registeredService = serviceID
	f.registeredHandler = handler
	if f.registeredHandlers == nil {
		f.registeredHandlers = make(map[uint8]clusterv2.NodeRPCHandler)
	}
	f.registeredHandlers[serviceID] = handler
}

func (f *fakePresenceCluster) AppendChannelBatch(_ context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	fakeItems := make([]channelv2.AppendBatchItemResult, 0, len(req.Messages))
	for _, msg := range req.Messages {
		f.appendSeq++
		msg.MessageSeq = f.appendSeq
		msg.Payload = append([]byte(nil), msg.Payload...)
		fakeItems = append(fakeItems, channelv2.AppendBatchItemResult{
			MessageID:  msg.MessageID,
			MessageSeq: msg.MessageSeq,
			Message:    msg,
		})
	}
	return channelv2.AppendBatchResult{Items: fakeItems}, nil
}

func (f *fakePresenceCluster) UpsertUserConversationStatesBatch(_ context.Context, states []metadb.UserConversationState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := append([]metadb.UserConversationState(nil), states...)
	f.conversationStateBatches = append(f.conversationStateBatches, batch)
	return nil
}

func (f *fakePresenceCluster) TouchUserConversationActiveAtBatch(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := append([]metadb.UserConversationActivePatch(nil), patches...)
	f.conversationPatchBatches = append(f.conversationPatchBatches, batch)
	return nil
}

func (f *fakePresenceCluster) ListChannelSubscribersPage(_ context.Context, channelID string, _ int64, afterUID string, limit int) ([]string, string, bool, error) {
	f.mu.Lock()
	uids := append([]string(nil), f.subscribers[channelID]...)
	f.mu.Unlock()
	start := 0
	for start < len(uids) && afterUID != "" {
		if uids[start] == afterUID {
			start++
			break
		}
		start++
	}
	if limit <= 0 || start >= len(uids) {
		return nil, "", true, nil
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := append([]string(nil), uids[start:end]...)
	if end >= len(uids) {
		return page, "", true, nil
	}
	return page, page[len(page)-1], false, nil
}

func (f *fakePresenceCluster) ListUserChannelMembershipPage(_ context.Context, _ string, _ metadb.UserChannelMembershipCursor, _ int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	return nil, metadb.UserChannelMembershipCursor{}, true, nil
}

func (f *fakePresenceCluster) ListUserConversationActivePage(_ context.Context, _ string, _ metadb.UserConversationActiveCursor, _ int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	return nil, metadb.UserConversationActiveCursor{}, true, nil
}

func (f *fakePresenceCluster) GetUserConversationState(_ context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.conversationStateBatches) - 1; i >= 0; i-- {
		for _, state := range f.conversationStateBatches[i] {
			if state.UID == uid && state.ChannelID == channelID && state.ChannelType == channelType {
				return state, true, nil
			}
		}
	}
	return metadb.UserConversationState{}, false, nil
}

func (f *fakePresenceCluster) ReadChannelLastVisible(context.Context, channelv2.ChannelID, uint64) (channelv2.Message, bool, error) {
	return channelv2.Message{}, false, nil
}

func (f *fakePresenceCluster) WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent {
	if f.calls != nil {
		*f.calls = append(*f.calls, "presence.start")
	}
	if f.events != nil {
		return f.events
	}
	ch := make(chan clusterv2.RouteAuthorityEvent)
	return ch
}

func (f *fakeConversationFallbackCluster) AppendChannelBatch(_ context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.messages == nil {
		f.messages = make(map[metadb.ConversationKey][]channelv2.Message)
	}
	key := metadb.ConversationKey{ChannelID: req.ChannelID.ID, ChannelType: int64(req.ChannelID.Type)}
	items := make([]channelv2.AppendBatchItemResult, 0, len(req.Messages))
	for _, msg := range req.Messages {
		f.appendSeq++
		msg.MessageSeq = f.appendSeq
		msg.Payload = append([]byte(nil), msg.Payload...)
		f.messages[key] = append(f.messages[key], msg)
		items = append(items, channelv2.AppendBatchItemResult{
			MessageID:  msg.MessageID,
			MessageSeq: msg.MessageSeq,
			Message:    msg,
		})
	}
	return channelv2.AppendBatchResult{Items: items}, nil
}

func (f *fakeConversationFallbackCluster) NodeID() uint64 {
	return 3
}

func (f *fakeConversationFallbackCluster) ResolveChannelAppendAuthority(_ context.Context, id channelv2.ChannelID) (channelv2.Meta, error) {
	return fakeChannelAuthorityMeta(3, id), nil
}

func (f *fakeConversationFallbackCluster) UpsertUserConversationStatesBatch(_ context.Context, states []metadb.UserConversationState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conversationStateBatches = append(f.conversationStateBatches, append([]metadb.UserConversationState(nil), states...))
	return nil
}

func (f *fakeConversationFallbackCluster) TouchUserConversationActiveAtBatch(context.Context, []metadb.UserConversationActivePatch) error {
	return errors.New("unexpected authority active patch fallback write")
}

func (f *fakeConversationFallbackCluster) ListChannelSubscribersPage(_ context.Context, channelID string, _ int64, afterUID string, limit int) ([]string, string, bool, error) {
	f.mu.Lock()
	uids := append([]string(nil), f.subscribers[channelID]...)
	f.mu.Unlock()
	start := 0
	for start < len(uids) && afterUID != "" {
		if uids[start] == afterUID {
			start++
			break
		}
		start++
	}
	if limit <= 0 || start >= len(uids) {
		return nil, "", true, nil
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := append([]string(nil), uids[start:end]...)
	if end >= len(uids) {
		return page, "", true, nil
	}
	return page, page[len(page)-1], false, nil
}

func (f *fakeConversationFallbackCluster) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		return nil, metadb.UserConversationActiveCursor{}, true, nil
	}
	latest := make(map[metadb.ConversationKey]metadb.UserConversationState)
	for _, batch := range f.conversationStateBatches {
		for _, state := range batch {
			if state.UID != uid {
				continue
			}
			key := metadb.ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}
			existing, ok := latest[key]
			if !ok {
				latest[key] = state
				continue
			}
			latest[key] = mergeConversationState(existing, state)
		}
	}
	rows := make([]metadb.UserConversationState, 0, len(latest))
	for _, state := range latest {
		if conversationRowAfter(state, after) {
			rows = append(rows, state)
		}
	}
	sortConversationRows(rows)
	if len(rows) <= limit {
		return rows, conversationRowsCursor(rows, after), true, nil
	}
	page := append([]metadb.UserConversationState(nil), rows[:limit]...)
	return page, conversationRowsCursor(page, after), false, nil
}

func (f *fakeConversationFallbackCluster) ReadChannelLastVisible(_ context.Context, id channelv2.ChannelID, visibleAfterSeq uint64) (channelv2.Message, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := metadb.ConversationKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	messages := f.messages[key]
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.MessageSeq <= visibleAfterSeq {
			continue
		}
		msg.Payload = append([]byte(nil), msg.Payload...)
		return msg, true, nil
	}
	return channelv2.Message{}, false, nil
}

type touchBatch struct {
	target presence.RouteTarget
	routes []presence.Route
}

type recordingPresenceDirectory struct {
	mu      sync.Mutex
	become  []presence.RouteTarget
	lose    []uint16
	expires []expireCall
}

type expireCall struct {
	now time.Time
	ttl time.Duration
}

func (r *recordingPresenceDirectory) BecomeAuthority(target presence.RouteTarget) {
	r.mu.Lock()
	r.become = append(r.become, target)
	r.mu.Unlock()
}

func (r *recordingPresenceDirectory) LoseAuthority(hashSlot uint16) {
	r.mu.Lock()
	r.lose = append(r.lose, hashSlot)
	r.mu.Unlock()
}

func (r *recordingPresenceDirectory) ExpireRoutes(now time.Time, ttl time.Duration) int {
	r.mu.Lock()
	r.expires = append(r.expires, expireCall{now: now, ttl: ttl})
	r.mu.Unlock()
	return 0
}

func (r *recordingPresenceDirectory) becomeSnapshot() []presence.RouteTarget {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]presence.RouteTarget(nil), r.become...)
}

func (r *recordingPresenceDirectory) loseSnapshot() []uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uint16(nil), r.lose...)
}

type recordingTouchAuthority struct {
	mu          sync.Mutex
	targets     map[string]presence.RouteTarget
	batches     []touchBatch
	err         error
	resolveHook func(string)
}

type recordingSessionHandle struct {
	reason   string
	writeErr error
	writes   []any
}

type fakeDeliverySubscriberSource struct {
	requests []runtimedelivery.SubscriberPageRequest
	pages    []runtimedelivery.UIDPage
}

type appStaticDeliveryPartitioner struct {
	partitions []runtimedelivery.Partition
}

func (p appStaticDeliveryPartitioner) Partitions(context.Context) ([]runtimedelivery.Partition, error) {
	return append([]runtimedelivery.Partition(nil), p.partitions...), nil
}

type appRecordingFanoutRunner struct {
	mu    sync.Mutex
	tasks []runtimedelivery.FanoutTask
}

type recordingWorkerRuntime struct {
	startCount int
	stopCount  int
	startErr   error
	stopErr    error
}

func (r *recordingWorkerRuntime) Start(context.Context) error {
	r.startCount++
	return r.startErr
}

func (r *recordingWorkerRuntime) Stop(context.Context) error {
	r.stopCount++
	return r.stopErr
}

func (r *appRecordingFanoutRunner) RunTask(_ context.Context, task runtimedelivery.FanoutTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, task)
	return nil
}

func (r *appRecordingFanoutRunner) snapshot() []runtimedelivery.FanoutTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtimedelivery.FanoutTask(nil), r.tasks...)
}

func (s *fakeDeliverySubscriberSource) ListSubscribers(_ context.Context, req runtimedelivery.SubscriberPageRequest) (runtimedelivery.UIDPage, error) {
	s.requests = append(s.requests, req)
	if len(s.pages) == 0 {
		return runtimedelivery.UIDPage{Done: true}, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, nil
}

func (r *recordingSessionHandle) WriteDelivery(payload any) error {
	r.writes = append(r.writes, payload)
	return r.writeErr
}

func (r *recordingSessionHandle) CloseSession(reason string) error {
	r.reason = reason
	return nil
}

func (r *recordingTouchAuthority) ResolveRouteTarget(uid string) (presence.RouteTarget, error) {
	r.mu.Lock()
	target, ok := r.targets[uid]
	hook := r.resolveHook
	r.mu.Unlock()
	if hook != nil {
		hook(uid)
	}
	if !ok {
		return presence.RouteTarget{}, errors.New("target not found")
	}
	return target, nil
}

func (r *recordingTouchAuthority) TouchRoutesTo(_ context.Context, target presence.RouteTarget, routes []presence.Route) error {
	r.mu.Lock()
	r.batches = append(r.batches, touchBatch{
		target: target,
		routes: append([]presence.Route(nil), routes...),
	})
	err := r.err
	r.mu.Unlock()
	return err
}

func routesForTarget(batches []touchBatch, target presence.RouteTarget) []presence.Route {
	for _, batch := range batches {
		if batch.target == target {
			return batch.routes
		}
	}
	return nil
}

func newAppDeliveryTestSession(id uint64, writes *sendackSmokeSessionWrites) session.Session {
	return session.New(session.Config{
		ID: id,
		WriteFrameFn: func(f frame.Frame, _ session.OutboundMeta) error {
			writes.append(f)
			return nil
		},
	})
}

func activateAppDeliverySession(t *testing.T, app *App, sess session.Session, uid string) {
	t.Helper()
	sess.SetValue(gateway.SessionValueUID, uid)
	sess.SetValue(gateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
	_, err := app.Handler().OnSessionActivate(&gateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	})
	if err != nil {
		t.Fatalf("OnSessionActivate(%s) error = %v", uid, err)
	}
}

func (w *sendackSmokeSessionWrites) waitForRecvPacket(t *testing.T, timeout time.Duration) *frame.RecvPacket {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		w.mu.Lock()
		for _, written := range w.frames {
			if recv, ok := written.(*frame.RecvPacket); ok {
				w.mu.Unlock()
				return recv
			}
		}
		count := len(w.frames)
		w.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for RecvPacket; written frame count=%d", count)
		}
		time.Sleep(time.Millisecond)
	}
}

func requireConversationEventually(t *testing.T, app *App, uid, channelID string, channelType uint8) conversationusecase.Conversation {
	t.Helper()
	var got []conversationusecase.Conversation
	var lastErr error
	waitUntil(t, time.Second, func() bool {
		list, err := app.Conversations().List(context.Background(), conversationusecase.ListRequest{UID: uid, Limit: 10})
		lastErr = err
		if err != nil {
			return false
		}
		got = list.Items
		if len(got) != 1 {
			return false
		}
		item := got[0]
		return item.ChannelID == channelID &&
			item.ChannelType == int64(channelType) &&
			item.ActiveAt > 0 &&
			!item.SparseActive
	})
	if lastErr != nil {
		t.Fatalf("Conversations().List(%s) error = %v", uid, lastErr)
	}
	item := got[0]
	if item.ChannelID != channelID || item.ChannelType != int64(channelType) || item.ActiveAt <= 0 || item.SparseActive {
		t.Fatalf("conversation list for %s = %#v, want dense row for %s/%d", uid, got, channelID, channelType)
	}
	return item
}

func waitUntil(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ok() {
		return
	}
	t.Fatalf("condition was not met within %v", timeout)
}

type fakeGateway struct {
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeGateway) Start() error {
	*f.calls = append(*f.calls, "gateway.start")
	return f.startErr
}

func (f *fakeGateway) Stop() error {
	*f.calls = append(*f.calls, "gateway.stop")
	return f.stopErr
}

type recordingAppLogger struct {
	syncCalls int
	entries   []recordedAppLogEntry
}

type recordedAppLogEntry struct {
	level  string
	fields []wklog.Field
}

func (r *recordingAppLogger) Debug(_ string, fields ...wklog.Field) { r.log("DEBUG", fields...) }
func (r *recordingAppLogger) Info(_ string, fields ...wklog.Field)  { r.log("INFO", fields...) }
func (r *recordingAppLogger) Warn(_ string, fields ...wklog.Field)  { r.log("WARN", fields...) }
func (r *recordingAppLogger) Error(_ string, fields ...wklog.Field) { r.log("ERROR", fields...) }
func (r *recordingAppLogger) Fatal(_ string, fields ...wklog.Field) { r.log("FATAL", fields...) }

func (r *recordingAppLogger) Named(string) wklog.Logger {
	return r
}

func (r *recordingAppLogger) With(...wklog.Field) wklog.Logger {
	return r
}

func (r *recordingAppLogger) log(level string, fields ...wklog.Field) {
	r.entries = append(r.entries, recordedAppLogEntry{
		level:  level,
		fields: append([]wklog.Field(nil), fields...),
	})
}

func (r *recordingAppLogger) Sync() error {
	r.syncCalls++
	return nil
}

type fakeAPI struct {
	calls    *[]string
	startErr error
	stopErr  error
}

func (f *fakeAPI) Start() error {
	*f.calls = append(*f.calls, "api.start")
	return f.startErr
}

func (f *fakeAPI) Stop(context.Context) error {
	*f.calls = append(*f.calls, "api.stop")
	return f.stopErr
}

func joinCalls(calls []string) string {
	return strings.Join(calls, ",")
}

func waitAppClusterSnapshotsConverge(t *testing.T, nodes []*clusterv2.Node) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last []clusterv2.Snapshot
	for time.Now().Before(deadline) {
		snapshots := make([]clusterv2.Snapshot, 0, len(nodes))
		for _, node := range nodes {
			snapshots = append(snapshots, node.Snapshot())
		}
		last = snapshots
		if appClusterSnapshotsConverged(snapshots) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cluster snapshots did not converge: %#v", last)
}

func appClusterSnapshotsConverged(snapshots []clusterv2.Snapshot) bool {
	if len(snapshots) == 0 {
		return false
	}
	want := snapshots[0]
	if want.StateRevision == 0 || !want.RoutesReady || !want.SlotsReady || !want.ChannelsReady || want.ControllerLead == 0 {
		return false
	}
	for _, snapshot := range snapshots[1:] {
		if snapshot.StateRevision != want.StateRevision ||
			snapshot.SlotCount != want.SlotCount ||
			snapshot.HashSlotCount != want.HashSlotCount ||
			snapshot.ControllerLead != want.ControllerLead ||
			!snapshot.RoutesReady ||
			!snapshot.SlotsReady ||
			!snapshot.ChannelsReady {
			return false
		}
	}
	return true
}

type blockingCluster struct {
	startEntered chan struct{}
	releaseStart chan struct{}
}

func newBlockingCluster() *blockingCluster {
	return &blockingCluster{
		startEntered: make(chan struct{}),
		releaseStart: make(chan struct{}),
	}
}

func (f *blockingCluster) Start(context.Context) error {
	close(f.startEntered)
	<-f.releaseStart
	return nil
}

func (f *blockingCluster) Stop(context.Context) error {
	return nil
}

type stateGateway struct {
	mu      sync.Mutex
	running bool
}

func newStateGateway() *stateGateway {
	return &stateGateway{}
}

func (f *stateGateway) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = true
	return nil
}

func (f *stateGateway) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	return nil
}

func (f *stateGateway) runningState() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}
