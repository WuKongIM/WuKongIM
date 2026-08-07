package chatlifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigExamplesLoadThroughStrictProductionParser(t *testing.T) {
	root := filepath.Join("..", "..", "..", "configs", "wkbench", "chat-lifecycle")
	formal, err := LoadConfig(filepath.Join(root, "formal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if formal.Profile != ProfileFormal || formal.Mode != ModeSoak ||
		len(formal.Observation.ServiceNodes) != 3 || len(formal.Observation.Workers) != 3 ||
		len(formal.Observation.HostMetrics) != 3 || formal.Workload.Topology.LogicalSlotGroups != 12 ||
		formal.Workload.Topology.HashSlots != 256 || formal.Workload.Topology.SlotReplicas != 3 ||
		formal.Workload.Topology.ChannelReplicas != 3 || formal.Workload.OnlineUsers != 10_000 ||
		formal.Workload.NewUsersPerDay != 250_000 || formal.Workload.SendRatePerSecond != 2_000 ||
		formal.Workload.Sync.Version != 0 || formal.Thresholds.Timeline.Warmup != 2*time.Hour ||
		formal.Thresholds.Timeline.Checkpoint != 24*time.Hour || formal.Thresholds.Timeline.Final != 72*time.Hour ||
		formal.Thresholds.MinimumDataFilesystemBytes != 500_000_000_000 ||
		formal.Thresholds.DiskSafeStopFreePercent != 5 {
		t.Fatalf("formal example drifted: %+v", formal)
	}
	local, err := LoadConfig(filepath.Join(root, "local-shakeout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if local.Profile != ProfileLocal || local.Mode != ModeSoak || local.Workload.Sync.Version != 0 ||
		local.Workload.Topology != (TopologyConfig{LogicalSlotGroups: 12, HashSlots: 256, SlotReplicas: 3, ChannelReplicas: 3}) {
		t.Fatalf("local example drifted: %+v", local)
	}
	for _, name := range []string{"formal.yaml", "local-shakeout.yaml"} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, "bearer") || strings.Contains(lower, "token:") || strings.Contains(lower, "docker") {
			t.Fatalf("%s contains a credential-like or prohibited dependency value", name)
		}
	}
}
