package top

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top/topapi"
)

func renderJSON(w io.Writer, snapshot aggregateSnapshot) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

func renderAlertsJSON(w io.Writer, snapshot aggregateSnapshot, filter string) error {
	alerts, err := selectAlerts(snapshot, filter)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(alerts)
}

func renderAlerts(w io.Writer, snapshot aggregateSnapshot, filter string) error {
	alerts, err := selectAlerts(snapshot, filter)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "WuKongIM top alerts")
	if !snapshot.GeneratedAt.IsZero() {
		fmt.Fprintf(w, "generated_at: %s\n", snapshot.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	fmt.Fprintf(w, "total: %d\n", len(alerts))
	if len(alerts) == 0 {
		fmt.Fprintln(w, "none")
		return nil
	}
	for i, alert := range alerts {
		if i > 0 {
			fmt.Fprintln(w)
		}
		renderAlertDetail(w, snapshot, alert)
	}
	return nil
}

func renderAlertDetail(w io.Writer, snapshot aggregateSnapshot, alert accessapi.TopAlert) {
	fmt.Fprintf(w, "id: %s\n", emptyDash(alert.ID))
	fmt.Fprintf(w, "node: %s\n", alertNodeName(alert))
	fmt.Fprintf(w, "state: %s\n", alertState(alert))
	fmt.Fprintf(w, "severity: %s\n", emptyDash(alert.Severity))
	fmt.Fprintf(w, "component: %s\n", emptyDash(alert.Component))
	fmt.Fprintf(w, "kind: %s\n", emptyDash(alert.Kind))
	fmt.Fprintf(w, "message: %s\n", emptyDash(alert.Message))
	if alert.Hint != "" {
		fmt.Fprintf(w, "hint: %s\n", alert.Hint)
	}
	if alert.Fingerprint != "" {
		fmt.Fprintf(w, "fingerprint: %s\n", alert.Fingerprint)
	}
	if alert.Count > 0 {
		fmt.Fprintf(w, "count: %d\n", alert.Count)
	}
	if !alert.FirstSeen.IsZero() {
		fmt.Fprintf(w, "first_seen: %s\n", alert.FirstSeen.Format(time.RFC3339))
	}
	if !alert.LastSeen.IsZero() {
		fmt.Fprintf(w, "last_seen: %s\n", alert.LastSeen.Format(time.RFC3339))
		fmt.Fprintf(w, "age: %s\n", formatAlertAge(snapshot.GeneratedAt, alert.LastSeen))
	}
	if alert.ResolvedAt != nil {
		fmt.Fprintf(w, "resolved_at: %s\n", alert.ResolvedAt.Format(time.RFC3339))
	}
	if len(alert.Evidence) == 0 {
		return
	}
	fmt.Fprintln(w, "evidence:")
	keys := make([]string, 0, len(alert.Evidence))
	for key := range alert.Evidence {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "  %s=%s\n", key, alert.Evidence[key])
	}
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

	if err := renderHumanAlerts(w, snapshot); err != nil {
		return err
	}
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tREADY\tCONN\tCPU%\tMEM\tSEND/s\tACK/s\tAPPEND_P99\tPRESSURE")
	for _, node := range snapshot.Nodes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			nodeName(node),
			yesNo(node.Node.Ready),
			formatInt64(connectionCount(node)),
			formatFloat(resourceCPUPercent(node)),
			formatBytes(resourceMemoryAlloc(node)),
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

func renderHumanAlerts(w io.Writer, snapshot aggregateSnapshot) error {
	fmt.Fprintln(w, "ALERTS")
	alerts := aggregateAlerts(snapshot)
	if len(alerts) == 0 {
		fmt.Fprintln(w, "none")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEVERITY\tNODE\tSTATE\tAGE\tCOUNT\tMESSAGE\tHINT")
	for _, alert := range alerts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			alert.Severity,
			alertNodeName(alert),
			alertState(alert),
			formatAlertAge(snapshot.GeneratedAt, alert.LastSeen),
			alert.Count,
			alert.Message,
			emptyDash(alert.Hint),
		)
	}
	return tw.Flush()
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

func formatBytes(v uint64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case v >= gib:
		if v%gib == 0 {
			return strconv.FormatUint(v/gib, 10) + "GiB"
		}
		return strconv.FormatFloat(float64(v)/float64(gib), 'f', 1, 64) + "GiB"
	case v >= mib:
		if v%mib == 0 {
			return strconv.FormatUint(v/mib, 10) + "MiB"
		}
		return strconv.FormatFloat(float64(v)/float64(mib), 'f', 1, 64) + "MiB"
	case v >= kib:
		if v%kib == 0 {
			return strconv.FormatUint(v/kib, 10) + "KiB"
		}
		return strconv.FormatFloat(float64(v)/float64(kib), 'f', 1, 64) + "KiB"
	default:
		return strconv.FormatUint(v, 10) + "B"
	}
}

func connectionCount(node accessapi.TopSnapshot) int64 {
	if node.Clients == nil {
		return 0
	}
	return node.Clients.Connections
}

func resourceCPUPercent(node accessapi.TopSnapshot) float64 {
	if node.Resources == nil {
		return 0
	}
	return node.Resources.CPUPercent
}

func resourceMemoryAlloc(node accessapi.TopSnapshot) uint64 {
	if node.Resources == nil {
		return 0
	}
	return node.Resources.MemoryRSSBytes
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

func aggregateAlerts(snapshot aggregateSnapshot) []accessapi.TopAlert {
	alerts := make([]accessapi.TopAlert, 0)
	for _, node := range snapshot.Nodes {
		if node.Alerts == nil {
			continue
		}
		nodeAlerts := node.Alerts.Recent
		if len(nodeAlerts) == 0 {
			nodeAlerts = node.Alerts.Active
		}
		for _, alert := range nodeAlerts {
			if alert.NodeName == "" {
				alert.NodeName = nodeName(node)
			}
			if alert.NodeID == 0 {
				alert.NodeID = node.Node.ID
			}
			alerts = append(alerts, alert)
		}
	}
	sortAlerts(alerts)
	return alerts
}

func selectAlerts(snapshot aggregateSnapshot, filter string) ([]accessapi.TopAlert, error) {
	alerts := aggregateAlerts(snapshot)
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return alerts, nil
	}
	filtered := make([]accessapi.TopAlert, 0, 1)
	for _, alert := range alerts {
		if alertMatchesFilter(alert, filter) {
			filtered = append(filtered, alert)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("top alert not found: %s", filter)
	}
	return filtered, nil
}

func alertMatchesFilter(alert accessapi.TopAlert, filter string) bool {
	switch filter {
	case alert.ID, alert.Fingerprint, alert.Component + "/" + alert.Kind:
		return true
	default:
		return false
	}
}

func sortAlerts(alerts []accessapi.TopAlert) {
	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].Active != alerts[j].Active {
			return alerts[i].Active
		}
		if alertSeverityRank(alerts[i].Severity) != alertSeverityRank(alerts[j].Severity) {
			return alertSeverityRank(alerts[i].Severity) > alertSeverityRank(alerts[j].Severity)
		}
		if !alerts[i].LastSeen.Equal(alerts[j].LastSeen) {
			return alerts[i].LastSeen.After(alerts[j].LastSeen)
		}
		if alertNodeName(alerts[i]) != alertNodeName(alerts[j]) {
			return alertNodeName(alerts[i]) < alertNodeName(alerts[j])
		}
		if alerts[i].Component != alerts[j].Component {
			return alerts[i].Component < alerts[j].Component
		}
		return alerts[i].Message < alerts[j].Message
	})
}

func alertSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "error":
		return 2
	case "warn":
		return 1
	default:
		return 0
	}
}

func alertNodeName(alert accessapi.TopAlert) string {
	if alert.NodeName != "" {
		return alert.NodeName
	}
	if alert.NodeID != 0 {
		return "node-" + strconv.FormatUint(alert.NodeID, 10)
	}
	return "unknown"
}

func alertState(alert accessapi.TopAlert) string {
	if alert.Active {
		return "active"
	}
	if alert.ResolvedAt != nil {
		return "resolved"
	}
	return "recent"
}

func formatAlertAge(now, then time.Time) string {
	if then.IsZero() {
		return "-"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	d := now.Sub(then)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	case d < time.Hour:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	default:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	}
}
