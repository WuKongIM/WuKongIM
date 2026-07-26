package backup

import (
	"reflect"
	"strings"
	"testing"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestPendingSourceFenceNodesRequiresObservedNotReadyReports(t *testing.T) {
	record := backupartifact.SourceFenceRecord{
		Format:  backupartifact.SourceFenceReceiptFormat,
		Version: backupartifact.SourceFenceReceiptVersion,
		ID:      "fence-1", SourceClusterID: "source",
		SourceGeneration: "source-generation",
		RestorePlanID:    "plan-1", RestorePointID: "checkpoint-1",
		ManifestSHA256:          strings.Repeat("a", 64),
		TargetClusterID:         "target",
		TargetGeneration:        "target-generation",
		FenceControllerRevision: 11,
		RequestedAtUnixMillis:   1_800_000_000_000,
	}
	state := controller.ClusterState{
		Revision: 11,
		Backup: &controller.BackupCoordinationState{
			SourceFence: &record,
		},
		Nodes: []controller.Node{
			{NodeID: 1, Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive},
			{NodeID: 2, Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateLeaving},
			{NodeID: 3, Roles: []controller.NodeRole{controller.NodeRoleControllerVoter}, JoinState: controller.NodeJoinStateActive},
			{NodeID: 4, Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateJoining},
		},
		NodeHealthReports: []controller.NodeHealthReport{
			{
				NodeID: 1, ObservedControlRevision: 11,
				ReportedAtUnixMilli: record.RequestedAtUnixMillis,
				RuntimeReady:        true,
			},
		},
	}
	pending, err := pendingSourceFenceNodes(state, record)
	if err != nil || !reflect.DeepEqual(pending, []uint64{1, 2}) {
		t.Fatalf("pending before convergence=%v err=%v", pending, err)
	}
	state.NodeHealthReports = []controller.NodeHealthReport{
		{
			NodeID: 1, ObservedControlRevision: 11,
			ReportedAtUnixMilli: record.RequestedAtUnixMillis,
			RuntimeReady:        false,
		},
		{
			NodeID: 2, ObservedControlRevision: 10,
			ReportedAtUnixMilli: record.RequestedAtUnixMillis,
			RuntimeReady:        false,
		},
	}
	pending, err = pendingSourceFenceNodes(state, record)
	if err != nil || !reflect.DeepEqual(pending, []uint64{2}) {
		t.Fatalf("pending stale revision=%v err=%v", pending, err)
	}
	state.NodeHealthReports[1].ObservedControlRevision = 11
	pending, err = pendingSourceFenceNodes(state, record)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after convergence=%v err=%v", pending, err)
	}
}
