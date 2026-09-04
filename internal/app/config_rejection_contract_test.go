package app

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestNormalizeConfigRejectsUnsafeRuntimeCompositionsBeforeWiring(t *testing.T) {
	t.Parallel()

	validManager := func() ManagerConfig {
		return ManagerConfig{
			ListenAddr: "127.0.0.1:5001",
			AuthOn:     true,
			JWTSecret:  "installation-secret",
			JWTExpire:  time.Hour,
			Users: []ManagerUserConfig{{
				Username: "operator",
				Password: "password",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			}},
		}
	}
	validPrometheus := PrometheusConfig{
		Enabled:       true,
		ListenAddr:    "127.0.0.1:9099",
		ScrapeTargets: []string{"127.0.0.1:5001"},
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "manager secret", mutate: func(cfg *Config) { cfg.Manager = validManager(); cfg.Manager.JWTSecret = "" }, want: "manager jwt secret"},
		{name: "manager expiry", mutate: func(cfg *Config) { cfg.Manager = validManager(); cfg.Manager.JWTExpire = -time.Second }, want: "manager jwt expire"},
		{name: "manager users", mutate: func(cfg *Config) { cfg.Manager = validManager(); cfg.Manager.Users = nil }, want: "manager users"},
		{name: "manager username", mutate: func(cfg *Config) { cfg.Manager = validManager(); cfg.Manager.Users[0].Username = " " }, want: "manager username"},
		{name: "manager password", mutate: func(cfg *Config) { cfg.Manager = validManager(); cfg.Manager.Users[0].Password = "" }, want: "manager password"},
		{name: "manager permission resource", mutate: func(cfg *Config) { cfg.Manager = validManager(); cfg.Manager.Users[0].Permissions[0].Resource = " " }, want: "permission resource"},
		{name: "manager permission actions", mutate: func(cfg *Config) { cfg.Manager = validManager(); cfg.Manager.Users[0].Permissions[0].Actions = nil }, want: "permission action must be set"},
		{name: "manager permission action", mutate: func(cfg *Config) {
			cfg.Manager = validManager()
			cfg.Manager.Users[0].Permissions[0].Actions = []string{"delete"}
		}, want: "one of r, w or *"},
		{name: "message system uid", mutate: func(cfg *Config) { cfg.Message.SystemUID = "bad@system" }, want: "message system uid"},
		{name: "message cache ttl", mutate: func(cfg *Config) { cfg.Message.PermissionCacheTTL = -time.Second }, want: "message permission cache ttl"},
		{name: "retention interval", mutate: func(cfg *Config) { cfg.ChannelMessageRetention.ScanInterval = -time.Second }, want: "retention scan interval"},
		{name: "retention batch", mutate: func(cfg *Config) { cfg.ChannelMessageRetention.ChannelBatchSize = -1 }, want: "retention channel batch size"},
		{name: "retention messages", mutate: func(cfg *Config) { cfg.ChannelMessageRetention.MaxTrimMessages = -1 }, want: "retention max trim messages"},
		{name: "retention bytes", mutate: func(cfg *Config) { cfg.ChannelMessageRetention.MaxTrimBytes = -1 }, want: "retention max trim bytes"},
		{name: "presence activation", mutate: func(cfg *Config) { cfg.Presence.ActivationTimeout = -time.Second }, want: "presence activation timeout"},
		{name: "presence flush", mutate: func(cfg *Config) { cfg.Presence.TouchFlushInterval = -time.Second }, want: "presence touch flush interval"},
		{name: "presence batch", mutate: func(cfg *Config) { cfg.Presence.TouchBatchSize = -1 }, want: "presence touch batch size"},
		{name: "presence route budget", mutate: func(cfg *Config) { cfg.Presence.TouchMaxRoutesPerFlush = -1 }, want: "presence touch max routes"},
		{name: "presence route budget below batch", mutate: func(cfg *Config) { cfg.Presence.TouchBatchSize = 8; cfg.Presence.TouchMaxRoutesPerFlush = 7 }, want: "greater than or equal"},
		{name: "presence ttl", mutate: func(cfg *Config) { cfg.Presence.RouteTTL = -time.Second }, want: "presence route ttl"},
		{name: "channel threshold", mutate: func(cfg *Config) { cfg.Channel.LargeGroupSubscriberThreshold = -1 }, want: "large group subscriber threshold"},
		{name: "append shards", mutate: func(cfg *Config) { cfg.ChannelAppend.AuthorityShardCount = -1 }, want: "authority shard count"},
		{name: "append advance pool", mutate: func(cfg *Config) { cfg.ChannelAppend.AdvancePoolSize = -1 }, want: "advance pool size"},
		{name: "append effect pool", mutate: func(cfg *Config) { cfg.ChannelAppend.EffectPoolSize = -1 }, want: "effect pool size"},
		{name: "append recipient concurrency", mutate: func(cfg *Config) { cfg.ChannelAppend.RecipientAuthorityDispatchConcurrency = -1 }, want: "recipient authority dispatch concurrency"},
		{name: "delivery fanout", mutate: func(cfg *Config) { cfg.Delivery.FanoutPageSize = -1 }, want: "delivery fanout page size"},
		{name: "delivery push", mutate: func(cfg *Config) { cfg.Delivery.PushBatchSize = -1 }, want: "delivery push batch size"},
		{name: "delivery ack ttl", mutate: func(cfg *Config) { cfg.Delivery.PendingAckTTL = -time.Second }, want: "delivery pending ack ttl"},
		{name: "delivery pending cap", mutate: func(cfg *Config) { cfg.Delivery.PendingAckMaxPerSession = -1 }, want: "delivery pending ack max"},
		{name: "delivery queue", mutate: func(cfg *Config) { cfg.Delivery.EventQueueSize = -1 }, want: "delivery event queue size"},
		{name: "delivery workers", mutate: func(cfg *Config) { cfg.Delivery.RecipientWorkerConcurrency = -1 }, want: "delivery recipient worker concurrency"},
		{name: "prometheus retention", mutate: func(cfg *Config) { cfg.Observability.Prometheus.RetentionTime = -time.Second }, want: "prometheus retention time"},
		{name: "prometheus scrape interval", mutate: func(cfg *Config) { cfg.Observability.Prometheus.ScrapeInterval = -time.Second }, want: "prometheus scrape interval"},
		{name: "prometheus query scheme", mutate: func(cfg *Config) { cfg.Observability.Prometheus.QueryBaseURL = "ftp://metrics.example" }, want: "must use http or https"},
		{name: "prometheus query host", mutate: func(cfg *Config) { cfg.Observability.Prometheus.QueryBaseURL = "http:///api" }, want: "requires host"},
		{name: "prometheus query parameters", mutate: func(cfg *Config) {
			cfg.Observability.Prometheus.QueryBaseURL = "https://metrics.example/api?tenant=one"
		}, want: "must not include query or fragment"},
		{name: "prometheus listen", mutate: func(cfg *Config) {
			cfg.Observability.MetricsEnabled = true
			cfg.Observability.Prometheus = validPrometheus
			cfg.Observability.Prometheus.ListenAddr = "missing-port"
			cfg.API.ListenAddr = "127.0.0.1:5001"
		}, want: "listen addr must be host:port"},
		{name: "prometheus targets", mutate: func(cfg *Config) {
			cfg.Observability.MetricsEnabled = true
			cfg.Observability.Prometheus = validPrometheus
			cfg.Observability.Prometheus.ScrapeTargets = []string{""}
			cfg.API.ListenAddr = "127.0.0.1:5001"
		}, want: "scrape target must be non-empty"},
		{name: "prometheus target scheme", mutate: func(cfg *Config) {
			cfg.Observability.MetricsEnabled = true
			cfg.Observability.Prometheus = validPrometheus
			cfg.Observability.Prometheus.ScrapeTargets = []string{"http://127.0.0.1:5001"}
			cfg.API.ListenAddr = "127.0.0.1:5001"
		}, want: "without scheme"},
		{name: "prometheus target host", mutate: func(cfg *Config) {
			cfg.Observability.MetricsEnabled = true
			cfg.Observability.Prometheus = validPrometheus
			cfg.Observability.Prometheus.ScrapeTargets = []string{":5001"}
			cfg.API.ListenAddr = "127.0.0.1:5001"
		}, want: "host must be non-empty"},
		{name: "prometheus target port", mutate: func(cfg *Config) {
			cfg.Observability.MetricsEnabled = true
			cfg.Observability.Prometheus = validPrometheus
			cfg.Observability.Prometheus.ScrapeTargets = []string{"127.0.0.1:0"}
			cfg.API.ListenAddr = "127.0.0.1:5001"
		}, want: "port must be 1-65535"},
		{name: "prometheus requires metrics", mutate: func(cfg *Config) {
			cfg.Observability.Prometheus = validPrometheus
			cfg.API.ListenAddr = "127.0.0.1:5001"
		}, want: "prometheus requires metrics"},
		{name: "prometheus requires api", mutate: func(cfg *Config) {
			cfg.Observability.MetricsEnabled = true
			cfg.Observability.Prometheus = validPrometheus
		}, want: "prometheus requires api listen addr"},
		{name: "diagnostics sample nan", mutate: func(cfg *Config) { cfg.Observability.Diagnostics.SampleRate = math.NaN() }, want: "diagnostics sample rate"},
		{name: "diagnostics error sample", mutate: func(cfg *Config) { cfg.Observability.Diagnostics.ErrorSampleRate = 2 }, want: "diagnostics error sample rate"},
		{name: "diagnostics deep sample", mutate: func(cfg *Config) { cfg.Observability.Diagnostics.DeepSampleRate = -0.1 }, want: "diagnostics deep sample rate"},
		{name: "diagnostics deep threshold", mutate: func(cfg *Config) { cfg.Observability.Diagnostics.DeepSlowThreshold = -time.Second }, want: "diagnostics deep slow threshold"},
		{name: "diagnostics batch", mutate: func(cfg *Config) { cfg.Observability.Diagnostics.DeepMaxItemsPerBatch = -1 }, want: "diagnostics deep max items"},
		{name: "diagnostics rule rate", mutate: func(cfg *Config) {
			cfg.Observability.Diagnostics.DebugMatches = []DiagnosticsDebugMatchConfig{{SampleRate: math.Inf(1)}}
		}, want: "debug match sample rate"},
		{name: "diagnostics rule ttl", mutate: func(cfg *Config) {
			cfg.Observability.Diagnostics.DebugMatches = []DiagnosticsDebugMatchConfig{{SampleRate: 1, TTLSeconds: -1}}
		}, want: "debug match ttl"},
		{name: "top interval", mutate: func(cfg *Config) {
			cfg.Top = TopConfig{APIEnabled: true, CollectInterval: -time.Second, HistoryWindow: time.Minute}
		}, want: "top collect interval"},
		{name: "top history", mutate: func(cfg *Config) {
			cfg.Top = TopConfig{APIEnabled: true, CollectInterval: time.Second, HistoryWindow: time.Second}
		}, want: "top history window"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{}
			cfg.Plugin.SetEnableExplicit(true)
			test.mutate(&cfg)
			_, err := NormalizeConfig(cfg)
			if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NormalizeConfig() error = %v, want ErrInvalidConfig containing %q", err, test.want)
			}
		})
	}
}

func TestManagerPermissionActionsAreClosedAndExplicit(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"r", "w", "*"} {
		if !validManagerPermissionAction(action) {
			t.Fatalf("validManagerPermissionAction(%q) = false", action)
		}
	}
	for _, action := range []string{"", "read", "R", "rw"} {
		if validManagerPermissionAction(action) {
			t.Fatalf("validManagerPermissionAction(%q) = true", action)
		}
	}
}
