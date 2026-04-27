package plane

import (
	"context"
	"fmt"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

func BenchmarkPlannerNextDecisionSteadyState(b *testing.B) {
	for _, slots := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("slots=%d", slots), func(b *testing.B) {
			state := benchmarkPlannerState(slots, 6, 3, benchmarkPlannerBalanced)
			planner := NewPlanner(PlannerConfig{
				SlotCount:              uint32(slots),
				ReplicaN:               3,
				RebalanceSkewThreshold: slots + 1,
			})
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				decision, err := planner.NextDecision(ctx, state)
				if err != nil {
					b.Fatal(err)
				}
				if decision.SlotID != 0 {
					b.Fatalf("unexpected decision: %+v", decision)
				}
			}
		})
	}
}

func BenchmarkPlannerNextDecisionRepair(b *testing.B) {
	for _, slots := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("slots=%d", slots), func(b *testing.B) {
			state := benchmarkPlannerState(slots, 6, 3, benchmarkPlannerBalanced)
			state.Nodes[2] = benchmarkPlannerNode(2, controllermeta.NodeStatusDead)
			planner := NewPlanner(PlannerConfig{SlotCount: uint32(slots), ReplicaN: 3})
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				decision, err := planner.NextDecision(ctx, state)
				if err != nil {
					b.Fatal(err)
				}
				if decision.SlotID == 0 || decision.Task == nil || decision.Task.Kind != controllermeta.TaskKindRepair {
					b.Fatalf("expected repair decision, got %+v", decision)
				}
			}
		})
	}
}

func BenchmarkPlannerNextDecisionRebalance(b *testing.B) {
	for _, slots := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("slots=%d", slots), func(b *testing.B) {
			state := benchmarkPlannerState(slots, 6, 3, benchmarkPlannerSkewed)
			planner := NewPlanner(PlannerConfig{SlotCount: uint32(slots), ReplicaN: 3})
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				decision, err := planner.NextDecision(ctx, state)
				if err != nil {
					b.Fatal(err)
				}
				if decision.SlotID == 0 || decision.Task == nil || decision.Task.Kind != controllermeta.TaskKindRebalance {
					b.Fatalf("expected rebalance decision, got %+v", decision)
				}
			}
		})
	}
}

type benchmarkPlannerLayout uint8

const (
	benchmarkPlannerBalanced benchmarkPlannerLayout = iota
	benchmarkPlannerSkewed
)

func benchmarkPlannerState(slots, nodes, replicaN int, layout benchmarkPlannerLayout) PlannerState {
	now := time.Unix(100, 0)
	state := PlannerState{
		Now:            now,
		Nodes:          make(map[uint64]controllermeta.ClusterNode, nodes),
		Assignments:    make(map[uint32]controllermeta.SlotAssignment, slots),
		Runtime:        make(map[uint32]controllermeta.SlotRuntimeView, slots),
		Tasks:          make(map[uint32]controllermeta.ReconcileTask),
		PhysicalSlots:  make(map[uint32]struct{}, slots),
		MigratingSlots: make(map[uint32]struct{}),
	}
	for nodeID := 1; nodeID <= nodes; nodeID++ {
		state.Nodes[uint64(nodeID)] = benchmarkPlannerNode(uint64(nodeID), controllermeta.NodeStatusAlive)
	}
	for slotID := 1; slotID <= slots; slotID++ {
		peers := benchmarkPlannerPeers(slotID, nodes, replicaN, layout)
		id := uint32(slotID)
		state.PhysicalSlots[id] = struct{}{}
		state.Assignments[id] = controllermeta.SlotAssignment{
			SlotID:       id,
			DesiredPeers: peers,
			ConfigEpoch:  1,
		}
		state.Runtime[id] = controllermeta.SlotRuntimeView{
			SlotID:              id,
			CurrentPeers:        append([]uint64(nil), peers...),
			LeaderID:            peers[0],
			HealthyVoters:       uint32(len(peers)),
			HasQuorum:           true,
			ObservedConfigEpoch: 1,
			LastReportAt:        now,
		}
	}
	return state
}

func benchmarkPlannerNode(nodeID uint64, status controllermeta.NodeStatus) controllermeta.ClusterNode {
	return controllermeta.ClusterNode{
		NodeID:          nodeID,
		Addr:            fmt.Sprintf("127.0.0.1:%d", 7000+nodeID),
		Role:            controllermeta.NodeRoleData,
		JoinState:       controllermeta.NodeJoinStateActive,
		Status:          status,
		LastHeartbeatAt: time.Unix(100, 0),
		CapacityWeight:  1,
	}
}

func benchmarkPlannerPeers(slotID, nodes, replicaN int, layout benchmarkPlannerLayout) []uint64 {
	if layout == benchmarkPlannerSkewed {
		peers := make([]uint64, 0, replicaN)
		for nodeID := 1; nodeID <= replicaN; nodeID++ {
			peers = append(peers, uint64(nodeID))
		}
		return peers
	}

	peers := make([]uint64, 0, replicaN)
	start := (slotID - 1) % nodes
	for offset := 0; offset < replicaN; offset++ {
		peers = append(peers, uint64((start+offset)%nodes+1))
	}
	return peers
}
