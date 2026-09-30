//go:build integration

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestNodeConstructionFailureDiscardsOwnedRuntimeForRetry(t *testing.T) {
	record := recordNodeRestartEvidence(t)
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	blocker := filepath.Join(node.cfg.DataDir, defaultSlotMetaDirName)
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := node.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded with a blocked Slot metadata directory")
	}
	record("construction-failed", node)
	if node.control != nil || node.defaultControl || node.transportServer != nil ||
		node.transportClient != nil || node.defaultTransport {
		t.Fatal("runtime construction failure retained owned Controller or transport")
	}
	assertOwnedSlotReferencesReleased(t, node)
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	startNode(t, node)
	waitNodeWriteReady(t, node)
	record("construction-retry-write-ready", node)
}

func TestDefaultSlotDisposalPreservesInjectedTaskExecutor(t *testing.T) {
	boom := errors.New("injected start failure")
	resource := &recordingResource{calls: new([]string), startErr: boom}
	executor := &snapshotNotificationExecutor{snapshots: make(chan control.Snapshot, 1)}
	node, err := New(validNodeConfig(t), withTaskExecutor(executor),
		withResources(namedTestResource("failing-start", resource)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if err := node.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start = %v, want injected failure", err)
	}
	if node.tasks != executor {
		t.Fatal("failed startup replaced borrowed task executor")
	}
	resource.startErr = nil
	if err := node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if node.tasks != executor {
		t.Fatal("Stop replaced borrowed task executor beside owned Slots")
	}
}

// These integrations must reuse the same Node: a process restart constructs a
// new facade and cannot expose adapters retained after their owner has closed.
func assertOwnedSlotReferencesReleased(t *testing.T, node *Node) {
	t.Helper()
	for name, retained := range map[string]bool{
		"proposer":       node.proposer != nil,
		"task_executor":  node.tasks != nil,
		"slot_status":    node.slotStatusRuntime != nil,
		"quorum_gateway": node.channelQuorumGateway != nil,
	} {
		if retained {
			t.Errorf("owned %s retained after runtime disposal", name)
		}
	}
}

// recordNodeRestartEvidence retains bounded lifecycle facts without credentials
// or decoding storage; test assertions prove durable records through real stores.
func recordNodeRestartEvidence(t *testing.T) func(string, ...*Node) {
	t.Helper()
	var observations []map[string]any
	t.Cleanup(func() {
		dir := os.Getenv("WK_CLUSTER_RESTART_REPORT_DIR")
		if dir == "" {
			dir = filepath.Join(os.TempDir(), "wukongim-node-restart")
		}
		data, err := json.MarshalIndent(map[string]any{
			"passed": !t.Failed(), "test": t.Name(),
			"source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"),
			"observations":    observations,
		}, "", "  ")
		if err == nil {
			err = os.MkdirAll(dir, 0755)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, t.Name()+".json"), append(data, '\n'), 0644)
		}
		if err != nil {
			t.Errorf("write restart evidence: %v", err)
		}
	})
	return func(phase string, nodes ...*Node) {
		for _, node := range nodes {
			snapshot := node.Snapshot()
			var loadedSlots int
			if node.defaultSlotRuntime != nil {
				loadedSlots = len(node.defaultSlotRuntime.Slots())
			}
			observations = append(observations, map[string]any{
				"phase": phase, "node_id": node.NodeID(),
				"hash_slots":     node.cfg.Slots.HashSlotCount,
				"physical_slots": node.cfg.Slots.InitialSlotCount,
				"started":        node.started.Load(),
				"slot_runtime":   node.defaultSlotRuntime != nil,
				"proposer":       node.proposer != nil, "task_executor": node.tasks != nil,
				"slot_status":    node.slotStatusRuntime != nil,
				"quorum_gateway": node.channelQuorumGateway != nil,
				"loaded_slots":   loadedSlots, "routes_ready": snapshot.RoutesReady,
				"slots_ready": snapshot.SlotsReady, "channels_ready": snapshot.ChannelsReady,
				"applied_revision":     snapshot.StateRevision,
				"placement_candidates": len(node.channelDataNodes.DataNodes()),
			})
		}
	}
}
