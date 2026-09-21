package worker

import (
	"context"
	"fmt"
	"testing"

	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/stretchr/testify/require"
)

func TestBuildGroupWorkloadsRandomSenderUsesScenarioSeed(t *testing.T) {
	sequence := func(seed int64) []string {
		assignment := groupShardAssignment("http://target.invalid")
		assignment.Scenario.Run.RandomSeed = seed
		assignment.Scenario.Messages.Traffic[0].SenderPick = "random_online"
		clients := make(map[string]benchworkload.PersonClient)
		for i := 0; i < 4; i++ {
			clients[fmt.Sprintf("bench-u-%d", i)] = &workerPersonClient{}
		}
		workloads, err := buildGroupWorkloads(assignment, groupBundlesForTest(t, assignment), clients, nil)
		require.NoError(t, err)
		require.Len(t, workloads, 1)
		var result []string
		for i := 0; i < 128; i++ {
			before := make(map[string]int)
			for uid, client := range clients {
				before[uid] = len(client.(*workerPersonClient).sentFrames)
			}
			require.NoError(t, workloads[0].SendOne(context.Background(), 0, i))
			for uid, client := range clients {
				if len(client.(*workerPersonClient).sentFrames) > before[uid] {
					result = append(result, uid)
				}
			}
		}
		return result
	}
	first := sequence(42)
	require.Equal(t, first, sequence(42), "the scenario seed must reproduce sender selection")
	require.NotEqual(t, first, sequence(43), "changing the seed must change the generated sender sequence")
	seen := make(map[string]bool)
	for _, uid := range first {
		seen[uid] = true
	}
	require.Len(t, first, 128)
	require.Len(t, seen, 4, "random_online must not silently select only the first member")
}
