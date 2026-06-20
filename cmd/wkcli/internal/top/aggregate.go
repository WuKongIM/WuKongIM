package top

import (
	"sort"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top/topapi"
)

type aggregateSnapshot struct {
	GeneratedAt time.Time               `json:"generated_at"`
	Nodes       []accessapi.TopSnapshot `json:"nodes"`
	Verdict     accessapi.TopVerdict    `json:"verdict"`
	ReadyNodes  int                     `json:"ready_nodes"`
	TotalNodes  int                     `json:"total_nodes"`
}

func aggregate(snapshots []accessapi.TopSnapshot) aggregateSnapshot {
	nodes := append([]accessapi.TopSnapshot(nil), snapshots...)
	sort.SliceStable(nodes, func(i, j int) bool {
		return topPressureScore(nodes[i]) > topPressureScore(nodes[j])
	})
	agg := aggregateSnapshot{
		Nodes:      nodes,
		TotalNodes: len(nodes),
		Verdict: accessapi.TopVerdict{
			Level:   "ok",
			Summary: "all nodes ok",
		},
	}
	for _, node := range nodes {
		if node.GeneratedAt.After(agg.GeneratedAt) {
			agg.GeneratedAt = node.GeneratedAt
		}
		if node.Node.Ready {
			agg.ReadyNodes++
		}
		if verdictSeverity(node.Verdict.Level) > verdictSeverity(agg.Verdict.Level) {
			agg.Verdict = node.Verdict
		}
	}
	if len(nodes) == 0 {
		agg.Verdict = accessapi.TopVerdict{Level: "critical", Summary: "no nodes returned a snapshot"}
	}
	return agg
}

func topPressureScore(node accessapi.TopSnapshot) float64 {
	if node.Pressure == nil {
		return float64(verdictSeverity(node.Verdict.Level)) / 4
	}
	score := 0.0
	for _, item := range node.Pressure.Top {
		if item.Score > score {
			score = item.Score
		}
	}
	for _, value := range node.Pressure.ComponentScores {
		if value > score {
			score = value
		}
	}
	if score == 0 {
		score = float64(verdictSeverity(node.Pressure.OverallLevel)) / 4
	}
	return score
}

func verdictSeverity(level string) int {
	switch level {
	case "critical":
		return 4
	case "degraded":
		return 3
	case "busy":
		return 2
	case "ok":
		return 1
	default:
		return 0
	}
}
