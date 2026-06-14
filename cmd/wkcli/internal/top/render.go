package top

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
)

func renderJSON(w io.Writer, snapshot aggregateSnapshot) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

func renderHuman(w io.Writer, snapshot aggregateSnapshot) error {
	fmt.Fprintln(w, "WuKongIM top")
	if !snapshot.GeneratedAt.IsZero() {
		fmt.Fprintf(w, "generated_at: %s\n", snapshot.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "VERDICT")
	fmt.Fprintf(w, "level: %s\n", snapshot.Verdict.Level)
	if snapshot.Verdict.Summary != "" {
		fmt.Fprintf(w, "summary: %s\n", snapshot.Verdict.Summary)
	}
	if len(snapshot.Verdict.Reasons) > 0 {
		fmt.Fprintf(w, "reasons: %s\n", strings.Join(snapshot.Verdict.Reasons, "; "))
	}
	fmt.Fprintf(w, "ready: %d/%d\n", snapshot.ReadyNodes, snapshot.TotalNodes)
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tREADY\tCONN\tSEND/s\tACK/s\tAPPEND_P99\tPRESSURE")
	for _, node := range snapshot.Nodes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			nodeName(node),
			yesNo(node.Node.Ready),
			formatInt64(connectionCount(node)),
			formatFloat(rateValue(node.Traffic, "send")),
			formatFloat(rateValue(node.Traffic, "ack")),
			formatMS(appendP99(node.Traffic)),
			pressureSummary(node),
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "HOT PRESSURE")
	if len(snapshot.Nodes) == 0 {
		fmt.Fprintln(w, "none")
		return nil
	}
	for _, node := range snapshot.Nodes {
		if node.Pressure == nil || len(node.Pressure.Top) == 0 {
			continue
		}
		for _, item := range node.Pressure.Top {
			parts := []string{nodeName(node), item.Component}
			if item.Pool != "" {
				parts = append(parts, "pool="+item.Pool)
			}
			if item.Queue != "" {
				parts = append(parts, "queue="+item.Queue)
			}
			if item.Priority != "" {
				parts = append(parts, "priority="+item.Priority)
			}
			parts = append(parts,
				"level="+item.Level,
				"score="+formatFloat(item.Score),
			)
			if item.Depth > 0 || item.Capacity > 0 {
				parts = append(parts, "depth="+formatInt64(item.Depth)+"/"+formatInt64(item.Capacity))
			}
			if item.Inflight > 0 || item.Workers > 0 {
				parts = append(parts, "inflight="+formatInt64(item.Inflight)+"/"+formatInt64(item.Workers))
			}
			appendP99Part(&parts, "wait_p99", item.WaitP99MS)
			appendP99Part(&parts, "task_p99", item.TaskP99MS)
			if item.AdmissionErrorPerSec > 0 {
				parts = append(parts, "admit_err/s="+formatFloat(item.AdmissionErrorPerSec))
			}
			if item.Hint != "" {
				parts = append(parts, "hint="+item.Hint)
			}
			fmt.Fprintln(w, strings.Join(parts, " "))
		}
	}
	return nil
}

func nodeName(node accessapi.TopSnapshot) string {
	if node.Node.Name != "" {
		return node.Node.Name
	}
	if node.Node.ID != 0 {
		return "node-" + strconv.FormatUint(node.Node.ID, 10)
	}
	return "unknown"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func formatInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func formatMS(v float64) string {
	if v <= 0 {
		return "-"
	}
	return formatFloat(v) + "ms"
}

func connectionCount(node accessapi.TopSnapshot) int64 {
	if node.Clients == nil {
		return 0
	}
	return node.Clients.Connections
}

func rateValue(traffic *accessapi.TopTraffic, name string) float64 {
	if traffic == nil {
		return 0
	}
	switch name {
	case "send":
		return traffic.SendPerSec
	case "ack":
		return traffic.SendackPerSec
	case "append":
		return traffic.AppendPerSec
	case "deliver":
		return traffic.DeliverPerSec
	default:
		return 0
	}
}

func appendP99Part(parts *[]string, name string, value float64) {
	if value > 0 {
		*parts = append(*parts, name+"="+formatMS(value))
	}
}

func appendP99(traffic *accessapi.TopTraffic) float64 {
	if traffic == nil {
		return 0
	}
	return traffic.AppendP99MS
}

func pressureSummary(node accessapi.TopSnapshot) string {
	if node.Pressure == nil {
		if node.Verdict.Level != "" {
			return node.Verdict.Level
		}
		return "unknown"
	}
	component := ""
	score := 0.0
	for name, value := range node.Pressure.ComponentScores {
		if value > score || (value == score && (component == "" || name < component)) {
			component = name
			score = value
		}
	}
	if component == "" && len(node.Pressure.Top) > 0 {
		component = node.Pressure.Top[0].Component
		score = node.Pressure.Top[0].Score
	}
	if component == "" {
		return node.Pressure.OverallLevel
	}
	return fmt.Sprintf("%s %s %s", component, node.Pressure.OverallLevel, formatFloat(score))
}
