package app

import (
	"context"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestNodeConfigSnapshotReturnsEffectiveGroupsAndRedactsSecrets(t *testing.T) {
	app := &App{cfg: Config{
		NodeID:  2,
		DataDir: "/var/lib/wukongim/node2",
		Cluster: clusterpkg.Config{
			NodeID:     2,
			ListenAddr: "127.0.0.1:12002",
			DataDir:    "/var/lib/wukongim/node2/cluster",
			Control: clusterpkg.ControlConfig{
				ClusterID: "cluster-a",
			},
			Join: clusterpkg.JoinConfig{
				AdvertiseAddr: "node2.example:12002",
				Token:         "join-secret",
			},
			Slots: clusterpkg.SlotConfig{
				InitialSlotCount: 1,
				HashSlotCount:    256,
				ReplicaCount:     3,
			},
			Channel: clusterpkg.ChannelConfig{
				ReplicaCount: 3,
			},
			HealthReport: clusterpkg.HealthReportConfig{
				Interval: 5 * time.Second,
				TTL:      30 * time.Second,
			},
		},
		Manager: ManagerConfig{
			ListenAddr: ":5300",
			AuthOn:     true,
			JWTSecret:  "super-secret",
			JWTIssuer:  "wk-manager",
			JWTExpire:  time.Hour,
			Users:      []ManagerUserConfig{{Username: "admin", Password: "admin-password"}},
		},
		Gateway: GatewayConfig{SendTimeout: 3 * time.Second},
		Log:     LogConfig{Level: "debug", Dir: "/var/log/wukongim"},
		Plugin:  PluginConfig{Enable: true, Timeout: 5 * time.Second},
		Webhook: WebhookConfig{
			Enabled:          true,
			HTTPAddr:         "http://127.0.0.1:19090/webhook",
			QueueSize:        1024,
			Workers:          16,
			RequestTimeout:   5 * time.Second,
			RetryMaxAttempts: 3,
		},
	}}

	snapshot, err := app.NodeConfigSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot() error = %v", err)
	}
	if snapshot.NodeID != 2 || snapshot.Source != "effective_startup_config" || !snapshot.RequiresRestart {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	if !containsNodeConfigItem(snapshot, "WK_NODE_ID", "2") {
		t.Fatalf("snapshot missing WK_NODE_ID=2: %#v", snapshot.Groups)
	}
	if !containsNodeConfigItem(snapshot, "WK_MANAGER_JWT_SECRET", "******") {
		t.Fatalf("snapshot missing redacted JWT secret: %#v", snapshot.Groups)
	}
	raw := nodeConfigSnapshotText(snapshot)
	for _, forbidden := range []string{"super-secret", "admin-password"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("snapshot leaked secret %q: %s", forbidden, raw)
		}
	}
}

func containsNodeConfigItem(snapshot managementusecase.NodeConfigSnapshot, key, value string) bool {
	for _, group := range snapshot.Groups {
		for _, item := range group.Items {
			if item.Key == key && item.Value == value {
				return true
			}
		}
	}
	return false
}

func nodeConfigSnapshotText(snapshot managementusecase.NodeConfigSnapshot) string {
	var builder strings.Builder
	for _, group := range snapshot.Groups {
		builder.WriteString(group.ID)
		builder.WriteString(" ")
		for _, item := range group.Items {
			builder.WriteString(item.Key)
			builder.WriteString("=")
			builder.WriteString(item.Value)
			builder.WriteString(" ")
		}
	}
	return builder.String()
}
