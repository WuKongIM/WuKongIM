package chatlifecycle

import (
	"os"
	"path/filepath"
	"reflect"
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
	if formal.Profile != ProfileFormal || formal.Mode != ModeSoak || formal.Stage != StageFormal ||
		len(formal.Observation.ServiceNodes) != 3 || len(formal.Observation.Workers) != 3 ||
		len(formal.Observation.HostMetrics) != 3 || formal.Workload.Topology.LogicalSlotGroups != 12 ||
		formal.Workload.Topology.HashSlots != 256 || formal.Workload.Topology.SlotReplicas != 3 ||
		formal.Workload.Topology.ChannelReplicas != 3 || formal.Workload.OnlineUsers != 10_000 ||
		formal.Workload.BootstrapLoginsPerSecond != 50 || formal.Workload.NewUsersPerDay != 250_000 || formal.Workload.SendRatePerSecond != 2_000 ||
		formal.Workload.Sync.CompletedCoverage != 0 || formal.Thresholds.Timeline.Warmup != 2*time.Hour ||
		formal.Thresholds.Timeline.Checkpoint != 24*time.Hour || formal.Thresholds.Timeline.Final != 72*time.Hour ||
		formal.Thresholds.MinimumDataFilesystemBytes != 500_000_000_000 ||
		formal.Thresholds.Resource.MinimumLoadFilesystemBytes != 200_000_000_000 ||
		formal.Thresholds.DiskSafeStopFreePercent != 5 {
		t.Fatalf("formal example drifted: %+v", formal)
	}
	rehearsal, err := LoadConfig(filepath.Join(root, "rehearsal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if rehearsal.Stage != StageRehearsal || rehearsal.measuredDuration() != 2*time.Hour {
		t.Fatalf("rehearsal example drifted: %+v", rehearsal)
	}
	formal.RunID, rehearsal.RunID = "", ""
	formal.Stage, rehearsal.Stage = "", ""
	if !reflect.DeepEqual(formal, rehearsal) {
		t.Fatal("rehearsal example differs from formal workload or thresholds")
	}
	local, err := LoadConfig(filepath.Join(root, "local-shakeout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if local.Profile != ProfileLocal || local.Mode != ModeSoak || local.Workload.Sync.CompletedCoverage != 0 ||
		local.Workload.OnlineUsers != 2_500 || local.Workload.BootstrapLoginsPerSecond != 200 ||
		local.Workload.NewUsersPerDay != 250_000 || local.Thresholds.Timeline.Warmup != 10*time.Minute ||
		local.Thresholds.Resource.MinimumLoadFilesystemBytes != 10_000_000_000 ||
		local.Thresholds.Latency.HotSendACK.P99 != 400*time.Millisecond ||
		local.Workload.MaxChannelsPerNode != 50_000 ||
		local.Workload.Groups.Small+local.Workload.Groups.Medium+local.Workload.Groups.Large+local.Workload.Groups.VeryLarge != 500 ||
		local.Workload.Groups.VeryLarge != 1 || local.Workload.Groups.VeryLargeMembers != 100_000 ||
		local.Workload.Topology != (TopologyConfig{LogicalSlotGroups: 12, HashSlots: 256, SlotReplicas: 3, ChannelReplicas: 3}) {
		t.Fatalf("local example drifted: %+v", local)
	}
	for _, name := range []string{"formal.yaml", "rehearsal.yaml", "local-shakeout.yaml"} {
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
