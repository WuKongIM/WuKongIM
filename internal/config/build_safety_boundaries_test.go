package config

import (
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func minimalBuildValues() map[string]string {
	return map[string]string{
		"WK_NODE_ID":             "1",
		"WK_NODE_DATA_DIR":       "/var/lib/wukongim",
		"WK_CLUSTER_LISTEN_ADDR": "127.0.0.1:7001",
	}
}

func TestBuildConfigGatewayTokenAuthenticationDefaultsOnAndAllowsExplicitOff(t *testing.T) {
	cfg, err := buildConfig(minimalBuildValues())
	if err != nil {
		t.Fatalf("buildConfig(default): %v", err)
	}
	if !cfg.Gateway.TokenAuthOn {
		t.Fatal("Gateway.TokenAuthOn = false, want default true")
	}

	values := minimalBuildValues()
	values["WK_GATEWAY_TOKEN_AUTH_ON"] = "false"
	cfg, err = buildConfig(values)
	if err != nil {
		t.Fatalf("buildConfig(explicit false): %v", err)
	}
	if cfg.Gateway.TokenAuthOn {
		t.Fatal("Gateway.TokenAuthOn = true, want explicit false")
	}
}

func TestBuildConfigSeedJoinRequiresCompleteNonConflictingIdentity(t *testing.T) {
	values := minimalBuildValues()
	values["WK_CLUSTER_SEEDS"] = `[" node-2:7001 ","node-3:7001"]`
	values["WK_CLUSTER_ADVERTISE_ADDR"] = "node-1:7001"
	values["WK_CLUSTER_JOIN_TOKEN"] = "join-token"
	cfg, err := buildConfig(values)
	if err != nil {
		t.Fatalf("buildConfig(seed join): %v", err)
	}
	if cfg.Cluster.Control.Role != cluster.ControlRoleMirror || cfg.Cluster.Control.AllowBootstrap ||
		len(cfg.Cluster.Join.Seeds) != 2 || cfg.Cluster.Join.Seeds[0] != "node-2:7001" ||
		cfg.Cluster.Join.AdvertiseAddr != "node-1:7001" || cfg.Cluster.Join.Token != "join-token" {
		t.Fatalf("seed join config = %+v", cfg.Cluster)
	}

	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{name: "empty explicit token", mutate: func(v map[string]string) {
			v["WK_CLUSTER_JOIN_TOKEN"] = " "
		}, want: "WK_CLUSTER_JOIN_TOKEN must not be empty"},
		{name: "static nodes conflict", mutate: func(v map[string]string) {
			v["WK_CLUSTER_NODES"] = `[{"id":1,"addr":"node-1:7001"}]`
		}, want: "cannot be combined"},
		{name: "malformed seeds", mutate: func(v map[string]string) {
			v["WK_CLUSTER_SEEDS"] = `not-json`
		}, want: "WK_CLUSTER_SEEDS"},
		{name: "missing advertise address", mutate: func(v map[string]string) {
			delete(v, "WK_CLUSTER_ADVERTISE_ADDR")
		}, want: "WK_CLUSTER_ADVERTISE_ADDR is required"},
		{name: "missing join token", mutate: func(v map[string]string) {
			delete(v, "WK_CLUSTER_JOIN_TOKEN")
		}, want: "WK_CLUSTER_JOIN_TOKEN is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := minimalBuildValues()
			input["WK_CLUSTER_SEEDS"] = `["node-2:7001"]`
			input["WK_CLUSTER_ADVERTISE_ADDR"] = "node-1:7001"
			input["WK_CLUSTER_JOIN_TOKEN"] = "join-token"
			test.mutate(input)
			if _, err := buildConfig(input); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("buildConfig() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBuildConfigRejectsNegativeRuntimeBudgetsBeforeStartup(t *testing.T) {
	tests := []struct {
		key string
		raw string
	}{
		{key: "WK_CLUSTER_SLOT_TICK_INTERVAL", raw: "0s"},
		{key: "WK_CLUSTER_SLOT_ELECTION_TICK", raw: "0"},
		{key: "WK_CLUSTER_SLOT_HEARTBEAT_TICK", raw: "0"},
		{key: "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES", raw: "0"},
		{key: "WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL", raw: "0s"},
		{key: "WK_CHANNEL_APPEND_SHARD_COUNT", raw: "-1"},
		{key: "WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE", raw: "-1"},
		{key: "WK_CHANNEL_APPEND_EFFECT_POOL_SIZE", raw: "-1"},
		{key: "WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY", raw: "-1"},
		{key: "WK_DELIVERY_FANOUT_PAGE_SIZE", raw: "-1"},
		{key: "WK_DELIVERY_PUSH_BATCH_SIZE", raw: "-1"},
		{key: "WK_DELIVERY_PENDING_ACK_TTL", raw: "-1s"},
		{key: "WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION", raw: "-1"},
		{key: "WK_DELIVERY_EVENT_QUEUE_SIZE", raw: "-1"},
		{key: "WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY", raw: "-1"},
		{key: "WK_WEBHOOK_QUEUE_SIZE", raw: "-1"},
		{key: "WK_WEBHOOK_WORKERS", raw: "-1"},
		{key: "WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_ITEMS", raw: "-1"},
		{key: "WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_WAIT", raw: "-1s"},
		{key: "WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_ITEMS", raw: "-1"},
		{key: "WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_WAIT", raw: "-1s"},
		{key: "WK_WEBHOOK_OFFLINE_UID_BATCH_SIZE", raw: "-1"},
		{key: "WK_WEBHOOK_REQUEST_TIMEOUT", raw: "-1s"},
		{key: "WK_WEBHOOK_RETRY_MAX_ATTEMPTS", raw: "-1"},
		{key: "WK_PLUGIN_TIMEOUT", raw: "-1s"},
		{key: "WK_PLUGIN_PERSIST_AFTER_QUEUE_SIZE", raw: "-1"},
		{key: "WK_PLUGIN_PERSIST_AFTER_WORKERS", raw: "-1"},
	}
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			values := minimalBuildValues()
			values[test.key] = test.raw
			if _, err := buildConfig(values); err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("buildConfig(%s=%q) error = %v", test.key, test.raw, err)
			}
		})
	}
}
